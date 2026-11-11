# How Bloem CI works

> **Provenance and status.** Bloem page, rewritten on 2026-11-01, updated on 2026-11-10
> and checked against `main` at `f12b3c75f`, which includes Silo through `07f0ae21c`.
> Since 2 November Silo's work comes in through the merge bot, and the GitHub upstream
> sync is switched off. Bloem's CI, image and upstream-sync workflows are Bloem
> additions; Silo's own `ci.yml` stays as Silo wrote it apart from one line. The
> technical rules are in the [For developers](#for-developers) section.

Every change to Bloem Server is checked before it can be trusted, every passing
commit on `main` gets a server image, and Silo's work comes in as ordinary merges,
made by a merge bot. This page walks through what runs and when, which machines run
it, how Silo's changes land, and what to do when a run fails.

## What runs, and when

![Two flows. Every change: a pull request, a push to main, the Monday run or a manual run starts Bloem CI; a passing push to main starts Bloem Image, which publishes an image tagged with the commit and moves latest when that commit is still main's newest. Every day, only when switched on (off since November 2, 2026): the upstream sync merges Silo into a branch and opens a pull request, Bloem CI checks it, and the merge gate lands it on main](../architecture/images/bloem-ci/workflows.svg)

Bloem has four workflows of its own:

1. **Bloem CI** checks a change. It runs on every pull request, on every push to
   `main`, every Monday morning, and when someone starts it by hand. Its jobs run
   side by side: Go tests, lint, contract checks, database checks, the web build
   and the web tests. A newer run for the same branch cancels an older one.
2. **Bloem Image** builds the **server image**, the package a server runs. It starts
   only after Bloem CI passes on a push to `main`, and tags the image with that
   commit. It also moves the `latest` tag, but only while that commit is still the
   newest on `main`.
3. **Upstream sync** merges Silo's latest work into a new branch every morning and
   opens a pull request, when it is switched on. It is off since November 2, 2026
   ([below](#how-silos-work-comes-in)).
4. **Upstream sync merge** lands that pull request on `main` once Bloem CI has passed
   on exactly the merge it tested. It is switched off together with the sync.

None of them deploys anything. The merge bot deploys each Silo batch once Bloem CI
passes it ([below](#how-silos-work-comes-in)), and a person starts every other
deployment. A dated record in [`docs/operations/`](../operations/) says what was
deployed and how.

## Where the jobs run

![Three questions decide where a Bloem CI job runs: whether self-hosted runners are switched on, whether the run is on main, and whether a pull request comes from a listed member. A yes on main or from a member goes to a disposable runner; anything else runs on GitHub's ubuntu-24.04](../architecture/images/bloem-ci/runners.svg)

A job runs on one of two kinds of machine:

- **GitHub's runners**, the standard `ubuntu-24.04` machines GitHub provides.
- **Disposable runners**, Bloem's own private machines. Each one runs a single job
  and is then thrown away, so nothing from one job can reach the next.

Bloem's own runners take the runs on `main` and the pull requests of listed
members, once a repository setting switches them on. Every other run, such as a
pull request from someone else, uses GitHub's runners. The machines that start
the disposable runners apply the same rule from their own settings, so the two
must change together.

## How Silo's work comes in

Bloem is a **tracking fork**: it takes in Silo's work as ordinary merges instead of
copying it by hand ([how Bloem keeps up with Silo](../architecture/bloem-following-silo.md)).

Since November 2, 2026 a **merge bot** on Bloem's build machine takes each Silo
batch, outside GitHub Actions:

1. **A free check every 10 minutes** starts a batch when Silo has new commits and has
   been quiet for 10 minutes. The batch is everything on Silo's `main` at that moment,
   and only one runs at a time.
2. **One thread per batch.** The bot works the batch in its own T3 thread and its own
   copy of the repository, which the owner can watch and answer.
3. **Merge and check.** Each conflict takes Silo's side, word for word, and Bloem's
   change goes on its own lines or in its own file, with a test where Silo's version
   would break Bloem. A review list names the seams, Bloem files, overlays, pages and
   migrations to read. Then the **fast local checks** run on the build machine: the
   build, the contract and verify checks and changed-line lint
   ([local gate](#local-gate)).
4. **Push and wait.** The bot pushes the merge to `main` and waits for Bloem CI, which
   runs the full suite, on that exact commit.
5. **Deploy.** Only a commit Bloem CI passed is deployed; the deploy refuses any other.
   The operator can override that by hand, as happened once
   ([receipt](../operations/2026-11-02-silo-sync-56e6f871c-deployment.md)). The bot
   writes the receipt and sends one message: deployed, stuck or rolled back.

![The merge bot pushes to main and waits for Bloem CI on that commit. Passed: it deploys. A failed job is rerun once; a run cancelled by a newer push is followed to main's new tip; a failure that comes back is fixed on Bloem's own lines and pushed again, at most three times. A failure it cannot fix stops the batch for the owner](../architecture/images/bloem-ci/merge-bot.svg)

The bot stops in its thread and sends the "stuck" message when a test or check
cannot be fixed, when Bloem cannot be kept working without editing Silo's lines, when
Bloem CI stays red after its fixes, or when the deploy fails or rolls back. No new
batch starts until the owner answers there or releases the batch.

The daily sync and its merge gate are still in the repository, switched off, so two
automatic mergers never race. A repository administrator turns both back on by
setting the repository variable `BLOEM_UPSTREAM_SYNC` to `true`. Switched on, they
work like this:

![The daily sync and five checks before a sync pull request merges itself: Bloem CI passed, the branch comes from this repository, it is the one open non-draft sync pull request, its head is the exact commit CI tested, and that commit is the untouched merge of Bloem's main and Silo's main. Any no leaves the pull request for a person](../architecture/images/bloem-ci/merge-gate.svg)

1. Every morning the sync checks whether Silo's `main` has anything Bloem lacks.
2. If it has, the sync merges it on a new branch and opens a pull request.
3. **A clean merge** is an ordinary pull request. Bloem CI checks it, and the merge
   gate lands it if all five checks in the picture pass.
4. **A merge with conflicts** opens as a **draft** pull request, with the conflicts
   left in. It never lands by itself. A person resolves each conflict on Silo's
   side, puts Bloem's change on its own lines, and merges it by hand once Bloem CI
   passes.

Nothing in the sync writes to `main` directly or rewrites history.

## When a run fails

- **A failure that has nothing to do with the change**, such as a runner that
  stopped mid-job, is re-run with GitHub's **Re-run failed jobs**. The jobs that
  passed keep their results; the failed ones run again on fresh machines.
- **A test that fails again, or fails often,** is a bug. Fix it rather than re-run
  it.
- **A cancelled run is not a pass.** When two pushes to `main` follow each other
  quickly, the second cancels the first run, and only the second commit gets an
  image.
- **The merge bot reruns once by itself.** When CI fails on a batch it pushed, it
  reruns the failed jobs once. The same failure twice is real: the bot fixes it, or
  stops for the owner.

## Example: two pushes in quick succession

This example is made up.

1. At 10:00 Ana pushes a fix to `main`. Bloem CI starts on a disposable runner.
2. At 10:04 she pushes a typo fix. A new Bloem CI run starts for `main`, and the
   10:00 run is cancelled.
3. At 10:30 the second run passes. Bloem Image builds an image tagged with the
   second commit and moves `latest` to it, because that commit is still the newest
   on `main`.
4. The 10:00 commit gets no image of its own. Its changes are in the 10:04 image,
   which contains both commits.

## Who can change what

| Who | What they manage |
| --- | --- |
| A repository administrator | The repository variables that switch on and route to the disposable runners (`BLOEM_SELF_HOSTED`, `BLOEM_CI_MEMBERS`), the one that switches the upstream sync back on (`BLOEM_UPSTREAM_SYNC`), and the secrets for the sync token and Discord (`UPSTREAM_SYNC_TOKEN`, `DISCORD_WEBHOOK_URL`). |
| The owner | Answering or releasing a stopped batch, pausing the merge bot, and every deployment the bot does not make. |
| The merge bot, outside this repository | Merging each Silo batch, pushing it to `main`, its one re-run of a failed CI run, and deploying a batch Bloem CI passed. |
| Anyone with push access | Bloem's workflow files and scripts, re-runs, pushing a checked Silo merge to `main`, and merging a resolved sync pull request by hand. |
| The runner controller, outside this repository | Starting a fresh disposable runner for each job. Its own settings are the authority for routing. |
| Silo | Its own workflow files, which Bloem keeps exactly as Silo wrote them except one line of `ci.yml`. |

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| Bloem CI | `.github/workflows/bloem-ci.yml`, workflow name "Bloem CI" |
| Bloem Image | `.github/workflows/bloem-image.yml`, workflow name "Bloem Image" |
| The merge bot | runs on Bloem's build machine; its configuration, scripts and prompts live in Bloem's private infrastructure repository |
| Review list | `scripts/bloem/silo-merge-review.py` |
| Local gate | `scripts/bloem/local-gate.py` |
| Upstream sync | `.github/workflows/upstream-sync.yml` |
| Upstream sync merge, the merge gate | `.github/workflows/upstream-sync-merge.yml` and `scripts/verify-upstream-sync-merge.sh` |
| The switch for both | the repository variable `BLOEM_UPSTREAM_SYNC`; `true` turns them on, anything else (unset today) keeps them off |
| Disposable runner | a self-hosted runner with the labels `bloem-linux`, `bloem-run-<run id>-<attempt>` and `bloem-job-<job id>` |
| GitHub's runners | `ubuntu-24.04` (Bloem CI) and `ubuntu-latest` (the other three workflows) |
| Server image | `ghcr.io/bloem-studios/bloem-server:<full commit SHA>`, and `latest` |
| The web bundle | the `frontend-dist-<sha>` artifact from the Web (build) job |
| Seam | a Silo file Bloem edits, listed in `contracts/seams.txt` and checked by `make verify-seams` |
| Changed-line lint | `scripts/bloem-lint-changed-ci.sh` |

## For developers

Commands assume the repository root is the cwd. Everything here is Bloem's own,
except Silo's `ci.yml`, which is described only where it affects Bloem.

Terms used below:

- **Silo** is the upstream server, `Silo-Server/silo-server`. Bloem is a
  *tracking fork*: it merges Silo's `main` regularly and keeps its own changes
  out of Silo's files where it can
  ([FORK.md](../../FORK.md#this-is-a-tracking-fork-deliberately)).
- A **seam** is a Silo-owned file that Bloem has changed. Each one is listed in
  the seam ledger, [`contracts/seams.txt`](../../contracts/seams.txt) (309
  entries at `a7359de60`), and `make verify-seams` fails on a change the
  ledger does not list.
- A **disposable runner** is a private self-hosted runner that runs one job
  and is not reused. See [Where jobs run](#where-jobs-run).

### Workflows

| Workflow | File | Runs on | What it does | Origin |
|---|---|---|---|---|
| Bloem CI | `bloem-ci.yml` | Pull requests; pushes to `main` and `apiv2`; Mondays at 04:23 UTC; manual | Runs every Bloem gate ([Jobs](#jobs)) | Bloem, 2026-10-26 (`dcbd1c525`) |
| Bloem Image | `bloem-image.yml` | A Bloem CI run on `main` completing; manual | Publishes `ghcr.io/bloem-studios/bloem-server:<full sha>` ([Bloem Image](#bloem-image)) | Bloem, 2026-10-11 (`04a69c0fe`) |
| Upstream sync | `upstream-sync.yml` | Daily at 05:20 UTC, but its job runs only when `BLOEM_UPSTREAM_SYNC` is `true`; manual, with a dry run, always | Merges Silo's `main` on a branch and opens a pull request | Bloem, 2026-08-26 (`af2be39f1`); switched off 2026-11-02 |
| Upstream sync merge | `upstream-sync-merge.yml` | Any Bloem CI run completing; acts only when `BLOEM_UPSTREAM_SYNC` is `true` | Merges a sync pull request whose exact head passed Bloem CI | Bloem, 2026-08-26 (`af2be39f1`); switched off 2026-11-02 |
| CI | `ci.yml` | As in Silo | Nothing on Bloem: its one job runs only in Silo's repository ([below](#silos-ciyml)) | Silo's file; a one-line seam |

The other files in `.github/workflows/` are Silo's and byte-identical to Silo's
`main` at `07f0ae21c`. None of them tests or publishes Bloem:

- Silo's `docker.yml` ("Docker Image") publishes only after a successful "CI"
  run, and on Bloem every "CI" run concludes `skipped`, so Silo's image job is
  skipped too.
- The reusable `trusted-linux-build.yml` and `omnibus-build.yml`, which build
  Silo's images, run only in Silo's repository.
- `macos.yml` and `release.yml` run only when started by hand.
- `claude.yml`, `discord-commits.yml` (with `reusable-discord-commits.yml`) and
  `v1-proposal-labeler.yml` react to comments, issues and pushes; they build and
  test nothing.

### Silo's `ci.yml`

Bloem's CI is its own workflow, `.github/workflows/bloem-ci.yml`
("Bloem CI"). Silo's `.github/workflows/ci.yml` stays as Silo wrote it, with
one added line: its single job, a call to Silo's reusable
`trusted-linux-ci.yml` from `Silo-Server/silo-server@main`, runs only when
`github.repository == 'Silo-Server/silo-server'`. That reusable workflow
takes its tools from Silo's `main` branch, not from the Silo revision Bloem
has merged, and it knows none of Bloem's gates, so on Bloem it would check the
wrong thing. Skipping it costs a one-line seam. Before October 26, 2026, Bloem's
jobs lived in its copy of `ci.yml`, an 880-line seam that conflicted with
every upstream CI change. `dcbd1c525` moved them unchanged into
`bloem-ci.yml`. Bloem Image and the upstream sync merge wait on "Bloem CI" by
name.

### Triggers and cancellation

Bloem CI runs on every pull request, on pushes to `main` and to `apiv2` (the
API v2 integration branch), every Monday at 04:23 UTC, and by hand.

Runs share the concurrency group `bloem-ci-<event>-<ref>` with
`cancel-in-progress: true`. A newer run for the same event and ref cancels
the older one, on `main` too. A cancelled run is not a success, so Bloem
Image publishes nothing for its commit: on October 27, 2026, two
documentation pushes to `main` in quick succession were cancelled this way,
and only the last commit got an image.

No job `needs` another, so all of them start together. Every matrix uses
`fail-fast: false`, so one failing shard does not cancel the others. The
workflow's token is read-only (`contents: read`); only "Prune old Go caches"
gets `actions: write`, to delete caches.

### Where jobs run

Bloem CI's jobs carry the same runner routing as Silo's
`trusted-linux-ci.yml`. It was added on October 26, 2026 (`90e15fcfd`).
Every job except "Prune old Go caches" has this `runs-on` expression, with
its own job id in the last label. It is shown here on several lines for
reading; the workflow has it on one line:

```text
vars.BLOEM_SELF_HOSTED == 'true'
  && ((github.event_name == 'pull_request'
       && contains(fromJSON(vars.BLOEM_CI_MEMBERS || '[]'), github.event.pull_request.user.login))
      || (github.event_name != 'pull_request' && github.ref == 'refs/heads/main'))
  && fromJSON(format('["self-hosted","linux","x64","bloem-linux","bloem-run-{0}-{1}","bloem-job-go-lint"]',
                     github.run_id, github.run_attempt))
  || 'ubuntu-24.04'
```

| Run | Runner |
|---|---|
| Any run while the repository variable `BLOEM_SELF_HOSTED` is not `true` | GitHub's `ubuntu-24.04` |
| A push to `main`, the weekly schedule, or a manual run on `main` | Self-hosted |
| A pull request opened by a login listed in the repository variable `BLOEM_CI_MEMBERS` (a JSON array) | Self-hosted |
| Any other pull request, and pushes to `apiv2` | GitHub's `ubuntu-24.04` |

The expression looks at the pull request author's login, not at whether the
pull request comes from a fork. The variables are repository settings and are
not recorded in this repository. On October 27, 2026, the runs on `main` used
self-hosted runners for every job but the cache prune.

The self-hosted runners are private and disposable. A runner controller
outside this repository starts a fresh runner for each job. The runner accepts
only a job that carries its own run's `bloem-run-<run id>-<attempt>` label and
its `bloem-job-<job id>` label, and it is not reused afterwards. The
controller applies the same routing rule from its own configuration and is the
authority, so the workflow expression and the controller must be changed
together. All shards of a matrix job ask for the same `bloem-job-<job id>`
label.

What differs by runner:

- The self-hosted image already has libvips and a warm Go cache. The libvips
  install exits early when `pkg-config --exists vips` succeeds. The Actions
  caches are restored and saved only on GitHub-hosted runners
  (`runner.environment == 'github-hosted'`): the Go build and module caches,
  the libvips packages, the golangci-lint analysis cache, and the web check,
  TypeScript and pnpm caches. Two caches are used on both: the golangci-lint
  binary, and the Web test job's pnpm store.
- "Keep the Go build cache on disk" runs only on GitHub-hosted runners, where
  Silo's setup action puts the Go build cache on a tmpfs that Bloem's suite
  outgrows.
- "Prune old Go caches" always runs on `ubuntu-24.04`, and only for runs on
  the default branch that are not pull requests.

Bloem Image, Upstream sync and Upstream sync merge run on GitHub's
`ubuntu-latest`. Before `90e15fcfd`, Bloem CI also ran on `ubuntu-latest`;
`ubuntu-24.04` replaced it to match Silo ([changelog](CHANGELOG.md)).

### Jobs

"Bloem CI" has the jobs Bloem's copy of `ci.yml` ran (only the workflow
name and concurrency group changed), with the slowest jobs split across
runners on October 26, 2026 (`52369dbad`, `5e6f9e407`):

| Job | What it gates |
|---|---|
| Prune old Go caches | Keeps the newest Go cache per job on the default branch. |
| Tenant identity (disposable PostgreSQL) | Tenant, access-group and profile-tenancy migrations and stores, race-checked resourcetenancy, access and policy, the v1 tenancy and OPA acceptance suites, and the multitenant administration release gate. |
| Tenant stores race (1-4/4, disposable PostgreSQL) | `internal/tenancy` under the race detector, its tests split four ways. |
| Native authentication (1-3/3, disposable PostgreSQL) | `internal/auth` with and without the race detector, its tests split three ways, and its vet (shard 1). |
| Go lint | gofmt, `go vet ./...`, govulncheck, changed-line lint (`scripts/bloem-lint-changed-ci.sh`) and the full-tree router recovery lint (`make lint-router-recovery`). |
| Go contract checks | Settings bindings, client DTOs, playback fixtures, route inventory, the API v2 OpenAPI, the Bloem native OpenAPI and API catalogue, the v2 contract diff and fixtures, the seam ledger, the local gate's coverage of this workflow (`make verify-local-gate`), and the Go test shards' database-list skip (`make verify-db-list-skip`). |
| Go test (1-4/4) | `make test-go` on a database `cmd/bloem-testdb` prepared, its packages split four ways, four at a time per database. Silo's database-list tests are left to Go database pins (below). |
| Go database pins | The database pins (`make test-db-pins`) and the upstream database regression gates. |
| Go database regressions | Watch Party and history-import race tests and the mixed library location pins. |
| Web (build, checks) | ESLint and format, typecheck and build, the launch bundle budget, the web settings binding and v2 types, and `make verify-local-paths`, which first runs the upstream sync merge helper's tests. On a push to `main` or `apiv2`, the build part uploads `frontend-dist-<sha>`, which Bloem Image publishes. |
| Web test (1-4/4) | Vitest in four shards. |

Go lint builds golangci-lint from source at the pinned `GOLANGCI_LINT_VERSION`
(`v2.12.2` at `b6039036d`), because a released binary refuses a Go newer than
the one it was built with. It caches the binary and reuses it only when it was
built with the current toolchain.

Changed-line lint reports only findings on lines that changed in the push or
pull request and that differ from Silo's copy of the file, so Silo's own lint
debt does not fail Bloem. An upstream sync merge is linted only on its
conflict-resolution lines.

The Go test shards skip Silo's database-list tests, the ones named in
`scripts/ci/db-pins.txt` and `scripts/ci/db-contracts.txt`. Silo's own
`make test-go` has no database, so Silo runs these only through
`scripts/ci/dbpins`, and some assume nothing else writes to the database while
they run: the marker population sync walks every eligible file in it, and
failed on November 1 and 2, 2026 when another package's files appeared
mid-test. Go database pins runs both lists on a fresh database, as Silo does,
and since Silo `51b1e0a40` the runner gives each package its own copy of that
database, so the shards pass `-skip` with the pattern
`scripts/ci/bloem-db-list-skip.py` prints. The script refuses a list name that
is also a test in a package the lists do not name, because `-skip` matches
names, not packages, and that test would then run nowhere.

The split jobs each run against their own PostgreSQL.
`scripts/ci/deal.sh N I` deals lines round-robin to N shards so each lands in
exactly one: Go test deals `make go-test-packages` with it, and
`scripts/ci/go-test-shard.sh PKG N I` deals a package's tests to print a `-run`
pattern. `scripts/ci/go-test-shard_test.sh` checks both. CI does not run that
test; run it by hand with `bash scripts/ci/go-test-shard_test.sh`.

The Go contract checks job byte-compares the Bloem native OpenAPI artifact
(`make verify-bloem-openapi`, `contracts/api/bloem/v1/openapi.json`) and
[the API catalogue](api-catalogue.md) generated from it
(`make verify-bloem-api-catalogue`) with a fresh generation. Both became CI
steps on October 27, 2026; before that the catalogue could fall behind the
OpenAPI artifacts without failing anything, and on that date it had.
`make verify-bloem-api-catalogue` also checks the per-topic API pages
(`scripts/bloem/api-topic-docs.py --check`) and the explainer pictures
(`scripts/bloem/draw-pictures.py --check`); see
[writing Bloem documentation](documentation-workflow.md#pictures).

### Local gate

`scripts/bloem/local-gate.py` runs every job in the table above on one
machine, in this order of phases: `build` (gofmt, build, vet), `verify` (the Go
contract checks, client coverage, local paths), `lint`, `web` (the web checks
and the four Vitest shards), `go` (the four Go test shards), `db` (the database
pins and regressions) and `tenancy` (Tenant identity, Tenant stores and Native
authentication). Commands assume the
repository root is the cwd:

```sh
python3 scripts/bloem/local-gate.py run                     # every phase, compared with origin/main
python3 scripts/bloem/local-gate.py run --base <sha> --only go,scenarios
python3 scripts/bloem/local-gate.py list                    # every step and its command
eval "$(python3 scripts/bloem/local-gate.py services up)"   # the databases alone, for debugging
python3 scripts/bloem/local-gate.py services down "$GATE_SERVICES_ID"
```

- It starts one disposable `pgvector/pgvector:pg18` with CI's
  `max_connections=300` and `max_locks_per_transaction=512`, and one Redis, on
  free localhost ports under container names unique to the run, and removes
  both when the run ends. Each job that CI gives its own database gets a fresh
  database of the same name here, and the Go tests run in CI's four shards.
- Every step runs under `umask 022`, with `GOWORK=off` and the Go toolchain
  `go.mod` names. Changed-line lint builds the pinned golangci-lint with that
  toolchain once and keeps it in the user's cache directory.
- `--base` is the commit changed-line lint and the v2 contract diff compare
  with: the old `main` when checking a merge.
- It keeps going after a failure, prints one `GATE PASS` or `GATE FAIL` line
  per step with the step's log file, and exits non-zero when any step failed.
- `make verify-local-gate`, a Go contract check, fails when this workflow
  gains a `make` target or a `go test` the gate does not run.
- The merge bot runs only its fast phases on each batch,
  `--only build,verify,lint` with `--base` set to the `main` the batch
  started from. Bloem CI runs the full
  suite on the pushed commit. After a fix the bot reruns, with `--only`, the failed
  phases and every phase the fix can reach.

### Failures and re-runs

Nothing in the workflows retries a failed test or job. The merge bot's one
re-run of a batch it pushed is its own ([below](#the-merge-bot)). When a job fails
for a reason that has nothing to do with the change, such as a flaky test or a lost
runner, the run is re-run with GitHub's **Re-run failed jobs**
(`gh run rerun <run id> --failed`). That starts attempt 2 of the same run.
The failed jobs run again, and the jobs that passed keep their attempt-1
results. Because the self-hosted labels include the attempt number, re-run
jobs on self-hosted runners ask for `bloem-run-<run id>-2` and get new
runners. When the attempt completes, Bloem Image and Upstream sync merge fire
again and act on the run's new conclusion.

A re-run does not rebuild the web bundle unless the Web (build) job is re-run
too. The `frontend-dist-<sha>` artifact is kept for one day
(`retention-days: 1`). A Bloem Image run more than a day after the build finds
no bundle to download until Web (build) runs again.

A re-run is for failures that are not about the change. A test that fails
again, or fails often, is a bug to fix. Recent examples:

- **Shared test database, October 27, 2026.** On `422a51fed`, attempt 1 of
  Go test (4/4) lost a cohort test in `internal/entitlements`. Libraries are
  global, and another package in the same shard added or deleted one while
  the cohort tests compared library sets. The tests passed on re-run.
  `f1d33d33f` gives the package its own database
  (`bloemtestdb.RunIsolatedSuite`).
- **Lost runner, October 27, 2026.** On `276becf4a`, the runner for Go test
  (2/4) powered off mid-run. It was gone before anyone could inspect it, so
  the cause is not known. Attempt 2 passed
  ([receipt](../operations/2026-10-27-main-deployment.md#follow-up-276becf4a)).
- **Daylight saving, October 26, 2026.** Silo's
  `TestLogPartitionsMatchPartmanOnNonUTCServer` (`internal/database`, run by
  Go test) filled its window with `now() + interval '8 days'` on a session in
  America/New_York. PostgreSQL adds days at wall-clock time, so across the
  2026-11-01 fall-back that is eight days and an hour. On a run that started
  after 23:00 UTC, the last hourly row fell past partman's last UTC partition
  and into the default partition. Bloem CI failed it at 23:20 UTC, while
  Silo's CI had passed the same commit earlier that day. `48513c1bd` made the
  windows hour-based (`interval '192 hours'` and `'1344 hours'`), which
  PostgreSQL adds as absolute time; partitioning itself is unchanged. That was
  a temporary Bloem edit to a Silo test file. Silo has since made the same fix
  (`cd64d10cc`, #3258, October 31), so
  `internal/database/log_partition_timezone_test.go` is Silo's bytes again and
  no longer a seam.

### Bloem Image

`.github/workflows/bloem-image.yml` ("Bloem Image") publishes the production
image for a commit that passed Bloem CI on `main`. It was added on October 11,
2026 (`04a69c0fe`). Since `dcbd1c525` (October 26) it waits on "Bloem CI".
`422a51fed` (October 27) was the first production deployment of an image this
workflow published ([receipt](../operations/2026-10-27-main-deployment.md#build)).

**Triggers.** A Bloem CI run completing on `main` starts it. Its one job,
`publish`, runs only in `Bloem-Studios/bloem-server`, and only when the CI
run concluded `success`, was started by a `push` (not the schedule, a manual
run or a pull request), and came from this repository. Any other completion on
`main` shows as a skipped Bloem Image run. It can also be started by hand on
`main`. It then looks up a successful Bloem CI push run on `main` for that
commit and stops if there is none. Runs queue in the group `bloem-image-main`
and are never cancelled. The workflow token has no permissions by default; the
job gets `contents: read`, `actions: read` and `packages: write`, and a
90-minute timeout.

**Steps.**

1. Check the source: the commit must be a full 40-character SHA, and the CI
   run must be a completed, successful `push` run of
   `.github/workflows/bloem-ci.yml` on `main` in this repository for exactly
   that commit.
2. Check out that commit, with no stored credential.
3. Download that CI run's `frontend-dist-<sha>` artifact into `web/dist`.
4. Log in to GitHub Container Registry with the workflow's own token
   (`packages: write`), then build and push with Buildx:
   - platform `linux/amd64` only;
   - tag `ghcr.io/bloem-studios/bloem-server:<full sha>`;
   - OCI labels for the revision, the build time and the source repository;
   - build arguments `BUILD_REVISION=<sha>`, `BUILD_DIRTY=false` and
     `BUILD_DATE`, which the Dockerfile links into `internal/buildinfo`, so
     the server's system info reports the revision.
5. Point `latest` at that image, but only if the commit is still `main`'s head
   (added in `6d0adcfe9`, 2026-10-27).

**How the web bundle gets in.** Silo's `Dockerfile`, which Bloem does not
change, has a `frontend_dist` stage. By default that stage holds the bundle
the in-image `frontend` stage builds, and the Go build stage embeds it
(`COPY --from=frontend_dist / web/dist`). Bloem Image passes
`build-contexts: frontend_dist=./web/dist`, which replaces that stage with the
bundle the Web (build) job produced. That is the same bundle that job
type-checked and held to the launch budget. Web (build) uploads it only on
pushes to `main` and `apiv2`, under the name `frontend-dist-<sha>`, for one
day.

**What it does not do.**

- It publishes no `build-<n>` or short-SHA tag and no arm64 image. A late run
  for an older commit leaves `latest` for the newer commit's own run, so
  `latest` never moves backwards. If the newest commit's CI fails, `latest`
  stays on the last image published while its commit was the head.
  `.env.bloem.example` sets `SILO_IMAGE` to `latest` for
  `docker-compose.advanced.yml`; deployments that must be attributable pin a
  full-SHA tag
  ([deployment guide](overlays/wiki/deployment/docker.md#container-image-selection)).
- It builds Bloem's server image only. It does not build Silo's all-in-one
  image, which bundles PostgreSQL, Redis and Meilisearch.
- It does not deploy. Deployment is a separate, manual step, recorded in
  [`docs/operations/`](../operations/). Production ran images this workflow
  published from October 27, 2026. `9cc80cd33` on the morning of October 28, the
  native scan redesign of October 28 and 29, and every deployment from October 29
  through November 1 used a local build at the owner's request instead: an image
  built from a `git archive` of the exact commit with Bloem Image's build
  arguments, without waiting for Bloem CI. Each record says which way its image
  was built ([release versioning overlay](overlays/release-versioning.md#bloem-deployment-records)).
- The image's Go build stage uses the Dockerfile's floating `golang:1.26` tag
  (Silo's choice), not the exact patch `go.mod` names, which the CI jobs use.

### Tools on the Go runners

`scripts/ci/bloem-install-test-tools.sh` installs what the Go jobs need
beyond libvips. Go test, Go database pins and Go database regressions call it
with `ffmpeg`. It was added on October 14, 2026 (`568bcfc5d`).

- **FFmpeg:** the native audio and video tests generate fixtures with real
  FFmpeg. The native audiobook regressions fail without it, and FFmpeg tests
  elsewhere skip. The script adds FFmpeg to `PATH` for later steps.
- **ripgrep:** the script can still install it, for
  `scripts/generate-bloem-storage-proto_test.sh`, which matches the
  generator's output with `rg`. No CI step or Go test runs that script, so
  since October 27, 2026 CI no longer asks for ripgrep. It was first
  installed for the compatibility-removal gate, which `2393c41ee` retired on
  October 18, 2026.

FFmpeg is Jellyfin's build, the family the runtime image ships. CI pins
`jellyfin-ffmpeg7` 7.1.4-3, while the image ships `jellyfin-ffmpeg8` 8.1.3-1
(the Dockerfile's `JELLYFIN_FFMPEG_VERSION`). The gap is deliberate:

- FFmpeg 8 prints a stream's display matrix as `Display Matrix: rotation of N
  degrees`. FFmpeg 7 printed `displaymatrix: rotation of N degrees`.
- Silo's input parser in `internal/mediasample` (`probe.go`) reads only the
  FFmpeg 7 spelling. Under FFmpeg 8 it misses the rotation, so trickplay
  sheets for rotated (phone) video get landscape tile heights.
- That parser is Silo-owned code. The fix is a one-line change upstream
  (accept both spellings). Once Silo merges it, move the CI pin to the
  Dockerfile's version.
- This is a production bug, not only a CI gap: the shipped image runs
  FFmpeg 8, so rotated video gets wrong trickplay tiles in production until
  the parser is fixed. At `63ef34b23`, Silo's parser still reads only the
  FFmpeg 7 spelling.
- `TestBloemCIFFmpegPinTracksTheDisplayMatrixParse` in `internal/mediasample`
  (`568ae5299`) ties the pin to that reason. While the parser misses FFmpeg
  8's spelling, the test skips and names the bug. Once the parser reads both
  spellings, it fails until the install script's FFmpeg major matches the
  Dockerfile's.

When the pin was set, `TestGenerateRotatedManifestEndToEndDB` was the test
that failed under FFmpeg 8. Silo removed that test on October 21, 2026, in
"remove low-value and never-run tests" (#2817, `174fb9a44`), and Bloem has
merged that change. No current test is known to fail under FFmpeg 8; that has
not been re-checked since the removal.

When the pin was set, everything else that uses FFmpeg passed on 8.1.3 too,
including the HDR software tone-map acceptance test. Its fixture tags frames
through `setparams`, because FFmpeg 8 ignores `-color_trc` and related options
when the frames carry their own color properties.

### Go patch

Silo's setup action (`.github/actions/setup-go`) installs the newest Go 1.26
patch. Bloem's lint, contract-check, test and scenario jobs then install
exactly the patch `go.mod` names (step `Use the Go patch go.mod requires`;
`go 1.26.10` at `b6039036d`; added `d72f5884f`, October 9, 2026). The tenant
and native authentication jobs install that patch directly
(`actions/setup-go` with `go-version-file: go.mod`). A security release
therefore takes effect when `go.mod` moves, not when runner images catch up.
`go tool govulncheck` reads the standard library of that patch, so a new
standard-library advisory fails the Go lint job until `go.mod` names the fixed
patch.

### Upstream sync merges

Since November 2, 2026 the merge bot takes in every Silo batch. The two GitHub
workflows that used to merge Silo are switched off, because two automatic mergers
must never race.

#### The merge bot

It runs on Bloem's build machine, outside GitHub Actions. Its configuration,
scripts and prompts live in Bloem's private infrastructure repository; nothing in
this repository starts it. It uses two scripts from this repository:
`scripts/bloem/silo-merge-review.py` and `scripts/bloem/local-gate.py`.

1. **The check.** A timer runs it every 10 minutes. It runs no model and starts no
   thread. It fetches Bloem's and Silo's `main`, and acts only when Silo's tip is
   not in Bloem's `main` and that tip's commit time is at least 10 minutes old. It
   then takes a lock, one batch at a time, and starts the batch. The check also
   reports a batch nobody picked up, a stopped batch that sent no message, a running
   batch silent for four hours, and its own repeated failures.
2. **The batch thread.** One T3 thread per batch, titled "Silo sync <sha9>", in its
   own worktree on `merge/upstream-<sha9>` from `origin/main`. The batch is exactly
   the Silo commit the check found; later commits wait. If that commit is already in
   `main`, the batch ends with nothing to do.
3. **Merge.** `git merge --no-ff --no-commit <batch>`; each conflicted hunk takes
   Silo's side verbatim, Bloem's behavior goes back on Bloem's own added lines or in
   a Bloem-owned file or field, and a Silo deletion wins. When keeping Bloem working
   would need a change to one of Silo's lines, the batch stops. The merge commit is
   titled "Merge Silo upstream through <sha9>".
4. **Review.** `python3 scripts/bloem/silo-merge-review.py --base <old main> --silo <batch>`
   lists the seams the batch touches, Bloem files beside or calling changed Silo code,
   overlays to bring in line, Bloem pages that mention changed files, and the
   batch's migrations. Each item gets a line saying why Bloem still works or what
   breaks; a break is fixed on Bloem's own lines with a test that fails first.
5. **Regenerate and document.** `make client-dtos`, `make playback-fixtures`, the API
   catalogue, coverage reasons in `contracts/client/v1/coverage.json` with
   `make client-digest`; then the overlays and Bloem pages the merge affects, with
   pictures redrawn, and the status rows that say which Silo commit Bloem includes.
6. **Fast checks.** `python3 scripts/bloem/local-gate.py run --base <old main> --only build,verify,lint`
   (the build, gofmt, vet, every contract and verify target and changed-line lint).
   Bloem CI runs the
   full suite on the pushed commit, and the deploy refuses any commit Bloem CI has not
   passed. A failed test is rerun alone once; a real failure gets at most three fix
   attempts. If `main` moved meanwhile, it is merged in and the fast checks run again;
   a third move stops the batch.
7. **Push and wait.** `git push origin HEAD:main`, never forced. The bot then waits
   for the Bloem CI run on that exact commit. A failed run is rerun once, failed jobs
   only (`gh run rerun <id> --failed`), unless `main` has moved on, since a re-run
   would cancel the newer push's run. A run cancelled by a newer push, or failed with
   `main` moved on, is followed to the new tip after checking it still contains the
   pushed commit. Red twice: a test that also fails alone is fixed and pushed again,
   at most three fix pushes per batch; anything else stops the batch. The bot never
   pushes an empty commit or starts CI by hand to turn it green.
8. **Deploy.** Only a commit Bloem CI passed, and the new tip may carry other
   commits pushed to `main`, which the receipt names. One fixed deploy script builds
   the image from a `git archive` of the commit, loads it on the server and both
   transcode nodes, takes a server snapshot and a database restore point, refuses to
   restart unless production runs the expected previous release, applies migrations
   while the server is stopped, restarts and checks readiness, health, the version,
   the logs and the transcode nodes. A failed check rolls the image back by itself,
   whether or not migrations ran. Only when the host stops answering during the
   restart, or the rollback itself fails, is nothing more tried and production left
   to the owner. The bot never restores the database. The operator can override the
   CI check by hand with the script's operator-only `--skip-ci-check`, which the bot
   never passes; that happened once, for `c5a2b43f3`
   ([receipt](../operations/2026-11-02-silo-sync-56e6f871c-deployment.md)).
9. **Receipt and message.** `docs/operations/<date>-silo-sync-<sha9>-deployment.md`,
   the index's "current:" receipt and the front page's `Production` row, pushed to
   `main` and checked by Bloem CI too; one message (deployed, stuck or rolled back)
   with the Silo range, the user-visible changes, any Bloem workaround or new seam,
   migrations and downtime, and a link to the thread.

It stops in its thread, writes "Needs you" with what failed, what it tried and what
it suggests, and sends the stuck message when a test or check will not pass and it
cannot fix the cause, when Bloem cannot be kept working without editing Silo's lines,
when Bloem CI stays red after its fixes, and when the deploy fails or rolls back.
Migrations and active streams are not stop reasons. A stopped batch keeps the lock,
so no new batch starts until the owner answers in its thread or releases it.

#### The switch for the GitHub sync

The two Bloem workflows below, added on August 26, 2026 (`af2be39f1`), merged Silo
into Bloem without anyone having to notice that Silo moved, with Bloem CI as the
gate: nothing reached `main` unless Bloem CI passed on the exact merge. Each job's
`if:` requires the repository variable `BLOEM_UPSTREAM_SYNC` to be `true`, and it is
unset. A scheduled sync run therefore starts and skips its job; a sync started by
hand, dry run included, still runs and opens its pull request; and the merge gate
skips every Bloem CI completion, so a sync pull request is merged by a person. The
rest of this section describes both workflows as they run when switched on.

#### The daily sync: `upstream-sync.yml`

When switched on, it runs daily at 05:20 UTC. GitHub can start scheduled runs hours late; from
October 23 to 26, 2026 they started between about 09:30 and 11:15 UTC. It can
also be started by hand with `dry_run`, which reports what would happen
without pushing or opening anything. Runs queue in the group `upstream-sync`.

1. Check out the full history with no stored credential, and fetch Silo's
   `main` from `https://github.com/Silo-Server/silo-server.git`. The upstream
   remote's push URL is set to `DISABLED`.
2. Count the commits on `upstream/main` that `main` lacks. If there are none,
   stop.
3. On a new branch `upstream-sync/<UTC date>-<short upstream sha>`, run
   `git merge --no-ff --no-edit upstream/main`.
4. If the merge is clean, check that `go build ./...` succeeds. A failed
   build is reported but does not stop the run.
5. Push the branch and open a pull request against `main`:
   - **clean merge:** a normal pull request titled
     "Merge upstream Silo-Server (N commits, sha)". If Bloem CI passes on it,
     the merge gate below lands it.
   - **conflict:** the conflicted tree, conflict markers and all, is
     committed with `--no-verify` and opened as a **draft** titled
     "[CONFLICTS] …", listing the conflicting files. It never merges itself,
     even after the conflicts are resolved. Resolve them on the branch, then
     merge by hand once Bloem CI passes. This replaced an abort that only
     wrote to the job summary (`1d4e2c860`, August 27, 2026).
6. Post a summary to Discord if `DISCORD_WEBHOOK_URL` is set.

Nothing in it force-pushes or writes to `main`.

#### The merge gate: `upstream-sync-merge.yml`

It runs whenever a Bloem CI run completes, re-run attempts included. It acts
only when the switch is on and that run was for a pull request on a branch named
`upstream-sync/*`.
It checks out the default branch, never the pull request's head, so a pull
request cannot change the helper that judges it. It then fetches Silo's
`main` and runs `scripts/verify-upstream-sync-merge.sh` (`e0e95626e`,
August 29; provenance checks `1e7eb44b0`, September 18, 2026). The helper
merges only if all of these hold:

- the Bloem CI run concluded `success`;
- the head came from this repository, not a fork;
- there is exactly one open pull request for that branch, and it is not a
  draft;
- the pull request's head is the exact commit Bloem CI tested;
- that commit is a two-parent merge whose first parent is on `main` and whose
  second is on Silo's `main`, and its tree is exactly the conflict-free merge
  `git merge-tree --write-tree` reproduces from those parents. A hand
  resolution, an amendment or an extra commit therefore leaves the pull
  request for a person.

It merges with `gh pr merge --match-head-commit <sha> --merge --delete-branch`.
That is a merge commit, never a squash: squashing would make the next merge
base treat every upstream commit as unmerged. The result is posted to Discord
if the webhook is set. `make verify-upstream-sync-merge`, part of
`make verify-local-paths`, tests the helper in the Web (checks) job.

The gate exists instead of GitHub's native auto-merge. Native auto-merge waits
for CI only when `main` has branch protection with required checks, and that
protection would also block direct pushes to `main`, which is how this
repository is normally worked.

#### The sync token

Both workflows push and merge with the `UPSTREAM_SYNC_TOKEN` repository
secret, a personal access token with contents, pull requests and workflows
write on this repository. Without it they fall back to `GITHUB_TOKEN`
(`1c3f61581`, October 25, 2026):

- GitHub does not start workflows for events raised by `GITHUB_TOKEN`, so a
  pull request it opens runs no CI until someone pushes to it or reopens it.
  The pull request body says so.
- `GITHUB_TOKEN` can never push or merge a commit that changes
  `.github/workflows`, whatever the job's permissions. Without the secret, a
  sync that brings in such a Silo commit fails at its push. Before
  `1c3f61581`, the daily sync's push failed this way once the merge carried
  Silo's changes to `.github/workflows/macos.yml`.
- A merge pushed with the token starts Bloem CI on `main`, and so Bloem Image.

The sync's checkout keeps no credential. The push supplies the token for that
one command, so it is not on disk while the merged upstream tree builds.

A sync pull request is opened by the token's account. It runs on self-hosted
runners only if that login is in `BLOEM_CI_MEMBERS`
([Where jobs run](#where-jobs-run)).

### PostgreSQL version

Every PostgreSQL service in `bloem-ci.yml` runs `pgvector/pgvector:pg18`, the
major production runs. `TestCIPostgresServicesMatchTheDeploymentMajor` in
`internal/api` keeps them equal (`9f7a9a3ee`, October 14, 2026): it reads
`ARG PG_MAJOR=` from `omnibus/Dockerfile`, the PostgreSQL major Silo's
all-in-one image bundles, and requires every CI service to run
`pgvector/pgvector:pg<major>` (18 at `07f0ae21c`). Bloem's own
`docker-compose.advanced.yml` runs `pgvector/pgvector:pg18` as well. PostgreSQL
18 reports an `ON DELETE RESTRICT` violation as SQLSTATE 23001, where 17
reports 23503, so a 17 service hides behavior production has.

### Mixed library location pins

`scripts/ci/bloem-library-locations.txt` lists the required mixed-location and
native audiobook regressions. `make test-bloem-library-locations` runs them
through the same runner as `make test-db-pins`, so a missing, skipped or
failing test fails the run. The Go database regressions job runs it last,
against its own `bloem_storage_test_ci` database and a Redis service. The
tests need:

- the `integration` build tag, which the target sets;
- a migrated database whose name starts with `bloem_storage_test_`, which the
  tagged tests clone as a template. Packages therefore run one at a time, and
  the target passes the runner `-shared`: without it the runner hands each
  package a `dbpins_*` copy, whose name the tests refuse;
- `SILO_TEST_REDIS_URL=redis://127.0.0.1:65421/0` on a disposable Redis. The
  tests flush it, so they refuse any other address unless
  `SILO_TEST_REDIS_DISPOSABLE_ADDR` names that same loopback address: the
  local gate sets it for the Redis it starts on a free port;
- a `$HOME` whose ancestors are not group- or world-writable, because the
  native S3 state directory checks refuse unsafe ancestors;
- FFmpeg on `PATH`.

Locally, create a disposable database with that prefix and run
`go run ./cmd/bloem-testdb` against it. Then run the target with both URLs
set.

### Tests that read the workflow

Go tests in `internal/api` parse `bloem-ci.yml`, so some workflow edits fail
Go test:

- `TestCIPostgresServicesMatchTheDeploymentMajor`: every PostgreSQL service
  runs the deployed major.
- `TestBloemFoundationCIRequiresDisposablePostgres`: Tenant identity and
  Tenant stores run against a disposable pgvector PostgreSQL, and each Tenant
  stores shard passes the matrix's shard count to `go-test-shard.sh`.
- `TestCIChangedLineLintNeedsNoRepositoryCredential`: changed-line lint works
  from the credential-free checkout.

Related pages:

- [How Bloem keeps up with Silo](../architecture/bloem-following-silo.md): each
  Silo batch, its deploy and the rule that Silo wins every conflict.
- [FORK.md](../../FORK.md): fork provenance and merge policy.
- [Docker deployment guide](overlays/wiki/deployment/docker.md): choosing and
  pinning an image.
- [Release versioning overlay](overlays/release-versioning.md): Bloem's image
  tags, releases and deployment records.
- [Developing Bloem Server](overlays/DEVELOPMENT.md): local test databases and
  the checks to run before pushing.

The pictures are SVG files under `docs/architecture/images/bloem-ci/`, drawn by
`scripts/bloem/pictures/bloem_ci.py` through `scripts/bloem/draw-pictures.py`.
Redraw them there, not by hand.
