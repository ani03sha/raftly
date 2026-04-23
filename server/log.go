package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ani03sha/raftly/raft"
)


// LogAPI exposes the Raft log for visualization in the dashboard. Each node
// serves its own log; the cluster endpoint fans out to every peer.
type LogAPI struct {
	node      *raft.RaftNode
	httpPeers map[string]string
}


func NewLogAPI(node *raft.RaftNode, httpPeers map[string]string) *LogAPI {
	return &LogAPI{node: node, httpPeers: httpPeers}
}


func (l *LogAPI) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/log", l.handleLog)
	mux.HandleFunc("/api/cluster/log", l.handleClusterLog)
}


type logEntryView struct {
	Index     uint64 `json:"index"`
	Term      uint64 `json:"term"`
	Type      string `json:"type"`
	Committed bool   `json:"committed"`
	Applied   bool   `json:"applied"`
	Data      string `json:"data,omitempty"` // decoded JSON command if parsable, else base64
}


type nodeLogView struct {
	Node        string         `json:"node"`
	LastIndex   uint64         `json:"last_index"`
	CommitIndex uint64         `json:"commit_index"`
	LastApplied uint64         `json:"last_applied"`
	Entries     []logEntryView `json:"entries"`
	Error       string         `json:"error,omitempty"`
}


// GET /api/log?limit=20 — this node's last N log entries.
func (l *LogAPI) handleLog(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 20)
	writeJSON(w, l.localView(limit))
}


// GET /api/cluster/log?limit=20 — aggregated across all peers.
func (l *LogAPI) handleClusterLog(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 20)
	ids := make([]string, 0, len(l.httpPeers))
	for id := range l.httpPeers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	results := make([]nodeLogView, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id == l.node.Status().ID {
				results[i] = l.localView(limit)
				return
			}
			v, err := l.fetchPeerLog(r.Context(), id, limit)
			if err != nil {
				results[i] = nodeLogView{Node: id, Error: err.Error()}
				return
			}
			results[i] = v
		}()
	}
	wg.Wait()
	writeJSON(w, map[string]interface{}{"nodes": results})
}


func (l *LogAPI) localView(limit int) nodeLogView {
	status := l.node.Status()
	entries := l.node.LogSnapshot(limit)
	views := make([]logEntryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, logEntryView{
			Index:     e.Index,
			Term:      e.Term,
			Type:      entryTypeString(e.Type),
			Committed: e.Index <= status.CommitIndex,
			Applied:   e.Index <= status.LastApplied,
			Data:      decodeEntry(e.Data),
		})
	}
	return nodeLogView{
		Node:        status.ID,
		LastIndex:   status.LogIndex,
		CommitIndex: status.CommitIndex,
		LastApplied: status.LastApplied,
		Entries:     views,
	}
}


func (l *LogAPI) fetchPeerLog(ctx context.Context, peer string, limit int) (nodeLogView, error) {
	addr := l.httpPeers[peer]
	reqCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	url := "http://" + addr + "/api/log?limit=" + strconv.Itoa(limit)
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nodeLogView{Node: peer}, err
	}
	defer resp.Body.Close()
	var v nodeLogView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nodeLogView{Node: peer}, err
	}
	return v, nil
}


func parseLimit(r *http.Request, fallback int) int {
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 200 {
			return n
		}
	}
	return fallback
}


func entryTypeString(t raft.EntryType) string {
	switch t {
	case raft.EntryNormal:
		return "normal"
	case raft.EntryConfig:
		return "config"
	default:
		return "unknown"
	}
}


// Decodes raw entry bytes into a compact string. If the payload is a
// JSON-encoded KV command (op, key, value), render it as "op key=value";
// otherwise return the raw string.
func decodeEntry(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var cmd Command
	if err := json.Unmarshal(data, &cmd); err == nil && cmd.Op != "" {
		switch cmd.Op {
		case "put":
			return "put " + cmd.Key + "=" + cmd.Value
		case "delete":
			return "del " + cmd.Key
		default:
			return cmd.Op + " " + cmd.Key
		}
	}
	return string(data)
}
