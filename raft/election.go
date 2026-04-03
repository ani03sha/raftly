package raft

// Starts a pre-vote or real election.
func (n *RaftNode) campaign(preVote bool) {
	// TODO
}

// Processes an incoming vote request from a candidate.
func (n *RaftNode) handleRequestVote(req VoteRequest) VoteResponse {
	// TODO
	return VoteResponse{}
}

// Processes an incoming pre-vote request.
func (n *RaftNode) handlePreVote(req VoteRequest) VoteResponse {
	// TODO
	return VoteResponse{}
}