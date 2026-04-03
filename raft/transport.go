package raft

import "context"


// Transport is the network abstraction Raft uses to send RPCs to peers.
type Transport interface {
	// Asks a peer to vote for us in the election
	SendRequestVote(ctx context.Context, peerID string, req VoteRequest) (VoteResponse, error)

	// Asks a peer if they would vote for us (dry run before real election)
	SendPreVote(ctx context.Context, peerID string, req VoteRequest) (VoteResponse, error)

	// Sends log entries (or a heartbeat) to a follower.
	SendAppendEntries(ctx context.Context, peerID string, req AppendEntriesRequest) (AppendEntriesResponse, error)

	// Connects this node to the transport so incoming RPCs are delivered to it.
	Register(node *RaftNode)

	// Shuts down the transport cleanly
	Close() error
}