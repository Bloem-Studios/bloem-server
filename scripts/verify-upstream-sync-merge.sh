#!/usr/bin/env bash
# Merge an upstream-sync PR only when its current head is the commit tested by
# the completed CI workflow. All event values are treated as untrusted data.
#
# A branch name proves nothing about who pushed it, so the tested head must
# also carry sync provenance: it is a two-parent merge whose first parent is
# already on the base branch, whose second parent is on the Silo upstream line,
# and whose tree is exactly the conflict-free merge git reproduces from those
# parents. That admits only upstream content, rejects committed conflict
# markers by construction (no text scan, so a legitimate "=======" line is not
# mistaken for one), and leaves any hand-resolved or amended merge to a human.
#
# The caller supplies the history: UPSTREAM_SYNC_BASE_REF (default
# refs/remotes/origin/<base branch>) and UPSTREAM_SYNC_UPSTREAM_REF (default
# refs/remotes/upstream/main) must exist in the current repository.
set -euo pipefail

result=invalid_pr_data
found=false
pr_number=
merged=false
output_path=${GITHUB_OUTPUT:-}

# A merge without workflow outputs would leave reporting unable to distinguish
# success from failure. Reject an unavailable runner output channel before any
# GitHub CLI operation can have side effects.
if [[ -z "$output_path" ]] || [[ ! -f "$output_path" ]] || [[ ! -w "$output_path" ]]; then
	printf 'upstream sync merge: workflow output path is unavailable\n' >&2
	exit 1
fi

finish() {
	local exit_status=$1

	if ! {
		printf 'result=%s\n' "$result"
		printf 'found=%s\n' "$found"
		printf 'pr_number=%s\n' "$pr_number"
		printf 'merged=%s\n' "$merged"
	} >>"$output_path"; then
		printf 'upstream sync merge: could not write workflow outputs\n' >&2
		exit 1
	fi

	printf 'upstream sync merge result: %s\n' "$result"
	exit "$exit_status"
}

conclusion=${UPSTREAM_SYNC_CONCLUSION:-}
repository=${UPSTREAM_SYNC_REPOSITORY:-}
head_repository=${UPSTREAM_SYNC_HEAD_REPOSITORY:-}
base_branch=${UPSTREAM_SYNC_BASE_BRANCH:-}
branch=${UPSTREAM_SYNC_BRANCH:-}
tested_sha=${UPSTREAM_SYNC_HEAD_SHA:-}

if [[ "$conclusion" != success ]]; then
	result=ci_not_success
	finish 0
fi

# Restrict repository values before they can reach gh, then require the
# workflow-run head to come from this repository rather than a fork.
repository_pattern='^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9._-]{1,100}$'
if [[ ! "$repository" =~ $repository_pattern ]] || [[ "$head_repository" != "$repository" ]]; then
	result=unexpected_repository
	finish 1
fi

if [[ "$branch" != upstream-sync/* ]] ||
	! git check-ref-format --branch "$branch" >/dev/null 2>&1 ||
	! git check-ref-format --branch "$base_branch" >/dev/null 2>&1; then
	result=unexpected_branch
	finish 1
fi

canonical_sha_pattern='^[0-9a-f]{40}$'
if [[ ! "$tested_sha" =~ $canonical_sha_pattern ]]; then
	result=invalid_tested_sha
	finish 1
fi

# Capture one bounded PR-list response and make every decision from that
# immutable snapshot. CLI diagnostics are deliberately suppressed because they
# can contain request or repository data that is not safe for notifications.
if ! pr_snapshot=$(gh pr list \
	--repo "$repository" \
	--base "$base_branch" \
	--head "$branch" \
	--state open \
	--limit 2 \
	--json number,headRefOid,baseRefName,headRefName,headRepository,isCrossRepository,isDraft 2>/dev/null); then
	result=lookup_failed
	finish 1
fi

if ! command -v jq >/dev/null 2>&1; then
	result=invalid_pr_json
	finish 1
fi

if ! pr_count=$(jq -er 'if type == "array" then length else error("expected array") end' <<<"$pr_snapshot" 2>/dev/null); then
	result=invalid_pr_json
	finish 1
fi

if [[ "$pr_count" == 0 ]]; then
	result=no_pr
	finish 0
fi

if [[ "$pr_count" != 1 ]]; then
	result=duplicate_prs
	finish 1
fi

if ! resolved_number=$(jq -er '.[0].number | if type == "number" and . > 0 and floor == . then tostring else error("invalid PR number") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_sha=$(jq -er '.[0].headRefOid | if type == "string" then . else error("invalid head SHA") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_base=$(jq -er '.[0].baseRefName | if type == "string" then . else error("invalid base branch") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_head=$(jq -er '.[0].headRefName | if type == "string" then . else error("invalid head branch") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_head_repository=$(jq -er '.[0].headRepository.nameWithOwner | if type == "string" then . else error("invalid head repository") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_cross_repository=$(jq -er '.[0].isCrossRepository | if type == "boolean" then tostring else error("invalid cross-repository flag") end' <<<"$pr_snapshot" 2>/dev/null) ||
	! resolved_draft=$(jq -er '.[0].isDraft | if type == "boolean" then tostring else error("invalid draft flag") end' <<<"$pr_snapshot" 2>/dev/null) ||
	[[ ! "$resolved_number" =~ ^[1-9][0-9]*$ ]] ||
	[[ ! "$resolved_sha" =~ $canonical_sha_pattern ]]; then
	result=invalid_pr_data
	finish 1
fi

if [[ "$resolved_base" != "$base_branch" ]] ||
	[[ "$resolved_head" != "$branch" ]] ||
	[[ "$resolved_head_repository" != "$repository" ]] ||
	[[ "$resolved_cross_repository" != false ]]; then
	result=unexpected_pr
	finish 1
fi

found=true
pr_number=$resolved_number

if [[ "$resolved_sha" != "$tested_sha" ]]; then
	result=head_mismatch
	finish 0
fi

# upstream-sync.yml opens conflicted syncs as drafts carrying the conflict
# markers. That is an expected state awaiting a human, not an error.
if [[ "$resolved_draft" != false ]]; then
	result=draft_pr
	finish 0
fi

base_ref=${UPSTREAM_SYNC_BASE_REF:-refs/remotes/origin/$base_branch}
upstream_ref=${UPSTREAM_SYNC_UPSTREAM_REF:-refs/remotes/upstream/main}

if ! git rev-parse --verify --quiet "$tested_sha^{commit}" >/dev/null 2>&1 ||
	! git rev-parse --verify --quiet "$base_ref^{commit}" >/dev/null 2>&1 ||
	! git rev-parse --verify --quiet "$upstream_ref^{commit}" >/dev/null 2>&1; then
	result=provenance_unavailable
	finish 1
fi

# rev-list --parents prints the commit followed by its parents.
read -r -a lineage <<<"$(git rev-list --parents -n 1 "$tested_sha" 2>/dev/null || true)"
if [[ "${#lineage[@]}" -ne 3 ]]; then
	# A single commit pushed on top of the sync merge, or anything else that is
	# not the merge itself. Possibly a legitimate resolution; never automatic.
	result=needs_manual_merge
	finish 0
fi
bloem_parent=${lineage[1]}
silo_parent=${lineage[2]}

if ! git merge-base --is-ancestor "$bloem_parent" "$base_ref" 2>/dev/null ||
	! git merge-base --is-ancestor "$silo_parent" "$upstream_ref" 2>/dev/null; then
	result=unexpected_provenance
	finish 1
fi

# --write-tree exits 0 with the tree id for a clean merge and 1 when it
# conflicts; anything else (including a git older than 2.38) is unverifiable.
set +e
replayed=$(git merge-tree --write-tree --no-messages "$bloem_parent" "$silo_parent" 2>/dev/null)
replay_status=$?
set -e
if [[ "$replay_status" -eq 1 ]]; then
	result=needs_manual_merge
	finish 0
fi
if [[ "$replay_status" -ne 0 ]]; then
	result=provenance_unavailable
	finish 1
fi
replayed_tree=${replayed%%$'\n'*}
if ! head_tree=$(git rev-parse --verify --quiet "$tested_sha^{tree}" 2>/dev/null) ||
	[[ "$replayed_tree" != "$head_tree" ]]; then
	result=needs_manual_merge
	finish 0
fi

if ! gh pr merge "$pr_number" \
	--repo "$repository" \
	--match-head-commit "$tested_sha" \
	--merge \
	--delete-branch >/dev/null 2>&1; then
	result=merge_failed
	finish 1
fi

result=merged
merged=true
finish 0
