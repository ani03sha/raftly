package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/transport"
	"github.com/ani03sha/raftly/web"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)


// Holds everything to start a Raftly server node
type ServerConfig struct {
	Raft *raft.Config
	GRPCAddr string // this node's gRPC listen address (Raft peer communication)
	HttpAddr string // this node's HTTP listen address (client API + /metrics)
	HTTPPeers map[string]string // nodeID → HTTP address for ALL nodes (including self, for redirects)
}


// Server owns and manages the lifecycle of every Raftly subsystem.
type Server struct {
	config     *ServerConfig
	node       *raft.RaftNode
	grpc       *transport.GRPCTransport
	kv         *KVStore
	metrics    *Metrics
	httpServer *http.Server
	stopCh     chan struct{}
	logger     *zap.Logger
}


// Creates a server but doesn't start anything
func New(cfg *ServerConfig) (*Server, error) {
	logger := zap.Must(zap.NewDevelopment()).With(zap.String("component", "server"))
	metrics := NewMetrics()

	// Build the gRPC peers map from the Raft config's peer list
	grpcPeers := make(map[string]string, len(cfg.Raft.Peers))
	for _, p := range cfg.Raft.Peers {
		grpcPeers[p.ID] = p.Address
	}

	proxy := transport.NewNetworkProxy() // no chaos rules by default
	grpcTransport := transport.NewGRPCTransport(cfg.Raft.NodeID, cfg.GRPCAddr, grpcPeers, proxy)

	node, err := raft.NewRaftNode(cfg.Raft, grpcTransport)
	if err != nil {
        return nil, fmt.Errorf("create raft node: %w", err)
    }

	kv := NewKVStore(node, cfg.HTTPPeers, metrics)
	chaos := NewChaosAPI(cfg.Raft.NodeID, proxy, cfg.HTTPPeers)
	logAPI := NewLogAPI(node, cfg.HTTPPeers)
	configAPI := NewConfigAPI(cfg.Raft.NodeID, node, cfg.HTTPPeers)

	mux := http.NewServeMux()
	kv.RegisterRoutes(mux)
	chaos.RegisterRoutes(mux)
	logAPI.RegisterRoutes(mux)
	configAPI.RegisterRoutes(mux)
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", web.Handler())

	httpServer := &http.Server{
			Addr:    cfg.HttpAddr,
			Handler: mux,
	}

	return &Server{
			config:     cfg,
			node:       node,
			grpc:       grpcTransport,
			kv:         kv,
			metrics:    metrics,
			httpServer: httpServer,
			stopCh:     make(chan struct{}),
			logger:     logger,
	}, nil
}


// Begins all subsystems
func (s *Server) Start() error {
	// 1. gRPC must be up before elections start
	if err := s.grpc.Start(); err != nil {
		return fmt.Errorf("start gRPC transport: %w", err)
	}

	// 2. Raft node — elections and heartbeats begin here
	if err := s.node.Start(); err != nil {
		return fmt.Errorf("start raft node: %w", err)
	}

	// 3. Apply loop — drains commitCh into the KV map
	s.kv.Start()

	// 4. Metrics collection — polls node status every second
	go s.collectMetrics()

	// 5. HTTP server — clients can now connect
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP listener failed", zap.Error(err))
		}
	}()

	s.logger.Info("started",
		zap.String("node", s.config.Raft.NodeID),
		zap.String("grpc", s.config.GRPCAddr),
		zap.String("http", s.config.HttpAddr),
	)
	return nil
}


// Gracefully shuts everything down.
func (s *Server) Stop(ctx context.Context) {
	s.logger.Info("stopping", zap.String("node", s.config.Raft.NodeID))
	close(s.stopCh)
	s.httpServer.Shutdown(ctx) // stop accepting new requests
	s.kv.Stop()                // drain apply loop
	s.node.Stop()              // closes WAL + stopCh
	s.grpc.Close()             // close gRPC connections (Close() is the exported version)
}

// Polls node status and updates Prometheus gauges once per second.
func (s *Server) collectMetrics() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevLeader string
	for {
		select {
		case <-ticker.C:
			status := s.node.Status()
			s.metrics.Update(status, prevLeader)
			prevLeader = status.LeaderID
		case <-s.stopCh:
			return
		}
	}
}