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
# So the primary controls are two allowlists, and everything outside them fails
# by construction, named in the error:
#
#   1. SERVICE SET — a combination may render the base stack plus exactly the
#      companions its overlays activate. A key allowlist means nothing if a
#      tampered overlay can simply append a fourth, privileged service.
#   2. SERVICE KEYS — a companion service may declare only allowlisted keys,
#      and every non-base service is held to the same list.
#
# Adding to either list is a deliberate, reviewable act.
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
#   - secrets    — exactly one enrollment secret, mounted at
#                  /run/secrets/vondel_compat_enrollment and backed by the
#                  committed ./.secrets/compat file, not an arbitrary host path;
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
# Normalize through `cd ... && pwd` so the value compared against the paths
# `docker compose config` emits is canonical and absolute. Comparing an
# unnormalized argument ("." or a path with a doubled slash) against Compose's
# normalized output made every value check that names a path fail on an
# untampered tree — and, because the test harness treated any non-zero exit as
# detection, made the whole tamper suite vacuous.
if ! compose_dir=$(cd "${1:-$script_dir/..}" 2>/dev/null && pwd); then
	printf 'verify-compat-compose: not a directory: %s\n' "${1:-$script_dir/..}" >&2
	exit 2
fi
if [[ "$compose_dir" != /* ]]; then
	printf 'verify-compat-compose: compose dir did not resolve to an absolute path: %s\n' "$compose_dir" >&2
	exit 2
fi

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
	# --profile '*' renders profile-gated services too, so a service cannot be
	# hidden from the scan behind a profile and switched on at deploy time.
	# COMPOSE_PROFILES is cleared for the same determinism reason as
	# --env-file /dev/null: the scan judges the committed files, not the
	# operator's shell.
	(
		cd "$compose_dir" &&
			MEDIA_ROOT="/dev/null" SECRET_KEY="verify-only" COMPOSE_PROFILES="" \
				docker compose --project-name "$project_name" \
				--env-file /dev/null --profile '*' "${args[@]}" config --format json
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

# verify_compat_network_membership <rendered-json> <combo-label> <expected-json>
# Withholding a DSN is not a boundary; REACHABILITY is. Nothing stopped an
# overlay from attaching postgres and redis to the companion network, which
# would hand a companion Vondel's database (compose defaults silo/silo) and an
# unauthenticated Redis while every environment check still passed. The set of
# services on vondel-compat is therefore pinned: Vondel plus this combination's
# companions, nobody else.
verify_compat_network_membership() {
	local json=$1 label=$2 expected=$3
	local actual
	actual=$(jq -c '[.services | to_entries[]
		| select((.value.networks // {}) | has("vondel-compat")) | .key] | sort' <<<"$json")
	if [[ "$actual" != "$(jq -c 'sort' <<<"$expected")" ]]; then
		fail "[$label] services attached to vondel-compat must be exactly $(jq -c 'sort' <<<"$expected"), found $actual"
	fi
}

# verify_overlay_delta <base-json> <combo-json> <combo-label> <expected-new-json>
# The general form of the same finding. Rather than exempting base services from
# checks — which is what let a privileged service named `meilisearch`, and a
# tampered `silo`, through — the base stack is rendered on its own and diffed
# against the combination. An overlay may add its companion services and touch
# exactly one key on silo (networks, to join the companion network). Any other
# service-level change is a finding, whatever service or key it lands on.
verify_overlay_delta() {
	local base_json=$1 combo_json=$2 label=$3 expected_new=$4
	local violations
	violations=$(jq -nr \
		--argjson base "$base_json" \
		--argjson combo "$combo_json" \
		--argjson allowed_new "$expected_new" '
		def changed_keys($a; $b):
			[(($a | keys) + ($b | keys)) | unique | .[] | select($a[.] != $b[.])];
		($base.services | keys) as $bk |
		($combo.services | keys) as $ck |
		[
			(($ck - $bk) | select(sort != ($allowed_new | sort))
				| "overlay added unexpected service(s): \(. - $allowed_new)"),
			(($bk - $ck) | select(length > 0)
				| "overlay removed base service(s): \(.)"),
			(
				($bk - ($bk - $ck))[]
				| . as $s
				| (if $s == "silo" then ["networks"] else [] end) as $allowed
				| (changed_keys($base.services[$s]; $combo.services[$s]) - $allowed) as $changed
				| select($changed | length > 0)
				| "overlay modified base service \($s): \($changed)"
			)
		] | .[]' <<<'{}')
	if [[ -n "$violations" ]]; then
		while IFS= read -r line; do
			[[ -z "$line" ]] && continue
			fail "[$label] $line"
		done <<<"$violations"
	fi
}

# verify_service_set <rendered-json> <combo-label> <expected-services-json>
# The other half of default-deny: no service may exist that this combination
# did not ask for. Also sweeps every non-base service through the companion key
# allowlist, so an unexpected service is named for what it is AND for what it
# declares.
verify_service_set() {
	local json=$1 label=$2 expected=$3
	local extra svc

	extra=$(jq -r --argjson expected "$expected" \
		'.services | keys | map(select(. as $k | $expected | index($k) | not)) | join(", ")' \
		<<<"$json")
	if [[ -n "$extra" ]]; then
		fail "[$label] unexpected service(s) in the rendered configuration: $extra"
	fi

	while IFS= read -r svc; do
		[[ -z "$svc" ]] && continue
		check_service_key_allowlist "$json" "$label" "$svc"
	done < <(jq -r --argjson base "$base_services_json" \
		'.services | keys[] | select(. as $k | $base | index($k) | not)' <<<"$json")
}

# ---------------------------------------------------------------------------
# THE KEY ALLOWLIST. A companion service may declare these keys and nothing
# else. Every entry is a key the committed overlays actually render, and every
# one of them is value-checked below.
#
#   image         the private companion image (value-checked: registry)
#   environment   companion configuration (value-checked: keys + values)
#   networks      private network membership (value-checked: exact set)
#   volumes       disposable state (value-checked: local named volumes)
#   secrets       enrollment token (value-checked: one, file-backed, exact file)
#   security_opt  hardening (value-checked: no-new-privileges:true only)
#   ports         denied outright except loopback diagnostics (value-checked)
#   restart       restart policy; container lifecycle only, no value to choose
#
# NOTHING is allowlisted speculatively. An earlier revision admitted a set of
# keys reasoned to be harmless — healthcheck ("runs inside the container"),
# labels ("metadata only"), depends_on, read_only, init, user,
# stop_grace_period, pull_policy — none of which any committed overlay used.
# Review holed two of those justifications immediately:
#
#   - healthcheck.test is arbitrary command execution inside the companion,
#     the same capability `command`/`entrypoint` are denied for. A CMD-SHELL
#     probe can read /run/secrets/vondel_compat_enrollment and post it to a
#     service it shares a network with.
#   - labels are inert only if nothing on the host consumes them. A Traefik
#     router label republishes an internal companion on public ingress,
#     defeating the ports rule this whole design leans on; ofelia labels
#     execute jobs.
#
# The lesson generalizes, so the rule is now mechanical: a key earns its place
# by being used by a committed contract AND having its values pinned. A
# companion that later needs a healthcheck gets one back the way security_opt
# is handled — pinned to an exact argv, reviewed at that time.
#
# Everything else is denied by this list rather than by any named rule: build,
# privileged, cap_add, devices, device_cgroup_rules, volumes_from, tmpfs, pid,
# ipc, uts, userns_mode, cgroup, cgroup_parent, network_mode, sysctls,
# group_add, extra_hosts, command, entrypoint, runtime, dns, storage_opt,
# healthcheck, labels, and every key Compose has not shipped yet.
companion_service_keys_json='[
	"image", "environment", "networks", "volumes", "secrets", "security_opt",
	"ports", "restart"
]'

# ---------------------------------------------------------------------------
# THE SERVICE ALLOWLIST. A per-service key allowlist is worth nothing if a new
# service is free, so the rendered SERVICE SET is constrained too: a scanned
# combination may contain the base stack plus exactly the companions its
# overlays activate. A tampered overlay that appends a privileged sidekick with
# the Docker socket and / bind-mounted is rejected because that service is not
# in the expected set — and every non-base service is additionally run through
# the companion key allowlist, so it is named twice over.
base_services_json='["postgres", "redis", "silo", "meilisearch"]'

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
	# File-backed is not enough: WHICH file matters. An unpinned `file:` can
	# name /etc/passwd (or a second top-level secret backed by /etc/hosts can be
	# swapped in through `source:`), mounting an arbitrary host file into the
	# companion at the enrollment path — the no-host-paths rule defeated through
	# an allowed key. The path is therefore pinned to the committed location,
	# resolved against the compose directory the way Compose resolves it.
	check "$json" "$label" \
		"($s.secrets[0].source // \"\") as \$src |
			(.secrets[\$src].file // \"\") == \"$compose_dir/.secrets/compat/$svc-enrollment.token\"" \
		"$svc enrollment secret must be the committed ./.secrets/compat/$svc-enrollment.token file"
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

# Each combination declares the exact service set it may render: the base stack
# plus the companions its overlays activate, and nothing else.
base_only='["postgres", "redis", "silo", "meilisearch"]'
with_abs='["postgres", "redis", "silo", "meilisearch", "vondel-audiobookshelf"]'
with_jf='["postgres", "redis", "silo", "meilisearch", "vondel-jellyfin"]'
with_both='["postgres", "redis", "silo", "meilisearch", "vondel-audiobookshelf", "vondel-jellyfin"]'

# Combination A: base file alone must define no companion services. This render
# is also the baseline every overlay combination is diffed against.
base_json=$(render "$base_file")
verify_service_set "$base_json" "base" "$base_only"
check "$base_json" "base" \
	'.services | keys | all(test("^vondel-(audiobookshelf|jellyfin)$") | not)' \
	"the base compose file must not define companion services; activation is overlay-only"

# Combination B: base + audiobookshelf.
json=$(render "$base_file" "$abs_file")
verify_service_set "$json" "audiobookshelf" "$with_abs"
verify_overlay_delta "$base_json" "$json" "audiobookshelf" '["vondel-audiobookshelf"]'
verify_compat_network_membership "$json" "audiobookshelf" '["silo", "vondel-audiobookshelf"]'
verify_companion "$json" "audiobookshelf" "vondel-audiobookshelf" "no"
verify_vondel "$json" "audiobookshelf"

# Combination C: base + jellyfin.
json=$(render "$base_file" "$jf_file")
verify_service_set "$json" "jellyfin" "$with_jf"
verify_overlay_delta "$base_json" "$json" "jellyfin" '["vondel-jellyfin"]'
verify_compat_network_membership "$json" "jellyfin" '["silo", "vondel-jellyfin"]'
verify_companion "$json" "jellyfin" "vondel-jellyfin" "no"
verify_vondel "$json" "jellyfin"

# Combination D: base + both companions.
json=$(render "$base_file" "$abs_file" "$jf_file")
verify_service_set "$json" "both" "$with_both"
verify_overlay_delta "$base_json" "$json" "both" '["vondel-audiobookshelf", "vondel-jellyfin"]'
verify_compat_network_membership "$json" "both" '["silo", "vondel-audiobookshelf", "vondel-jellyfin"]'
verify_companion "$json" "both" "vondel-audiobookshelf" "no"
verify_companion "$json" "both" "vondel-jellyfin" "no"
verify_vondel "$json" "both"

# Combination E: base + both companions + diagnostics override. The diagnostics
# file legitimately changes the companions (loopback ports, default network), so
# the delta check compares it against the two-companion combination instead of
# the base, holding the base services to the same no-change rule.
diag_json=$(render "$base_file" "$abs_file" "$jf_file" "$diag_file")
verify_service_set "$diag_json" "diagnostics" "$with_both"
verify_overlay_delta "$base_json" "$diag_json" "diagnostics" '["vondel-audiobookshelf", "vondel-jellyfin"]'
verify_compat_network_membership "$diag_json" "diagnostics" '["silo", "vondel-audiobookshelf", "vondel-jellyfin"]'
verify_companion "$diag_json" "diagnostics" "vondel-audiobookshelf" "yes"
verify_companion "$diag_json" "diagnostics" "vondel-jellyfin" "yes"
verify_vondel "$diag_json" "diagnostics"

if [[ "$failures" -ne 0 ]]; then
	printf 'verify-compat-compose: %d check(s) failed\n' "$failures" >&2
	exit 1
fi

echo "verify-compat-compose: all structural checks passed"
