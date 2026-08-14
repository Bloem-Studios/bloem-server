#!/usr/bin/env bash
# Tests for scripts/verify-compat-compose.sh.
#
# Two halves:
#   1. The committed Compose contract must pass the structural scan.
#   2. The scan must actually detect violations: each case copies the committed
#      files into a temp directory, injects one prohibited construct, verifies
#      the injection really changed the file, and requires the scan to fail.
#
# Run from anywhere inside the repository: bash scripts/verify-compat-compose_test.sh
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
verifier="$script_dir/verify-compat-compose.sh"

abs_file="docker-compose.audiobookshelf.yml"
jf_file="docker-compose.jellyfin.yml"
diag_file="docker-compose.compat-diagnostics.yml"

passed=0
failed=0

report() {
	local status=$1 name=$2
	if [[ "$status" == "ok" ]]; then
		printf 'ok   %s\n' "$name"
		passed=$((passed + 1))
	else
		printf 'FAIL %s\n' "$name" >&2
		failed=$((failed + 1))
	fi
}

workdir=$(mktemp -d "${TMPDIR:-/tmp}/verify-compat-compose-test.XXXXXX")
trap 'rm -rf "$workdir"' EXIT

# --- 1. The committed contract passes the scan. -----------------------------

if out=$("$verifier" "$repo_root" 2>&1); then
	report ok "committed compose contract passes the structural scan"
else
	printf '%s\n' "$out" >&2
	report fail "committed compose contract passes the structural scan"
fi

# --- 2. Tampered contracts fail the scan. -----------------------------------

# fresh_copy <case-dir>: copy the four committed compose files.
fresh_copy() {
	local dir=$1
	mkdir -p "$dir"
	cp "$repo_root/docker-compose.yml" \
		"$repo_root/$abs_file" \
		"$repo_root/$jf_file" \
		"$repo_root/$diag_file" \
		"$dir/"
}

# mutate <file> <sed-script>: apply sed in place and insist it changed the file,
# so a drifted anchor line cannot silently turn a tamper case into a no-op.
mutate() {
	local file=$1 sed_script=$2
	local before after
	before=$(cksum "$file")
	sed -i.bak -e "$sed_script" "$file"
	rm -f "$file.bak"
	after=$(cksum "$file")
	if [[ "$before" == "$after" ]]; then
		printf 'tamper anchor did not match in %s: %s\n' "$file" "$sed_script" >&2
		return 1
	fi
}

# expect_scan_failure <name> <file> <sed-script>
expect_scan_failure() {
	local name=$1 file=$2 sed_script=$3
	local dir="$workdir/case-$((passed + failed))"
	fresh_copy "$dir"
	if ! mutate "$dir/$file" "$sed_script"; then
		report fail "$name"
		return
	fi
	if "$verifier" "$dir" >/dev/null 2>&1; then
		report fail "$name"
	else
		report ok "$name"
	fi
}

# Each overlay defines exactly one companion service, and only that service
# carries `restart: unless-stopped`, so it is a safe insertion anchor.
service_anchor='restart: unless-stopped'

expect_scan_failure \
	"detects a host port on a companion" \
	"$abs_file" \
	"/$service_anchor/a\\
    ports:\\
      - \"13378:8080\"
"

expect_scan_failure \
	"detects a Vondel database URL in companion environment" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      DATABASE_URL: postgres://silo:silo@postgres:5432/silo
"

expect_scan_failure \
	"detects the Vondel SECRET_KEY in companion environment" \
	"$jf_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      SECRET_KEY: not-allowed-here
"

expect_scan_failure \
	"detects a Docker socket mount" \
	"$abs_file" \
	"/- vondel-audiobookshelf-state:\/var\/lib\/vondel-compat/a\\
      - /var/run/docker.sock:/var/run/docker.sock
"

expect_scan_failure \
	"detects a media bind mount" \
	"$jf_file" \
	"/- vondel-jellyfin-state:\/var\/lib\/vondel-compat/a\\
      - /mnt/media:/mnt/media:ro
"

expect_scan_failure \
	"detects privileged mode" \
	"$jf_file" \
	"/$service_anchor/a\\
    privileged: true
"

expect_scan_failure \
	"detects a local build context replacing the private image" \
	"$abs_file" \
	"/$service_anchor/a\\
    build: .
"

expect_scan_failure \
	"detects a public image source" \
	"$abs_file" \
	"s|ghcr.io/vondel-media/vondel-audiobookshelf:latest|docker.io/library/audiobookshelf:latest|"

expect_scan_failure \
	"detects a non-loopback diagnostic binding" \
	"$diag_file" \
	"s|127.0.0.1:|0.0.0.0:|g"

expect_scan_failure \
	"detects a companion escaping the internal network" \
	"$abs_file" \
	"s|internal: true|internal: false|"

expect_scan_failure \
	"detects a companion joining the default network outside diagnostics" \
	"$abs_file" \
	"/- vondel-audiobookshelf$/a\\
      default: {}
"

expect_scan_failure \
	"detects a renamed enrollment secret target" \
	"$jf_file" \
	"s|target: vondel_compat_enrollment|target: some_other_secret|"

expect_scan_failure \
	"detects an environment-backed enrollment secret" \
	"$abs_file" \
	"s|file: \${VONDEL_AUDIOBOOKSHELF_ENROLLMENT_FILE:-.*}|environment: VONDEL_AUDIOBOOKSHELF_ENROLLMENT|"

expect_scan_failure \
	"detects a host device mapping" \
	"$abs_file" \
	"/$service_anchor/a\\
    devices:\\
      - /dev/mem:/dev/mem
"

expect_scan_failure \
	"detects a host pid namespace" \
	"$jf_file" \
	"/$service_anchor/a\\
    pid: host
"

expect_scan_failure \
	"detects a host ipc namespace" \
	"$abs_file" \
	"/$service_anchor/a\\
    ipc: host
"

expect_scan_failure \
	"detects an external state volume aliasing a host volume" \
	"$abs_file" \
	"/^  vondel-audiobookshelf-state:$/a\\
    external: true
"

expect_scan_failure \
	"detects an explicitly named state volume aliasing another volume" \
	"$jf_file" \
	"/^  vondel-jellyfin-state:$/a\\
    name: shared-host-media
"

expect_scan_failure \
	"detects a keyword-form database key in companion environment" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      DB_HOST: postgres
"

expect_scan_failure \
	"detects a libpq keyword DSN in companion environment" \
	"$jf_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      STATE_BACKEND: host=postgres user=silo password=silo dbname=silo
"

# --- Default-deny: keys outside the companion allowlist ----------------------
#
# These are not caught by any rule naming them. The service-key allowlist
# denies them because they are simply not on it — which is the point: the same
# check covers Compose keys nobody has thought of yet (see the unknown-key case
# at the end).

expect_scan_failure \
	"detects a disabled seccomp sandbox in security_opt" \
	"$abs_file" \
	"s|- no-new-privileges:true|- seccomp:unconfined|"

expect_scan_failure \
	"detects an unconfined AppArmor profile in security_opt" \
	"$jf_file" \
	"/- no-new-privileges:true/a\\
      - apparmor:unconfined
"

expect_scan_failure \
	"detects unconfined system paths in security_opt" \
	"$abs_file" \
	"/- no-new-privileges:true/a\\
      - systempaths=unconfined
"

expect_scan_failure \
	"detects label:disable in security_opt" \
	"$jf_file" \
	"/- no-new-privileges:true/a\\
      - label:disable
"

expect_scan_failure \
	"detects volumes_from inheriting another container's mounts" \
	"$abs_file" \
	"/$service_anchor/a\\
    volumes_from:\\
      - silo
"

expect_scan_failure \
	"detects device_cgroup_rules granting device access" \
	"$jf_file" \
	"/$service_anchor/a\\
    device_cgroup_rules:\\
      - 'c 1:1 rwm'
"

expect_scan_failure \
	"detects a host uts namespace" \
	"$abs_file" \
	"/$service_anchor/a\\
    uts: host
"

expect_scan_failure \
	"detects a cgroup_parent escape" \
	"$jf_file" \
	"/$service_anchor/a\\
    cgroup_parent: /
"

expect_scan_failure \
	"detects extra_hosts reaching the host gateway" \
	"$abs_file" \
	"/$service_anchor/a\\
    extra_hosts:\\
      - \"host.docker.internal:host-gateway\"
"

expect_scan_failure \
	"detects sysctls tuning" \
	"$jf_file" \
	"/$service_anchor/a\\
    sysctls:\\
      net.ipv4.ip_forward: 1
"

expect_scan_failure \
	"detects group_add" \
	"$abs_file" \
	"/$service_anchor/a\\
    group_add:\\
      - \"0\"
"

expect_scan_failure \
	"detects a tmpfs shadowing the enrollment secret mount" \
	"$jf_file" \
	"/$service_anchor/a\\
    tmpfs:\\
      - /run/secrets
"

expect_scan_failure \
	"detects a DSN split across innocuously named environment keys" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      STORE_HOST: postgres\\
      STORE_DBNAME: silo\\
      STORE_USER: silo\\
      STORE_PW: silo
"

# The cases this whole design exists for: keys no rule of ours has ever named,
# standing in for whatever Compose adds next. They must be rejected by the
# allowlist alone.
#
# These use real Compose keys rather than an invented one on purpose. An
# invented key is rejected by Compose's own schema before the scan ever sees
# it, so it would prove nothing about the allowlist — an `x-` extension field
# is stripped from the rendered service entirely, which proves just as little.
# `runtime` swaps the container runtime, `dns` redirects name resolution, and
# `storage_opt` tunes the storage driver; all three render, all three are
# unruled, and all three must still fail.
expect_scan_failure \
	"rejects the unruled runtime key by allowlist alone" \
	"$jf_file" \
	"/$service_anchor/a\\
    runtime: sysbox-runc
"

expect_scan_failure \
	"rejects the unruled dns key by allowlist alone" \
	"$abs_file" \
	"/$service_anchor/a\\
    dns:\\
      - 10.0.0.1
"

expect_scan_failure \
	"rejects the unruled storage_opt key by allowlist alone" \
	"$jf_file" \
	"/$service_anchor/a\\
    storage_opt:\\
      size: 10G
"

# --- Summary -----------------------------------------------------------------

printf '%d passed, %d failed\n' "$passed" "$failed"
if [[ "$failed" -ne 0 ]]; then
	exit 1
fi
