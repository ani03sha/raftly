package chaos

import (
	"fmt"
	"time"
	"github.com/ani03sha/raftly/transport"
)


// Creates a full (symmetric) partition around nodeID. All messages to AND from nodeID are dropped.
// If duration > 0, the partition heals automatically after that duration.
// This simulates: a node losing its NIC, entering a network island, or being placed behind a firewall
// that drops all traffic.
func (c *ChaosInjector) PartitionNode (nodeID string, duration time.Duration) *InjectionResult {
	c.mu.Lock()

	result := &InjectionResult{
		Action: fmt.Sprintf("full-partition: %s", nodeID),
		AffectedNodes: []string{nodeID},
		StartTime: time.Now(),
	}

	// Two rules needed for a symmetric partition:
	// 1. Drop messages FROM this node (it can't reach nodes)
	// 2. Drop messages TO this node (others can't reach it)
	fromID := c.nextID("part-from-" + nodeID)
	toID := c.nextID("part-to-" + nodeID)

	c.proxy.AddRule(transport.ProxyRule{
		ID: fromID,
		FromNode: nodeID,
		Action: transport.ActionDrop,
	})
	c.proxy.AddRule(transport.ProxyRule{
		ID: toID,
		ToNode: nodeID,
		Action: transport.ActionDrop,
	})
	
	result.RuleIDs = []string{fromID, toID}
	c.mu.Unlock()

	c.RecordObservation(fmt.Sprintf("PARTITION: node %s fully isolated (auto-heal: %v)", nodeID, duration))

	if duration > 0 {
		go func() {
			time.Sleep(duration)
			c.proxy.RemoveRule(fromID)
			c.proxy.RemoveRule(toID)
			result.EndTime = time.Now()
			c.RecordObservation(fmt.Sprintf("HEAL: partition on node %s removed after %w", nodeID, duration))
		}()
	}

	return result
}


// Creates a one-way or two-way partition between two specific nodes.
// direction controls which direction(s) are blocked:
//   "both"      — fromNode and toNode cannot reach each other
//   "from_only" — fromNode cannot reach toNode (but toNode can still reach fromNode)
//   "to_only"   — toNode cannot reach fromNode (but fromNode can still reach toNode)
//
// Asymmetric partitions reproduce some of the nastiest real-world bugs.
// Example: node1 can send heartbeats to node2, but node2's responses never arrive.
// node1 thinks it is still leader; node2 starts an election.
func (c *ChaosInjector) PartitionAsymmetric(fromNode, toNode, direction string) *InjectionResult {
	c.mu.Lock()

	result := &InjectionResult{
		Action: fmt.Sprintf("asymmetric-partition: %s->%s(%s)", fromNode, toNode, direction),
		AffectedNodes: []string{fromNode, toNode},
		StartTime: time.Now(),
	}

	switch direction {
	case "both":
		id1 := c.nextID("apart")
		id2 := c.nextID("apart")
		c.proxy.AddRule(transport.ProxyRule{ID: id1, FromNode: fromNode, ToNode: toNode, Action: transport.ActionDrop})
		c.proxy.AddRule(transport.ProxyRule{ID: id2, FromNode: toNode, ToNode: fromNode, Action: transport.ActionDrop})
		result.RuleIDs = []string{id1, id2}

	case "from_only":
		id := c.nextID("apart")
		c.proxy.AddRule(transport.ProxyRule{ID: id, FromNode: fromNode, ToNode: toNode, Action: transport.ActionDrop})
		result.RuleIDs = []string{id}

	case "to_only":
		id := c.nextID("apart")
		c.proxy.AddRule(transport.ProxyRule{ID: id, FromNode: toNode, ToNode: fromNode, Action: transport.ActionDrop})
		result.RuleIDs = []string{id}

	default:
		c.mu.Unlock()
		c.RecordObservation(fmt.Sprintf("ERROR: unknown partition direction: %s", direction))
		return result
	}

	c.mu.Unlock()
	c.RecordObservation(fmt.Sprintf("PARTITION(asymmetric): %s<->%s direction: %s", fromNode, toNode, direction))
	return result
}


// Removes specific partition rules by their IDs. We use this for targeted healing instead of Heal() which removes everything.
func (c *ChaosInjector) HealPartition(result *InjectionResult) {
	for _, id := range result.RuleIDs {
		c.proxy.RemoveRule(id)
	}
	result.EndTime = time.Now()
	c.RecordObservation(fmt.Sprintf("HEAL: partition %s removed", result.Action))
}