package raft

import (
	"context"
	"sort"
	"time"

	"go.uber.org/zap"
)

// Processes an incoming AppendEntries RPC from the leader. This runs on followers.
// It is the most called function in a healthy cluster - every heartbeat (50ms) comes through here.
func (n *RaftNode) handleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Reject stale leaders
	if req.Term < n.currentTerm {
		return AppendEntriesResponse{Term: n.currentTerm, Success: false}
	}

	// A valid AppendEntries from the currentTerm or higher: update our leader knowledge and 
	// reset the election timer.
	// becomeFollower() handles both "higher term" (updates term, clears vote) and "same term"
	// (just resets timer and updates leaderID) cases
	n.becomeFollower(req.Term, req.LeaderID)

	// --- Log consistency check ---
	// Before appending, verify that our log matches the leader's at PrevLogIndex.
	// If not, reject and tell the leader where the conflict is so it can retry.
	if req.PrevLogIndex > 0 {
		prevEntry, err := n.log.GetEntry(req.PrevLogIndex)
		if err != nil {
			// Our log is shorter than PrevLogIndex - we are missing entries
			// Report our lastIndex + 1 as the conflicting point
			return AppendEntriesResponse{
				Term: n.currentTerm,
				Success: false,
				ConflictIndex: n.log.LastIndex() + 1,
				ConflictTerm: 0,
			}
		}
		if prevEntry.Term != req.PrevLogTerm {
			// Term mismatch at PrevLogIndex. Fast backtracking: find the first index with
			// this conflicting term so the leader can skip the entire bad term in one retry.
			conflictTerm := prevEntry.Term
			conflictIndex := req.PrevLogIndex
			for conflictIndex > 1 {
				entry, err := n.log.GetEntry(conflictIndex - 1)
				if err != nil || entry.Term != conflictTerm {
					break
				}
				conflictIndex--
			}
			return AppendEntriesResponse{
				Term: n.currentTerm,
				Success: false,
				ConflictTerm: conflictTerm,
				ConflictIndex: conflictIndex,
			}
		}
	}

	// --- Append new entries ---
	// Walk through the incoming entries. For each one:
	//   - If we already have it and terms match: skip (idempotent).
	//   - If we already have it but terms differ: truncate from here and append.
	//   - If we don't have it: append from here onward.
	for i, entry := range req.Entries {
		existing, err := n.log.GetEntry(entry.Index)
		if err != nil {
			// Entry doesn't exist in our log - append from here to end.
			newEntries := req.Entries[i:]
			if err := n.wal.SaveEntries(newEntries); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			if err := n.wal.Sync(); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			if err := n.log.Append(newEntries); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			break
		}
		if existing.Term != entry.Term {
			// Conflict: our entry at this index has a different term than the leader's.
			// The leader's version wins — truncate our log from this index onward, then append the leader's entries.
			// This is safe because conflicting entries were never committed (the commit rule ensures committed
			// entries are never overwritten).
			n.logger.Warn("log conflict: truncating",
				zap.Uint64("from_index", entry.Index),
				zap.Uint64("local_term", existing.Term),
				zap.Uint64("leader_term", entry.Term),
			)
			if err := n.wal.TruncateFrom(entry.Index); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			if err := n.log.TruncateFrom(entry.Index); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			newEntries := req.Entries[i:]
			if err := n.wal.SaveEntries(newEntries); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			if err := n.wal.Sync(); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			if err := n.log.Append(newEntries); err != nil {
				return AppendEntriesResponse{Term: n.currentTerm, Success: false}
			}
			break
		}
	}

	// --- Advance commit index ---
	// The leader tells us its commitIndex. We advance ours to min(leaderCommit, our lastLogIndex)
	// because we can't commit entries we don't have.
	if req.LeaderCommit > n.log.CommitIndex() {
		newCommit := req.LeaderCommit
		if lastIndex := n.log.LastIndex(); lastIndex < newCommit {
			newCommit = lastIndex
		}
		_ = n.log.CommitTo(newCommit)
		go n.applyCommitted()
	}

	return AppendEntriesResponse{Term: n.currentTerm, Success: true}
}

// Sends AppendEntries to all peers. Called every HeartbeatInterval from the run loop, and immediately
// after Propose(). Empty Entries = heartbeat. Non-empty = replication.
func (n *RaftNode) maybeSendHeartbeats() {
	n.mu.Lock()
	if n.state != Leader {
		n.mu.Unlock()
		return
	}
	// Commit run checks every heartbeat tick. For single-node clusters, this is the only trigger because
	// there are no peers whose replicateTo() would call maybeCommit()
	n.maybeCommit()
	peerIDs := make([]string, 0, len(n.peers))
	for id := range n.peers {
		peerIDs = append(peerIDs, id)
	}
	n.mu.Unlock()

	// One goroutine per follower - they run in parallel
	for _, peerID := range peerIDs {
		go n.replicateTo(peerID)
	}
	go n.applyCommitted() // also trigger apply in case we committed something on this tick
}


// Sends AppendEntries to follower and processes the response.
// Handles both the success path (advance matchIndex, maybe commit) and the failure path
// (backup nextIndex and let the next tick retry).
func (n *RaftNode) replicateTo(peerID string) {
	n.mu.Lock()

	if n.state != Leader {
		n.mu.Unlock()
		return
	}

	peer := n.peers[peerID]
	nextIndex := peer.NextIndex
	prevLogIndex := nextIndex - 1
	var prevLogTerm uint64

	if prevLogIndex > 0 {
		prevEntry, err := n.log.GetEntry(prevLogIndex)
		if err != nil {
			// Our log doesn't have prevLogIndex - shouldn't happen on a healthy leader.
			n.mu.Unlock()
			return
		}
		prevLogTerm = prevEntry.Term
	}

	// Collect entries from nextIndex to the end of our log.
	// If nextIndex > lastIndex, entries is empty and this becomes a heartbeat.
	var entries []LogEntry
	lastIndex := n.log.LastIndex()
	if nextIndex <= lastIndex {
		var err error
		entries, err = n.log.GetEntries(nextIndex, lastIndex + 1)
		if err != nil {
			n.mu.Unlock()
			return
		}
	}

	req := AppendEntriesRequest{
		Term: n.currentTerm,
		LeaderID: n.id,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm: prevLogTerm,
		Entries: entries,
		LeaderCommit: n.log.CommitIndex(),
	}
	n.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	resp, err := n.transport.SendAppendEntries(ctx, peerID, req)
	if err != nil {
		return // network failure - will retry on next heartbeat tick
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Guard: if we stepped down while the RPC was in flight, discard the response
	if n.state != Leader || n.currentTerm != req.Term {
		return
	}

	// If the follower has the higher term, we are the stale leader - step down.
	if resp.Term > n.currentTerm {
		n.becomeFollower(resp.Term, "")
		return
	}

	if resp.Success {
		// Advance this follower's tracked indexes.
		newMatch := req.PrevLogIndex + uint64(len(req.Entries))
		if newMatch > peer.MatchIndex {
			peer.MatchIndex = newMatch
		}
		peer.NextIndex = peer.MatchIndex + 1
		// Check if this new matchIndex lets us commit something.
		n.maybeCommit()
	} else {
		// Log inconsistency - back up nextIndex using the fast backtracking hint.
		if resp.ConflictTerm > 0 {
			// The follower reported a conflicting term. Find the last entry in OUR log
            // with that term. We can skip back to just after it.
			newNextIndex := resp.ConflictTerm
			for i := n.log.LastIndex(); i >= 1; i-- {
				entry, err := n.log.GetEntry(i)
				if err != nil {
					break
				}
				if entry.Term == resp.ConflictTerm {
					newNextIndex = i + 1
					break
				}
			}
			peer.NextIndex = newNextIndex
		} else {
			// Follower's log is shorter - jump back to its reported last index.
			peer.NextIndex = resp.ConflictIndex
		}
		if peer.NextIndex < 1 {
			peer.NextIndex = 1
		}
	}
}

// Checks whether the leader can advance the commit index. It is called after every successful AppendEntries response.
//
// An entry is committed when:
//   1. It is stored on a majority of nodes (matchIndex check)
//   2. It is from the CURRENT term
//
// Rule 2 prevents this scenario: leader A commits a term-1 entry, crashes, and a node that never got that entry 
// wins the next election and overwrites it. 
// 
// By only committing current-term entries directly, we guarantee any future leader must have all committed entries.
func (n *RaftNode) maybeCommit() {
	if n.state != Leader {
		return
	}

	// Collect matchIndexes for every node, including the leader itself.
    // The leader has all entries up to LastIndex().
	matchIndexes := make([]uint64, 0, len(n.peers) + 1)
	matchIndexes = append(matchIndexes, n.log.LastIndex()) // leader
	for _, peer := range n.peers {
		matchIndexes = append(matchIndexes, peer.MatchIndex)
	}

	// Sort ascending. The quorum-th highest value is the highest index that a majority of nodes have confirmed.
	sort.Slice(matchIndexes, func(i, j int) bool {
		return matchIndexes[i] < matchIndexes[j]
	})
	quorumMatchIndex := matchIndexes[len(matchIndexes) - n.quorum()]

	if quorumMatchIndex <= n.log.CommitIndex() {
		return // nothing new to commit
	}

	entry, err := n.log.GetEntry(quorumMatchIndex)
	if err != nil {
		return
	}

	// Only commit if this entry is from the current term.
	// Previous-term entries become committed as a side effect when their index falls below a current-term commit.
	if entry.Term != n.currentTerm {
		return
	}

	_ = n.log.CommitTo(quorumMatchIndex)
	n.logger.Info("committed",
		zap.Uint64("index", quorumMatchIndex),
		zap.Uint64("term", n.currentTerm),
	)
	go n.applyCommitted()
}

// Sends all newly committed entries to the application layer via commitCh, and advances lastApplied.
// Also unblocks any Propose() callers waiting on these indexes.
func (n *RaftNode) applyCommitted() {
	n.mu.Lock()
	commitIndex := n.log.CommitIndex()
	lastApplied := n.log.LastApplied()

	if lastApplied >= commitIndex {
		n.mu.Unlock()
		return
	}

	// Collect all unapplied entries in one pass under the lock.
	entries := make([]LogEntry, 0, commitIndex - lastApplied)
	for i := lastApplied + 1; i <= commitIndex; i++ {
		entry, err := n.log.GetEntry(i)
		if err != nil {
			break
		}
		entries = append(entries, entry)
	}
	n.mu.Unlock()

	// Send each entry to the application layer.
    // The select ensures we don't block forever if the node is stopping.
	for _, entry := range entries {
		select {
		case n.commitCh <- entry:
		case <-n.stopCh:
			return
		}
		// Mark applied and unblock any Propose() caller waiting on this index.
		n.mu.Lock()
		n.log.SetLastApplied(entry.Index)
		n.notifyProposal(entry.Index, nil)
		n.mu.Unlock()
	}
}

func (w *WAL) Path() string {
	return w.path
}