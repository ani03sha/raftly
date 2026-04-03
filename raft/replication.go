package raft

// Processes an incoming AppendEntries RPC from the leader.
func (n *RaftNode) handleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	// TODO
	return AppendEntriesResponse{}
}

// Sends AppendEntries to all peers (empty = heartbeat, non-empty = replication).
func (n *RaftNode) maybeSendHeartbeats() {
	// TODO
}

// Checks if any new entries can be committed based on match indexes.
func (n *RaftNode) maybeCommit() {
	// TODO
}

// Sends newly committed entries to commitCh for the application layer.
func (n *RaftNode) applyCommitted() {
	// TODO
}