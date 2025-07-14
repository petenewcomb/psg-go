#!/usr/bin/env bash

set -e
set -o pipefail

sed -rn '/ sim\.Run: Test plan:$/{:l;n;/(^| sim\.Run: Test plan \(continued\):)$/bl;/ sim\.run: begin$/q0;p;bl}' "$@"
