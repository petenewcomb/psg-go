#!/usr/bin/env bash

set -e
set -o pipefail

awk '/plan: Plan#/{print $NF};/sim.debugf.* done/{print $(NF-1)};/sim.debugf: ended/{print $(NF-6)}' "$@" | \
  sort -t'#' -k2,2n
