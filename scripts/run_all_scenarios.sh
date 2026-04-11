#!/usr/bin/env bash
# Run all chaos scenarios in sequence and print a formatted summary.
# Usage: bash scripts/run-all-scenarios.sh

set -euo pipefail

SCENARIOS=(
    "split-brain-2011:TestChaosSplitBrain"
    "wal-torn-write:TestChaosTornWrite"
    "leader-isolation-write-loss:TestChaosLeaderIsolation"
    "stale-log-elected-leader:TestChaosStaleElection"
)

PASSED=0
FAILED=0
declare -a RESULTS

LINE="---------------------------------------------------------------"

echo "$LINE"
printf "  %-35s %s\n" "SCENARIO" "RESULT"
echo "$LINE"

for entry in "${SCENARIOS[@]}"; do
    NAME="${entry%%:*}"
    TEST="${entry##*:}"

    OUTPUT=$(go test ./tests/ -run "^${TEST}$" -v -timeout 60s 2>&1) || true

    if echo "$OUTPUT" | grep -q "^--- PASS"; then
        STATUS="PASS"
        PASSED=$((PASSED + 1))
    else
        STATUS="FAIL"
        FAILED=$((FAILED + 1))
    fi

    printf "  %-35s %s\n" "$NAME" "$STATUS"
    RESULTS+=("$NAME|$STATUS|$OUTPUT")
done

echo "$LINE"
printf "  %d passed, %d failed\n" "$PASSED" "$FAILED"
echo "$LINE"

# Print failure details
for result in "${RESULTS[@]}"; do
    NAME="${result%%|*}"
    rest="${result#*|}"
    STATUS="${rest%%|*}"
    OUTPUT="${rest#*|}"

    if [[ "$STATUS" == "FAIL" ]]; then
        echo ""
        echo "FAIL details: $NAME"
        echo "$OUTPUT" | grep -E "Error|FAIL|Messages" | sed 's/^/  /'
    fi
done

[[ $FAILED -eq 0 ]] || exit 1