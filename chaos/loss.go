package chaos

import (
	"fmt"
	"time"

	"github.com/ani03sha/raftly/transport"
)


// Drops a random fraction of messages to/from nodeID. lossRate is a probability in [0.0, 1.0]: 0.3 = 30% of messages dropped.
//
// This simulates a flaky NIC, a congested switch, or a saturated uplink.
// Unlike a full partition, the node remains reachable — just unreliably.
//
// This is particularly interesting for Raft because it can cause elections to fail partway through 
// (some peers respond, others don't).
func (c *ChaosInjector) InjectPacketLoss(nodeID string, lossRate float64, duration time.Duration) {
	c.mu.Lock()
	
	params := map[string]interface{}{"loss_rate": lossRate}
	id1 := c.nextID("loss-from-" + nodeID)
	id2 := c.nextID("loss-to-" + nodeID)

	c.proxy.AddRule(transport.ProxyRule{
		ID: id1,
		FromNode: nodeID,
		Action: transport.ActionLoss,
		Params: params,
	})
	c.proxy.AddRule(transport.ProxyRule{
		ID: id2,
		ToNode: nodeID,
		Action: transport.ActionLoss,
		Params: params,
	})
	c.mu.Unlock()
	
	c.RecordObservation(fmt.Sprintf("LOSS: %.0f%% packet loss injected for node %s (duration: %v)", lossRate*100, nodeID, duration))

	if duration > 0 {
		go func() {
			time.Sleep(duration)
			c.proxy.RemoveRule(id1)
			c.proxy.RemoveRule(id2)
			c.RecordObservation(fmt.Sprintf("HEAL: packet loss removed for node %s", nodeID))
		}()
	}
}