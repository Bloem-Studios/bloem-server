#!/usr/bin/env bash
# Offline behavior tests for verify-upstream-sync-merge.sh. A fake gh binary
# records every argument and returns controlled snapshots; no network is used.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
helper="$script_dir/verify-upstream-sync-merge.sh"

if [[ ! -f "$helper" ]]; then
	printf 'FAIL exact-SHA merge helper is missing: %s\n' "$helper" >&2
	exit 1
fi

workdir=$(mktemp -d "${TMPDIR:-/tmp}/verify-upstream-sync-merge-test.XXXXXX")
trap 'rm -rf "$workdir"' EXIT
mkdir -p "$workdir/bin"
cat >"$workdir/bin/gh" <<'FAKE_GH'
#!/usr/bin/env bash
set -euo pipefail

log_call() {
	local kind=$1
	shift
	printf '%s' "$kind" >>"$FAKE_GH_LOG"
	for arg in "$@"; do
		printf '\t%s' "$arg" >>"$FAKE_GH_LOG"
	done
	printf '\n' >>"$FAKE_GH_LOG"
}

if [[ "${1:-}" == "pr" && "${2:-}" == "list" ]]; then
	shift 2
	log_call list "$@"
	if [[ -n "${FAKE_GH_LIST_STDERR:-}" ]]; then
		printf '%s\n' "$FAKE_GH_LIST_STDERR" >&2
	fi
	printf '%s\n' "${FAKE_GH_LIST_RESPONSE:-[]}"
	exit "${FAKE_GH_LIST_EXIT:-0}"
fi

if [[ "${1:-}" == "pr" && "${2:-}" == "merge" ]]; then
	shift 2
	log_call merge "$@"
	if [[ -n "${FAKE_GH_MERGE_STDERR:-}" ]]; then
		printf '%s\n' "$FAKE_GH_MERGE_STDERR" >&2
	fi
	exit "${FAKE_GH_MERGE_EXIT:-0}"
fi

log_call unexpected "$@"
exit 97
FAKE_GH
chmod +x "$workdir/bin/gh"

# A real history for the provenance check: a Bloem main line and a Silo line
# that diverged from one root, plus the merge shapes the helper must tell
# apart. Isolated from the developer's git config (signing, hooks).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid
fixture="$workdir/fixture"
git init -q -b main "$fixture"
(
	cd "$fixture"
	printf 'x=1\n' >x.txt
	printf 'y=1\n' >y.txt
	git add -A && git commit -q -m root

	git checkout -q -b silo
	# Legitimate content that merely looks like conflict separators.
	printf 'Title\n=======\n\nA fixture line: <<<<<<< ours\n>>>>>>> theirs\n' >doc.md
	printf 'y=2\n' >y.txt
	git add -A && git commit -q -m "silo: y and a setext heading"
	git tag silo-clean
	printf 'x=3\n' >x.txt
	git add -A && git commit -q -m "silo: x"
	git tag silo-conflicting

	git checkout -q main
	printf 'x=2\n' >x.txt
	git add -A && git commit -q -m "bloem: x"

	git checkout -q -b sync-clean main
	git merge -q --no-ff --no-edit silo-clean
	git tag clean-merge

	git checkout -q -b sync-amended clean-merge
	printf 'smuggled\n' >extra.txt
	git add -A && git commit -q --amend --no-edit
	git tag amended-merge

	git checkout -q -b sync-stacked clean-merge
	printf 'y=bloem\n' >y.txt
	git add -A && git commit -q -m "on top of the merge"
	git tag stacked-commit

	git checkout -q -b sync-conflicted main
	git merge -q --no-ff --no-edit silo-conflicting >/dev/null 2>&1 || true
	git add -A && git commit -q --no-verify --no-edit -m "merge upstream/main (CONFLICTED)"
	git tag conflicted-merge

	git checkout -q -b rogue "$(git rev-list --max-parents=0 main)"
	printf 'rogue\n' >rogue.txt
	git add -A && git commit -q -m "not on main"
	git merge -q --no-ff --no-edit silo-clean
	git tag foreign-first-parent

	git checkout -q -b not-silo main
	printf 'z\n' >z.txt
	git add -A && git commit -q -m "not on silo"
	git checkout -q -b sync-not-silo main
	git merge -q --no-ff --no-edit not-silo
	git tag foreign-second-parent

	git checkout -q main
	git update-ref refs/remotes/origin/main main
	git update-ref refs/remotes/upstream/main silo
)
fixture_sha() { git -C "$fixture" rev-parse "$1^{commit}"; }

if ! git -C "$fixture" merge-tree --write-tree main silo-clean >/dev/null 2>&1; then
	printf 'FAIL git merge-tree --write-tree is unavailable (needs git 2.38+)\n' >&2
	exit 1
fi

sha_a=$(fixture_sha clean-merge)
sha_b=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
default_repo=Bloem-Studios/bloem-server
default_base=main
default_branch=upstream-sync/2026-08-28-deadbeef
matching_pr='{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false,"isDraft":false}'
advanced_pr='{"number":42,"headRefOid":"'"$sha_b"'","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false,"isDraft":false}'

passed=0
failed=0
current_name=
current_failed=0
case_dir=
case_status=0

begin_case() {
	current_name=$1
	current_failed=0
	case_dir="$workdir/$current_name"
	mkdir -p "$case_dir"
	: >"$case_dir/gh.log"
	: >"$case_dir/output"
	: >"$case_dir/stdout"

	CASE_OUTPUT_PATH=$case_dir/output
	CASE_CONCLUSION=success
	CASE_REPOSITORY=$default_repo
	CASE_HEAD_REPOSITORY=$default_repo
	CASE_BASE_BRANCH=$default_base
	CASE_BRANCH=$default_branch
	CASE_SHA=$sha_a
	CASE_LIST_RESPONSE='[]'
	CASE_LIST_EXIT=0
	CASE_LIST_STDERR=
	CASE_MERGE_EXIT=0
	CASE_MERGE_STDERR=
	CASE_UPSTREAM_REF=refs/remotes/upstream/main
}

# The PR record GitHub would return for the default branch at a given head.
pr_at() {
	local sha=$1 draft=${2:-false}
	printf '{"number":42,"headRefOid":"%s","baseRefName":"%s","headRefName":"%s","headRepository":{"nameWithOwner":"%s"},"isCrossRepository":false,"isDraft":%s}' \
		"$sha" "$default_base" "$default_branch" "$default_repo" "$draft"
}

run_case() {
	set +e
	cd "$fixture"
	env \
		UPSTREAM_SYNC_UPSTREAM_REF="$CASE_UPSTREAM_REF" \
		PATH="$workdir/bin:$PATH" \
		GITHUB_OUTPUT="$CASE_OUTPUT_PATH" \
		UPSTREAM_SYNC_CONCLUSION="$CASE_CONCLUSION" \
		UPSTREAM_SYNC_REPOSITORY="$CASE_REPOSITORY" \
		UPSTREAM_SYNC_HEAD_REPOSITORY="$CASE_HEAD_REPOSITORY" \
		UPSTREAM_SYNC_BASE_BRANCH="$CASE_BASE_BRANCH" \
		UPSTREAM_SYNC_BRANCH="$CASE_BRANCH" \
		UPSTREAM_SYNC_HEAD_SHA="$CASE_SHA" \
		FAKE_GH_LOG="$case_dir/gh.log" \
		FAKE_GH_LIST_RESPONSE="$CASE_LIST_RESPONSE" \
		FAKE_GH_LIST_EXIT="$CASE_LIST_EXIT" \
		FAKE_GH_LIST_STDERR="$CASE_LIST_STDERR" \
		FAKE_GH_MERGE_EXIT="$CASE_MERGE_EXIT" \
		FAKE_GH_MERGE_STDERR="$CASE_MERGE_STDERR" \
		bash "$helper" >"$case_dir/stdout" 2>&1
	case_status=$?
	cd "$workdir"
	set -e
}

check_equal() {
	local got=$1 want=$2 label=$3
	if [[ "$got" != "$want" ]]; then
		printf '  %s: got %q, want %q\n' "$label" "$got" "$want" >&2
		current_failed=1
	fi
}

check_status() {
	check_equal "$case_status" "$1" "exit status"
}

output_value() {
	local key=$1
	awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print }' "$case_dir/output"
}

check_output() {
	local key=$1 want=$2
	local count got
	count=$(awk -F= -v key="$key" '$1 == key { count++ } END { print count + 0 }' "$case_dir/output")
	check_equal "$count" "1" "output $key count"
	got=$(output_value "$key")
	check_equal "$got" "$want" "output $key"
}

call_count() {
	local kind=$1
	awk -F '\t' -v kind="$kind" '$1 == kind { count++ } END { print count + 0 }' "$case_dir/gh.log"
}

check_call_count() {
	local kind=$1 want=$2
	check_equal "$(call_count "$kind")" "$want" "$kind call count"
}

check_exact_call() {
	local kind=$1 want=$2
	local got
	got=$(awk -F '\t' -v kind="$kind" '$1 == kind { print }' "$case_dir/gh.log")
	check_equal "$got" "$want" "$kind arguments"
}

check_secret_absent() {
	local secret=$1
	if grep -Fq "$secret" "$case_dir/stdout" || grep -Fq "$secret" "$case_dir/output"; then
		printf '  secret marker leaked into helper output\n' >&2
		current_failed=1
	fi
}

finish_case() {
	if [[ "$current_failed" -eq 0 ]]; then
		printf 'ok   %s\n' "$current_name"
		passed=$((passed + 1))
	else
		printf 'FAIL %s\n' "$current_name" >&2
		failed=$((failed + 1))
	fi
}

check_standard_outputs() {
	local result=$1 found=$2 number=$3 merged=$4
	check_output result "$result"
	check_output found "$found"
	check_output pr_number "$number"
	check_output merged "$merged"
}

list_call=$'list\t--repo\t'"$default_repo"$'\t--base\t'"$default_base"$'\t--head\t'"$default_branch"$'\t--state\topen\t--limit\t2\t--json\tnumber,headRefOid,baseRefName,headRefName,headRepository,isCrossRepository,isDraft'
merge_call=$'merge\t42\t--repo\t'"$default_repo"$'\t--match-head-commit\t'"$sha_a"$'\t--merge\t--delete-branch'

begin_case exact_head_merges
CASE_LIST_RESPONSE="[$matching_pr]"
run_case
check_status 0
check_standard_outputs merged true 42 true
check_call_count list 1
check_call_count merge 1
check_exact_call list "$list_call"
check_exact_call merge "$merge_call"
finish_case

begin_case head_advanced_after_ci
CASE_LIST_RESPONSE="[$advanced_pr]"
run_case
check_status 0
check_standard_outputs head_mismatch true 42 false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case no_open_pr
run_case
check_status 0
check_standard_outputs no_pr false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case duplicate_open_prs
CASE_LIST_RESPONSE="[$matching_pr,{\"number\":43,\"headRefOid\":\"$sha_a\",\"baseRefName\":\"$default_base\",\"headRefName\":\"$default_branch\",\"headRepository\":{\"nameWithOwner\":\"$default_repo\"},\"isCrossRepository\":false,\"isDraft\":false}]"
run_case
check_status 1
check_standard_outputs duplicate_prs false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case failed_ci
CASE_CONCLUSION=failure
run_case
check_status 0
check_standard_outputs ci_not_success false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case missing_workflow_output_path
CASE_OUTPUT_PATH=
CASE_LIST_RESPONSE="[$matching_pr]"
run_case
check_status 1
check_call_count list 0
check_call_count merge 0
check_equal "$(wc -c <"$case_dir/output" | tr -d ' ')" "0" "workflow output bytes"
finish_case

begin_case unusable_workflow_output_path
CASE_OUTPUT_PATH="$case_dir/missing/output"
CASE_LIST_RESPONSE="[$matching_pr]"
run_case
check_status 1
check_call_count list 0
check_call_count merge 0
check_equal "$(wc -c <"$case_dir/output" | tr -d ' ')" "0" "workflow output bytes"
finish_case

begin_case empty_tested_sha
CASE_SHA=
run_case
check_status 1
check_standard_outputs invalid_tested_sha false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case malformed_tested_sha
CASE_SHA=ABCDEF
run_case
check_status 1
check_standard_outputs invalid_tested_sha false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case malformed_pr_head_sha
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"ABCDEF","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false,"isDraft":false}]'
run_case
check_status 1
check_standard_outputs invalid_pr_data false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case gh_list_failure_is_sanitized
CASE_LIST_EXIT=1
CASE_LIST_STDERR=FAKE_SECRET_MARKER
run_case
check_status 1
check_standard_outputs lookup_failed false "" false
check_call_count list 1
check_call_count merge 0
check_secret_absent FAKE_SECRET_MARKER
finish_case

begin_case malformed_json
CASE_LIST_RESPONSE='{not-json'
run_case
check_status 1
check_standard_outputs invalid_pr_json false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case missing_pr_fields
CASE_LIST_RESPONSE='[{"number":42}]'
run_case
check_status 1
check_standard_outputs invalid_pr_data false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case merge_failure_is_sanitized
CASE_LIST_RESPONSE="[$matching_pr]"
CASE_MERGE_EXIT=1
CASE_MERGE_STDERR=FAKE_SECRET_MARKER
run_case
check_status 1
check_standard_outputs merge_failed true 42 false
check_call_count list 1
check_call_count merge 1
check_exact_call merge "$merge_call"
check_secret_absent FAKE_SECRET_MARKER
finish_case

begin_case wrong_pr_base
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"release","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false,"isDraft":false}]'
run_case
check_status 1
check_standard_outputs unexpected_pr false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case wrong_pr_head_branch
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"'"$default_base"'","headRefName":"upstream-sync/other","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false,"isDraft":false}]'
run_case
check_status 1
check_standard_outputs unexpected_pr false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case cross_repository_pr
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"attacker/fork"},"isCrossRepository":true,"isDraft":false}]'
run_case
check_status 1
check_standard_outputs unexpected_pr false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case mismatched_pr_head_repository
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"attacker/fork"},"isCrossRepository":false,"isDraft":false}]'
run_case
check_status 1
check_standard_outputs unexpected_pr false "" false
check_call_count list 1
check_call_count merge 0
finish_case

begin_case unexpected_branch
CASE_BRANCH=feature/not-upstream-sync
run_case
check_status 1
check_standard_outputs unexpected_branch false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case malformed_base_branch
CASE_BASE_BRANCH='main..invalid'
run_case
check_status 1
check_standard_outputs unexpected_branch false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case unexpected_head_repository
CASE_HEAD_REPOSITORY=attacker/fork
run_case
check_status 1
check_standard_outputs unexpected_repository false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case malformed_target_repository
CASE_REPOSITORY='not a repository'
CASE_HEAD_REPOSITORY='not a repository'
run_case
check_status 1
check_standard_outputs unexpected_repository false "" false
check_call_count list 0
check_call_count merge 0
finish_case

begin_case missing_draft_flag
CASE_LIST_RESPONSE='[{"number":42,"headRefOid":"'"$sha_a"'","baseRefName":"'"$default_base"'","headRefName":"'"$default_branch"'","headRepository":{"nameWithOwner":"'"$default_repo"'"},"isCrossRepository":false}]'
run_case
check_status 1
check_standard_outputs invalid_pr_data false "" false
check_call_count merge 0
finish_case

begin_case draft_pr_is_left_alone
CASE_LIST_RESPONSE="[$(pr_at "$sha_a" true)]"
run_case
check_status 0
check_standard_outputs draft_pr true 42 false
check_call_count list 1
check_call_count merge 0
finish_case

# Upstream content containing separator-looking lines is not a conflict.
begin_case clean_merge_with_separator_lines_merges
CASE_LIST_RESPONSE="[$(pr_at "$sha_a")]"
run_case
check_status 0
check_standard_outputs merged true 42 true
check_call_count merge 1
finish_case

begin_case committed_conflict_markers_are_not_merged
conflicted=$(fixture_sha conflicted-merge)
CASE_SHA=$conflicted
CASE_LIST_RESPONSE="[$(pr_at "$conflicted")]"
run_case
check_status 0
check_standard_outputs needs_manual_merge true 42 false
check_call_count merge 0
finish_case

begin_case amended_merge_tree_is_not_merged
amended=$(fixture_sha amended-merge)
CASE_SHA=$amended
CASE_LIST_RESPONSE="[$(pr_at "$amended")]"
run_case
check_status 0
check_standard_outputs needs_manual_merge true 42 false
check_call_count merge 0
finish_case

begin_case commit_on_top_of_merge_is_not_merged
stacked=$(fixture_sha stacked-commit)
CASE_SHA=$stacked
CASE_LIST_RESPONSE="[$(pr_at "$stacked")]"
run_case
check_status 0
check_standard_outputs needs_manual_merge true 42 false
check_call_count merge 0
finish_case

begin_case first_parent_off_base_is_rejected
rogue=$(fixture_sha foreign-first-parent)
CASE_SHA=$rogue
CASE_LIST_RESPONSE="[$(pr_at "$rogue")]"
run_case
check_status 1
check_standard_outputs unexpected_provenance true 42 false
check_call_count merge 0
finish_case

begin_case second_parent_off_upstream_is_rejected
rogue=$(fixture_sha foreign-second-parent)
CASE_SHA=$rogue
CASE_LIST_RESPONSE="[$(pr_at "$rogue")]"
run_case
check_status 1
check_standard_outputs unexpected_provenance true 42 false
check_call_count merge 0
finish_case

begin_case missing_upstream_history_is_unverifiable
CASE_UPSTREAM_REF=refs/remotes/upstream/absent
CASE_LIST_RESPONSE="[$(pr_at "$sha_a")]"
run_case
check_status 1
check_standard_outputs provenance_unavailable true 42 false
check_call_count merge 0
finish_case

begin_case unknown_tested_commit_is_unverifiable
CASE_SHA=cccccccccccccccccccccccccccccccccccccccc
CASE_LIST_RESPONSE="[$(pr_at "$CASE_SHA")]"
run_case
check_status 1
check_standard_outputs provenance_unavailable true 42 false
check_call_count merge 0
finish_case

printf '\n%d passed, %d failed\n' "$passed" "$failed"
if [[ "$failed" -ne 0 ]]; then
	exit 1
fi
