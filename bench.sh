#!/bin/bash
# Benchmark script for context caching

set -e

if [ "$(cpupower frequency-info -o proc | tail -n +2 | fgrep -v performance)" != "" ]; then
    echo "Error: CPU frequency scaling is not set to performance mode."
    echo "Set it to performance mode for more consistent results:"
    echo "  sudo cpupower frequency-set -g performance"
    exit 1
fi

if [ $# -gt 0 ]; then
    TAG="$1"; shift
fi
BENCH_FILE="bench_${TAG:+${TAG}_}$(date -u +%Y%m%dT%H%M%SZ).txt"
>"$BENCH_FILE"
tail -f "$BENCH_FILE" &
tailpid=$!
trap "kill $tailpid" EXIT

if [ $# -eq 0 ]; then
    set -- -bench=. -count=6 -timeout=10h
fi

echo "Running benchmarks ${TAG:+($TAG)}..."
go test -run='^$' "$@" >>"$BENCH_FILE" 2>&1
echo "Benchmarks completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_norm.txt"
echo "Normalizing results..."
go run -C internal/cmd/benchnorm ./... < "$BENCH_FILE" > "$NORMALIZED_FILE"
echo "Normalized results saved to: $NORMALIZED_FILE"
