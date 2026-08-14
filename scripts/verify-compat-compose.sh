#!/usr/bin/env bash
# Structural security scan for the private compatibility-companion Compose contracts.
#
# Renders every supported Compose file combination with `docker compose config`
# and enforces the deployment rulings from
# docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md.
#
# THE SCAN IS DEFAULT-DENY AT THE SERVICE LEVEL.
#
# Enumerating forbidden Compose keys does not converge: every Compose key — and
# every key a future Compose release adds — is permitted until someone thinks
# to forbid it, and each one is a candidate route to the host. Successive
# reviews of this scan produced exactly that treadmill (privileged, then
# cap_add, then devices, then pid/ipc, then volumes_from, then
# device_cgroup_rules, then uts/sysctls/tmpfs/extra_hosts...).
#
# So the primary control is COMPANION_SERVICE_KEYS below: a companion service's
# rendered key set must be a SUBSET of that allowlist. Anything else — known or
# not yet invented — fails the scan by construction and is named in the error.
# Adding a key to the allowlist is a deliberate, reviewable act.
#
# Permitting a key is not permitting any value, so each allowed key that can
# carry risk keeps a value-level check:
#
#   - image      — must be a private ghcr.io/vondel-media image, never a build;
#   - ports      — none at all, except the diagnostics override, which may bind
#                  127.0.0.1 only;
#   - networks   — exactly the internal vondel-compat network (plus the default
#                  bridge in the diagnostics override, which publishing needs);
#   - volumes    — locally defined named volumes only: no bind mounts, no
#                  external/aliased volumes, no driver_opts;
#   - secrets    — exactly one file-backed enrollment secret mounted at
#                  /run/secrets/vondel_compat_enrollment;
#   - security_opt — exactly no-new-privileges:true; never unconfined seccomp,
#                  AppArmor, system paths, or label:disable;
#   - environment — no Vondel database/Redis/signing/provider/tuner or
#                  credential-shaped keys, and no DSN-shaped values.
#
# The Vondel service is checked separately and only for what it must not gain
# (the companion enrollment secret) — it legitimately owns media mounts and the
# database, so a service-key allowlist does not apply to it.
#
# Usage: scripts/verify-compat-compose.sh [compose-dir]
#
# compose-dir defaults to the repository root and must contain
# docker-compose.yml plus the three compatibility overlay files. The scan only
# renders configurations; it never creates containers, networks, or volumes.
set -euo pipefail

usage() {
	printf 'usage: %s [compose-dir]\n' "${0##*/}" >&2
}

case "${1:-}" in
-h | --help)
	usage
	exit 0
	;;
esac

if [[ $# -gt 1 ]]; then
	usage
	exit 2
fi

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
compose_dir=${1:-$(cd "$script_dir/.." && pwd)}

for tool in docker jq; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'verify-compat-compose: required tool not found: %s\n' "$tool" >&2
		exit 3
	fi
done

base_file="docker-compose.yml"
abs_file="docker-compose.audiobookshelf.yml"
jf_file="docker-compose.jellyfin.yml"
diag_file="docker-compose.compat-diagnostics.yml"

for f in "$base_file" "$abs_file" "$jf_file" "$diag_file"; do
	if [[ ! -f "$compose_dir/$f" ]]; then
		printf 'verify-compat-compose: missing compose file: %s\n' "$f" >&2
		exit 1
	fi
done

# Fixed project name for rendering; locally defined volumes render with this
# prefix, which the state-volume locality check relies on.
project_name="vondel-compat-verify"

failures=0

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	failures=$((failures + 1))
}

# Render a combination of compose files to canonical JSON.
# --env-file /dev/null keeps any operator .env out of the scan so the committed
# defaults are what gets verified; the two required base variables are supplied
# with inert values. Rendering never touches the Docker daemon state.
render() {
	local args=()
	local f
	for f in "$@"; do
		args+=(-f "$f")
	done
	(
		cd "$compose_dir" &&
			MEDIA_ROOT="/dev/null" SECRET_KEY="verify-only" \
				docker compose --project-name "$project_name" \
				--env-file /dev/null "${args[@]}" config --format json
	)
}

# check <rendered-json> <combo-label> <jq-filter> <message>
# The jq filter must evaluate to true for the check to pass.
check() {
	local json=$1 label=$2 filter=$3 message=$4
	local ok
	if ! ok=$(jq -e \
		--arg forbidden "$forbidden_env_key_re" \
		--argjson allowed "$companion_service_keys_json" \
		--argjson required_security_opt "$required_security_opt_json" \
		"$filter" <<<"$json" 2>&1); then
		if [[ "$ok" == "false" || "$ok" == "null" ]]; then
			fail "[$label] $message"
		else
			fail "[$label] $message (jq error: $ok)"
		fi
	fi
}

# check_service_key_allowlist <rendered-json> <combo-label> <service>
# The default-deny control: fail on any declared key outside the allowlist, and
# name the offending keys so the operator learns what to justify or remove.
check_service_key_allowlist() {
	local json=$1 label=$2 svc=$3
	local unknown
	unknown=$(jq -r \
		--arg svc "$svc" \
		--argjson allowed "$companion_service_keys_json" \
		".services[\$svc] | to_entries
			| map(select($declared_filter))
			| map(.key)
			| map(select(. as \$k | \$allowed | index(\$k) | not))
			| join(\", \")" <<<"$json")
	if [[ -n "$unknown" ]]; then
		fail "[$label] $svc declares keys outside the companion allowlist: $unknown"
	fi
}

# ---------------------------------------------------------------------------
# THE ALLOWLIST. A companion service may declare these keys and nothing else.
#
# Derived from what the committed overlays actually render, plus a small
# reviewed set of operational keys that cannot widen a companion's reach beyond
# its own container. Every entry carries the reason it is safe; anything absent
# is denied, including Compose keys that do not exist yet.
#
#   image              the private companion image (value-checked: registry)
#   environment        companion configuration (value-checked: keys + values)
#   networks           private network membership (value-checked: exact set)
#   volumes            disposable state (value-checked: local named volumes)
#   secrets            enrollment token (value-checked: file-backed, one, path)
#   security_opt       hardening (value-checked: no-new-privileges:true only)
#   ports              denied outright except loopback diagnostics (value-checked)
#   restart            restart policy; container lifecycle only
#   healthcheck        runs a probe INSIDE the container; no host reach
#   depends_on         start ordering only
#   labels             metadata only; no runtime capability
#   read_only          hardening: read-only root filesystem
#   init               run a pid-1 reaper inside the container
#   user               drop privileges inside the container; cannot exceed the
#                      image default, which is already root
#   stop_grace_period  shutdown timing only
#   pull_policy        when to pull; the image value check still applies
#
# Deliberately NOT allowed, and therefore denied by the allowlist rather than by
# any named rule: build, privileged, cap_add, devices, device_cgroup_rules,
# volumes_from, tmpfs, pid, ipc, uts, userns_mode, cgroup, cgroup_parent,
# network_mode, sysctls, group_add, extra_hosts, command, entrypoint, and
# everything else.
companion_service_keys_json='[
	"image", "environment", "networks", "volumes", "secrets", "security_opt",
	"ports", "restart", "healthcheck", "depends_on", "labels", "read_only",
	"init", "user", "stop_grace_period", "pull_policy"
]'

# Values `docker compose config` renders for keys that were never declared.
# A key holding one of these is treated as absent, so the renderer's own null
# padding (command, entrypoint, ...) does not have to be allowlisted.
declared_filter='.value != null and .value != [] and .value != {}'

# security_opt is allowed as a key, so its value must be pinned: exactly the
# no-new-privileges hardening flag. Anything else — unconfined seccomp or
# AppArmor, unconfined system paths, label:disable — turns the sandbox off.
required_security_opt_json='["no-new-privileges:true"]'

# Environment keys a companion must never receive: Vondel's own service
# configuration, anything database/connection shaped (DB_*, PG*, CONN*),
# anything provider/tuner/signing shaped, and anything credential-shaped
# (secrets travel as files, never as environment values).
# shellcheck disable=SC2016  # single-quoted on purpose: this is a jq regex, not shell
forbidden_env_key_re='^(DATABASE_URL|REDIS_URL|SECRET_KEY|MEILI_MASTER_KEY)$|^(POSTGRES_|REDIS_|MEILI_|DB_|PG|CONN)|TUNER|PROVIDER|SIGNING|PASSWORD|TOKEN|SECRET|API_KEY|CREDENTIAL'

# Shared invariants for one companion service in one rendered combination.
#   verify_companion <json> <label> <service> <diagnostics: yes|no>
verify_companion() {
	local json=$1 label=$2 svc=$3 diagnostics=$4
	local s=".services[\"$svc\"]"

	check "$json" "$label" "$s != null" "service $svc is missing from the rendered configuration"
	if ! jq -e "$s != null" <<<"$json" >/dev/null 2>&1; then
		return
	fi

	# Primary control: everything not explicitly allowed is denied.
	check_service_key_allowlist "$json" "$label" "$svc"

	if [[ "$diagnostics" == "yes" ]]; then
		check "$json" "$label" \
			"[$s.ports // [] | .[] | .host_ip] | length > 0 and all(. == \"127.0.0.1\")" \
			"$svc diagnostic ports must all bind 127.0.0.1"
		check "$json" "$label" \
			"$s.networks | keys | sort == [\"default\", \"vondel-compat\"]" \
			"$svc must attach exactly the vondel-compat and default networks in diagnostics mode"
	else
		check "$json" "$label" \
			"($s.ports // []) | length == 0" \
			"$svc must publish no host ports outside the diagnostics override"
		check "$json" "$label" \
			"$s.networks | keys == [\"vondel-compat\"]" \
			"$svc must attach only the vondel-compat network"
	fi

	check "$json" "$label" \
		".networks[\"vondel-compat\"].internal == true" \
		"the vondel-compat network must be internal"
	check "$json" "$label" \
		"$s.networks[\"vondel-compat\"].aliases == [\"$svc\"]" \
		"$svc must declare its private network alias"

	# privileged, cap_add, devices, device_cgroup_rules, volumes_from, tmpfs,
	# pid, ipc, uts, userns_mode, cgroup, cgroup_parent, network_mode, sysctls,
	# group_add, extra_hosts and build are not enumerated here on purpose: the
	# allowlist above already denies every one of them, and denies their
	# not-yet-invented successors too.

	# security_opt IS allowed as a key, so pin its value. An unconfined seccomp
	# or AppArmor profile, unconfined system paths, or label:disable would
	# switch the sandbox off while keeping the key legitimate-looking.
	check "$json" "$label" \
		"($s.security_opt // []) == \$required_security_opt" \
		"$svc security_opt must be exactly no-new-privileges:true (no unconfined seccomp/AppArmor/system paths, no label:disable)"

	check "$json" "$label" \
		"$s.image | test(\"^ghcr\\\\.io/vondel-media/$svc(:|@|$)\")" \
		"$svc image must default to the private ghcr.io/vondel-media registry"

	check "$json" "$label" \
		"($s.volumes // []) | all(.type == \"volume\")" \
		"$svc may mount only named volumes (no bind mounts, media, or Docker socket)"
	check "$json" "$label" \
		"[($s.volumes // [])[] | .source] as \$names |
			[.volumes | to_entries[] | select(.key as \$k | \$names | index(\$k))] |
			all((.value.driver_opts // {}) | length == 0)" \
		"$svc state volumes must not use driver_opts to reach host paths"
	# A companion state volume must be defined locally by this project. An
	# `external: true` volume or an explicit `name:` can alias a pre-existing
	# host volume (for example one bound to the media tree), so both are
	# rejected: the rendered name must be the project-prefixed one Compose
	# generates for locally defined volumes.
	check "$json" "$label" \
		"[($s.volumes // [])[] | .source] as \$names |
			[.volumes | to_entries[] | select(.key as \$k | \$names | index(\$k))] |
			all(
				(.value.external != true)
				and (.value.name == \"${project_name}_\" + .key)
			)" \
		"$svc state volumes must be locally defined (no external: true, no explicit name aliasing another volume)"

	check "$json" "$label" \
		"($s.environment // {}) | keys | all(test(\$forbidden; \"\") | not)" \
		"$svc must not receive Vondel database/Redis/signing/provider/tuner or credential-shaped environment keys"
	check "$json" "$label" \
		"($s.environment // {}) | [.[]] | all(
			(tostring | ascii_downcase) as \$v |
			(
				(\$v | test(\"(postgres(ql)?|redis|rediss)://\"))
				or (\$v | test(\"password=\"))
				or ((\$v | test(\"host=\")) and (\$v | test(\"dbname=\")))
			) | not
		)" \
		"$svc environment must not carry database/Redis URLs or keyword-form (libpq) DSNs"
	# BEST EFFORT, and only that: a DSN split across innocuously named keys
	# (STORE_HOST / STORE_DBNAME / STORE_USER / STORE_PW) defeats key-prefix and
	# value-shape matching by construction, because no single key or value looks
	# like a connection string. Two weak signals are checked — an environment
	# value that is a bare hostname naming another service in this project, and
	# several keys whose suffixes together read as connection parameters. Do not
	# treat this as the control: the key allowlist is what actually contains the
	# blast radius, since a companion has no reason to hold connection settings
	# for anything at all.
	check "$json" "$label" \
		"[.services | keys[] | select(startswith(\"vondel-\") | not)] as \$svcnames |
			($s.environment // {}) | [.[]] | all(
				(tostring) as \$v |
				((\$v | test(\"^[A-Za-z0-9._-]+$\")) and (\$svcnames | index(\$v))) | not
			)" \
		"$svc environment must not name another service as a bare hostname (possible split connection settings)"
	check "$json" "$label" \
		"($s.environment // {}) | keys
			| map(select(ascii_upcase | test(\"(_HOST|_HOSTNAME|_DBNAME|_DATABASE|_USER|_USERNAME|_PW|_PASS|_PORT|_DSN|_SCHEMA)$\")))
			| length < 2" \
		"$svc environment keys together look like split connection settings"

	check "$json" "$label" \
		"($s.secrets // []) | length == 1 and .[0].target == \"vondel_compat_enrollment\"" \
		"$svc must mount exactly one enrollment secret at /run/secrets/vondel_compat_enrollment"
	check "$json" "$label" \
		"(($s.secrets // []) | .[0].source // \"\") | length > 0" \
		"$svc enrollment secret must name a source"
	check "$json" "$label" \
		"($s.secrets[0].source // \"\") as \$src |
			(.secrets[\$src] // {}) | has(\"file\") and (has(\"environment\") | not)" \
		"$svc enrollment secret must be file-backed, never environment-backed"
}

# Invariants for the Vondel service when companions are present.
#   verify_vondel <json> <label>
verify_vondel() {
	local json=$1 label=$2
	check "$json" "$label" \
		'.services.silo.networks | has("vondel-compat") and has("default")' \
		"the Vondel service must join vondel-compat while keeping its default network"
	check "$json" "$label" \
		'[.services.silo.secrets // [] | .[] | .target] | index("vondel_compat_enrollment") == null' \
		"the Vondel service must not receive the companion enrollment secret"
}

echo "verify-compat-compose: scanning rendered configurations in $compose_dir"

# Combination A: base file alone must define no companion services.
json=$(render "$base_file")
check "$json" "base" \
	'.services | keys | all(test("^vondel-(audiobookshelf|jellyfin)$") | not)' \
	"the base compose file must not define companion services; activation is overlay-only"

# Combination B: base + audiobookshelf.
json=$(render "$base_file" "$abs_file")
verify_companion "$json" "audiobookshelf" "vondel-audiobookshelf" "no"
verify_vondel "$json" "audiobookshelf"

# Combination C: base + jellyfin.
json=$(render "$base_file" "$jf_file")
verify_companion "$json" "jellyfin" "vondel-jellyfin" "no"
verify_vondel "$json" "jellyfin"

# Combination D: base + both companions.
json=$(render "$base_file" "$abs_file" "$jf_file")
verify_companion "$json" "both" "vondel-audiobookshelf" "no"
verify_companion "$json" "both" "vondel-jellyfin" "no"
verify_vondel "$json" "both"

# Combination E: base + both companions + diagnostics override.
json=$(render "$base_file" "$abs_file" "$jf_file" "$diag_file")
verify_companion "$json" "diagnostics" "vondel-audiobookshelf" "yes"
verify_companion "$json" "diagnostics" "vondel-jellyfin" "yes"
verify_vondel "$json" "diagnostics"

if [[ "$failures" -ne 0 ]]; then
	printf 'verify-compat-compose: %d check(s) failed\n' "$failures" >&2
	exit 1
fi

echo "verify-compat-compose: all structural checks passed"
