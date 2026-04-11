package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
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


// Creates a 3-node in-memory cluster, waits for a leader, and drains all nodes' commitCh so applyCommitted never blocks.
// Returns the cluster and the initial leader node.
func newBenchCluster(b *testing.B) (*scenarios.Cluster, *raft.RaftNode) {
	b.Helper()
	c, err := scenarios.NewCluster([]string{"b1", "b2", "b3"}, b.TempDir())
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

	return c, c.Nodes[leaderID]
}


// Measures proposal throughput at increasing concurrency.
// With one fsync per entry, proposals serialize on disk I/O — throughput stays
// roughly flat as goroutines increase. Group commit (batching entries per fsync)
// is required to break this ceiling.
func BenchmarkConcurrentPropose(b *testing.B) {
	for _, concurrency := range []int{1, 4, 16, 64} {
		concurrency := concurrency
		b.Run(fmt.Sprintf("c%d", concurrency), func(b *testing.B) {
			_, leader := newBenchCluster(b)
			payload := make([]byte, 64)

			work := make(chan struct{}, b.N)
			for i := 0; i < b.N; i++ {
				work <- struct{}{}
			}
			close(work)

			workers := concurrency
			if workers > b.N {
				workers = b.N
			}

			b.ResetTimer()
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range work {
						if _, _, err := leader.Propose(payload); err != nil {
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}


// Records per-proposal latency and reports p50/p95/p99.
// Mean (ns/op) hides tail latency — p99 is what clients actually experience
// on the slow path.
func BenchmarkLatencyPercentiles(b *testing.B) {
	_, leader := newBenchCluster(b)
	payload := make([]byte, 64)
	latencies := make([]int64, 0, b.N)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if _, _, err := leader.Propose(payload); err != nil {
			b.Fatal(err)
		}
		latencies = append(latencies, time.Since(start).Microseconds())
	}
	b.StopTimer()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	n := len(latencies)
	b.ReportMetric(float64(latencies[n*50/100]), "p50_µs")
	b.ReportMetric(float64(latencies[n*95/100]), "p95_µs")
	b.ReportMetric(float64(latencies[n*99/100]), "p99_µs")
}


// Measures wall-clock time from leader crash to the first write accepted by the new leader. 
// This is the client-visible unavailability window during a failure.
func BenchmarkLeaderElectionTime(b *testing.B) {
	c, err := scenarios.NewCluster([]string{"e1", "e2", "e3"}, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { c.Stop(); c.Cleanup() })
	if err := c.Start(); err != nil {
		b.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	drainNode := func(n *raft.RaftNode) {
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
	for _, n := range c.Nodes {
		drainNode(n)
	}

	payload := make([]byte, 8)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		leaderID, err := scenarios.WaitForLeader(c.Nodes, 2*time.Second)
		if err != nil {
			b.Fatal("no leader before iteration:", err)
		}
		b.StartTimer()

		//start := time.Now()
		c.Injector.CrashNode(leaderID)
		elapsed, ok := firstSuccessfulWrite(c.Nodes, leaderID, payload)
		if !ok {
			b.Fatal("no new leader within 5s")
		}

		b.StopTimer()
		b.ReportMetric(float64(elapsed.Milliseconds()), "ms/election")

		// Restart and drain the new instance so it doesn't block on commitCh.
		c.Injector.RestartNode(leaderID)
		drainNode(c.Nodes[leaderID])
		time.Sleep(300 * time.Millisecond) // allow rejoin as follower
	}
}


// Polls all nodes except excludeID until one accepts a write.
func firstSuccessfulWrite(nodes map[string]*raft.RaftNode, excludeID string, payload []byte) (time.Duration, bool) {
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for id, node := range nodes {
			if id == excludeID {
				continue
			}
			if _, _, err := node.Propose(payload); err == nil {
				return time.Since(start), true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 5 * time.Second, false
}