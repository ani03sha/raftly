package raft

import "time"

// Represents the role of this node in the Raft cluster (enum)
type NodeState int

const (
	Follower NodeState = iota // auto-increment
	Candidate
	PreCandidate
	Leader
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case PreCandidate:
		return "pre-candidate"
	case Leader:
		return "leader"
	default:
		return "unknown"
	}
}

// Holds the address of a peer node
type PeerConfig struct {
	ID string
	Address string
}

// Holds all configuration for a Raft node (data class)
type Config struct {
	NodeID string
	Peers []PeerConfig
	ElectionTimeout time.Duration // How long a follower waits before calling an election. Randomized between this and 2x this value
	HeartbeatInterval time.Duration // How often the leader sends "I am alive" to the followers
	MaxLogEntries int
	SnapshotThreshold int
	DataDir string // directory for WAL and snapshot files - durability guarantee
	EnablePreVote bool // default true - prevents disruption from partitioned nodes
}

// Returns a Config with sensible production defaults.
func DefaultConfig(nodeID string) *Config {
    return &Config{
		NodeID:            nodeID,
		ElectionTimeout:   150 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
		MaxLogEntries:     1000,
		SnapshotThreshold: 10000,
		DataDir:           "data/" + nodeID,
		EnablePreVote:     true,
	}
}