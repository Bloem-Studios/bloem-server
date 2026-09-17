#!/usr/bin/env bash
# Offline behavior tests for verify-seams.sh. Each case builds a throwaway git
# repository with a "silo" line and a Bloem "main" line; nothing touches the
# real checkout or the network.
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
gate="$script_dir/verify-seams.sh"

workdir=$(mktemp -d "${TMPDIR:-/tmp}/verify-seams-test.XXXXXX")
trap 'rm -rf "$workdir"' EXIT

# Keep the developer's git config (signing, hooks, default branch) out of the
# fixtures.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid

passed=0
failed=0
current_name=
current_failed=0
repo=
out=
case_status=0

begin_case() {
	current_name=$1
	current_failed=0
	repo="$workdir/$current_name"
	# Outside the fixture so `git add -A` never commits it.
	out="$workdir/$current_name.out"
	mkdir -p "$repo"
	cd "$repo"
	git init -q -b silo
	mkdir -p internal docs
	printf 'package a\n' >internal/a.go
	printf 'package b\n' >internal/b.go
	printf 'package c\n' >internal/c.go
	printf 'package d\n' >internal/d.go
	printf 'spaced\n' >"docs/a b.md"
	printf 'accent\n' >"docs/é.md"
	commit_all "silo: initial"
	git checkout -q -b main
	: >seams.txt
	git add seams.txt
	git commit -q -m "bloem: empty ledger"
}

commit_all() {
	git add -A
	git commit -q -m "$1"
}

run_gate() {
	set +e
	SEAM_BASE_REFS=silo SEAM_LEDGER=seams.txt bash "$gate" >"$out" 2>&1
	case_status=$?
	set -e
}

declare_seam() {
	printf '%s # test\n' "$@" >>seams.txt
}

check_status() {
	if [[ "$case_status" != "$1" ]]; then
		printf '  exit status: got %s, want %s\n' "$case_status" "$1" >&2
		sed 's/^/    | /' "$out" >&2
		current_failed=1
	fi
}

check_output_has() {
	if ! grep -Fq -- "$1" "$out"; then
		printf '  output lacks %q\n' "$1" >&2
		sed 's/^/    | /' "$out" >&2
		current_failed=1
	fi
}

check_output_lacks() {
	if grep -Fq -- "$1" "$out"; then
		printf '  output unexpectedly has %q\n' "$1" >&2
		sed 's/^/    | /' "$out" >&2
		current_failed=1
	fi
}

finish_case() {
	cd "$workdir"
	if [[ "$current_failed" -eq 0 ]]; then
		printf 'ok   %s\n' "$current_name"
		passed=$((passed + 1))
	else
		printf 'FAIL %s\n' "$current_name" >&2
		failed=$((failed + 1))
	fi
}

begin_case bloem_owned_files_need_no_entry
printf 'package a\n' >internal/bloem_a.go
commit_all "bloem: own file"
run_gate
check_status 0
finish_case

begin_case undeclared_modification_fails
printf 'package a // bloem\n' >internal/a.go
commit_all "bloem: patch a"
run_gate
check_status 1
check_output_has "internal/a.go"
finish_case

begin_case declared_modification_passes
printf 'package a // bloem\n' >internal/a.go
declare_seam internal/a.go
commit_all "bloem: patch a"
run_gate
check_status 0
finish_case

begin_case uncommitted_edits_count
printf 'package a // staged\n' >internal/a.go
git add internal/a.go
printf 'package b // unstaged\n' >internal/b.go
run_gate
check_status 1
check_output_has "internal/a.go"
check_output_has "internal/b.go"
finish_case

begin_case deletion_counts
git rm -q internal/c.go
git commit -q -m "bloem: drop c"
run_gate
check_status 1
check_output_has "internal/c.go"
finish_case

begin_case mode_change_counts
chmod +x internal/d.go
commit_all "bloem: exec d"
run_gate
check_status 1
check_output_has "internal/d.go"
finish_case

begin_case behind_upstream_is_not_a_divergence
git checkout -q silo
printf 'package a // silo v2\n' >internal/a.go
printf 'package e\n' >internal/e.go
git rm -q internal/b.go
commit_all "silo: move on"
git checkout -q main
run_gate
check_status 0
finish_case

# Regression: the old history-based gate reported every file upstream changed
# as an undeclared seam until the merge commit existed.
begin_case uncommitted_clean_merge_is_not_a_divergence
git checkout -q silo
printf 'package a // silo v2\n' >internal/a.go
printf 'package e\n' >internal/e.go
git rm -q internal/b.go
commit_all "silo: move on"
git checkout -q main
git merge -q --no-ff --no-commit silo
run_gate
check_status 0
check_output_lacks "internal/a.go"
finish_case

begin_case merge_head_older_than_upstream_tip
git checkout -q silo
printf 'package a // silo v2\n' >internal/a.go
commit_all "silo: v2"
older=$(git rev-parse HEAD)
printf 'package a // silo v3\n' >internal/a.go
commit_all "silo: v3"
git checkout -q main
git merge -q --no-ff --no-commit "$older"
run_gate
check_status 0
finish_case

begin_case bloem_edit_during_merge_is_still_a_seam
git checkout -q silo
printf 'package a // silo v2\n' >internal/a.go
commit_all "silo: v2"
git checkout -q main
git merge -q --no-ff --no-commit silo
printf 'package b // slipped in\n' >internal/b.go
run_gate
check_status 1
check_output_has "internal/b.go"
check_output_lacks "internal/a.go"
finish_case

begin_case resolved_conflict_keeps_declared_seam
printf 'package b // bloem\n' >internal/b.go
declare_seam internal/b.go
commit_all "bloem: patch b"
git checkout -q silo
printf 'package b // silo v2\n' >internal/b.go
commit_all "silo: v2"
git checkout -q main
git merge -q --no-ff --no-commit silo >/dev/null 2>&1 || true
printf 'package b // silo v2 + bloem\n' >internal/b.go
git add internal/b.go
run_gate
check_status 0
git commit -q --no-edit
run_gate
check_status 0
finish_case

begin_case upstream_adopting_the_change_leaves_a_stale_entry
printf 'package c // shared fix\n' >internal/c.go
declare_seam internal/c.go
commit_all "bloem: fix c"
git checkout -q silo
printf 'package c // shared fix\n' >internal/c.go
commit_all "silo: same fix"
git checkout -q main
git merge -q --no-ff --no-edit silo
run_gate
check_status 1
check_output_has "no longer needed"
check_output_has "internal/c.go"
finish_case

begin_case modifying_a_file_upstream_deleted_is_a_seam
git checkout -q silo
git rm -q internal/d.go
git commit -q -m "silo: drop d"
git checkout -q main
printf 'package d // bloem\n' >internal/d.go
commit_all "bloem: patch d"
run_gate
check_status 1
check_output_has "internal/d.go"
finish_case

begin_case unusual_paths_match_ledger_lines
printf 'spaced bloem\n' >"docs/a b.md"
printf 'accent bloem\n' >"docs/é.md"
declare_seam "docs/a b.md" "docs/é.md"
commit_all "bloem: docs"
run_gate
check_status 0
finish_case

begin_case unusual_paths_are_reported_whole
printf 'spaced bloem\n' >"docs/a b.md"
commit_all "bloem: docs"
run_gate
check_status 1
check_output_has "  docs/a b.md"
finish_case

begin_case missing_base_ref_is_an_error
set +e
SEAM_BASE_REFS=upstream/nope SEAM_LEDGER=seams.txt bash "$gate" >"$out" 2>&1
case_status=$?
set -e
check_status 2
check_output_has "git fetch upstream"
finish_case

printf '\n%d passed, %d failed\n' "$passed" "$failed"
if [[ "$failed" -ne 0 ]]; then
	exit 1
fi
