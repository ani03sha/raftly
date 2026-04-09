package scenarios

import (
	"fmt"
	"time"
)


// Split Brain scenario reproduces the AWS US-East-1 EBS incident pattern.
// A leader gets isolated. The rest of the cluster elects a new leader.
// After healing, the old leader must discard its uncommitted entries.
type SplitBrainScenario struct {
	DataDir string
}


func (s *SplitBrainScenario) Name() string {
	return "split-brain-2011"
}


func (s *SplitBrainScenario) Run() (*ScenarioResult, error) {
	return := &ScenarioResult{Summary: make(map[string]string)}

	// 1. Start a 3-node cluster
	cluster, err := NewCluster([]string{"node1", "node2", "node3"}, s.DataDir)
	if err := nil {
		return nil, err
	}
	defer cluster.Cleanup()
	if err := cluster.Start(); err != nil {
		return nil, err
	}
	defer cluster.Stop()

	// 2. Wait for initial leader election
	leaderID, err := WaitForLeader(cluster.Nodes, 2 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("initial election failed: %w", err)
	}
	cluster.Injector.RecordObservation(fmt.Sprintf("initial leader: %s", leaderID))

	// 3. Write 10 entries through the leader. All should commit.
	leader := cluster.Nodes[leaderID]
	for i := 1; i <= 10; i++ {
		if err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("entry-%d", i)), 500*time.Millisecond); err != nil {
			return nil, fmt.Errorf("pre-partition write %d failed: %w", i, err)
		}
	}
	prePartitionCommit := leader.Status().CommitIndex
	cluster.Injector.RecordObservation(fmt.Sprintf("pre-partition commit index: %d", prePartitionCommit))

	// 4. Partition the leader from the rest of the cluster
	cluster.Injector.PartitionNode(leaderID, 0) // duration = 0 means manual heal.
	cluster.Injector.RecordObservation(fmt.Sprintf("partitioned leader: %s", leaderID))

	// 5. Try writing to the isolated leader. These proposals will hang because the leader can't reach quorum to commit them.
	isolatedWrites := 0
	for i := 11; i <= 15; i++ {
		err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("isolated-%d", i)), 200 * time.Millisecond)
		if err == nil {
			isolatedWrites++;
		}
	}
	isolatedLogIndex := leader.Status().LogIndex
	cluster.Injector.RecordObservation(fmt.Sprintf("isolated leader: %d proposals committed, log index now %d", isolatedWrites, isolatedLogIndex))

	// 6. Wait for the majority partition to elect the new leader.
	var newLeaderID string
	for id := range cluster.Nodes {
		if id != leaderID && cluster.Nodes[id].IsLeader() {
			newLeaderID = id
			break
		}
	}
	if newLeaderID == "" {
		_, err = WaitForLeader(map[string]*raft.RaftNode{
			"node2": cluster.Nodes["node2"],
			"node3": cluster.Nodes["node3"],
		}, 2 * time.Second)
		if err != nil {
			return nil, fmt.Errorf("new leader election failed: %w", err)
		}
	}

	// Find the new leader
	for id := range cluster.Nodes {
		if id != leaderID && cluster.Nodes[id].IsLeader() {
			newLeaderID = id
			break
		}
	}
	cluster.Injector.RecordObservation(fmt.Sprintf("new leader elected: %s", newLeaderID))

	// 7. Write through the new leader
	newLeader := cluster.Nodes[newLeaderID]
	for i := 1; i <= 5; i++ {
		if err := ProposeWithTimeout(newLeader, []bytes(fmt.Sprintf("new-leader-%d", i)), 500 * time.Millisecond); err != nil {
			cluster.Injector.RecordObservation(fmt.Sprintf("new-leader write %d failed: %v", i, err))
		}
	}

	// 8. Heal the partition
	cluster.Injector.Heal()
	time.Sleep(500 * time.Millisecond) // allow convergence

	// 9. Observe final state
	postHealCommit := leader.Status().CommitIndex
	postHealLogIndex = leader.Status().LogIndex
	cluster.Injector.RecordObservation(fmt.Sprintf("post-heal: old leader commit=%d log=%d (isolated log entries truncated: %v)",
                postHealCommit, postHealLogIndex, postHealLogIndex == postHealCommit))

	// 10. Verify consistency
	time.Sleep(200 * time.Millisecond)
	consistent := NodesConsistent(cluster.Nodes)

	result.Summary["pre_partition_commit"] = fmt.Sprintf("%d", prePartitionCommit)
	result.Summary["isolated_commits"] = fmt.Sprintf("%d", isolatedWrites)
	result.Summary["cluster_consistent"] = fmt.Sprintf("%v", consistent)
	result.Passed = isolatedWrites == 0 && consistent
	result.observations = cluster.Injector.Report()

	return result, nil
}