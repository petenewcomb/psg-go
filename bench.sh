#!/bin/bash
# Benchmark script for context caching

set -e

if [ "$(cpupower frequency-info -o proc | tail -n +2 | fgrep -v performance)" != "" ]; then
    echo "Error: CPU frequency scaling is not set to performance mode."
    echo "Set it to performance mode for more consistent results:"
    echo "  sudo cpupower frequency-set -g performance"
    exit 1
fi

# Also warn if the clock isn't pinned. The "performance" governor still allows
# turbo boost up to the max frequency, which causes variance from thermal
# throttling and from per-core boost residency. Pinning min == max == base
# frequency removes both sources.
MIN_FREQ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_min_freq 2>/dev/null || true)
MAX_FREQ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq 2>/dev/null || true)
if [ -n "$MIN_FREQ" ] && [ -n "$MAX_FREQ" ] && [ "$MIN_FREQ" != "$MAX_FREQ" ]; then
    echo "Warning: CPU frequency is not pinned (min=${MIN_FREQ}kHz, max=${MAX_FREQ}kHz)."
    echo "For lower variance, pin both bounds to the CPU's base frequency:"
    BASE_FREQ=$(cat /sys/devices/system/cpu/cpu0/cpufreq/base_frequency 2>/dev/null || true)
    if [ -n "$BASE_FREQ" ]; then
        echo "  sudo cpupower frequency-set -d ${BASE_FREQ} -u ${BASE_FREQ}"
    else
        echo "  sudo cpupower frequency-set -d <base_freq_kHz> -u <base_freq_kHz>"
    fi
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
    STATS_FILE="${BENCH_FILE%.*}_stats.txt"
    echo "Generating stats..."
    benchstat -filter '.name:CombinerThroughput .unit:(p50-combine-latency-ns OR p99-combine-latency-ns OR tasks/sec)' -table '/workload,/duration,/flushPeriod' bench_norm.txt "$NORMALIZED_FILE" > "$STATS_FILE"
    echo "Stats saved to: $STATS_FILE"

    REPORT_FILE="${BENCH_FILE%.*}_report.txt"
    echo "Generating comparison report..."
    internal/bin/benchcmp bench_norm.txt "$NORMALIZED_FILE" > "$REPORT_FILE"
    echo "Comparison report saved to: $REPORT_FILE"
    echo ""
    cat "$REPORT_FILE"
else
    echo "No baseline found (bench_norm.txt). To enable future comparison reports:"
    echo "  cp $NORMALIZED_FILE bench_norm.txt"
fi
