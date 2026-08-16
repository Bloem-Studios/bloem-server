#!/usr/bin/env bash
# Structural security scan for the private compatibility-companion Compose contracts.
#
# Renders every supported Compose file combination with `docker compose config`
# and enforces the deployment rulings from
# docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md.
#
# THE SCAN IS DEFAULT-DENY AT THE SERVICE LEVEL AND AT THE TOP LEVEL.
#
# Enumerating forbidden Compose keys does not converge: every Compose key — and
# every key a future Compose release adds — is permitted until someone thinks
# to forbid it, and each one is a candidate route to the host. Successive
# reviews of this scan produced exactly that treadmill (privileged, then
# cap_add, then devices, then pid/ipc, then volumes_from, then
# device_cgroup_rules, then uts/sysctls/tmpfs/extra_hosts...).
#
# So the primary controls are allowlists, and everything outside them fails
# by construction, named in the error:
#
#   1. SERVICE SET — a combination may render the base stack plus exactly the
#      companions its overlays activate. A key allowlist means nothing if a
#      tampered overlay can simply append a fourth, privileged service.
#   2. SERVICE KEYS — a companion service may declare only allowlisted keys,
#      and every non-base service is held to the same list. Base services are
#      held to their own, wider list (see BASE SERVICES below).
#   3. TOP LEVEL — the rendered `networks`, `volumes`, `secrets` and `configs`
#      maps are diffed against the base render exactly the way `services` is,
#      so an overlay may add only its own companion entries and may not touch a
#      base one. Top-level network entries additionally have a key allowlist of
#      their own.
#
# Adding to any of these lists is a deliberate, reviewable act.
#
# The top-level rule is not theoretical. While only `.services` was diffed, an
# overlay could redefine the top-level `default` network as
# `{external: true, name: attacker_shared_bridge}` — directly, or through an
# `include:` directive — and move postgres, redis, silo and meilisearch onto an
# attacker-named network with ZERO service-level change; and it could give
# vondel-compat an arbitrary driver/driver_opts/ipam. Every service-level check
# passed on all three.
#
# Permitting a key is not permitting any value, so each allowed key that can
# carry risk keeps a value-level check:
#
#   - image      — must be a private ghcr.io/vondel-media image, never a build;
#   - ports      — none at all, except the diagnostics override, which may bind
#                  127.0.0.1 only;
#   - networks   — exactly the internal vondel-compat network (plus the default
#                  bridge in the diagnostics override, which publishing needs),
#                  and for silo exactly {default, vondel-compat} with no
#                  per-network options;
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
# The Vondel service is checked separately, because it legitimately owns media
# mounts and the database: it must not gain the companion enrollment secret,
# and its network membership is pinned to exactly {default, vondel-compat} with
# no options on vondel-compat. Exempting silo.networks wholesale let an overlay
# attach silo to an `external: true` network belonging to another stack, and let
# silo claim aliases such as `postgres` or `redis` on vondel-compat — which is
# service impersonation on the very network the companions trust.
#
# BASE SERVICES. docker-compose.yml is not trusted implicitly. Its services are
# swept through their own key allowlist (base_service_keys_json), because the
# overlay delta uses the base render as its own baseline: a `privileged: true`
# plus `pid: host` added to silo IN THE BASE FILE is invisible to a diff of base
# against base, and passed the entire scan before that sweep existed. What the
# base allowlist does NOT do is pin base values — base services legitimately
# hold media bind mounts, published ports, a DATABASE_URL and a SECRET_KEY, and
# there is no small set of correct values for those. So the base is covered at
# the key level only, and that is a boundary, not a closed class: a base-service
# change that stays within the allowlisted keys still needs human review.
#
# ENV INTERPOLATION. The scan renders each combination twice when the compose
# directory contains a .env: once with --env-file /dev/null, judging the values
# the repository commits, and once with the operator's .env, judging the values
# that deployment will actually use. Without the second pass an operator .env
# setting VONDEL_JELLYFIN_ENROLLMENT_FILE=/etc/passwd reproduced, invisibly, the
# exact tamper the committed-defaults pass catches. Failure messages therefore
# say a value must "resolve to" something, not that it is committed as such; the
# combination label carries a ".env" suffix on the second pass.
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

# The env file the current pass renders with. Pass 1 uses /dev/null so the
# committed defaults are what gets judged; pass 2 (only when the compose
# directory has a .env) uses the operator's file so the values that will
# actually be deployed are judged too.
render_env_file="/dev/null"

# Render a combination of compose files to canonical JSON.
# The two required base variables are supplied with inert values; they are set
# in the environment, which Compose gives precedence over .env, so the operator
# pass cannot redirect the media root either. Rendering never touches Docker
# daemon state.
render() {
	local args=()
	local f
	for f in "$@"; do
		args+=(-f "$f")
	done
	# --profile '*' renders profile-gated services too, so a service cannot be
	# hidden from the scan behind a profile and switched on at deploy time.
	# COMPOSE_PROFILES is cleared for determinism: the scan judges the committed
	# files and, on the second pass, the operator's .env — never the operator's
	# shell.
	(
		cd "$compose_dir" &&
			MEDIA_ROOT="/dev/null" SECRET_KEY="verify-only" COMPOSE_PROFILES="" \
				docker compose --project-name "$project_name" \
				--env-file "$render_env_file" --profile '*' "${args[@]}" config --format json
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

# check_key_allowlist <rendered-json> <combo-label> <service> <allowed-json> <list-name>
# The default-deny control: fail on any declared key outside the given allowlist,
# and name the offending keys so the operator learns what to justify or remove.
check_key_allowlist() {
	local json=$1 label=$2 svc=$3 allowed=$4 list_name=$5
	local unknown
	unknown=$(jq -r \
		--arg svc "$svc" \
		--argjson allowed "$allowed" \
		".services[\$svc] | to_entries
			| map(select($declared_filter))
			| map(.key)
			| map(select(. as \$k | \$allowed | index(\$k) | not))
			| join(\", \")" <<<"$json")
	if [[ -n "$unknown" ]]; then
		fail "[$label] $svc declares keys outside $list_name: $unknown"
	fi
}

# check_service_key_allowlist <rendered-json> <combo-label> <service>
# The companion form: every non-base service is held to the companion list.
check_service_key_allowlist() {
	check_key_allowlist "$1" "$2" "$3" "$companion_service_keys_json" \
		"the companion allowlist"
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
# against the combination. An overlay may add its companion entries and touch
# exactly one key on silo (networks, to join the companion network). Any other
# change is a finding, whatever it lands on.
#
# The diff covers `services`, `networks`, `volumes`, `secrets` and `configs`.
# Diffing only `services` left the entire top level default-allow, which an
# overlay could exploit with zero service-level change: redefining the top-level
# `default` network as external and renamed moves the whole base stack onto an
# attacker's network, and an `include:` directive does the same thing at one
# remove. Both are now "overlay modified base network default".
#
# <expected-new-json> is an object keyed by section, e.g.
#   {"services": ["vondel-jellyfin"], "networks": ["vondel-compat"],
#    "volumes": ["vondel-jellyfin-state"],
#    "secrets": ["vondel_jellyfin_enrollment"], "configs": []}
# Every section must be listed; a missing section is treated as "adds nothing".
verify_overlay_delta() {
	local base_json=$1 combo_json=$2 label=$3 expected_new=$4
	local violations
	violations=$(jq -nr \
		--argjson base "$base_json" \
		--argjson combo "$combo_json" \
		--argjson allowed_new "$expected_new" '
		def changed_keys($a; $b):
			[((($a // {}) | keys) + (($b // {}) | keys)) | unique | .[]
				| select(($a // {})[.] != ($b // {})[.])];
		def section($sec; $noun; $exempt):
			(($base[$sec] // {}) | keys) as $bk |
			(($combo[$sec] // {}) | keys) as $ck |
			(($allowed_new[$sec] // []) | sort) as $new |
			[
				(($ck - $bk) | select(sort != $new)
					| "overlay added unexpected \($noun)(s): \(. - $new)"),
				(($bk - $ck) | select(length > 0)
					| "overlay removed base \($noun)(s): \(.)"),
				(
					($bk - ($bk - $ck))[]
					| . as $k
					| ($exempt[$k] // []) as $allowed
					| (changed_keys($base[$sec][$k]; $combo[$sec][$k]) - $allowed) as $changed
					| select($changed | length > 0)
					| "overlay modified base \($noun) \($k): \($changed)"
				)
			];
		(
			section("services"; "service"; {"silo": ["networks"]})
			+ section("networks"; "network"; {})
			+ section("volumes"; "volume"; {})
			+ section("secrets"; "secret"; {})
			+ section("configs"; "config"; {})
		) | .[]' <<<'{}')
	if [[ -n "$violations" ]]; then
		while IFS= read -r line; do
			[[ -z "$line" ]] && continue
			fail "[$label] $line"
		done <<<"$violations"
	fi
}

# verify_top_level_networks <rendered-json> <combo-label> <expected-keys-json>
# The delta above pins base networks against the base render, but a network the
# overlay legitimately ADDS (vondel-compat) has nothing to be diffed against, so
# it gets the same treatment every companion service gets: a key allowlist plus
# a pinned name. `driver: macvlan` with `driver_opts.parent: eth0` on
# vondel-compat is a bridge onto the host LAN and passed every other check;
# `external: true` with a `name:` aliases somebody else's network under ours.
# Both are denied here by not being on the list, along with ipam, attachable,
# labels, enable_ipv6 and whatever Compose adds next.
verify_top_level_networks() {
	local json=$1 label=$2 expected=$3
	local actual unknown renamed

	actual=$(jq -c '(.networks // {}) | keys | sort' <<<"$json")
	if [[ "$actual" != "$(jq -c 'sort' <<<"$expected")" ]]; then
		fail "[$label] top-level networks must be exactly $(jq -c 'sort' <<<"$expected"), found $actual"
		return
	fi

	unknown=$(jq -r --argjson allowed "$network_entry_keys_json" \
		"(.networks // {}) | to_entries
			| map(.key as \$n | ((.value // {}) | to_entries
				| map(select($declared_filter))
				| map(select(.key as \$k | \$allowed | index(\$k) | not))
				| map(\"\(\$n).\(.key)\")))
			| flatten | join(\", \")" <<<"$json")
	if [[ -n "$unknown" ]]; then
		fail "[$label] top-level network keys outside the allowlist (external, driver, driver_opts, ipam and the rest are all denied here): $unknown"
	fi

	renamed=$(jq -r --arg prefix "${project_name}_" \
		'(.networks // {}) | to_entries
			| map(select((.value.name // "") != ($prefix + .key)))
			| map(.key) | join(", ")' <<<"$json")
	if [[ -n "$renamed" ]]; then
		fail "[$label] top-level network(s) must be locally defined by this project (no external: true, no explicit name aliasing another network): $renamed"
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

	# Base services get their own, wider list. Without this the base file was
	# trusted implicitly — the delta check uses the base render as its own
	# baseline, so `privileged: true` and `pid: host` added to silo IN THE BASE
	# FILE were invisible to every check in this script.
	while IFS= read -r svc; do
		[[ -z "$svc" ]] && continue
		check_key_allowlist "$json" "$label" "$svc" "$base_service_keys_json" \
			"the base service allowlist"
	done < <(jq -r --argjson base "$base_services_json" \
		'.services | keys[] | select(. as $k | $base | index($k))' <<<"$json")
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

# ---------------------------------------------------------------------------
# THE BASE SERVICE KEY ALLOWLIST. docker-compose.yml is not a trusted input:
# the overlay delta diffs the base render against itself, so anything tampered
# INTO the base file is the baseline rather than a finding. A base service is
# therefore held to this list — every key the committed base stack actually
# renders, and nothing more. `privileged`, `pid`, `cap_add`, `devices`,
# `network_mode`, `volumes_from`, `userns_mode` and their successors are absent,
# so a host escape planted in the base file is named the same way one planted in
# an overlay is.
#
# This is a key-level boundary only, and deliberately not a claim that base
# services are safe: their VALUES are not pinned, because base services
# legitimately hold media bind mounts, published host ports, a DATABASE_URL and
# the SECRET_KEY, and no small set of correct values exists for those. A change
# to a base service that stays inside these keys is invisible to this scan and
# still needs human review.
base_service_keys_json='[
	"image", "environment", "networks", "volumes", "ports", "restart",
	"command", "entrypoint", "depends_on", "healthcheck", "profiles", "shm_size"
]'

# ---------------------------------------------------------------------------
# THE TOP-LEVEL NETWORK KEY ALLOWLIST. A network the overlays add has no base
# counterpart to be diffed against, so it is default-denied the same way a
# service is. `name` is present because Compose always renders one (and it is
# value-checked: it must be the project-prefixed name Compose generates for a
# locally defined network); `internal` is the property the whole companion
# isolation story rests on. Everything else — external, driver, driver_opts,
# ipam, attachable, labels, enable_ipv6 — is denied.
network_entry_keys_json='["name", "internal"]'

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
		"$svc image must resolve to the private ghcr.io/vondel-media registry"

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
	#
	# "resolve to", not "is": the committed value is interpolated from
	# VONDEL_*_ENROLLMENT_FILE, so an operator .env can move it. That is exactly
	# why this scan makes a second pass over the operator's .env — a defaults-only
	# scan reported this check green while the deployment mounted /etc/passwd.
	check "$json" "$label" \
		"($s.secrets[0].source // \"\") as \$src |
			(.secrets[\$src].file // \"\") == \"$compose_dir/.secrets/compat/$svc-enrollment.token\"" \
		"$svc enrollment secret must resolve to the committed ./.secrets/compat/$svc-enrollment.token file"
}

# Invariants for the Vondel service when companions are present.
#   verify_vondel <json> <label>
#
# silo.networks is the one key the overlay delta lets an overlay touch, so it is
# pinned here rather than merely sampled. Checking only has("default") and
# has("vondel-compat") allowed BOTH halves of the key to be abused: an extra
# network entry (`lateral: {external: true, name: attacker_stack_default}`)
# bridged silo into another stack, and options on the vondel-compat entry let
# silo claim aliases such as `postgres` or `redis` on the network the companions
# resolve names over. Exact set, no options.
verify_vondel() {
	local json=$1 label=$2
	check "$json" "$label" \
		'(.services.silo.networks // {}) | keys | sort == ["default", "vondel-compat"]' \
		"the Vondel service must join exactly the default and vondel-compat networks"
	check "$json" "$label" \
		'((.services.silo.networks["vondel-compat"] // {}) == {})
			and ((.services.silo.networks["default"] // {}) == {})' \
		"the Vondel service must declare no network options (no aliases impersonating another service, no per-network overrides)"
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

# The top-level networks each combination may render.
nets_base='["default"]'
nets_compat='["default", "vondel-compat"]'

# The complete set of top-level entries each overlay combination may ADD to the
# base render. Everything not listed here — in any of the five sections — is a
# finding, and every base entry must survive unchanged.
delta_abs='{
	"services": ["vondel-audiobookshelf"],
	"networks": ["vondel-compat"],
	"volumes": ["vondel-audiobookshelf-state"],
	"secrets": ["vondel_audiobookshelf_enrollment"],
	"configs": []
}'
delta_jf='{
	"services": ["vondel-jellyfin"],
	"networks": ["vondel-compat"],
	"volumes": ["vondel-jellyfin-state"],
	"secrets": ["vondel_jellyfin_enrollment"],
	"configs": []
}'
delta_both='{
	"services": ["vondel-audiobookshelf", "vondel-jellyfin"],
	"networks": ["vondel-compat"],
	"volumes": ["vondel-audiobookshelf-state", "vondel-jellyfin-state"],
	"secrets": ["vondel_audiobookshelf_enrollment", "vondel_jellyfin_enrollment"],
	"configs": []
}'

# scan_combinations <label-suffix>
# Renders and checks every supported combination. Called once per env pass; the
# suffix names the pass in every failure message.
scan_combinations() {
	local sfx=$1
	local base_json json diag_json

	# Combination A: base file alone must define no companion services. This
	# render is also the baseline every overlay combination is diffed against.
	base_json=$(render "$base_file")
	verify_service_set "$base_json" "base$sfx" "$base_only"
	verify_top_level_networks "$base_json" "base$sfx" "$nets_base"
	check "$base_json" "base$sfx" \
		'.services | keys | all(test("^vondel-(audiobookshelf|jellyfin)$") | not)' \
		"the base compose file must not define companion services; activation is overlay-only"

	# Combination B: base + audiobookshelf. KEEP THIS COMBINATION. It is the only
	# one that can catch `internal: true` turned to `false` on vondel-compat in
	# the audiobookshelf overlay: both overlays define that network, so in every
	# combination that also loads the jellyfin overlay the later definition wins
	# and silently restores internal: true. A "simplification" that drops the
	# single-overlay combinations because D covers them loses that detection.
	json=$(render "$base_file" "$abs_file")
	verify_service_set "$json" "audiobookshelf$sfx" "$with_abs"
	verify_top_level_networks "$json" "audiobookshelf$sfx" "$nets_compat"
	verify_overlay_delta "$base_json" "$json" "audiobookshelf$sfx" "$delta_abs"
	verify_compat_network_membership "$json" "audiobookshelf$sfx" '["silo", "vondel-audiobookshelf"]'
	verify_companion "$json" "audiobookshelf$sfx" "vondel-audiobookshelf" "no"
	verify_vondel "$json" "audiobookshelf$sfx"

	# Combination C: base + jellyfin. Symmetrically, the only combination that can
	# catch the same tamper in the jellyfin overlay.
	json=$(render "$base_file" "$jf_file")
	verify_service_set "$json" "jellyfin$sfx" "$with_jf"
	verify_top_level_networks "$json" "jellyfin$sfx" "$nets_compat"
	verify_overlay_delta "$base_json" "$json" "jellyfin$sfx" "$delta_jf"
	verify_compat_network_membership "$json" "jellyfin$sfx" '["silo", "vondel-jellyfin"]'
	verify_companion "$json" "jellyfin$sfx" "vondel-jellyfin" "no"
	verify_vondel "$json" "jellyfin$sfx"

	# Combination D: base + both companions.
	json=$(render "$base_file" "$abs_file" "$jf_file")
	verify_service_set "$json" "both$sfx" "$with_both"
	verify_top_level_networks "$json" "both$sfx" "$nets_compat"
	verify_overlay_delta "$base_json" "$json" "both$sfx" "$delta_both"
	verify_compat_network_membership "$json" "both$sfx" '["silo", "vondel-audiobookshelf", "vondel-jellyfin"]'
	verify_companion "$json" "both$sfx" "vondel-audiobookshelf" "no"
	verify_companion "$json" "both$sfx" "vondel-jellyfin" "no"
	verify_vondel "$json" "both$sfx"

	# Combination E: base + both companions + diagnostics override. The delta is
	# taken against the BASE render, like every other combination: the diagnostics
	# file only ever touches the two companion services, which are new relative to
	# the base and so are judged by verify_companion in diagnostics mode rather
	# than by the delta. Diffing against the two-companion combination instead
	# would flag the loopback ports and default-network attachment this file
	# exists to add, while gaining nothing — the base services are pinned to the
	# base render either way.
	diag_json=$(render "$base_file" "$abs_file" "$jf_file" "$diag_file")
	verify_service_set "$diag_json" "diagnostics$sfx" "$with_both"
	verify_top_level_networks "$diag_json" "diagnostics$sfx" "$nets_compat"
	verify_overlay_delta "$base_json" "$diag_json" "diagnostics$sfx" "$delta_both"
	verify_compat_network_membership "$diag_json" "diagnostics$sfx" '["silo", "vondel-audiobookshelf", "vondel-jellyfin"]'
	verify_companion "$diag_json" "diagnostics$sfx" "vondel-audiobookshelf" "yes"
	verify_companion "$diag_json" "diagnostics$sfx" "vondel-jellyfin" "yes"
	verify_vondel "$diag_json" "diagnostics$sfx"
}

# Pass 1: committed defaults only.
render_env_file="/dev/null"
scan_combinations ""

# Pass 2: the operator's .env, when there is one. Every value in these contracts
# that matters is interpolated (image, enrollment file, diagnostic port), so a
# defaults-only scan judges a configuration nobody deploys.
if [[ -f "$compose_dir/.env" ]]; then
	echo "verify-compat-compose: re-scanning with the operator .env"
	render_env_file="$compose_dir/.env"
	scan_combinations " .env"
fi

if [[ "$failures" -ne 0 ]]; then
	printf 'verify-compat-compose: %d check(s) failed\n' "$failures" >&2
	exit 1
fi

echo "verify-compat-compose: all structural checks passed"
