#!/usr/bin/env bash
# Silo ⇄ Bloem backend switching check for an install on Silo's all-in-one
# image, whose PostgreSQL and Redis live inside the container under /config.
#
# Silo's all-in-one image creates and fills a /config volume. Bloem, wrapped in
# the same all-in-one layer (omnibus/Dockerfile on top of BLOEM_IMAGE), takes
# that volume over, enables Silo switching, and then the volume switches
# Bloem → Silo → Bloem. At each switch the next backend must read what the
# previous one wrote and write something the next one has to read. Only one
# container runs at a time; the bundled Redis data is deleted between them.
#
# Usage: SILO_IMAGE=ghcr.io/silo-server/silo-server@sha256:... \
#        BLOEM_IMAGE=bloem-server:local scripts/bloem/silo-switch-check-all-in-one.sh
# SILO_IMAGE is Silo's all-in-one image (a plain commit tag, not <sha>-base).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${SILO_IMAGE:?set SILO_IMAGE to the Silo all-in-one image}"
: "${BLOEM_IMAGE:?set BLOEM_IMAGE to the Bloem image under test}"
port="${SWITCH_PORT:-18095}"
bloem_aio="bloem-silo-switch-check:all-in-one"
volume="silo-switch-check-all-in-one-$$"
SECRET_KEY="$(openssl rand -base64 48)"
export SECRET_KEY

work="$(mktemp -d)"
state="$work/state.json"
driver="$work/bloem-silo-switch-check"
cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		for c in silo bloem; do
			echo "--- last $c log lines"; docker logs --tail 40 "$volume-$c" 2>&1 || true
		done
	fi
	docker rm -f "$volume-silo" "$volume-bloem" >/dev/null 2>&1 || true
	docker volume rm "$volume" >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

(cd "$root" && go build -o "$driver" ./cmd/bloem-silo-switch-check)
docker build -q -f "$root/omnibus/Dockerfile" --build-arg "SILO_IMAGE=$BLOEM_IMAGE" -t "$bloem_aio" "$root/omnibus" >/dev/null
docker volume create "$volume" >/dev/null

start_backend() {
	local backend="$1" image="$2"
	if [ -n "$(docker ps -q --filter "name=^$volume-")" ]; then
		echo "FAIL a backend is still running; refusing to start $backend" >&2
		exit 1
	fi
	docker run -d --name "$volume-$backend" -e SECRET_KEY -e ENABLE_REDIS=true \
		-v "$volume:/config" -p "127.0.0.1:$port:8080" "$image" >/dev/null
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

# Stop a backend and delete the bundled Redis data, so the next backend starts
# with an empty cache, as the separate-database check flushes its Redis.
stop_backend() {
	docker stop -t 90 "$volume-$1" >/dev/null
	docker rm "$volume-$1" >/dev/null
	docker run --rm -v "$volume:/config" --entrypoint sh "$bloem_aio" -c 'rm -rf /config/redis/*'
}

step() { "$driver" "$1" --base "http://127.0.0.1:$port" --state "$state"; }

# Bloem's CLI inside the all-in-one container: the container environment
# (DATABASE_URL among it) is root's, and the bundled PostgreSQL admits the
# silo account by peer authentication.
bloem_cli() {
	docker exec -e PATH=/command:/usr/local/bin:/usr/bin:/bin "$volume-bloem" \
		with-contenv s6-setuidgid silo sh -c 'cd /config/silo && HOME=/config/silo exec silo "$@"' silo "$@"
}

# A Silo all-in-one install: Silo creates its database and the first accounts.
start_backend silo "$SILO_IMAGE"
step silo-setup
stop_backend silo

# Bloem takes the volume over and migrates it; the operator enables switching.
start_backend bloem "$bloem_aio"
bloem_cli silo-switching enable --env /dev/null
stop_backend bloem

start_backend bloem "$bloem_aio"
step bloem-writes
stop_backend bloem

start_backend silo "$SILO_IMAGE"
step silo-verify-and-write
stop_backend silo

start_backend bloem "$bloem_aio"
step bloem-verify
stop_backend bloem

echo "SILO SWITCH CHECK (ALL-IN-ONE): PASS"
