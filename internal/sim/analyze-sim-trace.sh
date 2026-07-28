#!/usr/bin/env bash

trace_output="$1"; shift
if [ -z "$trace_output" -o ! -f "$trace_output" ]; then
  echo "Usage: $0 <trace_output>"
  exit 1
fi

set -e
set -o pipefail

case "$1" in
    *.zst)
        trace_zstd="$trace_output"
        ;;
    *)
        trace_zstd="trace.out.zst"
        if [ ! -e "$trace_zstd" ]; then
            zstd <"$trace_output" >"${trace_zstd}"
        fi
esac

SIMDIR="${BASH_SOURCE[0]%/*}"

brokenpipeok() {
    (
        set +e
        "$@"
        code=$?
        if [ $code -eq 141 ]; then
            code=0
        fi
        exit $code
    )
}

if [ ! -e trace.txt.zst ]; then
    "$SIMDIR/extract-sim-trace.sh" "$trace_zstd" | zstd >trace.txt.zst
fi
if [ ! -e plan.txt ]; then
    brokenpipeok zstd -dc trace.txt.zst | "$SIMDIR/extract-sim-plan.sh" >plan.txt
fi
if [ ! -e started.txt ]; then
    zstd -dc trace.txt.zst | "$SIMDIR/extract-sim-started.sh" >started.txt
fi
if [ ! -e completed.txt ]; then
    zstd -dc trace.txt.zst | "$SIMDIR/extract-sim-completed.sh" >completed.txt
fi
if [ ! -e incomplete.txt ]; then
    egrep '(Plan|Task|Combine|Gather)#[0-9]+:' plan.txt | fgrep -A1 --no-group-separator -f started.txt | fgrep -v -f completed.txt >incomplete.txt
fi

# For matching job work increments and decrements (half-baked)
# cd ~/src/psg-go/x && sed -rn '/TaskPool\.newScatterWork\.decrementingWorkFn: started=true/{n;/JobState\.IncrementWork: begin/{s/^.*JobState\.//;s/: begin$//;h;n;n;n;n;n;n;/InFlightCounter=0xc00068c038, newValue/{s/^([0-9]+).*(InFlight)/\1 \2/;H;x;y/\n/ /;s/^/workID= /;p}}};/newScatterWork.boundTaskFn: end/{n;/JobState\.DecrementWork: begin/{s/^.*JobState\.//;s/: begin$//;h;n;n;n;/InFlightCounter=0xc00068c038, newValue/{s/^([0-9]+).*(InFlight)/\1 \2/;H;x;y/\n/ /;s/^/workID= /;p}}};/workID=/{s/^.*(workID=)/\1/;h;n;/JobState\.(In|De)crementWork: begin/{s/^.*JobState\.//;s/: begin$//;H;/^In/{n;n;n};n;n;n;/InFlightCounter=0xc00068c038, newValue/{s/^([0-9]+).*(InFlight)/\1 \2/;H;x;y/\n/ /;p}}}' G=*.txt | sort -k3,3n

tail incomplete.txt
