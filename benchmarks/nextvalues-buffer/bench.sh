#!/bin/bash
# Benchmark script for nextValues buffer refactoring validation

set -e

if [ $# -ne 1 ] || [[ ! "$1" =~ ^(before|after)$ ]]; then
    echo "Usage: $0 <before|after>"
    echo "  before: benchmark with pre-nextValues implementation (bed6a6d)"
    echo "  after:  benchmark with fixed nextValues implementation"
    exit 1
fi

PHASE="$1"
BENCH_FILE="bench_nextvalues_fixed_${PHASE}_$(date -u +%Y%m%dT%H%M%SZ).txt"

echo "Running nextValues buffer fix validation benchmark ($PHASE)..."

# Test both key scenarios that revealed the abandonment bug
echo "Testing combiner throughput (primary benchmark)..."
go test -C ../.. -run='^$' -bench='BenchmarkCombinerThroughput/workload=waiting/duration=10µs/flushPeriod=10µs/method=gatherOnly/combinerLimit=0' -count=10 -benchtime=10s -timeout=10m > "$BENCH_FILE"

echo "Testing RDVQ stress test with abandonments..."
go test -C ../../internal/rdvq -run='TestQueue_StressWithAbandonments' -v >> "$BENCH_FILE"

echo "Benchmark completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_normalized.txt"
echo "Normalizing results..."
go run -C ../../internal/cmd/benchnorm ./... < "$BENCH_FILE" > "$NORMALIZED_FILE" 2>/dev/null || echo "Normalization skipped (test output mixed with benchmark results)"
echo "Results saved to: $BENCH_FILE"
if [ -f "$NORMALIZED_FILE" ]; then
    echo "Normalized results saved to: $NORMALIZED_FILE"
fi