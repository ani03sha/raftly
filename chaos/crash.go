package chaos

import (
	"fmt"
	"time"

	"github.com/ani03sha/raftly/transport"
)


// Stops the node and blackholes its network traffic. This simulates a process crash or power failure.
//
// Two things happen:
//   1. node.Stop() halts the Raft goroutines
//   2. Proxy rules drop all network traffic so peers time out naturally (without these rules, peers would get 
// 	    immediate connection errors instead of timeouts — a different failure mode)
func (c *ChaosInjector) CrashNode(nodeID string) *InjectionResult {
	c.mu.Lock()

	node, ok := c.nodes[nodeID]
	result := &InjectionResult{
		Action: fmt.Sprintf("crash:%s", nodeID),
		AffectedNodes: []string{nodeID},
		StartTime: time.Now(),
	}

	if !ok {
		c.mu.Unlock()
		c.RecordObservation(fmt.Sprintf("CRASH ERROR: node %s is not found", nodeID))
		return result
	}

	// Blackhole traffic so peers experience timeouts, not connection refused.
	// This more faithfully simulates a crashed host vs a graceful shutdown.
	id1 := c.nextID("crash-from-" + nodeID)
	id2 := c.nextID("crash-to-" + nodeID)
	c.proxy.AddRule(transport.ProxyRule{ID: id1, FromNode: nodeID, Action: transport.ActionDrop})
	c.proxy.AddRule(transport.ProxyRule{ID: id2, ToNode: nodeID, Action: transport.ActionDrop})
	result.RuleIDs = []string{id1, id2}

	c.mu.Unlock()

	// Stop outside the lock — Stop() blocks until goroutines exit.
	node.Stop()

	c.RecordObservation(fmt.Sprintf("CRASH: node %s stopped and blackholed", nodeID))
	return result
}


// Recreates and starts a previously crashed node using the NodeFactory.
// The new node reads its WAL to recover term, votedFor, and log entries — this is the crash-recovery path we built 
// WAL checksums to protect.
//
// Note: this clears ALL proxy rules for simplicity. In a multi-failure scenario you would want to remove only this 
// node's crash rules.
func (c *ChaosInjector) RestartNode(nodeID string) *InjectionResult {
	result := &InjectionResult{
		Action:        fmt.Sprintf("restart:%s", nodeID),
		AffectedNodes: []string{nodeID},
		StartTime:     time.Now(),
	}

	if c.nodeFactory == nil {
		c.RecordObservation(fmt.Sprintf("RESTART ERROR: no factory provided for node %s", nodeID))
		return result
	}

	// Remove this node's blackhole rules so network can flow again.
	// Simple approach: clear all rules. See note above.
	c.proxy.ClearRules()
	c.RecordObservation(fmt.Sprintf("RESTART: cleared proxy rules for node %s", nodeID))

	// Recreate the node. The factory calls NewRaftNode() with the original config,
	// which opens the WAL and replays all persisted entries.
	newNode, err := c.nodeFactory(nodeID)
	if err != nil {
		c.RecordObservation(fmt.Sprintf("RESTART ERROR: factory failed for node %s: %v", nodeID, err))
		return result
	}

	c.mu.Lock()
	c.nodes[nodeID] = newNode
	c.mu.Unlock()

	if err := newNode.Start(); err != nil {
		c.RecordObservation(fmt.Sprintf("RESTART ERROR: Start() failed for node %s: %v", nodeID, err))
		return result
	}

	result.EndTime = time.Now()
	c.RecordObservation(fmt.Sprintf("RESTART: node %s is back (WAL recovery complete, term and log restored)", nodeID))
	return result
}