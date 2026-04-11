#!/usr/bin/env bash
# Run all benchmarks and print a formatted report.
# Usage: bash scripts/bench.sh [pattern]
#   pattern — optional filter, e.g. "WAL" to run only WAL benchmarks

set -euo pipefail

PATTERN="${1:-}"
FILTER=""
if [[ -n "$PATTERN" ]]; then
    FILTER="-bench=${PATTERN}"
else
    FILTER="-bench=."
fi

echo "Running benchmarks (${PATTERN:-all})..."
go test ./... \
    ${FILTER} \
    -benchmem \
    -benchtime=3s \
    -count=1 \
    -run='^$' \
    | tee /tmp/raftly-bench.txt

echo ""
echo "Results saved to /tmp/raftly-bench.txt"