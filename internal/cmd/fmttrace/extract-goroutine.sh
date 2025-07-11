#!/usr/bin/env bash

set -e

goroutine_id="$1"; shift
if [[ -z "$goroutine_id" ]]; then
  echo "Usage: $0 <goroutine_id> [<input_file>]"
  exit 1
fi

sed -rn -e '/\) G='"$goroutine_id"' P=[0-9]+ M=[0-9]+$/{z;n;:l;p;z;n;/^$/d;bl}' "$@"
