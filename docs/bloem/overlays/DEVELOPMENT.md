# Bloem overlay: Developing Silo

Bloem additions and overrides for the upstream Silo document [`DEVELOPMENT.md`](../../../DEVELOPMENT.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** document title. Bloem titles this document “Developing Bloem Server”.

---

**Location:** section “Developing Silo”. **Bloem replaces** the corresponding upstream passage with:

How to build, run, and test Bloem Server from source. To run Bloem without
building it, see the [README](../../../.github/README.md). Contribution rules and the
pre-submission gate are in [CONTRIBUTING.md](../../../.github/CONTRIBUTING.md).

---

**Location:** section “Prerequisites”. **Bloem adds:**

Bloem's `go.mod` declares `go 1.26.10`, above Silo's 1.26.4 minimum, so build
with Go 1.26.10 or later. Bloem CI installs exactly the patch `go.mod` names.

---

**Location:** section “Working on the plugin SDK at the same time”. **Bloem replaces** the corresponding upstream passage with:

If a change spans Bloem Server and `silo-plugin-sdk`, use an untracked local `go.work`
workspace. `go.work` and `go.work.sum` are gitignored developer conveniences: CI
runs from a clean checkout without them, and release builds set `GOWORK=off`.
Any SDK package or symbol this repository uses must therefore be pushed and
tagged in `silo-plugin-sdk` before the change here can merge.

---

**Location:** section “Make targets”. **Bloem replaces** the corresponding upstream passage with:

| Target | Description |
|---|---|
| `make build` | Build frontend + Go binary |
| `make frontend` | Build frontend only |
| `make dev-frontend` | Vite dev server with HMR |
| `make dev-backend` | Run Go backend (integrated mode) |
| `make dev-proxy` | Run a standalone proxy node |
| `make dev-transcode` | Run a standalone transcode node |
| `make migrate-create NAME=add_thing` | Create a timestamped Goose SQL migration |
| `make migrate-validate` | Validate Goose migration files without touching a database |
| `make migrate-status` | Show Goose migration status using Bloem's bootstrapping runner |
| `make migrate-up` | Apply pending Goose migrations using Bloem's bootstrapping runner |
| `make clean` | Remove build artifacts |

---

**Location:** section “Tests and lint”. **Bloem replaces** the corresponding upstream passage with:

```sh
GOMAXPROCS=2 GOFLAGS=-p=2 GOWORK=off go test ./internal/<package>/...
cd web && pnpm exec vitest run path/to/test.tsx
```

---

**Location:** section “Tests and lint”, after the paragraph beginning “```sh…”. **Bloem adds:**

Run focused checks serially on constrained development hosts. Database-backed
tests need disposable PostgreSQL: set `SILO_TEST_DATABASE_URL` and
`SILO_REQUIRE_TEST_DATABASE=1` for required integration checks. Some fixtures create
and drop their own fully migrated child databases; the test account needs those
permissions. Never repair a fixture by changing public tables in an existing database.

Size the test PostgreSQL for the full suite. `make test-go` runs packages in
parallel, and many of them migrate their own child databases at once. The stock
lock table (`max_locks_per_transaction=64` across 100 connections) fails with
`out of shared memory` (SQLSTATE 53200); CI raises it to
`max_connections=300` and `max_locks_per_transaction=512`. Child databases also
need disk space: a PostgreSQL data directory on a small tmpfs fills up and
crashes the server mid-run. On hosts with many cores, cap package parallelism
with `GOFLAGS=-p=8` so local runs resemble CI.

The storage plugin executable tests build `examples/hello-storage` from the
published `bloem-plugin-sdk` module pinned in `internal/bloemtestsdk`, so they
need no extra setup. Set `BLOEM_STORAGE_SDK_WORKTREE` to an SDK checkout only to
test unreleased SDK changes; the host and SDK storage protos must match.

Scenario execution uses a separate `SILO_SCENARIO_DATABASE_URL` pointing to an owned
scratch database and `SILO_SCENARIO_REQUIRED=1` for required runs. Lifecycle routes
require a reachable phase store even for malformed-input cases; a missing prerequisite
is an explicit failure. See [scenario execution](../../architecture/scenario-acceptance.md)
for reset guards, scoped avatar storage and per-transport reporting. The ordinary
Bloem gate applies [explicit downstream adjudications](../../architecture/bloem-contract-adjudications.md);
historical Silo selectors keep their original expectations.

`make test-go` sets a 20-minute per-package timeout. The
[October 7 deployment record](../../operations/2026-10-07-native-storage-deployment.md#outstanding-validation)
records historical focused checks and remaining backend/client acceptance. Use
the [integration changelog](../CHANGELOG.md) for the newer source milestones. Older
interrupted or failed broad runs remain historical failures. Check the
[SDK guide](../../architecture/bloem-sdk-compatibility.md) for reproducible
contract generation and consumer adoption; a generated file alone is not a client feature.

Bloem's migration commands use `migrations.BloemFS`, including status and rollback
entrypoints. The [adapter guide](../../architecture/bloem-upstream-adapters.md) explains the
transaction marker, preserved account scope and exact-statement guard.

Silo switching (`public.bloem_silo_switching`) has database tests in
`internal/tenancy/silo_switching_test.go`, `silo_switching_writes_test.go`,
`internal/auth/bloem_repository_switching_test.go` and
`migrations/bloem_account_policy_on_users_test.go`.
They build their own disposable databases. End to end,
`make silo-switch-check SILO_IMAGE=ghcr.io/silo-server/silo-server@sha256:<digest> BLOEM_IMAGE=<tag>`
runs the upstream Silo image and a Bloem image in turn against one database
(`KEEP_ON_FAILURE=1` leaves the stack up for inspection). Local test databases need `max_locks_per_transaction = 512`, as in CI,
and `make lint-changed` needs a golangci-lint built with the toolchain `go.mod` names.

Run `make verify-seams` after an upstream merge. Bloem currently declares 331
seams (modified or deleted upstream files); additions belong in owned modules
wherever possible. CI's changed-line lint gate, `scripts/bloem-lint-changed-ci.sh`,
reports a finding only on a line that changed in the push and differs from Silo's
copy of the file, so lines a Silo merge brings in, or a file restored to Silo's
text, do not count against Bloem; Bloem's own changed lines, in any file, still do.
The web budget keeps Bloem's measured allowance in `web/bloem-perf-budget.json`
(326,067 B brotli for the eager launch bundle, within 1 KiB either way, and no
cross-origin render-blocking resources), checked by `pnpm run budget:check`
through the owned `web/scripts/bloem-bundle-budget.mjs` with Silo's manifest and
resource checks unchanged; Silo's own script reports launch bytes without gating them.

The [web coverage matrix](../../architecture/bloem-web-feature-coverage.md) and
[completion handoff](../../architecture/bloem-web-completion-handoff.md) distinguish
automated checks, browser acceptance, the completed deployment and remaining media
acceptance. Preserve the existing web exclusions; do not add skips or weaken
assertions to conceal failures.
For documentation-only changes, check changed links/anchors, `git diff --check`,
`make verify-local-paths` and `make verify-seams`; a new full build or test suite is unnecessary.

Before running or deploying a new binary, verify its platform and `go version -m`
VCS revision/dirty flag against the checkout that supplied the source. A successful
build or a healthy older process does not establish that the new artifact was tested.
In worktrees, check for parent-checkout VCS metadata before trusting the stamp.

## Documentation and API generation

Use the [documentation workflow](../documentation-workflow.md) for ownership,
Swagger/OpenAPI generation, native route coverage, settings/client bindings and
focused verification. The [API catalogue](../api-catalogue.md) identifies typed
operations and unresolved documentation gaps after the Silo integration.
