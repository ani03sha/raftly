package scenarios

import (
	"fmt"
	"time"
)


// Leader isolation isolates the leader from quorum and verifies that writes accepted by the
// isolated leader are not committed.
type LeaderIsolationScenario struct {
	DataDir string
}

func (s *LeaderIsolationScenario) Name() string {
	return "leader-isolation-write-loss"
}

func (s *LeaderIsolationScenario) Run() (*ScenarioResult, error) {
	result := &ScenarioResult{Summary: make(map[string]string)}

	cluster, err := NewCluster([]string{"node1", "node2", "node3"}, s.DataDir)
	if err != nil {
		return nil, err
	}
	defer cluster.Cleanup()
	if err := cluster.Start(); err != nil {
		return nil, err
	}
	defer cluster.Stop()

	// 1. Elect leader and write 20 committed entries
	leaderID, err := WaitForLeader(cluster.Nodes, 2 * time.Second)
	if err != nil {
		return nil, err
	}
	leader := cluster.Nodes[leaderID]

	for i := 1; i <= 20; i++ {
		if err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("committed-%d", i)), 500 * time.Millisecond); err != nil {
			return nil, fmt.Errorf("baseline write %d failed: %w", i, err)
		}
	}
	baselineCommit := leader.Status().CommitIndex
	cluster.Injector.RecordObservation(fmt.Sprintf("baseline: %d entries committed", baselineCommit))

	// 2. Isolate the leader asymmetrically - it cannot reach the quorum and the quorum cannot reach it. Both sides time out.
	cluster.Injector.PartitionNode(leaderID, 0)
	cluster.Injector.RecordObservation(fmt.Sprintf("isolated leader: %s", leaderID))

	// 3. Attempt writes to the isolated leader for 1 second. These proposals are added to the leader's local log and WAL,
	// but can never be committed (no quorum).
	isolatedAttempts := 0
	isolatedCommits := 0
	deadline := time.Now().Add(1 * time.Second)
	i := 21
	for time.Now().Before(deadline) {
		isolatedAttempts++
		err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("isolated-%d", i)), 150 * time.Millisecond)
		if err == nil {
			isolatedCommits++
		}
		i++
	}
	isolatedLogGrowth := leader.Status().LogIndex - baselineCommit
	cluster.Injector.RecordObservation(fmt.Sprintf("isolated leader: %d attempts, %d commits, log grew by %d entries", 
					isolatedAttempts, isolatedCommits, isolatedLogGrowth))

	// 4. Heal the partition. The isolated leader will step down when it receives AppendEntries from the new
	// leader with a higher term.
	cluster.Injector.Heal()
	time.Sleep(600 * time.Millisecond) // allow log sync

	// 5. Verify: the old leader's log has converged back to the cluster's state.
	postHealCommit := leader.Status().CommitIndex
	postHealLog := leader.Status().LogIndex
	survivingIsolatedWrites := postHealLog - baselineCommit

	cluster.Injector.RecordObservation(fmt.Sprintf(
                "post-heal: commit=%d log=%d surviving-isolated-entries=%d",
                postHealCommit, postHealLog, survivingIsolatedWrites))

	// Key assertion: none of the isolated leader's write should have survived.
	// They were never committed (no quorum) and should have been truncated.
	result.Summary["baseline_commit"] = fmt.Sprintf("%d", baselineCommit)
	result.Summary["isolated_attempts"] = fmt.Sprintf("%d", isolatedAttempts)
	result.Summary["isolated_commits"] = fmt.Sprintf("%d", isolatedCommits)
	result.Summary["surviving_isolated_writes"] = fmt.Sprintf("%d", survivingIsolatedWrites)
	result.Passed = isolatedCommits == 0 && survivingIsolatedWrites == 0
	result.Observations = cluster.Injector.Report()

	return result, nil
}