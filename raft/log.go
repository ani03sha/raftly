package raft

import (
	"errors"
	"sync"
)

// EntryType distinguishes normal data entries from cluster config change entries.
// We only use EntryNormal for now - EntryConfig is for future membership changes.
type EntryType int

const (
	EntryNormal EntryType = iota
	EntryConfig
)

// LogEntry is one record in the log file.
// Every write the client makes becomes one log entry
type LogEntry struct {
	Index uint64 // position in the log, 1-based, always increases
	Term uint64 // which election term this entry was created in
	Type EntryType
	Data []byte // the raw command (e.g., "set x=5" serialized)
}

// RaftLog is the in-memory log store.
type RaftLog struct {
	entries []LogEntry // index 0 is dummy sentinel entry
	commitIndex uint64 // highest entry know to be committed
	lastApplied uint64 // highest entry applied to state machine
	mu sync.RWMutex
}

// NewRaftLog creates an empty log with a sentinel entry at index 0.
// Why a sentinel? Log indexes in Raft are 1-based. Storing a dummy entry
// at position 0 means we can always safely read entries[i] without off-by-one
// guards. The sentinel has term=0, which is lower than any real term - useful
// in election comparison
func NewRaftLog() *RaftLog {
	return &RaftLog{
		entries: []LogEntry{{Index: 0, Term: 0}}, // sentinel
	}
}

// Returns the index of the last entry in the log.
// On an empty log, it returns 0 (the sentinel's index).
func (l *RaftLog) LastIndex() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.entries[len(l.entries) - 1].Index
}

// Return the term of the last entry in the log.
// Used during elections where a candidate must prove its log is
// at least as up-to-date as the voter's log. 
func (l *RaftLog) LastTerm() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.entries[len(l.entries) - 1].Term
}


// Returns the entry at the given index.
// Returns an error if the index is out of range.
func (l *RaftLog) GetEntry(index uint64) (LogEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if index >= uint64(len(l.entries)) {
		return LogEntry{}, errors.New("index out of range")
	}
	return l.entries[index], nil
}


// Returns all entries in the range [from, to).
// This is used by the leader when sending AppendEntries to a follower.
func (l *RaftLog) GetEntries(from, to uint64) ([]LogEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	last := uint64(len(l.entries))
	if from >= last || to > last || from > to {
		return nil, errors.New("invalid range")
	}
	result := make([]LogEntry, to-from)
	copy(result, l.entries[from:to])
	return result, nil
}


// Add new entries to the log.
// The caller is responsible for ensuring entries are in order.
func (l *RaftLog) Append(entries []LogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entries...)
	return nil
}


// This advanced commitIndex to the given index.
// This is called by the leader once the majority has acknowledged and entry.
// It is safe  to call with an index <= current commitIndex (a no-op).
func (l *RaftLog) CommitTo(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if index > uint64(len(l.entries) - 1) {
		return errors.New("cannot commit beyond last log index")
	}
	if index > l.commitIndex {
		l.commitIndex = index
	}
	return nil
}


// Removes all entries from the given index onward.
// This is called during conflict resolution: when a follower's log
// disagrees with the leader's, the follower must delete its conflicting
// entries and accept the leader's version.
// WARNING: never truncate below the commitIndex - that would discard already
// committed data, violating Raft's safety guarantee.
func (l *RaftLog) TruncateFrom(index uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if index <= l.commitIndex {
		return errors.New("cannot truncate committed entries")
	}
	l.entries = l.entries[:index]
	return nil
}


// Returns the current commit index
func (l *RaftLog) CommitIndex() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.commitIndex
}


// Returns the index of the last applied entry
func (l *RaftLog) LastApplied() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lastApplied
}


// Advances lastApplied after an entry is executed
func (l *RaftLog) SetLastApplied(index uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastApplied = index
}