package tests

import (
	"testing"

	"github.com/ani03sha/raftly/scenarios"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChaosSplitBrain(t *testing.T) {
	// Reproduces the AWS US-East-1 EBS incident (April 2011).
	// A leader gets isolated. The rest of the cluster elects a new leader.
	// Writes to the isolated leader must not survive — they were never committed.
	// After healing, the old leader rejoins as follower and discards its uncommitted entries.
	s := &scenarios.SplitBrainScenario{DataDir: t.TempDir()}
	result, err := s.Run()
	require.NoError(t, err)
	assert.True(t, result.Passed, "SplitBrain scenario failed:\n%s", result.Observations)
}

func TestChaosLeaderIsolation(t *testing.T) {
	// An asymmetrically partitioned leader can no longer reach quorum.
	// Any writes it accepts must not be committed.
	// Tests the §5.4.2 "commit only current-term entries" rule under real failure conditions.
	s := &scenarios.LeaderIsolationScenario{DataDir: t.TempDir()}
	result, err := s.Run()
	require.NoError(t, err)
	assert.True(t, result.Passed, "LeaderIsolation scenario failed:\n%s", result.Observations)
}

func TestChaosTornWrite(t *testing.T) {
	// Simulates a process crash mid-WAL write.
	// Garbage is appended to the WAL file (as an OS would leave a partial flush).
	// On restart, CRC32 detection must discard the corrupt tail and recover cleanly.
	s := &scenarios.TornWriteScenario{DataDir: t.TempDir()}
	result, err := s.Run()
	require.NoError(t, err)
	assert.True(t, result.Passed, "TornWrite scenario failed:\n%s", result.Observations)
}

func TestChaosStaleElection(t *testing.T) {
	// Reproduces the etcd 2018 bug pattern.
	// A node with a stale log (missed entries 11–20) must NOT win an election.
	// The §5.4.1 election restriction — voters reject candidates whose log is behind theirs —
	// is the only thing preventing this node from becoming leader and rolling back committed data.
	s := &scenarios.StaleElectionScenario{DataDir: t.TempDir()}
	result, err := s.Run()
	require.NoError(t, err)
	assert.True(t, result.Passed, "StaleElection scenario failed:\n%s", result.Observations)
}