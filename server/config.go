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

	"github.com/ani03sha/raftly/raft"
)


type ConfigAPI struct {
	nodeID    string
	node      *raft.RaftNode
	httpPeers map[string]string
}


func NewConfigAPI(nodeID string, node *raft.RaftNode, httpPeers map[string]string) *ConfigAPI {
	return &ConfigAPI{nodeID: nodeID, node: node, httpPeers: httpPeers}
}


func (c *ConfigAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/config", c.handleNodeConfig)
	mux.HandleFunc("/api/cluster/config", c.handleClusterConfig)
}


// GET /api/config  — current timings for this node
// POST /api/config — update timings on this node
func (c *ConfigAPI) handleNodeConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		et, hb := c.node.GetTimings()
		writeJSON(w, map[string]interface{}{
			"node":                c.nodeID,
			"election_timeout_ms": et.Milliseconds(),
			"heartbeat_ms":        hb.Milliseconds(),
		})
	case http.MethodPost:
		var body struct {
			ElectionTimeoutMs int `json:"election_timeout_ms"`
			HeartbeatMs       int `json:"heartbeat_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		et := time.Duration(body.ElectionTimeoutMs) * time.Millisecond
		hb := time.Duration(body.HeartbeatMs) * time.Millisecond
		c.node.UpdateTimings(et, hb)
		et2, hb2 := c.node.GetTimings()
		writeJSON(w, map[string]interface{}{
			"node":                c.nodeID,
			"election_timeout_ms": et2.Milliseconds(),
			"heartbeat_ms":        hb2.Milliseconds(),
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}


type nodeConfigView struct {
	Node              string `json:"node"`
	ElectionTimeoutMs int64  `json:"election_timeout_ms"`
	HeartbeatMs       int64  `json:"heartbeat_ms"`
	Error             string `json:"error,omitempty"`
}


// GET /api/cluster/config  — fan-out GET to every peer
// POST /api/cluster/config — fan-out POST to every peer
func (c *ConfigAPI) handleClusterConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		results := c.fanOutGet(r.Context())
		writeJSON(w, map[string]interface{}{"nodes": results})
	case http.MethodPost:
		var body struct {
			ElectionTimeoutMs int `json:"election_timeout_ms"`
			HeartbeatMs       int `json:"heartbeat_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		results := c.fanOutPost(r.Context(), body.ElectionTimeoutMs, body.HeartbeatMs)
		writeJSON(w, map[string]interface{}{"nodes": results})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}


func (c *ConfigAPI) peerIDs() []string {
	ids := make([]string, 0, len(c.httpPeers))
	for id := range c.httpPeers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}


func (c *ConfigAPI) fanOutGet(ctx context.Context) []nodeConfigView {
	ids := c.peerIDs()
	results := make([]nodeConfigView, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id == c.nodeID {
				et, hb := c.node.GetTimings()
				results[i] = nodeConfigView{
					Node:              id,
					ElectionTimeoutMs: et.Milliseconds(),
					HeartbeatMs:       hb.Milliseconds(),
				}
				return
			}
			addr := c.httpPeers[id]
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+"/api/config", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results[i] = nodeConfigView{Node: id, Error: err.Error()}
				return
			}
			defer resp.Body.Close()
			var v nodeConfigView
			if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
				results[i] = nodeConfigView{Node: id, Error: err.Error()}
				return
			}
			v.Node = id
			results[i] = v
		}()
	}
	wg.Wait()
	return results
}


func (c *ConfigAPI) fanOutPost(ctx context.Context, electionTimeoutMs, heartbeatMs int) []nodeConfigView {
	ids := c.peerIDs()
	results := make([]nodeConfigView, len(ids))
	payload, _ := json.Marshal(map[string]int{
		"election_timeout_ms": electionTimeoutMs,
		"heartbeat_ms":        heartbeatMs,
	})
	var wg sync.WaitGroup
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id == c.nodeID {
				et := time.Duration(electionTimeoutMs) * time.Millisecond
				hb := time.Duration(heartbeatMs) * time.Millisecond
				c.node.UpdateTimings(et, hb)
				et2, hb2 := c.node.GetTimings()
				results[i] = nodeConfigView{
					Node:              id,
					ElectionTimeoutMs: et2.Milliseconds(),
					HeartbeatMs:       hb2.Milliseconds(),
				}
				return
			}
			addr := c.httpPeers[id]
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
				"http://"+addr+"/api/config", bytes.NewReader(payload))
			if err != nil {
				results[i] = nodeConfigView{Node: id, Error: err.Error()}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results[i] = nodeConfigView{Node: id, Error: err.Error()}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				b, _ := io.ReadAll(resp.Body)
				results[i] = nodeConfigView{Node: id, Error: fmt.Sprintf("%d: %s", resp.StatusCode, string(b))}
				return
			}
			var v nodeConfigView
			if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
				results[i] = nodeConfigView{Node: id, Error: err.Error()}
				return
			}
			v.Node = id
			results[i] = v
		}()
	}
	wg.Wait()
	return results
}
