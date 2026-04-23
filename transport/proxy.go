package transport

import (
	"math/rand"
	"sync"
	"time"
)


// Defines what the proxy does with a matching message.
type ProxyAction int

const (
	ActionPass ProxyAction = iota // deliver normally (default)
	ActionDrop // silently discard
	ActionDelay // deliver after a fixed delay
	ActionLoss // deliver with probability (1 - loss_rate)
)


// This describes one interception rule. FromNode and ToNode are nodeID; empty string means "any node".
// Rules are evaluated in order; the first match wins.
type ProxyRule struct {
	ID string // unique identifier - used to remove the rule later
	FromNode string // "" = match any sender
	ToNode string // "" = match any receiver
	Action ProxyAction
	Params map[string]interface{} // action-specific parameters
}


// Param keys by action:
// 		ActionDelay: "delay_ms" int - milliseconds to hold the message
// 					 "jitter_ms" int - random jitter added to delay (optional)
//		ActionLoss: "loss_rate" float64 - probability [0.0, 1.0] of dropping

// This intercepts messages between Raft nodes.
// It is the single control point for all chaos injection.
// The gRPC transport calls ShouldDeliver before every outbound RPC.
type NetworkProxy struct {
	rules []ProxyRule
	mu sync.RWMutex
}


// Returns a proxy with new rules (all messages pass through)
func NewNetworkProxy() *NetworkProxy {
	return &NetworkProxy{}
}


// Appends a rule to the end of the rule list. Rules are evaluated in order; earlier rules take priority.
func (p *NetworkProxy) AddRule(rule ProxyRule) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = append(p.rules, rule)
}


// Deletes the rules with the given ID. No-op if the ID doesn't exist.
func (p *NetworkProxy) RemoveRule(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	filtered := p.rules[:0] // reuse the underlying array - avoids allocation
	for _, rule := range p.rules {
		if rule.ID != id {
			filtered = append(filtered, rule)
		}
	}
	p.rules = filtered
}


// Removes all rules. All messages pass through after this call.
func (p *NetworkProxy) ClearRules() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.rules = nil
}


// Returns a copy of the current rule set. Used by the dashboard to render
// active chaos.
func (p *NetworkProxy) Rules() []ProxyRule {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ProxyRule, len(p.rules))
	copy(out, p.rules)
	return out
}


// Returns the string name for a ProxyAction. Useful for JSON serialization.
func (a ProxyAction) String() string {
	switch a {
	case ActionPass:
		return "pass"
	case ActionDrop:
		return "drop"
	case ActionDelay:
		return "delay"
	case ActionLoss:
		return "loss"
	default:
		return "unknown"
	}
}


// Evaluates the rules list for a message traveling from -> to.
// Returns:
//    deliver bool - false means drop the message silently
//    delay time.Duration - how long to wait before delivering (0 = immediate)
//
// If no rule matches, the message is delivered immediately (pass-through default).
func (p *NetworkProxy) ShouldDeliver(from, to string) (deliver bool, delay time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, rule := range p.rules {
		if !p.matches(rule, from, to) {
			continue
		}

		switch rule.Action {
		case ActionDrop:
			return false, 0
		
		case ActionDelay:
			ms := 0
			if v, ok := rule.Params["delay_ms"].(int); ok {
				ms = v
			}
			jitter := 0
			if v, ok := rule.Params["jitter_ms"].(int); ok && v > 0 {
				jitter = rand.Intn(v)
			}
			return true, time.Duration(ms + jitter) * time.Millisecond

		case ActionLoss:
			rate := 0.0
			if v, ok := rule.Params["loss_rate"].(float64); ok {
				rate = v
			}
			// rand.Float64() returns [0.0, 1.0). Drop if random value < loss_rate.
			if rand.Float64() < rate {
				return false, 0
			}
			return true, 0
		
		case ActionPass:
			return true, 0
		}
	}

	// No rule matched - default is to pass through
	return true, 0
}


// Returns true if the rule applies to a message traveling from -> to.
// An empty FromNode to ToNode is a wildcard that any node ID.
func (p *NetworkProxy) matches(rule ProxyRule, from, to string) bool {
	fromOK := rule.FromNode == "" || rule.FromNode == from
	toOk := rule.ToNode == "" || rule.ToNode == to
	return fromOK && toOk
}