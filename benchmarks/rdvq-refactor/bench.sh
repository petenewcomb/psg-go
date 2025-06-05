#!/bin/bash
# Benchmark script for rdvq refactoring (Queue vs layered implementation)

set -e

if [ $# -ne 1 ] || [[ ! "$1" =~ ^(before|after)$ ]]; then
    echo "Usage: $0 <before|after>"
    echo "  before: benchmark with original rdvq.Queue"
    echo "  after:  benchmark with refactored rdvq layered implementation"
    exit 1
fi

PHASE="$1"
BENCH_FILE="bench_rdvq_refactor_${PHASE}_$(date -u +%Y%m%dT%H%M%SZ).txt"

echo "Running rdvq refactoring benchmark ($PHASE)..."
go test -C ../.. -run='^$' -bench='BenchmarkCombinerThroughput/workload=waiting/duration=10µs/flushPeriod=1ms/method=combine/combinerLimit=24' -count=20 -benchtime=10s -timeout=10m > "$BENCH_FILE"

echo "Benchmark completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_normalized.txt"
echo "Normalizing results..."
go run -C ../../internal/cmd/benchnorm ./... < "$BENCH_FILE" > "$NORMALIZED_FILE"
echo "Normalized results saved to: $NORMALIZED_FILE"