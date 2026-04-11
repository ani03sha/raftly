#!/usr/bin/env bash
# Run a named chaos scenario through its Go test wrapper.
# Usage: bash scripts/scenario.sh <scenario-name>
#
# Available names:
#   split-brain-2011              TestChaosSplitBrain
#   leader-isolation-write-loss   TestChaosLeaderIsolation
#   wal-torn-write                TestChaosTornWrite
#   stale-log-elected-leader      TestChaosStaleElection

set -euo pipefail

NAME="${1:-}"

declare -A SCENARIO_TO_TEST=(
    ["split-brain-2011"]="TestChaosSplitBrain"
    ["leader-isolation-write-loss"]="TestChaosLeaderIsolation"
    ["wal-torn-write"]="TestChaosTornWrite"
    ["stale-log-elected-leader"]="TestChaosStaleElection"
)

if [[ -z "$NAME" ]]; then
    echo "Usage: $0 <scenario-name>"
    echo ""
    echo "Available scenarios:"
    for key in "${!SCENARIO_TO_TEST[@]}"; do
        echo "  $key"
    done
    exit 1
fi

TEST="${SCENARIO_TO_TEST[$NAME]:-}"
if [[ -z "$TEST" ]]; then
    echo "Unknown scenario: $NAME"
    echo "Run without arguments to list available scenarios."
    exit 1
fi

echo "Running scenario: $NAME ($TEST)"
go test ./tests/ -run "^${TEST}$" -v -timeout 60s