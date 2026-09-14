#!/usr/bin/env bash
# Fails when Bloem modifies, deletes, or type-changes a Silo-owned file that
# the seam ledger does not declare.
#
# Silo shipped v2 on a separate apiv2 branch until 2026-09-14, when it was
# squashed onto main (a0ab8f0b7, #1075) and the branch was deleted. main is now
# the single Silo line; SEAM_BASE_REFS still accepts several if that changes
# again. A Silo-owned
# file is one that exists in ANY of the base refs below. A file counts as a
# Bloem seam only when it matches NO base ref at all. Absence counts as a
# value: inheriting main's version of a file apiv2 has not caught up to yet is
# not a divergence, and neither is dropping a file apiv2 itself deleted.
#
# Reads the working tree, so it answers for the change you are about to commit
# rather than for the last one. Staged and unstaged edits to tracked files both
# count.
set -euo pipefail

# SEAM_BASE_REF (singular) is still honoured for one-off comparisons.
BASES="${SEAM_BASE_REFS:-${SEAM_BASE_REF:-upstream/main}}"
LEDGER="${SEAM_LEDGER:-contracts/seams.txt}"

for base in $BASES; do
  if ! git rev-parse --verify --quiet "$base" >/dev/null; then
    echo "::error::$base is not available; run: git fetch upstream" >&2
    exit 2
  fi
done

# Silo-owned files this branch modifies, relative to every base that has them.
modified() {
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN

  for base in $BASES; do
    local tag=${base//\//_}
    git ls-tree -r --name-only "$base" | sort -u > "$tmp/exists.$tag"
    # --diff-filter=MDT catches modifications, deletions and type changes;
    # --no-renames stops a rename pairing at ~50% similarity from hiding one.
    # Diff the merge base against the WORKING TREE rather than against HEAD:
    # three-dot against HEAD answers for the last commit, which is a confident
    # wrong answer whenever anything is uncommitted -- including the change you
    # are about to make.
    local mergeBase
    mergeBase=$(git merge-base "$base" HEAD)
    git diff --name-only --diff-filter=MDT --no-renames "$mergeBase" \
      | sort -u | comm -12 - "$tmp/exists.$tag" > "$tmp/touched.$tag"
  done

  cat "$tmp"/touched.* | sort -u > "$tmp/candidates"
  git ls-files | sort -u > "$tmp/head"

  # Drop any candidate that still matches at least one Silo line. A file
  # matches a line either by carrying that line's content (present and
  # untouched) or by being absent from both that line and Bloem.
  : > "$tmp/matches-a-silo-line"
  for base in $BASES; do
    local tag=${base//\//_}
    comm -23 "$tmp/exists.$tag" "$tmp/touched.$tag" >> "$tmp/matches-a-silo-line"
    comm -23 "$tmp/candidates" "$tmp/exists.$tag" \
      | comm -23 - "$tmp/head" >> "$tmp/matches-a-silo-line"
  done
  sort -u "$tmp/matches-a-silo-line" -o "$tmp/matches-a-silo-line"

  comm -23 "$tmp/candidates" "$tmp/matches-a-silo-line"
}

if [[ "${1:-}" == "--list" ]]; then
  modified
  exit 0
fi

declared=$(grep -vE '^\s*(#|$)' "$LEDGER" | sed 's/[[:space:]]*#.*$//' | sed 's/[[:space:]]*$//' | sort -u)
undeclared=$(comm -23 <(modified) <(printf '%s\n' "$declared"))

if [[ -n "$undeclared" ]]; then
  echo "::error::Silo-owned files modified without a seam ledger entry:" >&2
  printf '  %s\n' $undeclared >&2
  echo "Move the change into a Bloem-owned file (bloem_*.go in the same package)," >&2
  echo "or add the file to $LEDGER with a reason if it is a genuine seam." >&2
  exit 1
fi

stale=$(comm -13 <(modified) <(printf '%s\n' "$declared"))
if [[ -n "$stale" ]]; then
  echo "Seam ledger entries no longer needed (remove them):" >&2
  printf '  %s\n' $stale >&2
  exit 1
fi

echo "seam ledger is current: $(printf '%s\n' "$declared" | grep -c . ) declared seams"
echo "base refs: $BASES"
