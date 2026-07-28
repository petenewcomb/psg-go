#!/usr/bin/env bash

set -e
set -o pipefail

SIMDIR="${BASH_SOURCE[0]%/*}"

"$SIMDIR/extract-sim-plan.sh" "$@" | awk '/ ends at /{print $1}' | sort -t'#' -k2,2n
