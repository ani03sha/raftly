/**
 * Scenarios run in-process without real gRPC. The in-memory transport directly calls node handler methods.
 * Same proxy rules, zero network overhead.
*/
package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ani03sha/raftly/raft"
)


// This is a shared directory of all nodes in an in-process cluster.
// Every InMemTransport holds a pointer to some registry instance.
type InMemRegistry struct {
	nodes map[string]*raft.RaftNode
	mu synce.RWMutex
}


func NewInMemRegistry() *InMemRegistry {
	return &InMemRegistry{nodes: make(map[string]*raft.RaftNode)}
}


func (r *InMemRegistry) add(id string, node *raft.RaftNode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = node
}


func (r *InMemRegistry) get(id string) (*raft.RaftNode, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return n, ok
}


// This implements raft.Transport without any network. Instead of gRPC calls, it looks up the target node in the
// registry and calls its handler methods directly. The proxy still applies - chaos injection works identically.
type InMemTransport struct {
	nodeID string
	proxy *NetworkProxy
	registry *InMemRegistry
}


func NewInMemTransport(nodeID string, proxy *NetworkProxy, registry *InMemRegistry) *InMemTransport {
	return &InMemTransport{nodeID: nodeID, proxy: proxy, registry: registry}
}


// This is called by RaftNode.Start(). It publishes this node to registry so peers can send it messages.
func (t *InMemTransport) Register(node *raft.RaftNode) {
	t.registry.add(t.nodeID, node)
}


func (t *InMemTransport) Close() error {
	return nil
}


func (t *InMemTransport) SendRequestVote(ctx context.Context, peerID string, req raft.VoteRequest) (raft.VoteResponse, error) {
	if err := t.applyProxy(ctx, peerID); err != nil {
		return raft.VoteResponse, err
	}
	peer, ok := t.registry.get(peerID)
	if !ok {
		return raft.VoteResponse{}, fmt.Errorf("peer %s is not available", peerID)
	}
	return peer.HandleRequestVote(req), nil
}


func (t *InMemTransport) SendPreVote(ctx context.Context, peerID string, req raft.VoteRequest) (raft.VoteResponse, error) {
	if err := t.applyProxy(ctx, peerID); err != nil {
		return raft.VoteResponse{}, err
	}
	peer, ok := t.registry.get(peerID)
	if !ok {
		return raft.VoteResponse{}, fmt.Errorf("peer %s not available", peerID)
	}
	return peer.HandlePreVote(req), nil
}


func (t *InMemTransport) SendAppendEntries(ctx context.Context, peerID string, req raft.AppendEntriesRequest) (raft.AppendEntriesResponse, error) {
	if err := t.applyProxy(ctx, peerID); err != nil {
		return raft.AppendEntriesResponse{}, err
	}
	peer, ok := t.registry.get(peerID)
	if !ok {
		return raft.AppendEntriesResponse{}, fmt.Errorf("peer %s not available", peerID)
	}
	return raft.HandleAppendEntries(req), nil
}



// Checks the proxy rules and sleeps for any configured delay. Returns an error if the message should be dropped.
func (t *InMemTransport) applyProxy(ctx context.Context, peerID string) error {
	deliver, delay := t.proxy.ShouldDeliver(t.nodeID, peerID)
	if !deliver {
		return fmt.Errorf("proxy: message to %s dropped", peerID)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}