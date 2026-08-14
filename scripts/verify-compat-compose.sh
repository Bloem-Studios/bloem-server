#!/usr/bin/env bash
# Structural security scan for the private compatibility-companion Compose contracts.
#
# Renders every supported Compose file combination with `docker compose config`
# and enforces the deployment rulings from
# docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md:
#
#   - companions publish no host ports; the diagnostics override is the sole
#     exception and may bind loopback (127.0.0.1) only;
#   - companions receive no Vondel database, Redis, secret-key, provider, or
#     tuner configuration through the environment, and no credential-shaped
#     environment values at all;
#   - companions mount only named volumes for disposable protocol state — no
#     bind mounts, no media, no Docker socket;
#   - companions run unprivileged, without added capabilities, are never built
#     locally, and default to the private ghcr.io/vondel-media images;
#   - each companion reads exactly one file-backed enrollment secret mounted at
#     /run/secrets/vondel_compat_enrollment, and the Vondel service never
#     receives that secret;
#   - companions attach only to the internal vondel-compat network (plus the
#     default bridge solely in the diagnostics override, which port publishing
#     requires).
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
				docker compose --project-name vondel-compat-verify \
				--env-file /dev/null "${args[@]}" config --format json
	)
}

# check <rendered-json> <combo-label> <jq-filter> <message>
# The jq filter must evaluate to true for the check to pass.
check() {
	local json=$1 label=$2 filter=$3 message=$4
	local ok
	if ! ok=$(jq -e --arg forbidden "$forbidden_env_key_re" "$filter" <<<"$json" 2>&1); then
		if [[ "$ok" == "false" || "$ok" == "null" ]]; then
			fail "[$label] $message"
		else
			fail "[$label] $message (jq error: $ok)"
		fi
	fi
}

# Environment keys a companion must never receive: Vondel's own service
# configuration, anything provider/tuner/signing shaped, and anything
# credential-shaped (secrets travel as files, never as environment values).
# shellcheck disable=SC2016  # single-quoted on purpose: this is a jq regex, not shell
forbidden_env_key_re='^(DATABASE_URL|REDIS_URL|SECRET_KEY|MEILI_MASTER_KEY)$|^(POSTGRES_|REDIS_|MEILI_)|TUNER|PROVIDER|SIGNING|PASSWORD|TOKEN|SECRET|API_KEY|CREDENTIAL'

# Shared invariants for one companion service in one rendered combination.
#   verify_companion <json> <label> <service> <diagnostics: yes|no>
verify_companion() {
	local json=$1 label=$2 svc=$3 diagnostics=$4
	local s=".services[\"$svc\"]"

	check "$json" "$label" "$s != null" "service $svc is missing from the rendered configuration"
	if ! jq -e "$s != null" <<<"$json" >/dev/null 2>&1; then
		return
	fi

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

	check "$json" "$label" \
		"$s.privileged != true" \
		"$svc must not run privileged"
	check "$json" "$label" \
		"($s.cap_add // []) | length == 0" \
		"$svc must not add capabilities"
	check "$json" "$label" \
		"$s.network_mode == null" \
		"$svc must not set network_mode"
	check "$json" "$label" \
		"$s | has(\"build\") | not" \
		"$svc must be pulled from the private registry, never built here"
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

	check "$json" "$label" \
		"($s.environment // {}) | keys | all(test(\$forbidden; \"\") | not)" \
		"$svc must not receive Vondel database/Redis/signing/provider/tuner or credential-shaped environment keys"
	check "$json" "$label" \
		"($s.environment // {}) | [.[]] | all(
			(tostring | test(\"(postgres(ql)?|redis|rediss)://\")) | not
		)" \
		"$svc environment must not carry database or Redis URLs"

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
