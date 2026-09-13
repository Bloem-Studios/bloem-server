#!/usr/bin/env bash
# Fails when Bloem modifies a Silo-owned file that the seam ledger does not
# declare. A Silo-owned file is one that exists in upstream/apiv2; anything else
# is Bloem's own and may change freely.
set -euo pipefail

BASE="${SEAM_BASE_REF:-upstream/apiv2}"
LEDGER="${SEAM_LEDGER:-contracts/seams.txt}"

if ! git rev-parse --verify --quiet "$BASE" >/dev/null; then
  echo "::error::$BASE is not available; run: git fetch upstream" >&2
  exit 2
fi

# Silo-owned files this branch modifies, relative to the merge base.
modified() {
  git diff --name-only --diff-filter=M "$BASE...HEAD" | while read -r f; do
    git cat-file -e "$BASE:$f" 2>/dev/null && echo "$f"
  done | sort -u
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
