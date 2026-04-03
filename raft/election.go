package raft

import (
	"context"
	"time"
)


// Returns true if a candidate's log is at least up-to-date as the voter's log.
// Rule: compare last log term first. If equal, compare log length.
// A longer log in a higher term always wins.
func logIsUpToDate(candidateLastTerm, candidateLastIndex, myLastTerm, myLastIndex uint64) bool {
	if candidateLastTerm != myLastTerm {
		return candidateLastTerm > myLastTerm
	}
	return candidateLastIndex >= myLastIndex
}

// Runs either a pre-vote phase or a real election.
// Pre-vote: ask peers "would you vote for me in term+1?" without changing any state.
//  		 Only if quorum says yes, proceed to a real election.
// Real:     increment term, vote for self (persisted), ask for votes, become leader if won.
func (n *RaftNode) campaign(preVote bool) {
	n.mu.Lock()

	// If we somehow become leader already (e.g., concurrent campaign), bail out
	if n.state == Leader {
		n.mu.Unlock()
		return
	}

	var campaignTerm uint64
	if preVote {
		n.state = Candidate
		campaignTerm = n.currentTerm + 1
		// Pre-Vote uses term + 1 in request but doesn't persist or increment yet
	} else {
		// becomeCandidate increments term, votes for self, persists to WAL - atomically
		// It is called with n.mu held
		n.becomeCandidate()
		campaignTerm = n.currentTerm
	}

	// Snapshot what we need from the locked state before releasing
	lastLogIndex := n.log.LastIndex()
	lastLogTerm := n.log.LastTerm()
	peers := make([]string, 0, len(n.peers))
	for id := range n.peers {
		peers = append(peers, id)
	}
	n.mu.Unlock()

	// Single node cluster - win immediately without any RPCs.
	if len(peers) == 0 {
		n.mu.Lock()
		if preVote {
			n.mu.Unlock()
			n.campaign(false) // prev-vote passed, now to the real election
		} else {
			n.becomeLeader()
			n.mu.Unlock()
		}
		return
	}

	// Send vote request to all peers in parallel.
	// Each peer gets its own goroutine; all results flow into voteCh.
	// The channel is buffered so goroutines never block even if we stop reading.
	voteCh := make(chan VoteResponse, len(peers))
	for _, peerID := range peers {
		go func(peerID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			req := VoteRequest{
				Term: campaignTerm,
				CandidateID: n.id,
				LastLogIndex: lastLogIndex,
				LastLogTerm: lastLogTerm,
				PreVote: preVote,
			}

			var resp VoteResponse
			var err error
			if preVote {
				resp, err = n.transport.SendPreVote(ctx, peerID, req)
			} else {
				resp, err = n.transport.SendRequestVote(ctx, peerID, req)
			}
			if err != nil {
				// Network failure counts as a "no" - Raft handles retries via the timer.
				resp = VoteResponse{VoteGranted: false}
			}
			voteCh <-resp
		}(peerID)
	}

	// Tally results. Start at 1 - we always vote for ourselves.
	votes := 1
	needed := n.quorum()

	for range peers {
		resp := <-voteCh

		n.mu.Lock()

		// Abort if our state changed while we were collecting votes.
		// This happens when we received a valid heartbeat and stepped down.
		if preVote && n.state != PreCandidate {
			n.mu.Unlock()
			return
		}
		if !preVote && (n.state != Candidate || n.currentTerm != campaignTerm) {
			n.mu.Unlock()
			return
		}

		// If any peer reports a higher term, we are stale - step down immediately.
		// Any server that sees a higher term converts to follower.
		if resp.Term > n.currentTerm {
			n.becomeFollower(resp.Term, "")
			n.mu.Unlock()
			return
		}

		n.mu.Unlock()

		if resp.VoteGranted {
			votes++
		}

		if votes >= needed {
			// Quorum reached - we won.
			n.mu.Lock()
			if preVote {
				// Pre-vote quorum means the cluster has no healthy leader.
				// Safe to proceed with a real election now.
				n.mu.Unlock()
				n.campaign(false)
			} else {
				n.becomeLeader()
				n.mu.Unlock()
			}
			return
		}
	}
	// Failed to reach quorum. Step back to follower and wait for the next timeout.
	n.mu.Lock()
	if n.state == Candidate || n.state == PreCandidate {
		n.becomeFollower(n.currentTerm, "")
	}
	n.mu.Unlock()
}

// Processes an incoming RequestVote RPC from a candidate.
// Called by the transport layer when a peer sends us the vote request.
func (n *RaftNode) handleRequestVote(req VoteRequest) VoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	// If the request comes from a higher term, update our term first.
	// This could cause us to step down if we were a candidate or a leader
	if req.Term > n.currentTerm {
		n.becomeFollower(n.currentTerm, "")
	}

	// Reject votes from stale terms
	if req.Term < n.currentTerm {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// Two conditions must both be true to grant a vote.
	// A: We haven't voted for someone else this term.
	// (It's okay if we voted for THIS candidate - idempotent retry)
	alreadyVoted := n.votedFor != "" && n.votedFor != req.CandidateID
	if alreadyVoted {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// B. Candidate's log is at least as up-to-date as ours.
	// This is the safety rule. Without this, a node with a stale log could win,
	// became leader, and overwrite entries that we already committed.
	if !n.isLogUpToDate(req.LastLogTerm, req.LastLogIndex) {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// Grant the vote. Persist before replying - if we crash between persisting and replying,
	// we simply don't reply. The candidate retries and we will see votedFor already set, granting
	// again (idempotent). Safe.
	n.votedFor = req.CandidateID
	_ = n.wal.SaveState(n.currentTerm, n.votedFor)
	_ = n.wal.Sync()

	// Hearing from a viable candidate resets our election timer.
	n.resetElectionTimer()

	return VoteResponse{Term: n.currentTerm, VoteGranted: true}
}

// Processes an incoming PreVote RPC.
// Critical difference from handleRequestVote:
//   - We do NOT update votedFor
//   - We do NOT update currentTerm
//   - We do NOT write to the WAL
//
// We are only answering a hypothetical: "if a real election happened, would you vote for me?"
func (n *RaftNode) handlePreVote(req VoteRequest) VoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Reject if the candidate is behind us in the term
	if req.Term < n.currentTerm {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// The core pre-vote defense:
	// If we have an active leader, the cluster is healthy — deny the pre-vote.
	// A partitioned node reconnecting will find that everyone has a leaderID
	// and will be unable to get pre-vote quorum. It cannot raise the term
	// and disrupt the cluster.
	if n.leaderID != "" {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	// Same log up-to-date check as real election
	if !n.isLogUpToDate(req.LastLogTerm, req.LastLogIndex) {
		return VoteResponse{Term: n.currentTerm, VoteGranted: false}
	}

	return VoteResponse{Term: n.currentTerm, VoteGranted: true}
}


// Returns true if the candidate's log is at least as up-to-date as ours.
//
// This rule exists to preserve the following invariant: "If an entry is committed, every future 
// leader must have that entry."
//
// By refusing to vote for candidates with stale logs, we ensure only a node that has all committed 
// entries can become leader.
func (n *RaftNode) isLogUpToDate(candidateLastTerm, candidateLastIndex uint64) bool {
	myLastTerm := n.log.LastTerm()
	myLastIndex := n.log.LastIndex()
	
	// In case of different terms, whoever has higher term is up-to-date
	if candidateLastTerm != myLastTerm {
		return candidateLastTerm > myLastTerm
	}
	// Same term: whoever has more entries is up-to-date
	return candidateLastIndex >= myLastIndex
}


// Returns the minimum votes needed to win: floor(N/2) + 1.
func (n *RaftNode) quorum() int {
	return (len(n.peers) + 1) / 2 + 1
}