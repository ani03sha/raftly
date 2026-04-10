package tests

import (
	"testing"
	"time"

	"github.com/ani03sha/raftly/scenarios"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func TestMinorityProgressNoProgress(t *testing.T) {
	// An isolated leader (minority of 1) must make zero commit progress.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("baseline"), time.Second))
	}
	time.Sleep(50 * time.Millisecond)
	baselineCommit := c.Nodes[leader].Status().CommitIndex

	// Isolate the leader — it becomes a minority of 1
	c.Injector.PartitionNode(leader, 0)
	time.Sleep(100 * time.Millisecond)

	// This must time out — no quorum reachable
	err = scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("isolated"), 500 * time.Millisecond)
	assert.Error(t, err, "minority must not commit any writes")

	// Commit index must not have advanced
	assert.Equal(t, baselineCommit, c.Nodes[leader].Status().CommitIndex)
}


func TestMajorityPartitionProgress(t *testing.T) {
	// The majority (2 of 3) must continue committing while the minority is isolated.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	// Isolate one follower (minority of 1)
	var follower string
	for id := range c.Nodes {
		if id != leader {
			follower = id
			break
		}
	}
	c.Injector.PartitionNode(follower, 0)

	// Leader + remaining follower = quorum of 2 — must still commit
	for i := 0; i < 10; i++ {
		err := scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("data"), time.Second)
		require.NoError(t, err, "majority (2 of 3) must continue committing writes")
	}
}


func TestPartitionHealRecovery(t *testing.T) {
	// After a partition heals, the isolated follower must catch up completely.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	var follower string
	for id := range c.Nodes {
		if id != leader {
			follower = id
			break
		}
	}

	c.Injector.PartitionNode(follower, 0)

	for i := 0; i < 20; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("during"), time.Second))
	}

	c.Injector.Heal()
	time.Sleep(500 * time.Millisecond)

	assert.True(t, scenarios.NodesConsistent(c.Nodes), "nodes must converge after partition heals")
	assert.Equal(t, uint64(20), c.Nodes[follower].Status().CommitIndex, "isolated follower must sync all 20 missed entries")
}


func TestLeaderIsolation(t *testing.T) {
	// Isolated leader's writes must not commit — quorum is unreachable.
	// After healing, those writes must be discarded (they were never committed).
	// This is the core of the AWS 2011 EBS split-brain defense.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("baseline"), time.Second))
	}
	time.Sleep(50 * time.Millisecond)

	// Isolate the leader for 500ms
	c.Injector.PartitionNode(leader, 500*time.Millisecond)

	// Count how many of these actually commit (they should not)
	committed := 0
	for i := 0; i < 5; i++ {
		if err := scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("isolated"), 200*time.Millisecond); err == nil {
			committed++
		}
	}

	// Wait for partition to heal + new leader to stabilize
	time.Sleep(600 * time.Millisecond)
	time.Sleep(electionWait)

	assert.Equal(t, 0, committed, "isolated leader must not commit any writes — no quorum available")
	assert.True(t, scenarios.NodesConsistent(c.Nodes), "all nodes must converge after leader isolation heals")
}