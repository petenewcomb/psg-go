#!/usr/bin/env bash

set -e
set -o pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <sed_address> <input_file>" >&2
  exit 1
fi

FMTTRACEDIR="${BASH_SOURCE[0]%/*}"
sed_address="$1"; shift
input_file="$1"; shift

catinput() {
  case "$input_file" in
    *.zst)
      zstd -dc "$input_file"
      ;;
    *)
      cat "$input_file"
      ;;
  esac
}

catinput | "$FMTTRACEDIR/find-goroutines.sh" "$sed_address" | \
  while read -r goroutine_id; do
    goroutine_txt="G=${goroutine_id}.txt"
    if [ ! -e "$goroutine_txt" ]; then
      echo "Extracting goroutine $goroutine_id"
      catinput | "$FMTTRACEDIR/extract-goroutine.sh" "$goroutine_id" >"$goroutine_txt"
    fi
  done
