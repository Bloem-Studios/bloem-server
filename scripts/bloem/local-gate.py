#!/usr/bin/env python3
"""Run every Bloem CI job on this machine, on disposable PostgreSQL and Redis.

    python3 scripts/bloem/local-gate.py list
    python3 scripts/bloem/local-gate.py run [--base SHA] [--only PHASE[,PHASE]]
    python3 scripts/bloem/local-gate.py services up
    python3 scripts/bloem/local-gate.py services down ID

Commands assume the repository root is the cwd. `run` executes the steps in
STEPS in order, keeps going after a failure, prints one `GATE PASS|FAIL` line
per step with its log file, and exits 1 when any step failed. Every step runs
under umask 022 with GOWORK=off and the Go toolchain named in go.mod.

STEPS copies the commands of .github/workflows/bloem-ci.yml job by job;
scripts/bloem/local-gate_test.py fails when CI gains a make target or a go
test this list lacks. Each job that CI gives its own database gets its own
fresh database here, in one PostgreSQL container sized like CI's.

`services up` starts the containers, creates bloem_test, prints shell exports
for it and leaves them running for debugging; `services down ID` removes them.
"""

import argparse
import os
import re
import secrets
import shlex
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import NamedTuple

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "bloem-ci.yml"

PG_IMAGE = "pgvector/pgvector:pg18"
REDIS_IMAGE = "redis:7-alpine"
PG_USER = "bloem_tenant"
PG_PASSWORD = "invented-ci-password"

PHASES = ["build", "verify", "lint", "web", "go", "db", "tenancy"]


class Step(NamedTuple):
    phase: str
    name: str
    cmd: str
    needs: frozenset = frozenset()


def _needs(*names):
    return frozenset(names)


def _pg(name):
    return f"@PG({name})@"


_TENANT = "export SILO_TEST_DATABASE_URL=" + _pg("bloem_tenant_test") + " SILO_REQUIRE_TEST_DATABASE=1 && "
_AUTH = (
    "export SILO_TEST_DATABASE_URL=" + _pg("postgres")
    + " SILO_INITIAL_SETUP_ATOMIC_DSN=" + _pg("silo_identity_setup_ci") + " && "
)
_MIGRATE = 'DATABASE_URL={db} SECRET_KEY="$(openssl rand -hex 32)" go run ./cmd/silo --migrate-only'


def _go_shard(n):
    return Step(
        "go",
        f"shard-{n}",
        "skip=$(python3 scripts/ci/bloem-db-list-skip.py) && SILO_TEST_DATABASE_URL=" + _pg(f"bloem_test_s{n}")
        + " GOFLAGS=\"-count=1 -p=4 -skip=$skip\" make test-go GO_TEST_PACKAGES=\"$(make -s --no-print-directory"
        + f" go-test-packages | scripts/ci/deal.sh 4 {n} | paste -sd ' ')\"",
        _needs("pg", f"fresh_db:bloem_test_s{n}"),
    )


def _web_shard(n):
    return Step("web", f"test-{n}", f'make test-web WEBTEST_ARGS="--shard={n}/4 --maxWorkers=4"')


STEPS = [
    # go-lint (format and vet) and the embed stub every Go step needs.
    Step("build", "embed-stub", "make embed-stub"),
    # The toolchain's own gofmt: the one on PATH belongs to the machine's Go.
    Step(
        "build",
        "gofmt",
        'unformatted="$("$(go env GOROOT)/bin/gofmt" -l .)"; [ -z "$unformatted" ] || { echo "$unformatted"; exit 1; }',
    ),
    Step("build", "go-build", "go build ./..."),
    Step("build", "go-vet", "go vet ./..."),
    # go-verify, in CI's order, plus the checks the web job and this gate own.
    Step("verify", "settings-bindings", "make verify-settings-bindings"),
    Step("verify", "client-dtos", "make verify-client-dtos"),
    Step("verify", "playback-fixtures", "make verify-playback-fixtures"),
    Step("verify", "route-inventory", "make verify-route-inventory"),
    Step("verify", "apiv2-openapi", "make verify-apiv2-openapi"),
    Step("verify", "bloem-openapi", "make verify-bloem-openapi"),
    Step("verify", "bloem-api-catalogue", "make verify-bloem-api-catalogue"),
    Step("verify", "apiv2-contract", "make verify-apiv2-contract BASE_REF=@BASE@"),
    Step("verify", "apiv2-fixtures", "make verify-apiv2-fixtures"),
    Step("verify", "seams", "git fetch --quiet upstream main && make verify-seams"),
    Step("verify", "client-coverage", "go test ./cmd/... ./contracts/..."),
    Step("verify", "local-paths", "make verify-local-paths"),
    Step("verify", "local-gate", "make verify-local-gate"),
    Step("verify", "db-list-skip", "make verify-db-list-skip"),
    # go-lint.
    Step("lint", "govulncheck", "go tool govulncheck ./..."),
    Step("lint", "changed-lines", "BASE_SHA=@BASE@ PATH=@GOLANGCI_DIR@:$PATH scripts/bloem-lint-changed-ci.sh"),
    Step("lint", "router-recovery", "make lint-router-recovery"),
    # web and web-test.
    Step("web", "install", "cd web && pnpm install --frozen-lockfile"),
    Step("web", "eslint", "cd web && pnpm run lint"),
    Step("web", "prettier", "cd web && pnpm run format:check"),
    Step("web", "typecheck-app", "cd web && pnpm exec tsc -p tsconfig.app.json"),
    Step("web", "typecheck-node", "cd web && pnpm exec tsc -p tsconfig.node.json"),
    Step("web", "vite-build", "cd web && pnpm exec vite build"),
    Step("web", "budget", "cd web && pnpm run budget:check"),
    Step("web", "settings-bindings-web", "make verify-settings-bindings-web"),
    Step("web", "apiv2-web-types", "make verify-apiv2-web-types"),
    *[_web_shard(n) for n in range(1, 5)],
    # go-test: CI deals the packages to four shards, each on its own database.
    *[_go_shard(n) for n in range(1, 5)],
    # go-db-pins and go-db-regress.
    Step(
        "db",
        "pins",
        "export SILO_TEST_DATABASE_URL=" + _pg("bloem_pins_test")
        + " && go run ./cmd/bloem-testdb && make test-db-pins",
        _needs("pg", "fresh_db:bloem_pins_test"),
    ),
    Step(
        "db",
        "pins-marker-claims",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_pins_test")
        + " go test -race -count=1 -run '^TestMarkerContributionClaimsByTargetMigrationPostgres$' ./migrations/",
        _needs("pg"),
    ),
    Step(
        "db",
        "pins-taste-clusters",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_pins_test")
        + " go test -race -count=1 -run '^TestUpsertTasteClusters.*Postgres$' ./internal/recommendations/",
        _needs("pg"),
    ),
    Step(
        "db",
        "pins-compat-subtitles",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_pins_test")
        + " go test -race -count=1 -run '^TestCompatDetailServiceResolvesViewerSubtitleDefaultsPostgres$' ./cmd/silo/",
        _needs("pg"),
    ),
    Step(
        "db",
        "pins-image-cache",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_pins_test")
        + " go test -race -count=1 -run '^(TestImageCacheCleanup|TestRequeuedImageCacheCleanup|TestJobOutcomeIsRecorded)'"
        + " ./internal/adminjob/",
        _needs("pg"),
    ),
    Step(
        "db",
        "watch-party",
        "export SILO_TEST_DATABASE_URL=" + _pg("bloem_regress_test")
        + " && go run ./cmd/bloem-testdb && go test -race -count=1 ./internal/watchtogether/",
        _needs("pg", "fresh_db:bloem_regress_test"),
    ),
    Step(
        "db",
        "history-import",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_regress_test")
        + " go test -race -count=1 -run '^(TestHistoryImportCancellation.*|TestQueue.*|TestHistoryImportV1ProfileAuthorization)$'"
        + " ./migrations/ ./internal/historyimport/ ./internal/api/handlers/",
        _needs("pg"),
    ),
    Step(
        "db",
        "library-locations",
        "export SILO_TEST_DATABASE_URL=" + _pg("bloem_storage_test_ci") + " SILO_TEST_REDIS_URL=@REDIS@ SILO_TEST_REDIS_DISPOSABLE_ADDR=@REDIS_ADDR@"
        + " && go run ./cmd/bloem-testdb && make test-bloem-library-locations",
        _needs("pg", "redis", "fresh_db:bloem_storage_test_ci"),
    ),
    # tenant-identity, tenant-stores and native-auth.
    Step("tenancy", "migrate", _MIGRATE.format(db=_pg("bloem_tenant_test")), _needs("pg", "fresh_db:bloem_tenant_test")),
    Step(
        "tenancy",
        "identity-migrations",
        _TENANT + "go test ./internal/database -run 'TestTenantIdentityMigration|TestResourceTenancyMigration"
        "|TestOrganizationAccessGroupMigration|TestProfileAccessGroupRequired' -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "rebrand-rollback",
        _TENANT + "go test ./internal/database -run '^TestBloemFunctionRebrandPreservesCanonicalNamesOnRollback$'"
        " -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "profile-persistence",
        _TENANT + "go test ./internal/userstore/pgstore -run 'TestProfileOrganizationAndAccessGroupPersistence"
        "|TestProfileAccessGroupRejectsDifferentOrganization' -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "stores-access-policy",
        _TENANT + "go test -race ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "opa-compat",
        _TENANT + "go test ./internal/api -run 'TestV1TenancyCompatibility|TestOPATenantFoundationWithDisposablePostgres'"
        " -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "multitenant-admin",
        _TENANT + "go test ./internal/api ./internal/tenancy ./internal/adminpeople ./internal/database"
        " -run 'TestMultitenantAdmin|TestAdminStore|TestService|TestAdminPeopleMigrations"
        "|TestOrganizationAdminProjectionMigration' -count=1 -v -timeout=30m",
        _needs("pg"),
    ),
    Step(
        "tenancy",
        "stores-migrate",
        _MIGRATE.format(db=_pg("bloem_tenant_stores_test")),
        _needs("pg", "fresh_db:bloem_tenant_stores_test"),
    ),
    Step(
        "tenancy",
        "stores-race",
        "SILO_TEST_DATABASE_URL=" + _pg("bloem_tenant_stores_test")
        + " SILO_REQUIRE_TEST_DATABASE=1 go test -race ./internal/tenancy -count=1 -v -timeout=60m",
        _needs("pg"),
    ),
    Step("tenancy", "auth", _AUTH + "go test -timeout=60m ./internal/auth -count=1",
         _needs("pg", "fresh_db:silo_identity_setup_ci")),
    Step("tenancy", "auth-race", _AUTH + "go test -race -timeout=60m ./internal/auth -count=1",
         _needs("pg", "fresh_db:silo_identity_setup_ci")),
]


def pg_url(port, name):
    return f"postgres://{PG_USER}:{PG_PASSWORD}@127.0.0.1:{port}/{name}?sslmode=disable"


def redis_url(port):
    return f"redis://127.0.0.1:{port}/0"


def postgres_command(sid, port):
    return [
        "docker", "run", "-d", "--rm", "--name", f"bloem-gate-{sid}-pg",
        "-e", f"POSTGRES_USER={PG_USER}", "-e", f"POSTGRES_PASSWORD={PG_PASSWORD}",
        "-p", f"127.0.0.1:{port}:5432", "--shm-size=1g", PG_IMAGE,
        "-c", "max_connections=300", "-c", "max_locks_per_transaction=512",
    ]


def redis_command(sid, port):
    return ["docker", "run", "-d", "--rm", "--name", f"bloem-gate-{sid}-redis", "-p", f"127.0.0.1:{port}:6379", REDIS_IMAGE]


def services_exports(sid, pg_port, redis_port):
    pairs = [
        ("SILO_TEST_DATABASE_URL", pg_url(pg_port, "bloem_test")),
        ("SILO_TEST_REDIS_URL", redis_url(redis_port)),
        ("SILO_TEST_REDIS_DISPOSABLE_ADDR", f"127.0.0.1:{redis_port}"),
        ("GATE_SERVICES_ID", sid),
    ]
    return "".join(f"export {key}={shlex.quote(value)}\n" for key, value in pairs)


def format_list(steps):
    return "".join(f"{s.phase}/{s.name}\t{s.cmd}\n" for s in steps)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def go_toolchain():
    match = re.search(r"^go\s+(\S+)", (ROOT / "go.mod").read_text(), re.M)
    return f"go{match.group(1)}"


def base_env():
    env = dict(os.environ)
    env.update(
        GOWORK="off",
        GOTOOLCHAIN=go_toolchain(),
        GOPROXY="https://proxy.golang.org,direct",
        GOPRIVATE="github.com/Silo-Server/*",
        GONOSUMDB="github.com/Silo-Server/*",
        PYTHONDONTWRITEBYTECODE="1",
    )
    return env


def start_services(sid):
    pg_port, r_port = free_port(), free_port()
    subprocess.run(postgres_command(sid, pg_port), check=True, stdout=subprocess.DEVNULL)
    subprocess.run(redis_command(sid, r_port), check=True, stdout=subprocess.DEVNULL)
    deadline = time.monotonic() + 120
    # Over TCP: the image's init server listens on the socket only, so a
    # socket probe can answer before the real server is up.
    probe = ["docker", "exec", f"bloem-gate-{sid}-pg", "pg_isready", "-h", "127.0.0.1", "-U", PG_USER, "-d", "postgres"]
    while subprocess.run(probe, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0:
        if time.monotonic() > deadline:
            raise RuntimeError("PostgreSQL did not become ready within 120 s")
        time.sleep(1)
    return pg_port, r_port


def stop_services(sid):
    for suffix in ("pg", "redis"):
        subprocess.run(["docker", "rm", "-f", f"bloem-gate-{sid}-{suffix}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def recreate_database(sid, name):
    subprocess.run(
        ["docker", "exec", "-e", "PGOPTIONS=-c client_min_messages=warning", f"bloem-gate-{sid}-pg",
         "psql", "-v", "ON_ERROR_STOP=1", "-q", "-U", PG_USER, "-d", "postgres",
         "-c", f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)', "-c", f'CREATE DATABASE "{name}"'],
        check=True,
        stdout=subprocess.DEVNULL,
    )


def golangci_dir(env):
    version = re.search(r"GOLANGCI_LINT_VERSION:\s*(\S+)", WORKFLOW.read_text()).group(1)
    target = Path.home() / ".cache" / "bloem-gate" / f"golangci-lint-{version}-{env['GOTOOLCHAIN']}"
    if not (target / "golangci-lint").exists():
        target.mkdir(parents=True, exist_ok=True)
        subprocess.run(
            ["go", "install", f"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{version}"],
            check=True,
            env={**env, "GOBIN": str(target)},
            cwd=ROOT,
        )
    return str(target)


def expand(cmd, *, base, pg_port, r_port, env):
    cmd = re.sub(r"@PG\(([a-z0-9_]+)\)@", lambda m: shlex.quote(pg_url(pg_port, m.group(1))), cmd)
    cmd = cmd.replace("@REDIS@", redis_url(r_port)).replace("@REDIS_ADDR@", f"127.0.0.1:{r_port}")
    cmd = cmd.replace("@BASE@", base)
    if "@GOLANGCI_DIR@" in cmd:
        cmd = cmd.replace("@GOLANGCI_DIR@", golangci_dir(env))
    return cmd


def select(only):
    if not only:
        return list(STEPS)
    wanted = set(only.split(","))
    unknown = wanted - set(PHASES)
    if unknown:
        sys.exit(f"local-gate: unknown phase(s): {', '.join(sorted(unknown))}; phases: {', '.join(PHASES)}")
    return [s for s in STEPS if s.phase in wanted]


def run(base_ref, only):
    os.umask(0o022)
    env = base_env()
    base = subprocess.run(["git", "rev-parse", "--verify", f"{base_ref}^{{commit}}"], check=True,
                          capture_output=True, text=True, cwd=ROOT).stdout.strip()
    steps = select(only)
    needs = set().union(*(s.needs for s in steps))
    logs = Path("/var/tmp") / f"bloem-gate-{time.strftime('%Y%m%dT%H%M%S', time.gmtime())}-{os.getpid()}"
    logs.mkdir(parents=True)
    sid = f"{os.getpid()}-{secrets.token_hex(2)}"
    pg_port = r_port = 0
    failed = []

    def on_signal(signum, _frame):
        raise KeyboardInterrupt(f"signal {signum}")

    signal.signal(signal.SIGTERM, on_signal)
    try:
        if needs & {"pg", "redis"}:
            pg_port, r_port = start_services(sid)
        print(f"GATE base={base[:9]} steps={len(steps)} logs={logs}", flush=True)
        for step in steps:
            label = f"{step.phase}/{step.name}"
            log = logs / f"{step.phase}-{step.name}.log"
            started = time.monotonic()
            try:
                for need in sorted(step.needs):
                    if need.startswith("fresh_db:"):
                        recreate_database(sid, need.split(":", 1)[1])
                cmd = expand(step.cmd, base=base, pg_port=pg_port, r_port=r_port, env=env)
                with log.open("w") as out:
                    out.write(f"$ {cmd}\n")
                    out.flush()
                    code = subprocess.run(["bash", "-o", "pipefail", "-c", cmd], cwd=ROOT, env=env,
                                          stdout=out, stderr=subprocess.STDOUT).returncode
            except subprocess.CalledProcessError as err:
                log.write_text(f"setup failed: {err}\n")
                code = 1
            secs = int(time.monotonic() - started)
            print(f"GATE {'PASS' if code == 0 else 'FAIL'} {label} {secs}s log={log}", flush=True)
            if code != 0:
                failed.append(label)
    finally:
        if pg_port:
            stop_services(sid)
    print(f"GATE {'FAILED' if failed else 'PASSED'}: {len(steps) - len(failed)}/{len(steps)} steps passed"
          + (f"; failed: {' '.join(failed)}" if failed else ""), flush=True)
    return 1 if failed else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", help="print every step and its command")
    run_parser = sub.add_parser("run", help="run the gate")
    run_parser.add_argument("--base", default="origin/main", help="commit the changed-line checks compare with")
    run_parser.add_argument("--only", help=f"comma-separated phases: {','.join(PHASES)}")
    services = sub.add_parser("services", help="start or remove the gate's PostgreSQL and Redis for debugging")
    services.add_argument("action", choices=["up", "down"])
    services.add_argument("id", nargs="?")
    args = parser.parse_args(argv)

    if args.command == "list":
        sys.stdout.write(format_list(STEPS))
        return 0
    if args.command == "run":
        return run(args.base, args.only)
    if args.action == "down":
        if not args.id:
            parser.error("services down needs the ID that services up printed")
        stop_services(args.id)
        return 0
    sid = f"{os.getpid()}-{secrets.token_hex(2)}"
    pg_port, r_port = start_services(sid)
    recreate_database(sid, "bloem_test")
    sys.stdout.write(services_exports(sid, pg_port, r_port))
    return 0


if __name__ == "__main__":
    sys.exit(main())
