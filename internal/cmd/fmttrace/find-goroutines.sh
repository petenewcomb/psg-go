#!/usr/bin/env bash

set -e
set -o pipefail

sed_address_re='^(.).+\1$'
if [[ ! "$1" =~ $sed_address_re ]]; then
  echo "Usage: $0 <sed_address> [<input_file>]" >&2
  exit 1
fi
sed_address="$1"; shift

sed -rn -e '/\) G=[0-9]+ P=[0-9]+ M=[0-9]+$/{s/^.*G=//;s/ .*$//;h};\'"$sed_address"'{x;p;x}' "$@" | sort -u
