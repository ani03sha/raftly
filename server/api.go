package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ani03sha/raftly/raft"
)


type Command struct {
	Op string `json:"op"` // "put" or "delete"
	Key string `json:"key"`
	Value string `json:"value,omitempty"` // only set for "put"
}


// KVStore is the state machine: map[string]string driven by the Raft commit log.
// Only applyLoop() writes to data; HTTP handlers read from it.
type KVStore struct {
	mu sync.RWMutex
	data map[string]string
	node *raft.RaftNode
	httpPeers map[string]string // nodeID -> HTTP address, used for leader redirects
	metrics *Metrics
	stopCh chan struct{}
}


func NewKVStore(node *raft.RaftNode, httpPeers map[string]string, m *Metrics) *KVStore {
	return &KVStore{
		data: make(map[string]string),
		node: node,
		httpPeers: httpPeers,
		metrics: m,
		stopCh: make(chan struct{}),
	}
}


// Start launches the background goroutine that applies committed entries to the map.
func (kv *KVStore) Start() {
	go kv.applyLoop()
}


// Stop signals the apply loop to exit.
func (kv *KVStore) Stop() {
	close(kv.stopCh)
}


// Reads committed log entries from the Raft node and applies them to the map.
// This is the only goroutine allowed to write to kv.data.
func (kv *KVStore) applyLoop() {
	for {
		select {
		case entry, ok := <-kv.node.CommitCh():
			if !ok {
				return
			}
			kv.apply(entry)
		case <-kv.stopCh:
			return
		}
	}
}


// Apply decodes one committed log entry and mutates the state machine.
func (kv *KVStore) apply(entry raft.LogEntry) {
	var cmd Command
	if err := json.Unmarshal(entry.Data, &cmd); err != nil {
		return // malformed entry - skip
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	switch cmd.Op {
	case "put":
		kv.data[cmd.Key] = cmd.Value
		if kv.metrics != nil {
			kv.metrics.LogEntriesTotal.Inc()
		}
	case "delete":
		delete(kv.data, cmd.Key)
		if kv.metrics != nil {
			kv.metrics.LogEntriesTotal.Inc()
		}
	}
}


// --- HTTP Handlers ---

// Forwards the request to the current leader and streams the response back.
// This keeps internal Docker hostnames invisible to clients.
func (kv *KVStore) proxyToLeader(w http.ResponseWriter, r *http.Request) {
	leaderID := kv.node.LeaderID()
	if leaderID == "" {
		http.Error(w, "no leader elected", http.StatusServiceUnavailable)
		return
	}
	addr, ok := kv.httpPeers[leaderID]
	if !ok {
		http.Error(w, fmt.Sprintf("leader %s address unknown", leaderID), http.StatusServiceUnavailable)
		return
	}

	target := "http://" + addr + r.RequestURI
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "proxy error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for key, vals := range r.Header {
		for _, v := range vals {
			proxyReq.Header.Add(key, v)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "proxy to leader failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}


// Handles: PUT /keys/{key}   body: {"value":"..."}
func (kv *KVStore) HandlePut(w http.ResponseWriter, r *http.Request) {
	if !kv.node.IsLeader() {
		kv.proxyToLeader(w, r)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/keys/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
        return
	}

	cmd, _ := json.Marshal(Command{Op: "put", Key: key, Value: body.Value})
	if _, _, err := kv.node.Propose(cmd); err != nil {
		if strings.Contains(err.Error(), "not leader") {
			kv.proxyToLeader(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}


// Handles: GET /keys/{key}
func (kv *KVStore) HandleGet(w http.ResponseWriter, r *http.Request) {
	if !kv.node.IsLeader() {
		kv.proxyToLeader(w, r)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/keys/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	kv.mu.RLock()
	value, exists := kv.data[key]
	kv.mu.RUnlock()

	if !exists {
		http.Error(w, "key not found", http.StatusNotFound)
        return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": key, "value": value})
}


// Handles: DELETE /keys/{key}
func (kv *KVStore) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if !kv.node.IsLeader() {
		kv.proxyToLeader(w, r)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/keys/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	cmd, _ := json.Marshal(Command{Op: "delete", Key: key})
	if _, _, err := kv.node.Propose(cmd); err != nil {
		if strings.Contains(err.Error(), "not leader") {
			kv.proxyToLeader(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}


// Handles: GET /status — returns node's Raft state as JSON.
func (kv *KVStore) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s := kv.node.Status()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           s.ID,
		"state":        s.State.String(),
		"term":         s.Term,
		"leader_id":    s.LeaderID,
		"commit_index": s.CommitIndex,
		"last_applied": s.LastApplied,
	})
}


// RegisterRoutes wires all KVStore handlers into an HTTP mux.
func (kv *KVStore) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/keys/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			kv.HandleGet(w, r)
		case http.MethodPut:
			kv.HandlePut(w, r)
		case http.MethodDelete:
			kv.HandleDelete(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/status", kv.HandleStatus)
	mux.HandleFunc("/api/cluster", kv.HandleCluster)
	mux.HandleFunc("/api/events", kv.HandleEvents)
}


// --- Cluster aggregation & SSE event stream ---

type nodeView struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Term        uint64 `json:"term"`
	LeaderID    string `json:"leader_id"`
	CommitIndex uint64 `json:"commit_index"`
	LastApplied uint64 `json:"last_applied"`
	Reachable   bool   `json:"reachable"`
}


type clusterView struct {
	Nodes     []nodeView `json:"nodes"`
	LeaderID  string     `json:"leader_id"`
	Term      uint64     `json:"term"`
	Timestamp int64      `json:"timestamp"`
}


// Fans out to every peer's /status endpoint in parallel. Unreachable nodes
// are reported with Reachable=false rather than failing the whole request.
func (kv *KVStore) fetchClusterSnapshot(ctx context.Context) clusterView {
	ids := make([]string, 0, len(kv.httpPeers))
	for id := range kv.httpPeers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var mu sync.Mutex
	views := make([]nodeView, 0, len(ids))
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		addr := kv.httpPeers[id]
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := fetchNodeStatus(ctx, id, addr)
			mu.Lock()
			views = append(views, v)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })

	snap := clusterView{Nodes: views, Timestamp: time.Now().UnixMilli()}
	for _, n := range views {
		if n.State == "Leader" {
			snap.LeaderID = n.ID
		}
		if n.Term > snap.Term {
			snap.Term = n.Term
		}
	}
	return snap
}


func fetchNodeStatus(ctx context.Context, id, addr string) nodeView {
	reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+"/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nodeView{ID: id, State: "Down", Reachable: false}
	}
	defer resp.Body.Close()
	var raw struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Term        uint64 `json:"term"`
		LeaderID    string `json:"leader_id"`
		CommitIndex uint64 `json:"commit_index"`
		LastApplied uint64 `json:"last_applied"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nodeView{ID: id, State: "Down", Reachable: false}
	}
	return nodeView{
		ID:          raw.ID,
		State:       raw.State,
		Term:        raw.Term,
		LeaderID:    raw.LeaderID,
		CommitIndex: raw.CommitIndex,
		LastApplied: raw.LastApplied,
		Reachable:   true,
	}
}


// Handles: GET /api/cluster — aggregated snapshot across all peers.
func (kv *KVStore) HandleCluster(w http.ResponseWriter, r *http.Request) {
	snap := kv.fetchClusterSnapshot(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap)
}


// Handles: GET /api/events — Server-Sent Events. Emits a "status" frame every
// 500ms and diff events (leader_change, term_change, node_down, node_up)
// whenever the snapshot changes.
func (kv *KVStore) HandleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(typ string, payload interface{}, nodeID string) {
		evt := map[string]interface{}{
			"type":      typ,
			"timestamp": time.Now().UnixMilli(),
			"data":      payload,
		}
		if nodeID != "" {
			evt["node_id"] = nodeID
		}
		b, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	prev := kv.fetchClusterSnapshot(r.Context())
	send("status", prev, "")

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			curr := kv.fetchClusterSnapshot(r.Context())
			send("status", curr, "")

			if curr.LeaderID != prev.LeaderID && curr.LeaderID != "" {
				send("leader_change", map[string]interface{}{
					"from": prev.LeaderID,
					"to":   curr.LeaderID,
				}, curr.LeaderID)
			}
			if curr.Term > prev.Term {
				send("term_change", map[string]interface{}{
					"from": prev.Term,
					"to":   curr.Term,
				}, "")
			}
			prevMap := make(map[string]nodeView, len(prev.Nodes))
			for _, n := range prev.Nodes {
				prevMap[n.ID] = n
			}
			for _, n := range curr.Nodes {
				p, existed := prevMap[n.ID]
				if existed && p.Reachable && !n.Reachable {
					send("node_down", map[string]interface{}{"id": n.ID}, n.ID)
				}
				if existed && !p.Reachable && n.Reachable {
					send("node_up", map[string]interface{}{"id": n.ID}, n.ID)
				}
			}
			prev = curr
		}
	}
}