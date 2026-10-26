#!/usr/bin/env bash
# Print shard I of N (1-based) of the lines on stdin: the lines sorted in C
# order and dealt round-robin, so the N shards together print every line
# exactly once. Fails on bad arguments or a shard with nothing in it.
# Usage: ... | scripts/ci/deal.sh N I
set -euo pipefail
if ! [[ $# -eq 2 && $1 =~ ^[1-9][0-9]*$ && $2 =~ ^[1-9][0-9]*$ ]] || (($2 > $1)); then
  echo "usage: $0 N I (1 <= I <= N)" >&2
  exit 2
fi
n=$1 i=$2
picked=$(grep . | LC_ALL=C sort -u | awk -v n="$n" -v i="$i" '(NR - 1) % n == i - 1')
[[ -n $picked ]] || {
  echo "shard $i of $n is empty; use fewer shards" >&2
  exit 1
}
printf '%s\n' "$picked"
