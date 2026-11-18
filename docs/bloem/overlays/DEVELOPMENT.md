# Bloem overlay: Developing Silo

Silo's `DEVELOPMENT.md` explains how to build, run and test the server from source.
Bloem builds the same way, so read Silo's page first. This page lists the few places
where Bloem Server differs, and why. It covers Silo's root
[`DEVELOPMENT.md`](../../../DEVELOPMENT.md), which stays exactly as Silo wrote it.

## What Bloem changes

- **A newer Go.** Bloem asks for a newer Go patch than Silo does, and its CI
  installs exactly that patch.
- **One set of migrations.** Bloem's database commands run Silo's migrations and
  Bloem's own together, so `make migrate-status` and `make migrate-up` show both.
- **Bigger test databases.** Bloem's full test suite runs many database tests at
  once. Its test PostgreSQL needs more connections and locks than a stock one, and
  some tests need their own scratch databases.
- **More checks before pushing.** A Bloem change also keeps the **seam ledger**
  (the list of Silo files Bloem is allowed to edit) and the Silo-switching tests
  passing.

Silo's `DEVELOPMENT.md` stays exactly as Silo wrote it, so it merges cleanly.

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| Bloem's migrations together with Silo's | `migrations.BloemFS` (`migrations/bloem.go`) |
| Test database | the database named by `SILO_TEST_DATABASE_URL`, prepared by `cmd/bloem-testdb` (`make test-db-prepare`) |
| Scenario database | the scratch database named by `SILO_SCENARIO_DATABASE_URL` |
| Storage plugin SDK pin | `StorageModule` in `internal/bloemtestsdk/sdk.go` |
| Seam ledger | `contracts/seams.txt`, checked by `make verify-seams` |
| Silo switching | `public.bloem_silo_switching`, checked end to end by `make silo-switch-check` |

## For developers

### Section by section

---

**Location:** document title. Bloem titles this document “Developing Bloem Server”.

---

**Location:** section “Developing Silo” (the opening paragraph under the title).
**Bloem replaces** the corresponding upstream passage with:

How to build, run, and test Bloem Server from source. To run Bloem without
building it, see the [README](../../../.github/README.md). Contribution rules and the
pre-submission gate are in [CONTRIBUTING.md](../../../.github/CONTRIBUTING.md).

---

**Location:** section “Prerequisites”. **Bloem adds:**

Bloem's `go.mod` declares the same Go patch as Silo's, `go 1.27.4`, so build with Go
1.27.4 or later. Bloem CI installs
exactly the patch `go.mod` names.

---

**Location:** section “Working on the plugin SDK at the same time”, the first
paragraph. **Bloem replaces** it with:

If a change spans Bloem Server and `silo-plugin-sdk`, use an untracked local `go.work`
workspace. `go.work` and `go.work.sum` are gitignored developer conveniences: CI
runs from a clean checkout without them, and release builds set `GOWORK=off`.
Any SDK package or symbol this repository uses must therefore be pushed and
tagged in `silo-plugin-sdk` before the change here can merge.

---

**Location:** section “Make targets”, the table. **Bloem replaces** it with the
same table, except that `migrate-status` and `migrate-up` run Bloem's
`cmd/silo`, which applies Silo's and Bloem's migrations together through
`migrations.BloemFS`:

| Target | Description |
|---|---|
| `make build` | Build frontend + Go binary |
| `make frontend` | Build frontend only |
| `make dev-frontend` | Vite dev server with HMR |
| `make dev-backend` | Run Go backend (integrated mode) |
| `make dev-proxy` | Run a standalone proxy node |
| `make dev-transcode` | Run a transcode node against a running server (needs `SILO_CORE_URL` and `SILO_NODE_KEY`) |
| `make migrate-create NAME=add_thing` | Create a timestamped Goose SQL migration |
| `make migrate-validate` | Validate Goose migration files without touching a database |
| `make migrate-status` | Show Goose migration status using Bloem's bootstrapping runner |
| `make migrate-up` | Apply pending Goose migrations using Bloem's bootstrapping runner |
| `make clean` | Remove build artifacts |

---

**Location:** section “Tests and lint”, the code block under “While iterating”.
**Bloem replaces** it with:

```sh
GOMAXPROCS=2 GOFLAGS=-p=2 GOWORK=off go test ./internal/<package>/...
cd web && pnpm exec vitest run path/to/test.tsx
```

---

**Location:** section “Tests and lint”, after that code block. **Bloem adds:**

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

To run every Bloem CI job at once before pushing, use the local gate,
`python3 scripts/bloem/local-gate.py run`. It starts and removes its own sized
PostgreSQL and Redis; see [Bloem CI](../ci.md#local-gate). A plain `make test-go` on
a shared database also runs Silo's database-list tests beside every other
package, where some fail by design; skip them as CI does with
`GOFLAGS="-skip=$(python3 scripts/ci/bloem-db-list-skip.py)"` and run them with
`make test-db-pins` on a fresh database.

The storage plugin executable tests build `examples/hello-storage` from the
published `bloem-plugin-sdk` module pinned in `internal/bloemtestsdk`
(`StorageModule`, `github.com/Bloem-Studios/bloem-plugin-sdk@v0.27.0` as of
November 2026), so they need no extra setup. Set `BLOEM_STORAGE_SDK_WORKTREE` to an
SDK checkout only to test unreleased SDK changes; the host and SDK storage protos
must match.

`make test-go` sets a 20-minute per-package timeout. Check the
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
(`KEEP_ON_FAILURE=1` leaves the stack up for inspection), and
`make silo-switch-check-all-in-one` does the same for an install on Silo's
all-in-one image, whose database lives inside the container's `/config` volume.
Local test databases need `max_locks_per_transaction = 512`, as in CI,
and `make lint-changed` needs a golangci-lint built with the toolchain `go.mod` names.

Run `make verify-seams` and `make verify-silo-lines` after an upstream merge.
`contracts/seams.txt` lists every seam (a modified or deleted upstream file), and
`contracts/silo-line-changes.json` records every Silo line Bloem still overrides;
additions belong in owned modules or on Bloem's own lines wherever possible. CI's changed-line lint gate, `scripts/bloem-lint-changed-ci.sh`,
reports a finding only on a line that changed in the push and differs from Silo's
copy of the file, so lines a Silo merge brings in, or a file at Silo's
text, do not count against Bloem; Bloem's own changed lines, in any file, still do.
`pnpm run budget:check` runs Silo's `web/scripts/check-bundle-budget.mjs`: it
reports the eager launch bundle's brotli size without gating it, and fails when
the vendor chunk is missing or imports another chunk, or when `index.html` loads
a cross-origin render-blocking resource.

The [web coverage matrix](../../architecture/bloem-web-feature-coverage.md)
distinguishes automated checks, browser acceptance and remaining media acceptance. Preserve the existing web exclusions; do not add skips or weaken
assertions to conceal failures.
For documentation-only changes, check changed links/anchors, `git diff --check`,
`make verify-local-paths` and `make verify-seams`; a new full build or test suite is unnecessary.

Before running or deploying a new binary, verify its platform and `go version -m`
VCS revision/dirty flag against the checkout that supplied the source. A successful
build or a healthy older process does not establish that the new artifact was tested.
In worktrees, check for parent-checkout VCS metadata before trusting the stamp.

#### Documentation and API generation

Use the [documentation workflow](../documentation-workflow.md) for ownership,
Swagger/OpenAPI generation, native route coverage, settings/client bindings and
focused verification. The [API catalogue](../api-catalogue.md) identifies typed
operations and unresolved documentation gaps after the Silo integration.

### Related pages

- [Bloem CI](../ci.md): the jobs, test databases and pins CI runs.
- [Writing Bloem documentation](../documentation-workflow.md): overlays and the
  page shape.
- [Silo backend switching](../../architecture/silo-backend-switching.md).
- [Native video playback](../../architecture/bloem-native-video-playback.md): how a
  transcode node reads a file in Bloem's native storage.
- Index: [Bloem documentation](../README.md).
