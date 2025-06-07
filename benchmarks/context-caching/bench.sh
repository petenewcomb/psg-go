#!/bin/bash
# Benchmark script for context caching

set -e

if [ $# -ne 1 ] || [[ ! "$1" =~ ^(before|after)$ ]]; then
    echo "Usage: $0 <before|after>"
    echo "  before: benchmark without context caching"
    echo "  after:  benchmark with context caching"
    exit 1
fi

if [ "$(cpupower frequency-info -o proc | tail -n +2 | fgrep -v performance)" != "" ]; then
    echo "Error: CPU frequency scaling is not set to performance mode."
    echo "Set it to performance mode for more consistent results:"
    echo "  sudo cpupower frequency-set -g performance"
    exit 1
fi

PHASE="$1"
BENCH_FILE="bench_context_caching_${PHASE}_$(date -u +%Y%m%dT%H%M%SZ).txt"

echo "Running context caching benchmark ($PHASE)..."
# Use a benchmark that heavily exercises the work queue
go test -C ../.. -run='^$' -bench='BenchmarkCombinerThroughput/workload=waiting/duration=10µs/flushPeriod=1ms/method=combine/combinerLimit=24' -count=6 -benchtime=30s -timeout=30m > "$BENCH_FILE"

echo "Benchmark completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_normalized.txt"
echo "Normalizing results..."
go run -C ../../internal/cmd/benchnorm ./... < "$BENCH_FILE" > "$NORMALIZED_FILE"
echo "Normalized results saved to: $NORMALIZED_FILE"
