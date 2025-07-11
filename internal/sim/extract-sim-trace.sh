#!/usr/bin/env bash

trace_zstd="$1"; shift
if [ -z "$trace_zstd" -o ! -f "$trace_zstd" ]; then
  echo "Usage: $0 <trace_zstd>"
  exit 1
fi

set -e
set -o pipefail

extract() {
    zstd -dc "$trace_zstd" | \
        go run -C ~/src/psg-go/internal/cmd/fmttrace ./...
}

first_line="$(
    extract | \
        awk -vOFS=: '{n++};/\) G=[0-9]+ P=[0-9]+ M=[0-9]+$/{print n, $0};/ sim.Run: Test plan:$/{print n, $0}' | \
        egrep -B1 -e ' sim.Run: Test plan:$' | \
        tail -2 | head -1 | cut -d: -f1
)"

extract | tail -n +"$first_line"
