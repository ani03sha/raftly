package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

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

// Sends an HTTP 307 pointing the client to the current leader.
func (kv *KVStore) redirectToLeader(w http.ResponseWriter, r *http.Request) {
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
	// Preserve full path + query on redirect
	http.Redirect(w, r, "http://" + addr + r.RequestURI, http.StatusTemporaryRedirect)
}


// Handles: PUT /keys/{key}   body: {"value":"..."}
func (kv *KVStore) HandlePut(w http.ResponseWriter, r *http.Request) {
	if !kv.node.IsLeader() {
		kv.redirectToLeader(w, r)
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
			kv.redirectToLeader(w, r)
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
		kv.redirectToLeader(w, r)
		return
	}

	key := strings.TrimPrefix(r.URL.Path, "/keys")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	kv.mu.RLock()
	value, exists := kv.data[key]
	kv.mu.Unlock()

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
		kv.redirectToLeader(w, r)
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
			kv.redirectToLeader(w, r)
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
}