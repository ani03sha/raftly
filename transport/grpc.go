package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ani03sha/raftly/raft"
	pb "github.com/ani03sha/raftly/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)


// Implements raft.Transport using gRPC. It has 2 roles:
// 1. Client: sends RPCs to peers (SendRequestVote, SendAppendEntries, etc.)
// 2. Server: receives RPCs from peers (implements pb.RaftServiceServer)
// The NetworkProxy sits in front of all outbound RPCs to enable chaos injection.
type GRPCTransport struct {
	// Embedding UnimplementedRaftServiceServer satisfies the pb.RaftServiceServer
    // interface for any RPC methods we haven't explicitly implemented.
	pb.UnimplementedRaftServiceServer

	nodeID string
	address string // this node's listen address
	peers map[string]string // peerID -> address
	proxy *NetworkProxy

	node *raft.RaftNode
	server *grpc.Server

	// Client connections are created lazily on first use and then cached
	clients map[string]pb.RaftServiceClient
	conns map[string]*grpc.ClientConn
	mu sync.RWMutex
}


// This creates a transport for this node.
func NewGRPCTransport(nodeID, address string, peers map[string]string, proxy *NetworkProxy) *GRPCTransport {
	return &GRPCTransport{
		nodeID: nodeID,
		address: address,
		peers: peers,
		proxy: proxy,
		clients: make(map[string]pb.RaftServiceClient),
		conns: make(map[string]*grpc.ClientConn),
	}
}


// Starts begin listening for incoming RPCs.
// Call this before RaftNode.Start() so the service is ready before elections begin.
func (t *GRPCTransport) Start() error {
	lis, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", t.address, err)
	}
	t.server = grpc.NewServer()
	pb.RegisterRaftServiceServer(t.server, t)
	go t.server.Serve(lis) // non-blocking: serve run in its own goroutine
	return nil
}


// Stores the Raft node reference so incoming RPCs can be dispatched. Called automatically by RaftNode.Start()
func (t *GRPCTransport) Register(node *raft.RaftNode) {
	t.node = node
}


// Shuts down the gRPC server and all outbound client connections.
func (t *GRPCTransport) Close() error {
	if t.server != nil {
		t.server.GracefulStop() // waits for in-flight RPCs to complete
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, conn := range t.conns {
		conn.Close()
	}
	return nil
}


// --- Outbound RPCs (client side )---

// Sends a RequestVote RPC to peerID. The proxy decides whether to deliver, drop, or delay the message.
func (t *GRPCTransport) SendRequestVote(ctx context.Context, peerID string, req raft.VoteRequest) (raft.VoteResponse, error) {
	deliver, delay := t.proxy.ShouldDeliver(t.nodeID, peerID)
	if !deliver {
		return raft.VoteResponse{}, fmt.Errorf("proxy: message dropped to %s", peerID)
	}
	if delay > 0 {
		time.Sleep(delay)
	}

	client, err := t.getClient(peerID)
	if err != nil {
		return raft.VoteResponse{}, err
	}

	resp, err := client.RequestVote(ctx, &pb.VoteRequest{
		Term: req.Term,
		CandidateId:  req.CandidateID,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm:  req.LastLogTerm,
		PreVote:      req.PreVote,
	})

	if err != nil {
		return raft.VoteResponse{}, err
	}

	return raft.VoteResponse{Term: resp.Term, VoteGranted: resp.VoteGranted}, nil
}


// Sends a PreVote RPC to peerID
func (t *GRPCTransport) SendPreVote(ctx context.Context, peerID string, req raft.VoteRequest) (raft.VoteResponse, error) {
	deliver, delay := t.proxy.ShouldDeliver(t.nodeID, peerID)
	if !deliver {
		return raft.VoteResponse{}, fmt.Errorf("proxy: message dropped to %s", peerID)
    }
    if delay > 0 {
        time.Sleep(delay)
	}

	client, err := t.getClient(peerID)
	if err != nil {
		return raft.VoteResponse{}, err
	}

	resp, err := client.PreVote(ctx, &pb.VoteRequest{
		Term: req.Term,
		CandidateId: req.CandidateID,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm: req.LastLogTerm,
		PreVote: req.PreVote,
	})
	if err != nil {
		return raft.VoteResponse{}, err
	}

	return raft.VoteResponse{Term: resp.Term, VoteGranted: resp.VoteGranted}, nil
}


// Sends an AppendEntries RPC to peerID
func (t *GRPCTransport) SendAppendEntries(ctx context.Context, peerID string, req raft.AppendEntriesRequest) (raft.AppendEntriesResponse, error) {
	deliver, delay := t.proxy.ShouldDeliver(t.nodeID, peerID)
	if !deliver {
			return raft.AppendEntriesResponse{}, fmt.Errorf("proxy: message dropped to %s", peerID)
	}
	if delay > 0 {
			time.Sleep(delay)
	}

	client, err := t.getClient(peerID)
	if err != nil {
			return raft.AppendEntriesResponse{}, err
	}

	resp, err := client.AppendEntries(ctx, &pb.AppendEntriesRequest{
		Term:         req.Term,
		LeaderId:     req.LeaderID,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		Entries:      raftToPbEntries(req.Entries),
		LeaderCommit: req.LeaderCommit,
	})
	if err != nil {
			return raft.AppendEntriesResponse{}, err
	}
	return raft.AppendEntriesResponse{
			Term:          resp.Term,
			Success:       resp.Success,
			ConflictTerm:  resp.ConflictTerm,
			ConflictIndex: resp.ConflictIndex,
	}, nil
}


// --- Inbound RPCs (server side) ---
// These methods are called by the gRPC framework when a peer sends us an RPC.
// They convert proto types → raft types, dispatch to the node, and convert back.

func (t *GRPCTransport) RequestVote(_ context.Context, req *pb.VoteRequest) (*pb.VoteResponse, error) {
	resp := t.node.HandleRequestVote(raft.VoteRequest{
		Term:         req.Term,
		CandidateID:  req.CandidateId,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm:  req.LastLogTerm,
		PreVote:      req.PreVote,
	})
	return &pb.VoteResponse{Term: resp.Term, VoteGranted: resp.VoteGranted}, nil
}


func (t *GRPCTransport) PreVote(_ context.Context, req *pb.VoteRequest) (*pb.VoteResponse, error) {
	resp := t.node.HandlePreVote(raft.VoteRequest{
		Term:         req.Term,
		CandidateID:  req.CandidateId,
		LastLogIndex: req.LastLogIndex,
		LastLogTerm:  req.LastLogTerm,
		PreVote:      req.PreVote,
	})
	return &pb.VoteResponse{Term: resp.Term, VoteGranted: resp.VoteGranted}, nil
}

func (t *GRPCTransport) AppendEntries(_ context.Context, req *pb.AppendEntriesRequest) (*pb.AppendEntriesResponse, error) {
	resp := t.node.HandleAppendEntries(raft.AppendEntriesRequest{
		Term:         req.Term,
		LeaderID:     req.LeaderId,
		PrevLogIndex: req.PrevLogIndex,
		PrevLogTerm:  req.PrevLogTerm,
		Entries:      pbToRaftEntries(req.Entries),
		LeaderCommit: req.LeaderCommit,
	})
	return &pb.AppendEntriesResponse{
		Term:          resp.Term,
		Success:       resp.Success,
		ConflictTerm:  resp.ConflictTerm,
		ConflictIndex: resp.ConflictIndex,
	}, nil
}


// --- Helpers ---

// Returns a gRPC client for peerID, creating one lazily if needed.
// Uses double-checked locking: fast RLock path for the common case (client exists),
// slow Lock path only when creating a new connection.
func (t *GRPCTransport) getClient(peerID string) (pb.RaftServiceClient, error) {
	// Fast path - client already exists
	t.mu.RLock()
	client, ok := t.clients[peerID]
	t.mu.RUnlock()
	if ok {
		return client, nil
	}

	// Slow path - create a new connection
	t.mu.Lock()
	defer t.mu.Unlock()
	// Re-check after acquiring write lock: another goroutine may have created it between our RUnlock and Lock. 
	// Without this check we'd create duplicate connections.
	if client, ok := t.clients[peerID]; ok {
		return client, nil
	}

	addr, ok := t.peers[peerID]
	if !ok {
		return nil, fmt.Errorf("unknown peer: %s", peerID)
	}

	// Create a connection pool lazily - it doesn't block or fail if the peer is not reachable yet.
	// The first RPC will attempt the connection.
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),)
	if err != nil {
		return nil, fmt.Errorf("create client for %s (%s): %w", peerID, addr, err)
	}
	t.conns[peerID] = conn
	client = pb.NewRaftServiceClient(conn)
	t.clients[peerID] = client
	return client, nil
}


// Converts internal LogEntry slice to proto LogEntry slice
func raftToPbEntries(entries []raft.LogEntry) []*pb.LogEntry {
	out := make([]*pb.LogEntry, len(entries))
	for i, e := range entries {
		out[i] = &pb.LogEntry{
			Index: e.Index,
			Term: e.Term,
			Type: int32(e.Type),
			Data: e.Data,
		}
	}
	return out
}


// Converts proto LogEntry slice to internal LogEntry slice
func pbToRaftEntries(pbEntries []*pb.LogEntry) []raft.LogEntry {
	out := make([]raft.LogEntry, len(pbEntries))
	for i, e := range pbEntries {
		out[i] = raft.LogEntry{
			Index: e.Index,
			Term: e.Term,
			Type: raft.EntryType(e.Type),
			Data: e.Data,
		}
	}
	return out
}