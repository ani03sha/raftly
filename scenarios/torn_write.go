package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/transport"
)


// This simulates the process crash mid-WAL write. A garbage suffix is appended to the WAL file 
// (as would happen if the OS flushed a partial write before the process was killed).
// On restart, the WAL must detect the corruption and recover cleanly.
type TornWriteScenario struct {
	DataDir string
}


func (s *TornWriteScenario) Name() string {
	return "wal-torn-write"
}


func (s *TornWriteScenario) Run() (*ScenarioResult, error) {
	result := &ScenarioResult{Summary: make(map[string]string)}
	dataDir := filepath.Join(s.DataDir, "torn-write-node")

	if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }
    defer os.RemoveAll(s.DataDir)

	// 1. Create and start a single-node cluster. A single node forms its own quorum - it elects itself and can commit.
	proxy := transport.NewNetworkProxy()
	registry := transport.NewInMemRegistry()

	cfg := raft.DefaultConfig("node1")
	cfg.DataDir = dataDir
	cfg.Peers = nil // no peers - single node cluster

	t := transport.NewInMemTransport("node1", proxy, registry)
	node, err := raft.NewRaftNode(cfg, t)
	if err != nil {
		return nil, err
	}
	if err := node.Start(); err != nil {
		return nil, err
	}

	// 2. Wait for self-election
	deadline := time.Now().Add(2 * time.Second)
	for !node.IsLeader() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !node.IsLeader() {
		return nil, fmt.Errorf("single node did not elect itself as a leader")
	}

	// 3. Write 5 entries. All commit (single-node quorum)
	for i := 1; i <= 5; i++ {
		if err := ProposeWithTimeout(node, []byte(fmt.Sprintf("good-%d", i)), 500 * time.Millisecond); err != nil {
			return nil, fmt.Errorf("write %d failed: %w", i, err)
		}
	}
	goodCommit := node.Status().CommitIndex
	result.Summary["committed-before-crash"] = fmt.Sprintf("%d", goodCommit)

	// 4. Stop the node cleanly (simulate crash point). In production this would be os.Kill(os.Getpid(), syscall.SIGKILL)
	// between wal.SaveEntry and wal.Sync
	node.Stop()

	// 5. Simulate a torn write: append garbage bytes to the WAL file. This mimics what happens when a partial record is
	// written to the OS page cache but the process is killed before fsync completes.
	walPath := node.WALPath()
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open WAL for corruption: %w", err)
	}
	// Write partial record: valid-looking length prefix but garbage data. CRC32 will not match, so ReadAll discards this
	// and everything after.
	garbage := []byte{0x00, 0x00, 0x00, 0x10, 0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}
	if _, err := f.Write(garbage); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	result.Summary["garbage_bytes_written"] = fmt.Sprintf("%d", len(garbage))

	// 6. Restart the node
	t2 := transport.NewInMemTransport("node1", proxy, registry)
	recovered, err := raft.NewRaftNode(cfg, t2)
	if err != nil {
		return nil, fmt.Errorf("recovery failed: %w", err)
	}
	if err := recovered.Start(); err != nil {
		return nil, err
	}

	// 7. Wait for self-election process
	deadline = time.Now().Add(2 * time.Second)
	for !recovered.IsLeader() && time.Now().Before(deadline) {
        time.Sleep(10 * time.Millisecond)
    }

	// 8. Verify: all good entries are present, no corrupt entry visible
	recoveredLogIndex := recovered.Status().LogIndex
    result.Summary["log_index_after_recovery"] = fmt.Sprintf("%d", recoveredLogIndex)
    result.Summary["expected_log_index"] = fmt.Sprintf("%d", goodCommit)

	// The log index after recovery must equal what we committed before the crash.
	// The garbage entry must NOT appear.
	result.Passed = recoveredLogIndex == goodCommit
	result.Observations = fmt.Sprintf(
			"Committed before crash: %d\nLog index after recovery: %d\nCRC32 torn write detected and truncated: %v",
			goodCommit, recoveredLogIndex, recoveredLogIndex == goodCommit)

	recovered.Stop()
	return result, nil
}