# Raftly

A production-quality implementation of the Raft consensus algorithm in Go, built from the paper:
[_In Search of an Understandable Consensus Algorithm_ (Ongaro & Ousterhout, 2014)](https://raft.github.io/raft.pdf).

Raftly implements the full Raft protocol including leader election, log replication, pre-vote, election restriction, and WAL crash recovery, and ships with a chaos injection framework that replays real-world distributed systems incidents.

---

## Features

| Feature            | Details                                                         |
| ------------------ | --------------------------------------------------------------- |
| Leader election    | Randomized timeouts, self-vote, term tracking                   |
| Pre-vote           | Prevents term disruption from partitioned followers             |
| Log replication    | AppendEntries with fast-backtracking conflict resolution        |
| Commit rule        | Leader only commits entries from its own term (§5.4.2)          |
| No-op on takeover  | New leader writes a no-op to flush stale entries                |
| WAL                | CRC32 checksums, torn-write detection, truncation on corruption |
| Chaos injection    | Network partition, node crash, message delay, packet loss       |
| KV state machine   | `PUT /keys/{k}`, `GET /keys/{k}`, `DELETE /keys/{k}`            |
| Prometheus metrics | Term, state, commit index, applied index, leader changes        |
| gRPC transport     | Real network transport for multi-process clusters               |

---

## Quick Start

**Prerequisites:** Go 1.26+, Docker (optional)

### Build

```bash
make build
# binary at bin/raftly-server

Test

make test          # all tests with race detector (~60s)

Run a 3-node cluster locally

Terminal 1:
./bin/raftly-server -id node1 -grpc-addr :7001 -http-addr :8001 \
-peers node2=:7002,node3=:7003 \
-http-peers node1=:8001,node2=:8002,node3=:8003

Terminal 2:
./bin/raftly-server -id node2 -grpc-addr :7002 -http-addr :8002 \
-peers node1=:7001,node3=:7003 \
-http-peers node1=:8001,node2=:8002,node3=:8003

Terminal 3:
./bin/raftly-server -id node3 -grpc-addr :7003 -http-addr :8003 \
-peers node1=:7001,node2=:7002 \
-http-peers node1=:8001,node2=:8002,node3=:8003

Use the KV API

# Write
curl -X PUT http://localhost:8001/keys/hello -d '{"value":"world"}'

# Read (follows 307 redirects to leader automatically)
curl http://localhost:8002/keys/hello

# Delete
curl -X DELETE http://localhost:8001/keys/hello

# Node status
curl http://localhost:8001/status

---
Docker

make docker-build   # build image
make docker-up      # start 3-node cluster + Prometheus + Grafana
make docker-down    # stop everything
make docker-logs    # tail logs

Services:

┌────────────┬─────────────────────────────────────┐
│  Service   │                 URL                 │
├────────────┼─────────────────────────────────────┤
│ node1 HTTP │ http://localhost:8001               │
├────────────┼─────────────────────────────────────┤
│ node2 HTTP │ http://localhost:8002               │
├────────────┼─────────────────────────────────────┤
│ node3 HTTP │ http://localhost:8003               │
├────────────┼─────────────────────────────────────┤
│ Prometheus │ http://localhost:9090               │
├────────────┼─────────────────────────────────────┤
│ Grafana    │ http://localhost:3000 (admin/admin) │
└────────────┴─────────────────────────────────────┘

---
Chaos Scenarios

Each scenario replays a real incident pattern and asserts the correct Raft safety property holds.

┌─────────────────────────────┬────────────────────────────┬──────────────────────────────────────────────────────────┐
│          Scenario           │      Incident modeled      │                     Safety property                      │
├─────────────────────────────┼────────────────────────────┼──────────────────────────────────────────────────────────┤
│ split-brain-2011            │ AWS EBS US-East-1 2011     │ Isolated leader's uncommitted writes discarded on heal   │
├─────────────────────────────┼────────────────────────────┼──────────────────────────────────────────────────────────┤
│ leader-isolation-write-loss │ Minority leader, no quorum │ Zero isolated commits survive                            │
├─────────────────────────────┼────────────────────────────┼──────────────────────────────────────────────────────────┤
│ wal-torn-write              │ Process crash mid-fsync    │ Corrupt WAL tail detected by CRC32 and truncated         │
├─────────────────────────────┼────────────────────────────┼──────────────────────────────────────────────────────────┤
│ stale-log-elected-leader    │ etcd 2018 stale leader     │ Election restriction blocks behind-log node from winning │
└─────────────────────────────┴────────────────────────────┴──────────────────────────────────────────────────────────┘

Run a scenario:

make scenario NAME=split-brain-2011
# or
bash scripts/scenario.sh wal-torn-write

---
Metrics

Scraped at /metrics on each node's HTTP port.

┌──────────────────────────────┬───────────┬────────────────────────────────────────┐
│            Metric            │   Type    │              Description               │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_current_term          │ Gauge     │ Current Raft term                      │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_state                 │ Gauge     │ 0=follower, 1=candidate, 2=leader      │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_committed_index       │ Gauge     │ Highest committed log index            │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_applied_index         │ Gauge     │ Highest applied log index              │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_log_entries_total     │ Counter   │ Total entries appended                 │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_elections_total       │ Counter   │ Elections started by this node         │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_leader_changes_total  │ Counter   │ Leader changes observed                │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_replication_lag       │ Histogram │ Replication lag per peer (leader only) │
├──────────────────────────────┼───────────┼────────────────────────────────────────┤
│ raftly_wal_fsync_duration_ms │ Histogram │ WAL fsync latency                      │
└──────────────────────────────┴───────────┴────────────────────────────────────────┘

---
Benchmarks

make bench          # all benchmarks
bash scripts/bench.sh WAL   # WAL benchmarks only

---
```
