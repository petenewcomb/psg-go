#!/usr/bin/env bash

set -e

goroutine_id="$1"; shift
if [[ -z "$goroutine_id" ]]; then
  echo "Usage: $0 <goroutine_id> [<input_file>]"
  exit 1
fi

sed -rn -e '
  /\) G='"$goroutine_id"' P=[0-9]+ M=[0-9]+$/{
    n
    :l # loop start
      p
      n
      /^$/{
        n
        /\) G=[0-9]+ P=[0-9]+ M=[0-9]+$/{
          / G='"$goroutine_id"' /{
            n
            bl # continue
          }
          d
        }

        # current line is not a goroutine header but was preceded by a blank line,
        # so insert a blank line before looping around to print the current one.
        i
      }
    bl # end loop
  }
' "$@"
