package server

import (
	"github.com/ani03sha/raftly/raft"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)


// This holds all Prometheus instruments for the Raftly node. All prometheus types are safe for concurrent use.
type Metrics struct {
	CurrentTerm prometheus.Gauge
	State prometheus.Gauge
	CommittedIndex prometheus.Gauge
	AppliedIndex prometheus.Gauge

	LogEntriesTotal prometheus.Counter
	ElectionsTotal prometheus.Counter
	LeaderChanges prometheus.Counter

	ReplicationLag *prometheus.HistogramVec
	WALFsyncDuration prometheus.Histogram
}


func NewMetrics() *Metrics {
	return &Metrics{
		CurrentTerm: promauto.NewGauge(prometheus.GaugeOpts{
		Name: "raftly_current_term",
		Help: "Current Raft term number",
		}),
		State: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "raftly_state",
			Help: "Node state: 0=follower, 1=candidate, 2=leader",
		}),
		CommittedIndex: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "raftly_committed_index",
			Help: "Highest log index known to be committed",
		}),
		AppliedIndex: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "raftly_applied_index",
			Help: "Highest log index applied to the state machine",
		}),
		LogEntriesTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "raftly_log_entries_total",
			Help: "Total log entries appended to this node",
		}),
		ElectionsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "raftly_elections_total",
			Help: "Total elections started by this node",
		}),
		LeaderChanges: promauto.NewCounter(prometheus.CounterOpts{
			Name: "raftly_leader_changes_total",
			Help: "Total leader changes observed by this node",
		}),
		ReplicationLag: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "raftly_replication_lag",
			Help:    "Replication lag in log entries per peer (leader only)",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1, 2, 4 ... 512
		}, []string{"peer"}),
		WALFsyncDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "raftly_wal_fsync_duration_ms",
			Help:    "WAL fsync latency in milliseconds",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 0.1ms to ~400ms
		}),
	}
}


// Converts NodeState -> float64 for Prometheus (which only stores numbers).
func stateValue(s raft.NodeState) float64 {
	switch s {
	case raft.Candidate, raft.PreCandidate:
		return 1
	case raft.Leader:
		return 2
	default: // Follower
		return 0
	}
}


// Refreshes all gauges from the current node status. We call this on a regular interval (every second) from a
// background goroutine. prevLeader is the leaderID from the previous call - used to detect transitions.
func (m *Metrics) Update(status raft.NodeStatus, prevLeader string) {
	m.CurrentTerm.Set(float64(status.Term))
	m.State.Set(stateValue(status.State))
    m.CommittedIndex.Set(float64(status.CommitIndex))
    m.AppliedIndex.Set(float64(status.LastApplied))
	
	// Detect leader change: leaderID changed AND we now know who the new leader is
	if prevLeader != status.LeaderID && status.LeaderID != "" {
		m.LeaderChanges.Inc()
	}
}