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

Scenario execution uses a separate `SILO_SCENARIO_DATABASE_URL` pointing to an owned
scratch database and `SILO_SCENARIO_REQUIRED=1` for required runs. Lifecycle routes
require a reachable phase store even for malformed-input cases; a missing prerequisite
is an explicit failure. See [scenario execution](../../architecture/scenario-acceptance.md)
for reset guards, scoped avatar storage and per-transport reporting. The ordinary
Bloem gate applies [explicit downstream adjudications](../../architecture/bloem-contract-adjudications.md);
historical Silo selectors keep their original expectations.

`make test-go` sets a 20-minute per-package timeout. Remote CI for deployed revision
`418a18b7d` still exceeded that bound in the executor; the 189 other package passes
are not full-suite success. Its [deployment exception](../../operations/2026-09-19-xtream-deployment.md#ci-result-and-approved-exception)
was specific to that revision and run, not a testing-policy exemption.

The [web coverage matrix](../../architecture/bloem-web-feature-coverage.md) and
[completion handoff](../../architecture/bloem-web-completion-handoff.md) distinguish
automated checks, browser acceptance, the completed deployment and remaining media
acceptance. Preserve the existing web exclusions; do not add skips or weaken
assertions to conceal failures.
For documentation-only changes, check changed links/anchors, `git diff --check` and
`make verify-local-paths`; a new full build or test suite is unnecessary.

Before running or deploying a new binary, verify its platform and `go version -m`
VCS revision/dirty flag against the checkout that supplied the source. A successful
build or a healthy older process does not establish that the new artifact was tested.
In worktrees, check for parent-checkout VCS metadata before trusting the stamp.
