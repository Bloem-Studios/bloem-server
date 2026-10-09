#!/usr/bin/env bash
# Silo ⇄ Bloem backend switching check.
#
# Builds a database the way production gets one (upstream Silo installs and
# writes first), hands it to Bloem, enables Silo switching, and then switches
# Bloem → Silo → Bloem. At each switch the next backend must read what the
# previous one wrote and write something the next one has to read. Only one
# backend runs at a time, each with its own Redis, flushed before it starts.
#
# Usage: SILO_IMAGE=ghcr.io/silo-server/silo-server@sha256:... \
#        BLOEM_IMAGE=bloem-server:local scripts/bloem/silo-switch-check.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
compose_file="$root/scripts/bloem/silo-switch-check.compose.yaml"
: "${SILO_IMAGE:?set SILO_IMAGE to the upstream Silo image}"
: "${BLOEM_IMAGE:?set BLOEM_IMAGE to the Bloem image under test}"
export SILO_IMAGE BLOEM_IMAGE
export SILO_PORT="${SILO_PORT:-18090}" BLOEM_PORT="${BLOEM_PORT:-18091}"
SECRET_KEY="$(openssl rand -base64 48)"
export SECRET_KEY

work="$(mktemp -d)"
state="$work/state.json"
driver="$work/bloem-silo-switch-check"
compose() { docker compose -f "$compose_file" --profile silo --profile bloem "$@"; }
cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		echo "--- last silo log lines"; compose logs --tail 40 silo 2>/dev/null || true
		echo "--- last bloem log lines"; compose logs --tail 40 bloem 2>/dev/null || true
		echo "--- database errors"; compose logs postgres 2>/dev/null | grep -A2 "ERROR" | tail -30 || true
		if [ "${KEEP_ON_FAILURE:-0}" = 1 ]; then
			echo "KEEP_ON_FAILURE=1: leaving the stack up; state in $work; tear down with:"
			echo "  docker compose -f $compose_file --profile silo --profile bloem down -v"
			exit "$status"
		fi
	fi
	compose down -v --remove-orphans >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

(cd "$root" && go build -o "$driver" ./cmd/bloem-silo-switch-check)

running() { compose ps --status running --services 2>/dev/null | grep -qx "$1"; }

start_backend() {
	local backend="$1" port="$2" other
	other=silo
	[ "$backend" = silo ] && other=bloem
	if running "$other"; then
		echo "FAIL $other is still running; refusing to start $backend" >&2
		exit 1
	fi
	compose exec -T "redis-$backend" redis-cli FLUSHALL >/dev/null
	compose up -d "$backend" >/dev/null
	for _ in $(seq 1 180); do
		if [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/v1/ready")" = 200 ]; then
			echo "$backend ready"
			return 0
		fi
		sleep 2
	done
	echo "FAIL $backend did not become ready" >&2
	exit 1
}

stop_backend() { compose stop "$1" >/dev/null; }

step() { "$driver" "$1" --base "http://127.0.0.1:$2" --state "$state"; }

compose up -d postgres redis-silo redis-bloem >/dev/null

# A Silo-origin database: Silo installs and creates the first accounts.
start_backend silo "$SILO_PORT"
step silo-setup "$SILO_PORT"
stop_backend silo

# Bloem migrates it, then the operator enables Silo switching.
start_backend bloem "$BLOEM_PORT"
compose exec -T bloem silo membership-policy enable-silo-switching --env /dev/null
stop_backend bloem

start_backend bloem "$BLOEM_PORT"
step bloem-writes "$BLOEM_PORT"
stop_backend bloem

start_backend silo "$SILO_PORT"
step silo-verify-and-write "$SILO_PORT"
stop_backend silo

start_backend bloem "$BLOEM_PORT"
step bloem-verify "$BLOEM_PORT"
stop_backend bloem

echo "SILO SWITCH CHECK: PASS"
