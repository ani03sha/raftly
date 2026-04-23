package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ani03sha/raftly/transport"
)


// ChaosAPI exposes HTTP control endpoints for the NetworkProxy attached to
// this node, plus cluster-wide orchestration endpoints that fan out to peers.
//
// Per-node endpoints manipulate only the local proxy (affect this node's
// outbound traffic). Cluster endpoints let the UI issue a single request that
// installs matching rules on every node — essential for symmetric partitions
// and preset scenarios.
type ChaosAPI struct {
	nodeID    string
	proxy     *transport.NetworkProxy
	httpPeers map[string]string // nodeID -> HTTP addr, including self
	counter   int
	mu        sync.Mutex
}


func NewChaosAPI(nodeID string, proxy *transport.NetworkProxy, httpPeers map[string]string) *ChaosAPI {
	return &ChaosAPI{
		nodeID:    nodeID,
		proxy:     proxy,
		httpPeers: httpPeers,
	}
}


func (c *ChaosAPI) RegisterRoutes(mux *http.ServeMux) {
	// Per-node
	mux.HandleFunc("/api/chaos/rules", c.handleRules)
	mux.HandleFunc("/api/chaos/apply", c.handleApplyLocal)
	mux.HandleFunc("/api/chaos/heal", c.handleHealLocal)

	// Cluster orchestration
	mux.HandleFunc("/api/cluster/chaos/state", c.handleClusterState)
	mux.HandleFunc("/api/cluster/chaos/isolate", c.handleIsolate)
	mux.HandleFunc("/api/cluster/chaos/partition", c.handlePartition)
	mux.HandleFunc("/api/cluster/chaos/delay", c.handleDelay)
	mux.HandleFunc("/api/cluster/chaos/loss", c.handleLoss)
	mux.HandleFunc("/api/cluster/chaos/heal", c.handleClusterHeal)
}


// --- Wire types ---

type ruleSpec struct {
	ID       string                 `json:"id"`
	FromNode string                 `json:"from_node,omitempty"`
	ToNode   string                 `json:"to_node,omitempty"`
	Action   string                 `json:"action"` // drop | delay | loss
	Params   map[string]interface{} `json:"params,omitempty"`
}


type ruleResponse struct {
	ID       string                 `json:"id"`
	FromNode string                 `json:"from_node,omitempty"`
	ToNode   string                 `json:"to_node,omitempty"`
	Action   string                 `json:"action"`
	Params   map[string]interface{} `json:"params,omitempty"`
}


type applyRequest struct {
	Rules []ruleSpec `json:"rules"`
}


// --- Per-node handlers ---

// GET /api/chaos/rules — list active rules on this node's proxy.
func (c *ChaosAPI) handleRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"node":  c.nodeID,
		"rules": toRuleResponses(c.proxy.Rules()),
	})
}


// POST /api/chaos/apply — install the given rules on this node's proxy.
// Existing rules are preserved; use /api/chaos/heal to clear.
func (c *ChaosAPI) handleApplyLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req applyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	installed := c.applyLocal(req.Rules)
	writeJSON(w, map[string]interface{}{"installed": installed, "node": c.nodeID})
}


// POST /api/chaos/heal — clear all rules on this node's proxy.
func (c *ChaosAPI) handleHealLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.proxy.ClearRules()
	writeJSON(w, map[string]interface{}{"healed": c.nodeID})
}


// Installs rules on the local proxy. Any rule missing an ID gets one assigned.
// Returns the canonical IDs.
func (c *ChaosAPI) applyLocal(rules []ruleSpec) []string {
	ids := make([]string, 0, len(rules))
	for _, spec := range rules {
		rule := transport.ProxyRule{
			ID:       c.ensureID(spec.ID, spec.Action),
			FromNode: spec.FromNode,
			ToNode:   spec.ToNode,
			Action:   actionFromString(spec.Action),
			Params:   spec.Params,
		}
		c.proxy.AddRule(rule)
		ids = append(ids, rule.ID)
	}
	return ids
}


func (c *ChaosAPI) ensureID(id, action string) string {
	if id != "" {
		return id
	}
	c.mu.Lock()
	c.counter++
	n := c.counter
	c.mu.Unlock()
	return fmt.Sprintf("%s-%s-%d", c.nodeID, action, n)
}


// --- Cluster orchestration ---

// GET /api/cluster/chaos/state — aggregated rules from every reachable peer.
func (c *ChaosAPI) handleClusterState(w http.ResponseWriter, r *http.Request) {
	type peerRules struct {
		Node  string         `json:"node"`
		Rules []ruleResponse `json:"rules"`
		Error string         `json:"error,omitempty"`
	}
	ids := c.peerIDs()
	results := make([]peerRules, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			rules, err := c.fetchPeerRules(r.Context(), id)
			pr := peerRules{Node: id}
			if err != nil {
				pr.Error = err.Error()
			} else {
				pr.Rules = rules
			}
			results[i] = pr
		}()
	}
	wg.Wait()
	writeJSON(w, map[string]interface{}{"peers": results})
}


// POST /api/cluster/chaos/isolate  body: {node}
// Fully isolates the target node: drops all traffic from it and to it.
func (c *ChaosAPI) handleIsolate(w http.ResponseWriter, r *http.Request) {
	var body struct{ Node string `json:"node"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Node == "" {
		http.Error(w, "body requires {node}", http.StatusBadRequest)
		return
	}
	target := body.Node

	plan := map[string][]ruleSpec{}
	// On the target: drop everything it tries to send.
	plan[target] = []ruleSpec{{
		ID:     fmt.Sprintf("isolate-%s-out", target),
		Action: "drop",
	}}
	// On every other node: drop traffic destined for the target.
	for _, id := range c.peerIDs() {
		if id == target {
			continue
		}
		plan[id] = []ruleSpec{{
			ID:     fmt.Sprintf("isolate-%s-in-from-%s", target, id),
			ToNode: target,
			Action: "drop",
		}}
	}

	writeJSON(w, c.executePlan(r.Context(), plan, fmt.Sprintf("isolate %s", target)))
}


// POST /api/cluster/chaos/partition  body: {a, b}
// Drops traffic in both directions between two nodes; other links untouched.
func (c *ChaosAPI) handlePartition(w http.ResponseWriter, r *http.Request) {
	var body struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.A == "" || body.B == "" {
		http.Error(w, "body requires {a, b}", http.StatusBadRequest)
		return
	}
	plan := map[string][]ruleSpec{
		body.A: {{ID: fmt.Sprintf("part-%s-%s", body.A, body.B), ToNode: body.B, Action: "drop"}},
		body.B: {{ID: fmt.Sprintf("part-%s-%s", body.B, body.A), ToNode: body.A, Action: "drop"}},
	}
	writeJSON(w, c.executePlan(r.Context(), plan, fmt.Sprintf("partition %s <-> %s", body.A, body.B)))
}


// POST /api/cluster/chaos/delay  body: {node, delay_ms, jitter_ms}
// Adds latency to all messages on the target node's outbound links and on
// every other node's links back to the target.
func (c *ChaosAPI) handleDelay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Node     string `json:"node"`
		DelayMs  int    `json:"delay_ms"`
		JitterMs int    `json:"jitter_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Node == "" || body.DelayMs <= 0 {
		http.Error(w, "body requires {node, delay_ms > 0}", http.StatusBadRequest)
		return
	}
	params := map[string]interface{}{"delay_ms": body.DelayMs, "jitter_ms": body.JitterMs}
	plan := map[string][]ruleSpec{}
	plan[body.Node] = []ruleSpec{{
		ID: fmt.Sprintf("delay-%s-out", body.Node), Action: "delay", Params: params,
	}}
	for _, id := range c.peerIDs() {
		if id == body.Node {
			continue
		}
		plan[id] = []ruleSpec{{
			ID: fmt.Sprintf("delay-%s-to-from-%s", body.Node, id), ToNode: body.Node, Action: "delay", Params: params,
		}}
	}
	writeJSON(w, c.executePlan(r.Context(), plan, fmt.Sprintf("delay %dms±%dms on %s", body.DelayMs, body.JitterMs, body.Node)))
}


// POST /api/cluster/chaos/loss  body: {node, loss_rate}
func (c *ChaosAPI) handleLoss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Node     string  `json:"node"`
		LossRate float64 `json:"loss_rate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Node == "" || body.LossRate <= 0 || body.LossRate > 1 {
		http.Error(w, "body requires {node, loss_rate in (0, 1]}", http.StatusBadRequest)
		return
	}
	params := map[string]interface{}{"loss_rate": body.LossRate}
	plan := map[string][]ruleSpec{}
	plan[body.Node] = []ruleSpec{{
		ID: fmt.Sprintf("loss-%s-out", body.Node), Action: "loss", Params: params,
	}}
	for _, id := range c.peerIDs() {
		if id == body.Node {
			continue
		}
		plan[id] = []ruleSpec{{
			ID: fmt.Sprintf("loss-%s-to-from-%s", body.Node, id), ToNode: body.Node, Action: "loss", Params: params,
		}}
	}
	writeJSON(w, c.executePlan(r.Context(), plan, fmt.Sprintf("loss %.0f%% on %s", body.LossRate*100, body.Node)))
}


// POST /api/cluster/chaos/heal — heal every node.
func (c *ChaosAPI) handleClusterHeal(w http.ResponseWriter, r *http.Request) {
	ids := c.peerIDs()
	results := make(map[string]string, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.healPeer(r.Context(), id)
			mu.Lock()
			if err != nil {
				results[id] = err.Error()
			} else {
				results[id] = "ok"
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	writeJSON(w, map[string]interface{}{"healed": results})
}


// --- Orchestration helpers ---

// Sends each node its rule set and aggregates the response. The local node is
// updated in-process; remote nodes receive POST /api/chaos/apply.
func (c *ChaosAPI) executePlan(ctx context.Context, plan map[string][]ruleSpec, label string) map[string]interface{} {
	type result struct {
		Node      string   `json:"node"`
		Installed []string `json:"installed,omitempty"`
		Error     string   `json:"error,omitempty"`
	}
	results := []result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for node, rules := range plan {
		node, rules := node, rules
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := result{Node: node}
			if node == c.nodeID {
				res.Installed = c.applyLocal(rules)
			} else {
				ids, err := c.dispatchRules(ctx, node, rules)
				if err != nil {
					res.Error = err.Error()
				} else {
					res.Installed = ids
				}
			}
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Node < results[j].Node })
	return map[string]interface{}{"action": label, "results": results}
}


func (c *ChaosAPI) dispatchRules(ctx context.Context, peer string, rules []ruleSpec) ([]string, error) {
	addr, ok := c.httpPeers[peer]
	if !ok {
		return nil, fmt.Errorf("unknown peer %s", peer)
	}
	body, _ := json.Marshal(applyRequest{Rules: rules})
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://"+addr+"/api/chaos/apply", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("peer %s responded %d: %s", peer, resp.StatusCode, string(b))
	}
	var parsed struct {
		Installed []string `json:"installed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Installed, nil
}


func (c *ChaosAPI) healPeer(ctx context.Context, peer string) error {
	if peer == c.nodeID {
		c.proxy.ClearRules()
		return nil
	}
	addr, ok := c.httpPeers[peer]
	if !ok {
		return fmt.Errorf("unknown peer %s", peer)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://"+addr+"/api/chaos/heal", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer %s responded %d: %s", peer, resp.StatusCode, string(b))
	}
	return nil
}


func (c *ChaosAPI) fetchPeerRules(ctx context.Context, peer string) ([]ruleResponse, error) {
	if peer == c.nodeID {
		return toRuleResponses(c.proxy.Rules()), nil
	}
	addr, ok := c.httpPeers[peer]
	if !ok {
		return nil, fmt.Errorf("unknown peer %s", peer)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+"/api/chaos/rules", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed struct {
		Rules []ruleResponse `json:"rules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Rules, nil
}


func (c *ChaosAPI) peerIDs() []string {
	ids := make([]string, 0, len(c.httpPeers))
	for id := range c.httpPeers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}


// --- Helpers ---

func actionFromString(s string) transport.ProxyAction {
	switch s {
	case "drop":
		return transport.ActionDrop
	case "delay":
		return transport.ActionDelay
	case "loss":
		return transport.ActionLoss
	default:
		return transport.ActionPass
	}
}


func toRuleResponses(rules []transport.ProxyRule) []ruleResponse {
	out := make([]ruleResponse, 0, len(rules))
	for _, r := range rules {
		out = append(out, ruleResponse{
			ID:       r.ID,
			FromNode: r.FromNode,
			ToNode:   r.ToNode,
			Action:   r.Action.String(),
			Params:   r.Params,
		})
	}
	return out
}


func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
