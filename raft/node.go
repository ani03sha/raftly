package raft

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)


// --- RPC message types ---
// These are the four message types Raft nodes exchange over the network.
// In production these would be protobuf-serialized; for now they are plain structs.

// VoteRequest is sent by a candidate to request a vote.
// The same struct is reused for pre-vote (PreVote=true) and real vote (PreVote=false).
type VoteRequest struct {
	Term uint64 // candidate's term (or term + 1 for pre-vote)
	CandidateID string
	LastLogIndex uint64 // index of candidate's last log entry
	LastLogTerm uint64 // term of candidate's last entry
	PreVote bool // if true, peer must NOT updates its term or receipt
}


// Reply to the VoteRequest
type VoteResponse struct {
	Term uint64 // responder's current term - lets candidate detect if its stale
	VoteGranted bool
}


// Sent by the leader to replicate entries and as a heartbeat.
// When Entries is empty, it's a heartbeat - just resets the follower's election timer.
type AppendEntriesRequest struct {
	Term uint64 // leader's term
	LeaderID string
	PrevLogIndex uint64 // index of the entry immediately before the new ones
	PrevLogTerm uint64 // term of the PrevLogIndex - follower rejects if this doesn't match
	Entries []LogEntry // entries to append; empty = heartbeat
	LeaderCommit uint64 // leader's commit index - follower advances its own commit to this
}


// This is the reply to AppendEntriesRequest.
// ConflictTerm and ConflictIndex are the "fast backup" optimization:
// instead of the leader decrementing nextIndex one at a time on rejection,
// the follower reports exactly where the conflict starts.
type AppendEntriesResponse struct {
	Term 			uint64
	Success 		bool
	ConflictTerm 	uint64 // term of the conflicting entry (0 if follower log is just short)
	ConflictIndex 	uint64 // first index of ConflictTerm, or last log index + 1
}


// --- Supporting types

// Tracks the replication state for one follower.
// Only the leader maintains this; reset every time a node wins an election.
type Peer struct {
	ID string
	NextIndex uint64 // optimistic: next log index to send to this follower
	MatchIndex uint64 // conservative: highest index known to be on this follower
}


// This is read-only snapshot of the node's state.
// Used by metrics, health checks, and the HTTP API.
type NodeStatus struct {
	ID string
	State NodeState
	Term uint64
	LeaderID string
	CommitIndex uint64
	LastApplied uint64
	LogIndex uint64
}


// This is the outcome of a Propose() call, delivered back to the called via a
// channel once the entry is committed.
type proposeResult struct {
	err error
}


// --- The node itself ---

// This is the core part of the Raft state machine.
// Lock ordering rule: always acquire n.mu before n.log.mu (via log methods)
type RaftNode struct {
	// Identity
	id string
	config *Config

	// Durable state - these must be written to WAL before responding to any RPC.
	// Rule: if these are wrong after a crash, correctness breaks.
	currentTerm uint64
	votedFor string

	// Volatile state - rebuilt for log/network on restart.
	state NodeState
	leaderID string

	// Storage
	log *RaftLog
	wal *WAL

	// Peer tracking - leader only, reset on each election win.
	peers map[string]*Peer

	// Network
	transport Transport

	// Timers
	electionTimer *time.Timer
	electionTimeout time.Duration // randomized; reset on every valid heartbeat
	lastLeaderContactTime time.Time // last time we heart from a valid leader, used bu pre-vote

	// Channels
	// commitCh: applied entries flow out here to the application layer (key-value store).
	// stopCh: closing this channel signals all goroutines to exit.
	commitCh chan LogEntry
	stopCh chan struct {}

	// Proposal tracking
	// When Propose() is called, we register a result channel keyed by logIndex.
	// When that index is committed, notifyProposal() unlocks the Propose() caller.
	proposals map[uint64]chan proposeResult

	mu sync.Mutex
}


// Creates a new Raft node, recover any durable state from the WAL.
// It doesn't start the background goroutine - call Start() for that
func NewRaftNode(config *Config, transport Transport) (*RaftNode, error) {
	wal, err := Open(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	// Recover persisted state: term, voted-for, and all log entries.
	// On a fresh node this returns zero values.
	term, votedFor, walEntries, err := wal.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read: WAL %w", err)
	}

	raftLog := NewRaftLog()
	if len(walEntries) > 0 {
		if err := raftLog.Append(walEntries); err != nil {
			return nil, fmt.Errorf("replay WAL entries: %w", err)
		}
	}

	peers := make(map[string]*Peer, len(config.Peers))
	for _, p := range config.Peers {
		peers[p.ID] = &Peer{ID: p.ID}
	}

	return &RaftNode{
		id: config.NodeID,
		config: config,
		currentTerm: term,
		votedFor: votedFor,
		state: Follower, // every node starts as a follower
		log: raftLog,
		wal: wal,
		peers: peers,
		transport: transport,
		commitCh: make(chan LogEntry, 256),
		stopCh: make(chan struct{}),
		proposals: make(map[uint64]chan proposeResult),
	}, nil
}


// Registers with the transport and starts background run loop.
func (n *RaftNode) Start() error {
	n.transport.Register(n)

	n.mu.Lock()
	n.electionTimeout = n.randomElectionTimeout()
	n.electionTimer = time.NewTimer(n.electionTimeout)
	n.mu.Unlock()

	go n.run()
	return nil
}


// Shuts the node down cleanly.
// Closing stopCh is a broadcast - every goroutine selecting on its exit.
func (n* RaftNode) Stop() {
	close(n.stopCh)
	n.mu.Lock()
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}
	n.mu.Unlock()
	n.wal.Close()
}


// Submits a write to the cluster. Blocks until committed or failed.
// Only the leader accepts proposals. If this node is not the leader,
// returns an error containing the leader ID so the client can redirect.
func (n *RaftNode) Propose(data []byte) (index uint64, term uint64, err error) {
	n.mu.Lock()

	if n.state != Leader {
		leaderID := n.leaderID
		n.mu.Unlock()
		return 0, 0, fmt.Errorf("not leader, try: %s", leaderID)
	}

	entry := LogEntry{
		Index: n.log.LastIndex() + 1,
		Term: n.currentTerm,
		Type: EntryNormal,
		Data: data,
	}

	// Durability first: write to WAL and fsync before appending to in-memory log.
	// If we crash here, the entry is either durable or not - never half written.
	if err = n.wal.SaveEntry(entry); err != nil {
		n.mu.Unlock()
		return 0, 0, fmt.Errorf("WAL write: %w", err)
	}
	if err = n.wal.Sync(); err != nil {
		n.mu.Unlock()
		return 0, 0, fmt.Errorf("WAL sync: %w", err)
	}
	if err = n.log.Append([]LogEntry{entry}); err != nil {
		go n.maybeSendHeartbeats() // trigger immediate replication instead of waiting for next tick
		n.mu.Unlock()
		return 0, 0, fmt.Errorf("log append: %w", err)
	}

	// Register a result channel; replication.go will call notify proposal() when committed.
	resultCh := make(chan proposeResult, 1)
	n.proposals[entry.Index] = resultCh
	index, term = entry.Index, entry.Term
	n.mu.Unlock()

	// Block until committed or node stops.
	select {
	case result := <-resultCh:
		return index, term, result.err
	case <-n.stopCh:
		return index, term, fmt.Errorf("node stopped")
	}
}


// Returns true if the node currently believes it is a leader
func (n *RaftNode) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state == Leader
}


// Returns id of the node this node believes is the leader
func (n *RaftNode) LeaderID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leaderID
}


// Returns a read-only snapshot of the node's state
func (n *RaftNode) Status() NodeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return NodeStatus{
		ID: n.id,
		State: n.state,
		Term: n.currentTerm,
		LeaderID: n.leaderID,
		CommitIndex: n.log.CommitIndex(),
		LastApplied: n.log.LastApplied(),
		LogIndex: n.log.LastIndex(),
	}
}


// Returns the read-only channel of committed entries.
// The application layer consumes this to apply writes to the state machine.
func (n *RaftNode) CommitCh() <-chan LogEntry {
	return n.commitCh
}


// --- State Transitions ---
// These are called with n.mu held.

// Steps down to follower. This is called when we see a higher term, lose an election, or on startup.
func (n *RaftNode) becomeFollower(term uint64, leaderID string) {
	n.state = Follower
	n.leaderID = leaderID

	if term > n.currentTerm {
		n.currentTerm = term
		n.votedFor = ""
		// Persist immediately
		_ = n.wal.SaveState(term, "")
		_ = n.wal.Sync()
	}

	if leaderID != "" {
		n.lastLeaderContactTime = time.Now()
	}

	n.resetElectionTimer()
}


// Increments the term and vote for ourselves. Called at the start of the real election (not pre-vote).
func (n *RaftNode) becomeCandidate() {
	n.state = Candidate
	n.currentTerm++
	n.votedFor = n.id // self-vote
	n.leaderID = ""

	// CRITICAL: persist term + vote before sending any RequestVote RPCs.
	// If we crash after persisting, we won't vote twice in the same term on restart.
	_ = n.wal.SaveState(n.currentTerm, n.votedFor)
	_ = n.wal.Sync()

	n.resetElectionTimer()
}


// This is called when a candidate receives vote from the quorum
func (n *RaftNode) becomeLeader() {
	n.state = Leader
	n.leaderID = n.id

	// Initialize per-follower tracking.
	// nextIndex is optimistic: start at our lastIndex + 1
	// matchIndex is pessimistic: start at 0 (we know nothing about the follower's log yet).
	lastIndex := n.log.LastIndex()
	for _, peer := range n.peers {
		peer.NextIndex = lastIndex + 1
		peer.MatchIndex = 0
	}

	// Leaders don't wait for election timeout
	n.electionTimer.Stop()

	// Assert leadership immediately with a heartbeat
	go n.maybeSendHeartbeats()
}


// --- Timer helpers ---
// Called with n.mu held

// Returns a random duration in [ElectionTimeout, 2*ElectionTimeout).
// Without randomization, all followers timeout simultaneously and split every vote.
func (n *RaftNode) randomElectionTimeout() time.Duration {
	base := n.config.ElectionTimeout
	jitter := time.Duration(rand.Int63n(int64(base)))
	return base + jitter
}


// Stops the current timer and starts a fresh one. This is called on a valid heartbeat,
// vote grant, or state transition
func (n * RaftNode) resetElectionTimer() {
	if n.electionTimer != nil {
		// If Stop() returns false, the timer already fired and its value is sitting
		// in the channel. Drain it so the next receive doesn't fire immediately.
		if !n.electionTimer.Stop() {
			select {
			case <-n.electionTimer.C:
			default:
			}
		}
	}
	n.electionTimeout = n.randomElectionTimeout()
	n.electionTimer.Reset(n.electionTimeout)
}


// --- Main Loop ---

// This is the only goroutine that drives the election timer.
// All other goroutines (replication, apply) are spawned from here or from becomeLeader.
func (n *RaftNode) run() {
	heartbeatTicker := time.NewTicker(n.config.HeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		// Heartbeat tick: if we are the leader, replicate/heartbeat all followers.
		case <-heartbeatTicker.C:
			n.mu.Lock()
			isLeader := n.state == Leader
			n.mu.Unlock()
			if isLeader {
				go n.maybeSendHeartbeats()
			}
		// Election timer fired: start an election (or pre-vote)
		case <-n.electionTimer.C:
			n.mu.Lock()
			state := n.state
			n.mu.Unlock()
			if state != Leader {
				// campaign() handles locking internally - it spans multiple RPCs
				go n.campaign(n.config.EnablePreVote)
			} else {
				// Leaders don't hold elections; just reset the timer.
				n.mu.Lock()
				n.resetElectionTimer()
				n.mu.Unlock()
			}
		case <-n.stopCh:
			return
		}
	}
}


// --- Internal helpers ---

// Unblocks a Propose() caller when their entry is committed or fails. Must be called with n.mu held.
func (n *RaftNode) notifyProposal(index uint64, err error) {
	if ch, ok := n.proposals[index]; ok {
		ch <- proposeResult{err: err}
		delete(n.proposals, index)
	}
}


// HandleRequestVote is the exported entry point for incoming RequestVote RPCs.
// Called by the transport layer when a peer requests our vote.
func (n *RaftNode) HandleRequestVote(req VoteRequest) VoteResponse {
	return n.handleRequestVote(req)
}

// HandlePreVote is the exported entry point for incoming PreVote RPCs.
func (n *RaftNode) HandlePreVote(req VoteRequest) VoteResponse {
	return n.handlePreVote(req)
}

// HandleAppendEntries is the exported entry point for incoming AppendEntries RPCs.
func (n *RaftNode) HandleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	return n.handleAppendEntries(req)
}