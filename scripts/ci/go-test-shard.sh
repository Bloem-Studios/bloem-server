#!/usr/bin/env bash
# Print a go test -run pattern for shard I of N (1-based) of PKG's top-level
# tests, dealt round-robin in name order, so the N shards together run every
# test exactly once. Flags after I go to the listing; pass the ones the shard
# runs with (-race, -tags) so the listing builds the same test binary.
# The listing runs no tests, and without the test database URL the package
# TestMain hooks skip their database setup.
# Usage: scripts/ci/go-test-shard.sh PKG N I [GO TEST FLAGS...]
set -euo pipefail
[[ $# -ge 3 && $2 -ge 1 && $3 -ge 1 && $3 -le $2 ]] || {
  echo "usage: $0 PKG N I [GO TEST FLAGS...] (1 <= I <= N)" >&2
  exit 2
}
pkg=$1 n=$2 i=$3
shift 3
list=$(env -u SILO_TEST_DATABASE_URL -u SILO_REQUIRE_TEST_DATABASE go test "$@" -list . "$pkg")
mapfile -t tests < <(grep -E '^(Test|Example|Fuzz)' <<< "$list" | LC_ALL=C sort)
[[ ${#tests[@]} -gt 0 ]] || {
  echo "no tests in $pkg" >&2
  exit 1
}
picked=()
for ((k = i - 1; k < ${#tests[@]}; k += n)); do picked+=("${tests[k]}"); done
[[ ${#picked[@]} -gt 0 ]] || {
  echo "shard $i of $n of $pkg has no tests; use fewer shards" >&2
  exit 1
}
echo "shard $i of $n: ${#picked[@]} of ${#tests[@]} tests in $pkg" >&2
(
  IFS='|'
  echo "^(${picked[*]})\$"
)
