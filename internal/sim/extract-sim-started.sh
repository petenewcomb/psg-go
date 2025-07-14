#!/usr/bin/env bash

set -e
set -o pipefail

sed -rn -e 's;^.* ([A-Za-z]+#[0-9]+) step 1/[0-9]+: .*$;\1;p' "$@" | \
    sort -t'#' -k2,2n
