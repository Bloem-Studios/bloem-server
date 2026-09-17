#!/usr/bin/env bash
# Fails when Bloem modifies, deletes, or type-changes a Silo-owned file that
# the seam ledger does not declare.
#
# Silo shipped v2 on a separate apiv2 branch until 2026-09-14, when it was
# squashed onto main (a0ab8f0b7, #1075) and the branch was deleted. main is now
# the single Silo line; SEAM_BASE_REFS still accepts several if that changes
# again.
#
# The gate compares content, not history. For each base ref it collects the
# Silo versions Bloem could legitimately be carrying:
#
#   - the base tip itself;
#   - the merge base with HEAD, i.e. the Silo commit Bloem last integrated, so
#     being behind upstream is not a divergence;
#   - during an uncommitted merge, the merge base with MERGE_HEAD, so taking the
#     incoming Silo version of a file is not a divergence before the merge
#     commit exists.
#
# A file is Silo-owned when it exists in any of those versions. It is a Bloem
# seam only when the working tree matches NONE of them. Absence counts as a
# value: a file missing from both a Silo version and the working tree matches
# that version.
#
# Reads the working tree, so it answers for the change you are about to commit
# rather than for the last one. Staged and unstaged edits to tracked files both
# count; untracked files do not.
set -euo pipefail
# comm requires one collation for every list it reads.
export LC_ALL=C

# SEAM_BASE_REF (singular) is still honoured for one-off comparisons.
BASES="${SEAM_BASE_REFS:-${SEAM_BASE_REF:-upstream/main}}"
LEDGER="${SEAM_LEDGER:-contracts/seams.txt}"

for base in $BASES; do
  if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
    echo "::error::$base is not available; run: git fetch upstream" >&2
    exit 2
  fi
done

if [[ ! -f "$LEDGER" ]]; then
  echo "::error::seam ledger $LEDGER does not exist" >&2
  exit 2
fi

# Quoted path output would turn non-ASCII names into escaped strings that can
# never match a ledger line.
GIT=(git -c core.quotepath=false)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Every Silo commit whose content counts as "not a Bloem change". A missing
# merge base (unrelated histories) simply contributes nothing.
: >"$tmp/commits"
for base in $BASES; do
  git rev-parse "$base^{commit}" >>"$tmp/commits"
  git merge-base "$base" HEAD >>"$tmp/commits" || true
  if git rev-parse --verify --quiet MERGE_HEAD >/dev/null; then
    git merge-base "$base" MERGE_HEAD >>"$tmp/commits" || true
  fi
done
sort -u "$tmp/commits" -o "$tmp/commits"

# Silo-owned files the working tree diverges from in every comparison commit.
modified() {
  local commit n=0 i

  : >"$tmp/owned"
  while IFS= read -r commit; do
    [[ -n "$commit" ]] || continue
    n=$((n + 1))
    "${GIT[@]}" ls-tree -r --name-only "$commit" >>"$tmp/owned"
    # No --diff-filter: A (Bloem has a file this version lacks) is a
    # divergence from this version just as M, D and T are. --no-renames stops a
    # rename pairing at ~50% similarity from hiding one side.
    "${GIT[@]}" diff --name-only --no-renames "$commit" | sort -u >"$tmp/differs.$n"
  done <"$tmp/commits"

  sort -u "$tmp/owned" -o "$tmp/owned"
  cp "$tmp/owned" "$tmp/seams"
  for ((i = 1; i <= n; i++)); do
    comm -12 "$tmp/seams" "$tmp/differs.$i" >"$tmp/next"
    mv "$tmp/next" "$tmp/seams"
  done
  cat "$tmp/seams"
}

if [[ "${1:-}" == "--list" ]]; then
  modified
  exit 0
fi

modified_list=$(modified)
declared=$(sed 's/[[:space:]]*#.*$//; s/[[:space:]]*$//; /^[[:space:]]*$/d' "$LEDGER" | sort -u)

undeclared=$(comm -23 <(printf '%s\n' "$modified_list" | grep -v '^$' || true) <(printf '%s\n' "$declared"))
if [[ -n "$undeclared" ]]; then
  echo "::error::Silo-owned files modified without a seam ledger entry:" >&2
  while IFS= read -r path; do printf '  %s\n' "$path" >&2; done <<<"$undeclared"
  echo "Move the change into a Bloem-owned file (bloem_*.go in the same package)," >&2
  echo "or add the file to $LEDGER with a reason if it is a genuine seam." >&2
  exit 1
fi

stale=$(comm -13 <(printf '%s\n' "$modified_list" | grep -v '^$' || true) <(printf '%s\n' "$declared" | grep -v '^$' || true))
if [[ -n "$stale" ]]; then
  echo "Seam ledger entries no longer needed (remove them):" >&2
  while IFS= read -r path; do printf '  %s\n' "$path" >&2; done <<<"$stale"
  exit 1
fi

echo "seam ledger is current: $(printf '%s\n' "$declared" | grep -c . || true) declared seams"
echo "base refs: $BASES"
