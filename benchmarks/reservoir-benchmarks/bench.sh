#!/bin/bash
# Benchmark script for tdigest->reservoir combiner benchmark refactoring

set -e

if [ $# -ne 1 ] || [[ ! "$1" =~ ^(before|after)$ ]]; then
    echo "Usage: $0 <before|after>"
    echo "  before: benchmark with tdigest"
    echo "  after:  benchmark with reservoir sampling"
    exit 1
fi

if [ "$(cpupower frequency-info -o proc | tail -n +2 | fgrep -v performance)" != "" ]; then
    echo "Error: CPU frequency scaling is not set to performance mode."
    echo "Set it to performance mode for more consistent results:"
    echo "  sudo cpupower frequency-set -g performance"
    exit 1
fi

PHASE="$1"
BENCH_FILE="bench_reservoir_benchmarks_${PHASE}_$(date -u +%Y%m%dT%H%M%SZ).txt"

echo "Running reservoir benchmarks benchmark ($PHASE)..."
go test -C ../.. -run='^$' -bench='BenchmarkCombinerThroughput/workload=waiting/duration=10µs/flushPeriod=1ms/method=combine/combinerLimit=24' -count=20 -benchtime=10s -timeout=10m > "$BENCH_FILE"

echo "Benchmark completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_normalized.txt"
echo "Normalizing results..."
go run -C ../../internal/cmd/benchnorm ./... < "$BENCH_FILE" > "$NORMALIZED_FILE"
echo "Normalized results saved to: $NORMALIZED_FILE"
