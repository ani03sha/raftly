package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/scenarios"
	"github.com/ani03sha/raftly/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


// creates and starts a single-node cluster (self-quorum, no peers).
// Caller is responsible for calling node.Stop() when done.
func startSingleNode(t *testing.T, id, dataDir string) *raft.RaftNode {
	t.Helper()
	cfg := raft.DefaultConfig(id)
	cfg.DataDir = dataDir
	cfg.Peers = nil // no peers → self is quorum

	proxy := transport.NewNetworkProxy()
	registry := transport.NewInMemRegistry()
	tr := transport.NewInMemTransport(id, proxy, registry)

	node, err := raft.NewRaftNode(cfg, tr)
	require.NoError(t, err)
	require.NoError(t, node.Start())
	return node
}


// Blocks until the single node self-elects as leader.
func waitLeader(t *testing.T, node *raft.RaftNode) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if node.IsLeader() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("single node did not self-elect within 2s")
}


func TestWALSurvivesCrash(t *testing.T) {
	// Write entries, stop the node (simulate crash), restart from the same WAL.
	// All committed entries must be recovered.
	dataDir := filepath.Join(t.TempDir(), "node")

	node := startSingleNode(t, "n1", dataDir)
	waitLeader(t, node)

	for i := 0; i < 10; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(node, []byte("entry"), time.Second))
	}
	logBefore := node.Status().LogIndex
	node.Stop()

	// Restart from the same dataDir — NewRaftNode calls WAL.ReadAll() internally
	recovered := startSingleNode(t, "n1", dataDir)
	defer recovered.Stop()
	waitLeader(t, recovered)

	assert.Equal(t, logBefore, recovered.Status().LogIndex, "WAL recovery must restore all %d entries", logBefore)
}


func TestWALChecksumDetectsCorruption(t *testing.T) {
	// After stopping the node, append garbage to the WAL file.
	// On restart, the CRC32 mismatch must be detected and the corrupt tail discarded.
	// This simulates an OS partial flush before process kill.
	dataDir := filepath.Join(t.TempDir(), "node")

	node := startSingleNode(t, "n1", dataDir)
	waitLeader(t, node)

	for i := 0; i < 5; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(node, []byte("good"), time.Second))
	}
	goodCommit := node.Status().CommitIndex
	walPath := node.WALPath()
	node.Stop()

	// Append garbage — looks like a valid length prefix but has wrong CRC
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_APPEND, 0644)
	require.NoError(t, err)
	_, err = f.Write([]byte{0x00, 0x00, 0x00, 0x10, 0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE})
	require.NoError(t, err)
	f.Close()

	// Restart — ReadAll must stop at the corruption boundary
	recovered := startSingleNode(t, "n1", dataDir)
	defer recovered.Stop()
	waitLeader(t, recovered)

	assert.Equal(t, goodCommit, recovered.Status().LogIndex, "corrupted tail must be discarded; only %d good entries should survive", goodCommit)
}


func TestWALTornWrite(t *testing.T) {
	// Delegates to TornWriteScenario — the definitive torn-write test.
	// It commits 5 entries, appends a partial garbage record, restarts,
	// and verifies the log index after recovery equals the good commit count.
	s := &scenarios.TornWriteScenario{DataDir: t.TempDir()}
	result, err := s.Run()
	require.NoError(t, err)
	assert.True(t, result.Passed, result.Observations)
}


func TestWALTruncation(t *testing.T) {
	// WAL truncation is exercised when a partitioned follower reconnects and the leader replays entries the follower missed.
	// Verifies the follower's WAL correctly appends (and in conflict cases, truncates + rewrites).
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

	// Write 10 entries — all replicated to follower
	for i := 0; i < 10; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("before"), time.Second))
	}
	time.Sleep(50 * time.Millisecond)

	// Partition follower — it will miss entries 11–20
	c.Injector.PartitionNode(follower, 0)
	for i := 0; i < 10; i++ {
		require.NoError(t, scenarios.ProposeWithTimeout(c.Nodes[leader], []byte("during"), time.Second))
	}

	// Heal — leader replays missing entries, follower appends them to WAL
	c.Injector.Heal()
	time.Sleep(500 * time.Millisecond)

	assert.True(t, scenarios.NodesConsistent(c.Nodes))
	assert.Equal(t, uint64(20), c.Nodes[follower].Status().CommitIndex, "follower must sync all 20 entries via WAL after partition heals")
}