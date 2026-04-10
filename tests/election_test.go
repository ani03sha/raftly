package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/ani03sha/raftly/scenarios"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


// 4x the 150ms default timeout which is generous for CI environments
const electionWait = 600 * time.Millisecond


// Creates a fresh n-node in-process cluster and registers cleanup. Defined here, accessible from every file in package tests.
func newCluster(t *testing.T, count int) *scenarios.Cluster {
	t.Helper()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("n%d", i + 1)
	}
	c, err := scenarios.newCluster(ids, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func(){
		c.Stop(); c.Cleanup()
	})
	require.NoError(t, c.Start())
	return c
}


func TestBasicElection(t *testing.T) {
	c := newCluster(t, 3)

	leaderID, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err, "not leader within %v", electionWait)
	assert.NotEmpty(t, leaderID)

	// Raft safety: exact one leader at any time
	leaderCount := 0
	for _, n := range c.Nodes {
		if n.IsLeader() {
			leaderCount++
		}
	}
	assert.Equal(t, 1, leaderCount, "expected only one leader")
}


func TestReElectionOnLeaderDeath(t * testing.T) {
	c := newCluster(t, 3)

	firstLeader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	c.Injector.CrashNode(firstLeader)

	newLeader, err := scenarios.WaitForLeader(c.Nodes, electionWait * 2)
	require.NoError(t, err, "no new leader after crashing %s", firstLeader)
    assert.NotEqual(t, firstLeader, newLeader)
}


func TestNoElectionWithMinority(t *testing.T) {
	// 1 node out of 3 => minority. It must NEVER become leader.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	// duration=0 means permanent partition until Heal() is called
	c.Injector.PartitionNode(leader, 0)

	// Give it time to attempt (and fail) elections
	time.Sleep(electionWait * 2)

	assert.False(t, c.Nodes[leader].IsLeader(), "isolated minority must not become leader — quorum unreachable")
}


func TestPreVotePreventDisruption(t *testing.T) {
	// A reconnecting follower must NOT bump the term and disrupt the stable cluster.
	// Without pre-vote: follower bumps term on reconnect → forces leader to step down → needless re-election.
	// With pre-vote: follower asks "would you vote for me?" → peers say no (leader is alive) → no term bump.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)
	termBefore := c.Nodes[leader].Status().Term

	var follower string
	for id := range c.Nodes {
		if id != leader {
			follower = id
			break
		}
	}

	// Partition a follower for 500ms — it will try elections but cannot win
	c.Injector.PartitionNode(follower, 500*time.Millisecond)
	time.Sleep(700 * time.Millisecond) // let partition expire and network settle

	// Cluster must still have a leader
	_, err = scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err, "cluster lost stability during follower partition/heal")

	// Term must not have jumped — if it had, pre-vote failed to prevent disruption
	termAfter := c.Nodes[leader].Status().Term
	assert.LessOrEqual(t, termAfter, termBefore+1, "pre-vote must prevent unnecessary term bumps when follower reconnects")
}

func TestVotingIdempotency(t *testing.T) {
	// If a node could vote twice in the same term, two leaders could exist simultaneously.
	// This verifies that invariant holds after a clean election.
	c := newCluster(t, 3)

	_, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	leaderCount := 0
	for _, n := range c.Nodes {
			if n.IsLeader() {
					leaderCount++
			}
	}
	assert.Equal(t, 1, leaderCount, "two leaders elected simultaneously — voting is not idempotent")
}