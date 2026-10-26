#!/usr/bin/env bash
# Tests for go-test-shard.sh against a throwaway module. Run:
# bash scripts/ci/go-test-shard_test.sh
set -uo pipefail
shard=$(cd "$(dirname "$0")" && pwd)/go-test-shard.sh
failures=0
fail() {
  echo "FAIL $*"
  failures=$((failures + 1))
}
mod=$(mktemp -d)
trap 'rm -rf "$mod"' EXIT
cd "$mod" || exit 1
printf 'module example.com/shard\n\ngo 1.22\n' > go.mod
{
  echo 'package shard'
  echo 'import ("fmt"; "testing")'
  for t in Alpha Bravo Charlie Delta Echo Foxtrot Golf; do
    echo "func Test$t(t *testing.T) { t.Run(\"sub\", func(t *testing.T) {}) }"
  done
  printf 'func Example() {\n\tfmt.Println("hi")\n\t// Output: hi\n}\n'
} > shard_test.go

ran() { # every top-level test the given shards run, one per line, sorted
  local n=$1 i out=""
  shift
  for i in "$@"; do
    out+=$(go test -v -run "$("$shard" . "$n" "$i" 2> /dev/null)" . | sed -n 's/^=== RUN   \([^/]*\)$/\1/p')$'\n'
  done
  grep . <<< "$out" | sort
}
all=$(go test -v . | sed -n 's/^=== RUN   \([^/]*\)$/\1/p' | sort)

for n in 1 3 7; do
  got=$(ran "$n" $(seq "$n"))
  [[ $got == "$all" ]] || fail "$n shards run every test once: $(diff <(echo "$all") <(echo "$got") | tr '\n' ' ')"
done
[[ $(ran 3 1 | wc -l) -eq 3 ]] || fail "shard 1 of 3 runs 3 of 8 tests"
"$shard" . 9 9 > /dev/null 2>&1 && fail "a shard with no tests succeeds"
"$shard" . 3 4 > /dev/null 2>&1 && fail "shard 4 of 3 is accepted"
"$shard" . 3 0 > /dev/null 2>&1 && fail "shard 0 of 3 is accepted"

# deal.sh, which also deals make test-go's packages to the Go test shards.
deal=${shard%/*}/deal.sh
lines=$(printf 'pkg/%s\n' c a e b d)
for n in 1 2 5; do
  got=$(for i in $(seq "$n"); do "$deal" "$n" "$i" <<< "$lines"; done | sort)
  [[ $got == "$(sort <<< "$lines")" ]] || fail "deal.sh: $n shards print every line once"
done
[[ $("$deal" 2 1 <<< "$lines") == $'pkg/a\npkg/c\npkg/e' ]] || fail "deal.sh deals sorted lines round-robin"
"$deal" 6 6 <<< "$lines" > /dev/null 2>&1 && fail "deal.sh: an empty shard succeeds"
"$deal" 2 3 <<< "$lines" > /dev/null 2>&1 && fail "deal.sh: shard 3 of 2 is accepted"

if [[ $failures -ne 0 ]]; then
  echo "$failures failed"
  exit 1
fi
echo "all passed"
