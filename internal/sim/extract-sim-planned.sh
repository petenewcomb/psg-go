#!/usr/bin/env bash

set -e
set -o pipefail

awk '/ ends at /{print $1}' "$@" | sort -t'#' -k2,2n
