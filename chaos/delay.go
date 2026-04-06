package chaos

import (
	"fmt"
	"time"

	"github.com/ani03sha/raftly/transport"
)


// Adds artificial latency to all messages to/from nodeID. delayMs is the base delay in milliseconds; jitterMs adds randomness.
// Effective delay per message = delayMs + rand(0, jitterMs).
//
// This simulates a geographically distant node, a slow inter-datacenter link, or a VM experiencing CPU steal. 
// Very useful for testing whether election timeouts are calibrated correctly relative to network RTT.
func (c *ChaosInjector) InjectDelay(nodeID string, delayMs, jitterMs int, duration time.Duration) {
	c.mu.Lock()

	params := map[string]interface{}{"delay_ms": delayMs, "jitter_ms": jitterMs}
	id1 := c.nextID("delay-from-" + nodeID)
	id2 := c.nextID("delay-to-" + nodeID)

	c.proxy.AddRule(transport.ProxyRule{
			ID:       id1,
			FromNode: nodeID,
			Action:   transport.ActionDelay,
			Params:   params,
	})
	c.proxy.AddRule(transport.ProxyRule{
			ID:     id2,
			ToNode: nodeID,
			Action: transport.ActionDelay,
			Params: params,
	})
	c.mu.Unlock()

	c.RecordObservation(fmt.Sprintf("DELAY: %dms ±%dms injected for node %s (duration: %v)", delayMs, jitterMs, nodeID, duration))

	if duration > 0 {
		go func() {
			time.Sleep(duration)
			c.proxy.RemoveRule(id1)
			c.proxy.RemoveRule(id2)
			c.RecordObservation(fmt.Sprintf("HEAL: delay removed for node %s", nodeID))
		}()
	}
}