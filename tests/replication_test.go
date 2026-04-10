package tests

import (
	"testing"
	"time"

	"github.com/ani03sha/raftly/scenarios"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func TestBasicReplication(t *testing.T) {
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
        require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("data"), 2 * time.Second))
    }

	time.Sleep(100 * time.Millisecond) // let followers apply

	assert.True(t, scenarios.NodesConsistent(c.Nodes))
	for id, n := range c.Nodes {
		assert.Equal(t, uint64(10), n.Status().CommitIndex,
			"node %s: wrong commit index", id)
	}
}


func TestLogConsistency(t *testing.T) {
	// 100 sequential writes — all nodes must converge to the same log.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("x"), 2*time.Second))
	}
	time.Sleep(100 * time.Millisecond)

	assert.True(t, scenarios.NodesConsistent(c.Nodes))
	for _, n := range c.Nodes {
		assert.Equal(t, uint64(100), n.Status().CommitIndex)
	}
}


func TestReplicationWithStragglers(t *testing.T) {
	// A follower with 80ms message delay must catch up after the delay expires.
	// This tests that the leader's retry loop correctly handles slow followers.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	var straggler string
	for id := range c.Nodes {
		if id != leader {
			straggler = id
			break
		}
	}

	// 80ms delay for 2 seconds — longer than the heartbeat interval (50ms)
	// so the straggler can still make progress, just slowly
	c.Injector.InjectDelay(straggler, 80, 0, 2*time.Second)

	for i := 0; i < 20; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("data"), 2*time.Second))
	}

	time.Sleep(3 * time.Second) // wait for delay to expire + straggler to sync

	assert.True(t, scenarios.NodesConsistent(c.Nodes), "straggler did not catch up after delay expired")
}


func TestCommitOnlyCurrentTerm(t *testing.T) {
	// A leader must not directly commit entries from a previous term.
	// If violated: an isolated leader's uncommitted writes would survive after healing
	// and overwrite entries committed by the new quorum — data loss.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("baseline"), time.Second))
	}
	time.Sleep(50 * time.Millisecond)
	baselineCommit := c.Nodes[leader].Status().CommitIndex

	// Isolate the leader — it can no longer reach quorum
	c.Injector.PartitionNode(leader, 300*time.Millisecond)

	// These timeout: the isolated leader accepts them locally but cannot commit
	for i := 0; i < 3; i++ {
		_ = scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("isolated"), 200*time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond) // let partition heal
	time.Sleep(electionWait)           // let new leader stabilize with a no-op

	for id, n := range c.Nodes {
		ci := n.Status().CommitIndex
		// +1 allows for the new leader's no-op commit entry
		assert.LessOrEqual(t, ci, baselineCommit+1, "node %s: violated — isolated entries survived partition healing", id)
	}
	assert.True(t, scenarios.NodesConsistent(c.Nodes))
}


func TestConflictResolution(t *testing.T) {
	// A partitioned follower that missed entries must sync cleanly after reconnecting.
	// The leader uses the fast-backtracking (ConflictTerm/ConflictIndex) to catch it up.
	c := newCluster(t, 3)

	leader, err := scenarios.WaitForLeader(c.Nodes, electionWait)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("pre"), time.Second))
	}
	time.Sleep(50 * time.Millisecond)

	var follower string
	for id := range c.Nodes {
		if id != leader {
			follower = id
			break
		}
	}

	// Partition follower — it will miss entries 11–20
	c.Injector.PartitionNode(follower, 0)

	for i := 0; i < 10; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("post"), time.Second))
	}

	// Heal — leader detects gap and replays missing entries to the follower
	c.Injector.Heal()
	time.Sleep(500 * time.Millisecond)

	assert.True(t, scenarios.NodesConsistent(c.Nodes))
	assert.Equal(t, uint64(20), c.Nodes[follower].Status().CommitIndex, "follower must sync all 20 entries after partition heals")
}