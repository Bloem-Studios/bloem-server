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
	return 0
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
	return 0
}

# THE CONTROL CASE. Every tamper case below copies the committed files into
# $workdir and asserts the scan then fails. That proves nothing unless an
# UNTAMPERED copy in the same place passes: when the scan carried a path
# comparison that broke for any directory other than the repository root, every
# copy failed, and all thirty tamper cases reported ok without their injected
# construct contributing anything. This case fails first when that recurs.
control_dir="$workdir/control-untampered"
fresh_copy "$control_dir"
if control_out=$("$verifier" "$control_dir" 2>&1); then
	report ok "an untampered copy outside the repository passes the scan"
else
	printf '%s\n' "$control_out" | sed 's/^/  | /' >&2
	report fail "an untampered copy outside the repository passes the scan"
fi

# expect_scan_failure <name> <file> <sed-script> <expected-FAIL-substring>
#
# A non-zero exit is NOT evidence: the scan can fail for reasons that have
# nothing to do with the injected construct, and when that happened every case
# in this file reported ok while proving nothing. So each case must name the
# check it expects to trip, and the matching FAIL line must appear in the
# output. The 4th argument is mandatory.
expect_scan_failure() {
	local name=$1 file=$2 sed_script=$3 expected=$4
	local dir="$workdir/case-$((passed + failed))"

	if [[ -z "$expected" ]]; then
		printf 'test bug: %s declares no expected failure message\n' "$name" >&2
		report fail "$name"
		return
	fi

	fresh_copy "$dir"
	if ! mutate "$dir/$file" "$sed_script"; then
		report fail "$name"
		return
	fi

	local out status
	out=$("$verifier" "$dir" 2>&1) && status=0 || status=$?
	if [[ "$status" -eq 0 ]]; then
		printf '  scan passed; the evasion was not detected\n' >&2
		report fail "$name"
		return
	fi
	if ! grep -Fq "FAIL:" <<<"$out" || ! grep -Fq "$expected" <<<"$out"; then
		printf '  scan failed, but not for the expected reason\n' >&2
		printf '  expected to find: %s\n' "$expected" >&2
		printf '%s\n' "$out" | sed 's/^/  | /' >&2
		report fail "$name"
		return
	fi
	# A crashed scan is not a detection. A jq fault, or an abort before the
	# summary, can make an unrelated check's message appear while later
	# combinations never run at all — which is how three cases once reported
	# success on a run that had already died.
	if grep -Eq 'jq: error|jq: syntax error' <<<"$out"; then
		printf '  scan hit a jq fault; the run aborted rather than detecting\n' >&2
		printf '%s\n' "$out" | sed 's/^/  | /' >&2
		report fail "$name"
		return
	fi
	if ! grep -Eq 'check\(s\) failed' <<<"$out"; then
		printf '  scan never reached its summary; it aborted before finishing\n' >&2
		printf '%s\n' "$out" | sed 's/^/  | /' >&2
		report fail "$name"
		return
	fi
	report ok "$name"
}

# Each overlay defines exactly one companion service, and only that service
# carries `restart: unless-stopped`, so it is a safe insertion anchor.
service_anchor='restart: unless-stopped'

expect_scan_failure \
	"detects an overlay removing a base service" \
	"$jf_file" \
	"$ a\\
  meilisearch: !reset null" \
	"overlay removed base service(s)"

expect_scan_failure \
	"detects a host port on a companion" \
	"$abs_file" \
	"/$service_anchor/a\\
    ports:\\
      - \"13378:8080\"
" \
	"must publish no host ports outside the diagnostics override"

expect_scan_failure \
	"detects a Vondel database URL in companion environment" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      DATABASE_URL: postgres://silo:silo@postgres:5432/silo
" \
	"must not receive Vondel database/Redis/signing/provider/tuner or credential-shaped environment keys"

expect_scan_failure \
	"detects the Vondel SECRET_KEY in companion environment" \
	"$jf_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      SECRET_KEY: not-allowed-here
" \
	"must not receive Vondel database/Redis/signing/provider/tuner or credential-shaped environment keys"

expect_scan_failure \
	"detects a Docker socket mount" \
	"$abs_file" \
	"/- vondel-audiobookshelf-state:\/var\/lib\/vondel-compat/a\\
      - /var/run/docker.sock:/var/run/docker.sock
" \
	"may mount only named volumes"

expect_scan_failure \
	"detects a media bind mount" \
	"$jf_file" \
	"/- vondel-jellyfin-state:\/var\/lib\/vondel-compat/a\\
      - /mnt/media:/mnt/media:ro
" \
	"may mount only named volumes"

expect_scan_failure \
	"detects privileged mode" \
	"$jf_file" \
	"/$service_anchor/a\\
    privileged: true
" \
	"outside the companion allowlist: privileged"

expect_scan_failure \
	"detects a local build context replacing the private image" \
	"$abs_file" \
	"/$service_anchor/a\\
    build: .
" \
	"outside the companion allowlist: build"

expect_scan_failure \
	"detects a public image source" \
	"$abs_file" \
	"s|ghcr.io/vondel-media/vondel-audiobookshelf:latest|docker.io/library/audiobookshelf:latest|" \
	"image must default to the private ghcr.io/vondel-media registry"

expect_scan_failure \
	"detects a non-loopback diagnostic binding" \
	"$diag_file" \
	"s|127.0.0.1:|0.0.0.0:|g" \
	"diagnostic ports must all bind 127.0.0.1"

expect_scan_failure \
	"detects a companion escaping the internal network" \
	"$abs_file" \
	"s|internal: true|internal: false|" \
	"the vondel-compat network must be internal"

expect_scan_failure \
	"detects a companion joining the default network outside diagnostics" \
	"$abs_file" \
	"/- vondel-audiobookshelf$/a\\
      default: {}
" \
	"must attach only the vondel-compat network"

expect_scan_failure \
	"detects a renamed enrollment secret target" \
	"$jf_file" \
	"s|target: vondel_compat_enrollment|target: some_other_secret|" \
	"must mount exactly one enrollment secret at /run/secrets/vondel_compat_enrollment"

expect_scan_failure \
	"detects an environment-backed enrollment secret" \
	"$abs_file" \
	"s|file: \${VONDEL_AUDIOBOOKSHELF_ENROLLMENT_FILE:-.*}|environment: VONDEL_AUDIOBOOKSHELF_ENROLLMENT|" \
	"must be file-backed, never environment-backed"

expect_scan_failure \
	"detects a host device mapping" \
	"$abs_file" \
	"/$service_anchor/a\\
    devices:\\
      - /dev/mem:/dev/mem
" \
	"outside the companion allowlist: devices"

expect_scan_failure \
	"detects a host pid namespace" \
	"$jf_file" \
	"/$service_anchor/a\\
    pid: host
" \
	"outside the companion allowlist: pid"

expect_scan_failure \
	"detects a host ipc namespace" \
	"$abs_file" \
	"/$service_anchor/a\\
    ipc: host
" \
	"outside the companion allowlist: ipc"

expect_scan_failure \
	"detects an external state volume aliasing a host volume" \
	"$abs_file" \
	"/^  vondel-audiobookshelf-state:$/a\\
    external: true
" \
	"state volumes must be locally defined"

expect_scan_failure \
	"detects an explicitly named state volume aliasing another volume" \
	"$jf_file" \
	"/^  vondel-jellyfin-state:$/a\\
    name: shared-host-media
" \
	"state volumes must be locally defined"

expect_scan_failure \
	"detects a keyword-form database key in companion environment" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      DB_HOST: postgres
" \
	"must not receive Vondel database/Redis/signing/provider/tuner or credential-shaped environment keys"

expect_scan_failure \
	"detects a libpq keyword DSN in companion environment" \
	"$jf_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      STATE_BACKEND: host=postgres user=silo password=silo dbname=silo
" \
	"must not carry database/Redis URLs or keyword-form (libpq) DSNs"

# --- Default-deny: keys outside the companion allowlist ----------------------
#
# These are not caught by any rule naming them. The service-key allowlist
# denies them because they are simply not on it — which is the point: the same
# check covers Compose keys nobody has thought of yet (see the unknown-key case
# at the end).

expect_scan_failure \
	"detects a disabled seccomp sandbox in security_opt" \
	"$abs_file" \
	"s|- no-new-privileges:true|- seccomp:unconfined|" \
	"security_opt must be exactly no-new-privileges:true"

expect_scan_failure \
	"detects an unconfined AppArmor profile in security_opt" \
	"$jf_file" \
	"/- no-new-privileges:true/a\\
      - apparmor:unconfined
" \
	"security_opt must be exactly no-new-privileges:true"

expect_scan_failure \
	"detects unconfined system paths in security_opt" \
	"$abs_file" \
	"/- no-new-privileges:true/a\\
      - systempaths=unconfined
" \
	"security_opt must be exactly no-new-privileges:true"

expect_scan_failure \
	"detects label:disable in security_opt" \
	"$jf_file" \
	"/- no-new-privileges:true/a\\
      - label:disable
" \
	"security_opt must be exactly no-new-privileges:true"

expect_scan_failure \
	"detects volumes_from inheriting another container's mounts" \
	"$abs_file" \
	"/$service_anchor/a\\
    volumes_from:\\
      - silo
" \
	"volumes_from"
# ^ substring only, not the full "allowlist: volumes_from": Compose synthesizes
# an implicit depends_on from volumes_from, so both keys are named in the
# message and their order is Compose's business, not ours.

expect_scan_failure \
	"detects device_cgroup_rules granting device access" \
	"$jf_file" \
	"/$service_anchor/a\\
    device_cgroup_rules:\\
      - 'c 1:1 rwm'
" \
	"outside the companion allowlist: device_cgroup_rules"

expect_scan_failure \
	"detects a host uts namespace" \
	"$abs_file" \
	"/$service_anchor/a\\
    uts: host
" \
	"outside the companion allowlist: uts"

expect_scan_failure \
	"detects a cgroup_parent escape" \
	"$jf_file" \
	"/$service_anchor/a\\
    cgroup_parent: /
" \
	"outside the companion allowlist: cgroup_parent"

expect_scan_failure \
	"detects extra_hosts reaching the host gateway" \
	"$abs_file" \
	"/$service_anchor/a\\
    extra_hosts:\\
      - \"host.docker.internal:host-gateway\"
" \
	"outside the companion allowlist: extra_hosts"

expect_scan_failure \
	"detects sysctls tuning" \
	"$jf_file" \
	"/$service_anchor/a\\
    sysctls:\\
      net.ipv4.ip_forward: 1
" \
	"outside the companion allowlist: sysctls"

expect_scan_failure \
	"detects group_add" \
	"$abs_file" \
	"/$service_anchor/a\\
    group_add:\\
      - \"0\"
" \
	"outside the companion allowlist: group_add"

expect_scan_failure \
	"detects a tmpfs shadowing the enrollment secret mount" \
	"$jf_file" \
	"/$service_anchor/a\\
    tmpfs:\\
      - /run/secrets
" \
	"outside the companion allowlist: tmpfs"

expect_scan_failure \
	"detects a DSN split across innocuously named environment keys" \
	"$abs_file" \
	"/VONDEL_COMPAT_ENROLLMENT_FILE: \/run\/secrets\/vondel_compat_enrollment/a\\
      STORE_HOST: postgres\\
      STORE_DBNAME: silo\\
      STORE_USER: silo\\
      STORE_PW: silo
" \
	"must not name another service as a bare hostname"

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
" \
	"outside the companion allowlist: runtime"

expect_scan_failure \
	"rejects the unruled dns key by allowlist alone" \
	"$abs_file" \
	"/$service_anchor/a\\
    dns:\\
      - 10.0.0.1
" \
	"outside the companion allowlist: dns"

expect_scan_failure \
	"rejects the unruled storage_opt key by allowlist alone" \
	"$jf_file" \
	"/$service_anchor/a\\
    storage_opt:\\
      size: 10G
" \
	"outside the companion allowlist: storage_opt"

# --- Default-deny at the SERVICE level ---------------------------------------
#
# A key allowlist constrains services that exist; it says nothing about a
# service that did not exist before. These tamper the service SET.

expect_scan_failure \
	"detects an appended privileged sidekick service" \
	"$jf_file" \
	"\$a\\
\\
  vondel-jellyfin-helper:\\
    image: alpine:latest\\
    privileged: true\\
    pid: host\\
    network_mode: host\\
    volumes:\\
      - /:/host\\
      - /var/run/docker.sock:/var/run/docker.sock\\
    command: [\"sleep\", \"infinity\"]
" \
	"unexpected service(s) in the rendered configuration: vondel-jellyfin-helper"

# The sharper version of the same finding: this service declares ONLY keys that
# are on the companion key allowlist (image, volumes), so the key check clears
# it completely. Only the service-set check can reject it — which is the point
# of having one.
expect_scan_failure \
	"detects a bare sidekick whose keys are all allowlisted" \
	"$abs_file" \
	"\$a\\
\\
  vondel-audiobookshelf-sidecar:\\
    image: alpine:latest\\
    volumes:\\
      - /var/run/docker.sock:/var/run/docker.sock
" \
	"unexpected service(s) in the rendered configuration: vondel-audiobookshelf-sidecar"

# A service hidden behind a Compose profile renders only when that profile is
# enabled, so it would be invisible to a scan that renders the default set.
expect_scan_failure \
	"detects a service hidden behind a compose profile" \
	"$jf_file" \
	"\$a\\
\\
  vondel-jellyfin-debug:\\
    image: alpine:latest\\
    profiles: [debug]\\
    volumes:\\
      - /var/run/docker.sock:/var/run/docker.sock
" \
	"unexpected service(s) in the rendered configuration: vondel-jellyfin-debug"

# --- Values behind allowed keys ----------------------------------------------

# healthcheck.test is arbitrary command execution inside the companion — the
# capability command/entrypoint are denied for. This probe reads the enrollment
# secret and posts it to a service it shares a network with.
expect_scan_failure \
	"detects a healthcheck exfiltrating the enrollment secret" \
	"$jf_file" \
	"/$service_anchor/a\\
    healthcheck:\\
      test: [\"CMD-SHELL\", \"cat /run/secrets/vondel_compat_enrollment | wget -qO- --post-data=@- http://silo:8080/\"]
" \
	"outside the companion allowlist: healthcheck"

# labels are inert only if nothing on the host consumes them; a Traefik router
# label republishes an internal companion on public ingress.
expect_scan_failure \
	"detects ingress-republishing labels" \
	"$abs_file" \
	"/$service_anchor/a\\
    labels:\\
      traefik.enable: \"true\"\\
      traefik.http.routers.abs.rule: \"Host(\\\`abs.example\\\`)\"
" \
	"outside the companion allowlist: labels"

# File-backed is not the same as the right file: an unpinned secret path mounts
# an arbitrary host file into the companion at the enrollment path.
expect_scan_failure \
	"detects an enrollment secret backed by an arbitrary host file" \
	"$jf_file" \
	"s|file: \${VONDEL_JELLYFIN_ENROLLMENT_FILE:-.*}|file: /etc/passwd|" \
	"enrollment secret must be the committed"

# The same defeat one level indirect: leave the committed secret alone, add a
# second host-backed secret and point the companion's source at it.
expect_scan_failure \
	"detects a swapped source naming a host-backed secret" \
	"$abs_file" \
	"s|source: vondel_audiobookshelf_enrollment|source: vondel_audiobookshelf_alt|;
	 s|^secrets:\$|secrets:\\
  vondel_audiobookshelf_alt:\\
    file: /etc/hosts|" \
	"enrollment secret must be the committed"

# --- Summary -----------------------------------------------------------------

printf '%d passed, %d failed\n' "$passed" "$failed"
if [[ "$failed" -ne 0 ]]; then
	exit 1
fi
