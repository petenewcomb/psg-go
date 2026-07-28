#!/usr/bin/env bash

set -e
set -o pipefail

sed -rn 's;^.* ([A-Za-z]+#[0-9]+) step ([0-9]+)/\2: done$;\1;p' "$@" | \
    sort -t'#' -k2,2n
