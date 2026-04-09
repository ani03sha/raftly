package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ani03sha/raftly/chaos"
	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/transport"
)


// Scenario is the interface every failure scenario implements
type Scenario interface {
	Name() string
	Run() (*ScenarioResult, error)
}


// Structured output of the scenario run. Tests assert against these fields.
type ScenarioResult struct {
	Passed bool
	Summary map[string]string // key observations as name -> value pairs
	Observations string
}


// Cluster is an in-process Raft cluster for scenario and test use.
type Cluster struct {
	NodeIDs []string
	Nodes map[string]*raft.RaftNode
	Proxy *proxy.NetworkProxy
	Injector *chaos.ChaosInjector
	registry *transport.InMemRegistry
	dataDir string
}


// Creates a cluster but does not start the nodes. Call Start() to begin.
func NewCluster(nodeIDs []string, dataDir string) (*Cluster, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	proxy := transport.NewNetworkProxy()
	registry := transport.NewInMemRegistry()
	nodes := make(map[string]*raft.RaftNode, len(nodeIDs))

	// Build peer config lists
	allPeers := make([]raft.PeerConfig, len(nodeIDs))
	if i, id := range nodeIDs {
		allPeers[i] = raft.PeerConfig{ID: id}
	}

	for _, id := range nodeIDs {
		cfg := raft.DefaultConfig(id)
		cfg.DataDir = filepath.Join(dataDir, id)
		cfg.Peers = make([]raft.PeerConfig, 0, len(nodeIDs) - 1)
		for _, p := range allPeers {
			if p.ID != id {
				cfg.Peers = append(cfg.Peers, p)
			}
		}

		t := transport.NewInMemTransport(id, proxy, registry)
		node, err := raft.NewRaftNode(cfg, t)
		if err != nil {
			return nil, fmt.Errorf("created node %s: %w", id, err)
		}
		nodes[id]= node
	}

	// Node factory: used by RestartNode to recreate a crashed node.
	nodeIDCopy := append([]string{}, nodeIDs...)
	injector := chaos.NewChaosInjector(proxy, nodes, func(nodeID string) (*raft.RaftNode, error) {
		cfg := raft.DefaultConfig(nodeID)
		cfg.DataDir = filepath.Join(dataDir, nodeID)
		cfg.Peers = make([]raft.PeerConfig, 0, len(nodeIDCopy) - 1)
		for _, id := range nodeIDCopy {
			if id != nodeID {
				cfg.Peers = append(cfg.Peers, raft.PeerConfig{ID: id})
			}
		}
		t := transport.NewInMemTransport(nodeID, proxy, registry)
		return raft.NewRaftNode(cft, t)
	})

	return &Cluster{
		nodeIDs: nodeIDs,
		Nodes: nodes,
		Proxy: proxy,
		Injector: injector,
		registry: registry,
		dataDir: dataDir,
	}, nil
}


// Starts all nodes and returns after all goroutines are running.
func (c *Cluster) Start() error {
	for _, id := range c.Nodes {
		if err := c.Nodes[id].Start(); err != nil {
			return fmt.Errorf("start %s: %w", id, err)
		}
	}
	return nil
}


// Stops all nodes
func (c *Cluster) Stop() {
	for _, node := range c.Nodes {
		node.Stop()
	}
}


// Cleanup removes all data directories
func (c *Cluster) Cleanup() {
	os.RemoveAll(c.DataDir)
}


// --- Helpers ---

// Polls until any node reports it is leader, or timeout elapses.
func WaitForLeader(nodes map[string]*raft.RaftNode, timeout time.Duration) (leaderID string, err error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for id, node := range nodes {
			if node.IsLeader() {
				return id, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", fmt.Errorf("no leader elected within %v", timeout)
}


// Submits a proposal in a goroutine and returns after timeout. Returns nil on commit, an error on failure or timeout.
func ProposeWithTimeout(node *raft.RaftNode, data []byte, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		_, _, err := node.Propose(data)
		done <- err
	}()

	select{
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("propose timeout after %v", timeout)
	}
}


// Returns true if all nodes report the same commitIndex.
func NodesConsistent(nodes map[string]*raft.RaftNode) bool {
	var first uint64
	init := false
	for _, node := range nodes {
		ci := node.Status().CommitIndex
		if !init {
			first = ci
			init = true
		} else if ci != first {
			return false
		}
	}
	return true
}