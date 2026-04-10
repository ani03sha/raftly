package scenarios

import (
	"fmt"
	"time"
)


// This reproduces the etcd 2018 bug pattern. A node with stale log is prevented from becoming a leader by 
// the election restriction: voters refuse candidates whose log is behind theirs.
type StaleElectionScenario struct {
	DataDir string
}


func (s *StaleElectionScenario) Name() string {
	return "stale-log-elected-leader"
}


func (s *StaleElectionScenario) Run() (*ScenarioResult, error) {
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

	// 1. Elect a leader and write entries 1-10 (replicated to all 3 nodes)
	leaderID, err := WaitForLeader(cluster.Nodes, 2 * time.Second)
	if err != nil {
		return nil, err
	}
	leader := cluster.Nodes[leaderID]
	for i := 1; i <= 10; i++ {
		if err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("entry-%d", i)), 500 * time.Millisecond); err != nil {
			return nil, fmt.Errorf("write %d: %w", i, err)
		}
	}
	time.Sleep(100 * time.Millisecond) // allow replication to all nodes
	allNodesCommit := leader.Status().CommitIndex
	cluster.Injector.RecordObservation(fmt.Sprintf("all nodes at commit index %d", allNodesCommit))

	// 2. Find a non-leader node to become node3 (stale one)
	var node3ID string
	for id := range cluster.Nodes {
		if id != leaderID {
			node3ID = id
			break
		}
	}

	// 3. Partition node3 away from cluster
	cluster.Injector.PartitionNode(node3ID, 0)
	cluster.Injector.RecordObservation(fmt.Sprintf("node3 (%s) partitioned", node3ID))

	// 4. Write entries 11-20 to the remaining two nodes (quorum = 2). node3 doesn't get these entries.
	for i := 11; i <= 20; i++ {
		if err := ProposeWithTimeout(leader, []byte(fmt.Sprintf("entry-%d", i)), 500 * time.Millisecond); err != nil {
			cluster.Injector.RecordObservation(fmt.Sprintf("write %d failed: %v", i, err))
		}
	}
	time.Sleep(100 * time.Millisecond)
	advancedCommit := leader.Status().CommitIndex
	node3LogIndex := cluster.Nodes[node3ID].Status().LogIndex
	cluster.Injector.RecordObservation(fmt.Sprintf(
                "cluster committed up to %d; node3's log ends at %d (stale by %d entries)",
                advancedCommit, node3LogIndex, advancedCommit-node3LogIndex))

	// 3. Crash the current leader
	cluster.Injector.CrashNode(leaderID)
	cluster.Injector.RecordObservation(fmt.Sprintf("leader %s crashed", leaderID))

	// 6. Heal the partition so node3 can rejoin. Now the node3 and one remaining node can communicate
	cluster.Injector.Heal()
	cluster.Injector.RecordObservation("partition healed — node3 rejoins cluster")

	// 7. Wait for a new leader. The election restriction means node3 cannot win: the remaining node will refuse
	// to vote for node3 because node3's log ends at index 10 while the voter's log ends at index 20.
	// Use WaitForLeader (with 2s timeout) rather than a fixed sleep to avoid flakiness.
	newLeaderID, _ := WaitForLeader(cluster.Nodes, 2*time.Second)

	node3IsLeader := cluster.Nodes[node3ID].IsLeader()
	cluster.Injector.RecordObservation(fmt.Sprintf(
                "new leader: %s (node3 is leader: %v — must be false)",
                newLeaderID, node3IsLeader))

	// 8. Allow node3 to sync its log
	time.Sleep(500 * time.Millisecond)
	node3FinalCommit := cluster.Nodes[node3ID].Status().CommitIndex
	cluster.Injector.RecordObservation(fmt.Sprintf(
                "node3 final commit index: %d (expected %d)", node3FinalCommit, advancedCommit))

	result.Summary["pre_partition_commit"] = fmt.Sprintf("%d", allNodesCommit)
	result.Summary["node3_stale_log_index"] = fmt.Sprintf("%d", node3LogIndex)
	result.Summary["advanced_commit"] = fmt.Sprintf("%d", advancedCommit)
	result.Summary["node3_became_leader"] = fmt.Sprintf("%v", node3IsLeader)
	result.Summary["node3_final_commit"] = fmt.Sprintf("%d", node3FinalCommit)

	// Stale node must NOT have become leader.
	// Stale node MUST have synced the committed entries after healing.
	result.Passed = !node3IsLeader && node3FinalCommit >= advancedCommit
	result.Observations = cluster.Injector.Report()

	return result, nil
}