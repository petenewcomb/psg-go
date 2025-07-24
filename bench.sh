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
    set -- -bench='^BenchmarkCombinerThroughput$' -count=6 -timeout=10h
fi

echo "Running benchmarks ${TAG:+($TAG)}..."
go test -run='^$' "$@" >>"$BENCH_FILE" 2>&1
echo "Benchmarks completed. Results saved to: $BENCH_FILE"

# Normalize the results
NORMALIZED_FILE="${BENCH_FILE%.*}_norm.txt"
echo "Normalizing results..."
internal/bin/benchnorm < "$BENCH_FILE" > "$NORMALIZED_FILE"
echo "Normalized results saved to: $NORMALIZED_FILE"

# Generate benchcmp report if baseline exists
if [ -f "bench_norm.txt" ]; then
    REPORT_FILE="${BENCH_FILE%.*}_report.txt"
    echo "Generating comparison report..."
    internal/bin/benchcmp -baseline bench_norm.txt -current "$NORMALIZED_FILE" > "$REPORT_FILE"
    echo "Comparison report saved to: $REPORT_FILE"
    echo ""
    echo "=== Quick Summary ==="
    head -20 "$REPORT_FILE"
else
    echo "No baseline found (bench_norm.txt). To enable comparison reports:"
    echo "  cp $NORMALIZED_FILE bench_norm.txt"
fi
