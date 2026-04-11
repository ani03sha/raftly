package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/scenarios"
	"github.com/ani03sha/raftly/transport"
)


// BenchmarkWALWriteSync measures the critical path for every Raft proposal:
// one entry serialized + buffered write + fsync to disk.
func BenchmarkWALWriteSync(b *testing.B) {
	wal, err := raft.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer wal.Close()

	entry := raft.LogEntry{Term: 1, Type: raft.EntryNormal, Data: make([]byte, 64)}
	b.SetBytes(int64(len(entry.Data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		entry.Index = uint64(i + 1)
		if err := wal.SaveEntry(entry); err != nil {
			b.Fatal(err)
		}
		if err := wal.Sync(); err != nil {
			b.Fatal(err)
		}
	}
}


// BenchmarkWALBatchWrite measures batched writes: 10 entries per Sync.
// This is the pattern used when a follower catches up from a replay.
func BenchmarkWALBatchWrite(b *testing.B) {
	wal, err := raft.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer wal.Close()

	const batchSize = 10
	batch := make([]raft.LogEntry, batchSize)
	for i := range batch {
		batch[i] = raft.LogEntry{Term: 1, Type: raft.EntryNormal, Data: make([]byte, 64)}
	}
	b.SetBytes(64 * batchSize)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j := range batch {
			batch[j].Index = uint64(i*batchSize + j + 1)
		}
		if err := wal.SaveEntries(batch); err != nil {
			b.Fatal(err)
		}
		if err := wal.Sync(); err != nil {
			b.Fatal(err)
		}
	}
}


// BenchmarkLogAppend measures in-memory log append throughput.
// No disk I/O — this is the pure CPU cost of maintaining the slice-backed log.
func BenchmarkLogAppend(b *testing.B) {
	log := raft.NewRaftLog()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = log.Append([]raft.LogEntry{
			{Index: uint64(i + 1), Term: 1, Type: raft.EntryNormal, Data: []byte("payload")},
		})
	}
}


// BenchmarkSingleNodePropose measures end-to-end propose latency with no peers.
// This isolates WAL + commit latency from network round-trip cost.
func BenchmarkSingleNodePropose(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "node")
	cfg := raft.DefaultConfig("bench")
	cfg.DataDir = dir
	cfg.Peers = nil

	proxy := transport.NewNetworkProxy()
	registry := transport.NewInMemRegistry()
	tr := transport.NewInMemTransport("bench", proxy, registry)

	node, err := raft.NewRaftNode(cfg, tr)
	if err != nil {
		b.Fatal(err)
	}
	if err := node.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(node.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for !node.IsLeader() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !node.IsLeader() {
		b.Fatal("self-election timeout")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	go func() {
		for {
			select {
			case <-node.CommitCh():
			case <-ctx.Done():
				return
			}
		}
	}()

	payload := make([]byte, 64)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, _, err := node.Propose(payload); err != nil {
			b.Fatal(err)
		}
	}
}


// BenchmarkThreeNodePropose measures end-to-end propose latency with 2 followers.
// Each commit requires an AppendEntries round-trip to reach quorum.
// This is the realistic production latency.
func BenchmarkThreeNodePropose(b *testing.B) {
	ids := []string{"b1", "b2", "b3"}
	c, err := scenarios.NewCluster(ids, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { c.Stop(); c.Cleanup() })
	if err := c.Start(); err != nil {
		b.Fatal(err)
	}

	leaderID, err := scenarios.WaitForLeader(c.Nodes, 2*time.Second)
	if err != nil {
		b.Fatal(err)
	}
	leader := c.Nodes[leaderID]

	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	for _, n := range c.Nodes {
		n := n
		go func() {
			for {
				select {
				case <-n.CommitCh():
				case <-ctx.Done():
						return
				}
			}
			}()
	}

	payload := make([]byte, 64)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, _, err := leader.Propose(payload); err != nil {
			b.Fatal(fmt.Sprintf("propose %d: %v", i, err))
		}
	}
}