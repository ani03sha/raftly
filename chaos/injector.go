package chaos

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/transport"
)


// Recreates a stopped node with same id and config. The injector calls this during RestartNode to simulate
// crash recovery.
type NodeFactory func(nodeID string) (*raft.RaftNode, error)


// Records what was done and what was observed during a chaos action. Scenarios assert against this: "How many
// entries were truncated?", "did a new leader get elected in time?", etc.
type InjectionResult struct {
	Action string
	AffectedNodes []string
	StartTime time.Time
	EndTime time.Time
	RuleIDs []string // proxy rule IDs created - stored for targeted cleanup
	Observations []string // timestamped logs of events during this injection
}


// High-level chaos control plane. It wraps the NetworkProxy with cluster aware commands
type ChaosInjector struct {
	proxy *transport.NetworkProxy
	nodes map[string]*raft.RaftNode
	nodeFactory NodeFactory
	observations []string
	ruleCounter int // monotonic counter generating unique rule ID
	mu sync.Mutex
}


// Creates an injector for the given cluster.
func NewChaosInjector(
	proxy *transport.NetworkProxy,
	nodes map[string]*raft.RaftNode,
	factory NodeFactory,
) *ChaosInjector {
	return &ChaosInjector{
		proxy: proxy,
		nodes: nodes,
		nodeFactory: factory,
	}
}


// Removes all chaos rules, restoring normal network behaviour. Called at the end of a scenario or between phases.
func (c *ChaosInjector) Heal() {
	c.proxy.ClearRules()
	c.RecordObservation("HEAL: All network chaos rules removed")
}


// Appends a timestamped observation to the global log. Called by scenarios and tests to document what they see during chaos.
func (c *ChaosInjector) RecordObservation(obs string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ts := time.Now().Format("15:04:05.000")
	c.observations = append(c.observations, fmt.Sprintf("[%s] %s", ts, obs))
}


// Returns the full observation log a single formatted string.
func (c *ChaosInjector) Report() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.observations, "\n")
}


// Generates a new proxy rule ID. Must be called with c.mu held.
func (c *ChaosInjector) nextID(prefix string) string {
	c.ruleCounter++
	return fmt.Sprintf("%s-%d", prefix, c.ruleCounter)
}