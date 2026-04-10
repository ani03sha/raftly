package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ani03sha/raftly/raft"
	"github.com/ani03sha/raftly/server"
)


func main() {
	nodeID    := flag.String("id", "", "Node ID (required)")
	grpcAddr  := flag.String("grpc-addr", "", "gRPC listen address, e.g. :7001")
	httpAddr  := flag.String("http-addr", "", "HTTP listen address, e.g. :8001")
	dataDir   := flag.String("data-dir", "", "WAL directory (default: data/<id>)")
	peers     := flag.String("peers", "", "Other nodes' gRPC addrs: node2=:7002,node3=:7003")
	httpPeers := flag.String("http-peers", "", "Other nodes' HTTP addrs: node2=:8002,node3=:8003")
	flag.Parse()

	if *nodeID == "" || *grpcAddr == "" || *httpAddr == "" {
		fmt.Fprintln(os.Stderr, "Usage: raftly-server -id <nodeID> -grpc-addr <grpcAddr> -http-addr <httpAddr> [-data-dir <dataDir>] [-peers <peerList>] [-http-peers <httpPeerList>]")
		flag.Usage()
		os.Exit(1)
	}

	dir := *dataDir
	if dir == "" {
		dir = "data/" + *nodeID
	}
	
	cfg := &server.ServerConfig{
		Raft: &raft.Config{
			NodeID:            *nodeID,
			Peers:             parseGRPCPeers(*peers),
			ElectionTimeout:   150 * time.Millisecond,
			HeartbeatInterval: 50 * time.Millisecond,
			MaxLogEntries:     1000,
			SnapshotThreshold: 10000,
			DataDir:           dir,
			EnablePreVote:     true,
		},
		GRPCAddr: *grpcAddr,
		HttpAddr: *httpAddr,
		HTTPPeers: parseHTTPPeers(*httpPeers),
	}

	srv, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create server: %v\n", err)
        os.Exit(1)
	}
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start server: %v\n", err)
        os.Exit(1)
	}

	fmt.Printf("[%s] started — gRPC=%s HTTP=%s\n", *nodeID, *grpcAddr, *httpAddr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Printf("[%s] shutting down...\n", *nodeID)
	ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
	defer cancel()
	srv.Stop(ctx)
}


// Parses "node2=:7002,node3=:7003" into []raft.PeerConfig
func parseGRPCPeers(s string) []raft.PeerConfig {
	if s == "" {
		return nil
	}
	var peers []raft.PeerConfig
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			peers = append(peers, raft.PeerConfig{ID: kv[0], Address: kv[1]})
		}
	}
	return peers
}

// Parses "node2=:8002,node3=:8003" into map[string]string
func parseHTTPPeers(s string) map[string]string {
	m := make(map[string]string)
	if s == "" {
		return m
	}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	return m
}