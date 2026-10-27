# Bloem CI

This page describes the GitHub Actions workflows that test Bloem Server, publish
its image and keep it merged with Silo. It reflects `main` at `e852f92bf`
(October 27, 2026; the last code change is `276becf4a`). Everything here is
Bloem's own, except Silo's `ci.yml`, which is described only where it affects
Bloem. Commands assume the repository root is the cwd.

Terms used below:

- **Silo** is the upstream server, `Silo-Server/silo-server`. Bloem is a
  *tracking fork*: it merges Silo's `main` regularly and keeps its own changes
  out of Silo's files where it can
  ([FORK.md](../../FORK.md#this-is-a-tracking-fork-deliberately)).
- A **seam** is a Silo-owned file that Bloem has changed. Each one is listed in
  the seam ledger, [`contracts/seams.txt`](../../contracts/seams.txt) (305
  entries at `7c431d470`), and `make verify-seams` fails on a change the
  ledger does not list.
- A **disposable runner** is a private self-hosted runner that runs one job
  and is not reused. See [Where jobs run](#where-jobs-run).

## Workflows

| Workflow | File | Runs on | What it does | Origin |
|---|---|---|---|---|
| Bloem CI | `bloem-ci.yml` | Pull requests; pushes to `main` and `apiv2`; Mondays at 04:23 UTC; manual | Runs every Bloem gate ([Jobs](#jobs)) | Bloem, 2026-10-26 (`dcbd1c525`) |
| Bloem Image | `bloem-image.yml` | A Bloem CI run on `main` completing; manual | Publishes `ghcr.io/bloem-studios/bloem-server:<full sha>` ([Bloem Image](#bloem-image)) | Bloem, 2026-10-11 (`04a69c0fe`) |
| Upstream sync | `upstream-sync.yml` | Daily at 05:20 UTC; manual, with a dry run | Merges Silo's `main` on a branch and opens a pull request | Bloem, 2026-08-26 (`af2be39f1`) |
| Upstream sync merge | `upstream-sync-merge.yml` | Any Bloem CI run completing | Merges a sync pull request whose exact head passed Bloem CI | Bloem, 2026-08-26 (`af2be39f1`) |
| CI | `ci.yml` | As in Silo | Nothing on Bloem: its one job runs only in Silo's repository ([below](#silos-ciyml)) | Silo's file; a one-line seam |

The other files in `.github/workflows/` are Silo's and byte-identical to
Silo's `main` at `73a6ae6a3`. None of them tests or publishes Bloem. Silo's
`docker.yml` publishes only after a successful "CI" run, and on Bloem every
"CI" run concludes `skipped`, so Silo's image job is skipped too. The
reusable `trusted-linux-build.yml` runs only in Silo's repository, and
`macos.yml` and `release.yml` run only when started by hand.

## Silo's `ci.yml`

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

## Triggers and cancellation

Bloem CI runs on every pull request, on pushes to `main` and to `apiv2` (the
API v2 integration branch), every Monday at 04:23 UTC, and by hand.

Runs share the concurrency group `bloem-ci-<event>-<ref>` with
`cancel-in-progress: true`. A newer run for the same event and ref cancels
the older one, on `main` too. A cancelled run is not a success, so Bloem
Image publishes nothing for its commit: on October 27, 2026, two
documentation pushes to `main` in quick succession were cancelled this way,
and only the last commit got an image.

No job `needs` another, so all of them start together. Every matrix uses
`fail-fast: false`, so one failing shard does not cancel the others.

## Where jobs run

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

## Jobs

"Bloem CI" has the jobs Bloem's copy of `ci.yml` ran (only the workflow
name and concurrency group changed), with the slowest jobs split across
runners on October 26, 2026 (`52369dbad`, `5e6f9e407`):

| Job | What it gates |
|---|---|
| Prune old Go caches | Keeps the newest Go cache per job on the default branch. |
| Tenant identity (disposable PostgreSQL) | Tenant, access-group and profile-tenancy migrations and stores, race-checked resourcetenancy, access and policy, the v1 tenancy and OPA acceptance suites, and the multitenant administration release gate. |
| Tenant stores race (1-4/4, disposable PostgreSQL) | `internal/tenancy` under the race detector, its tests split four ways. |
| Native authentication (1-3/3, disposable PostgreSQL) | `internal/auth` with and without the race detector, its tests split three ways, and its vet (shard 1). |
| Go lint | gofmt, `go vet ./...`, govulncheck, changed-line lint (`scripts/bloem-lint-changed-ci.sh`) and the full-tree router recovery lint. |
| Go contract checks | Settings bindings, client DTOs, playback fixtures, route inventory, migration ledger, scenario catalogs, offline routes, the API v2 OpenAPI, contract diff and fixtures, and the seam ledger. |
| Go test (1-4/4) | `make test-go` on a database `cmd/bloem-testdb` prepared, its packages split four ways, four at a time per database. |
| Go database pins | The database pins (`make test-db-pins`) and the upstream database regression gates. |
| Go database regressions | Watch Party and history-import race tests and the mixed library location pins. |
| Scenario executor | `make test-scenarios`, with a 45-minute job timeout. |
| Web (build, checks) | ESLint and format, typecheck and build, the launch bundle budget, the web settings binding and v2 types, and `make verify-local-paths`, which first runs the upstream sync merge helper's tests. On a push to `main` or `apiv2`, the build part uploads `frontend-dist-<sha>`, which Bloem Image publishes. |
| Web test (1-4/4) | Vitest in four shards. |

Changed-line lint reports only findings on lines that changed in the push or
pull request and that differ from Silo's copy of the file, so Silo's own lint
debt does not fail Bloem. An upstream sync merge is linted only on its
conflict-resolution lines.

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

## Failures and re-runs

Nothing in the workflows retries a failed test or job. When a job fails for a
reason that has nothing to do with the change, such as a flaky test or a lost
runner, the run is re-run with GitHub's **Re-run failed jobs**
(`gh run rerun <run id> --failed`). That starts attempt 2 of the same run.
The failed jobs run again, and the jobs that passed keep their attempt-1
results. Because the self-hosted labels include the attempt number, re-run
jobs on self-hosted runners ask for `bloem-run-<run id>-2` and get new
runners. When the
attempt completes, Bloem Image and Upstream sync merge fire again and act on
the run's new conclusion.

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
  Silo's CI had passed the same commit earlier that day. `48513c1bd` makes the
  windows hour-based (`interval '192 hours'` and `'1344 hours'`), which
  PostgreSQL adds as absolute time. Partitioning itself is unchanged. This is a
  Bloem edit to a Silo test file, so the seam ledger lists it as temporary,
  to be dropped once Silo fixes its copy. At `3e2d306b2`, Silo's copy still
  uses `interval '8 days'`.

## Bloem Image

`.github/workflows/bloem-image.yml` ("Bloem Image") publishes the production
image for a commit that passed Bloem CI on `main`. It was added on October 11,
2026 (`04a69c0fe`). Since `dcbd1c525` (October 26) it waits on "Bloem CI".
`422a51fed` (October 27) was the first production deployment of an image this
workflow published ([receipt](../operations/2026-10-27-main-deployment.md#build)).

**Triggers.** A Bloem CI run completing on `main` starts it. Its one job,
`publish`, runs only in `Bloem-Studios/bloem-server`, and only when the CI
run concluded `success`, was started by a `push` (not the schedule, a manual
run or a pull request), and came from this repository. Any other completion on
`main` shows as a skipped Bloem Image run. It can also be started by hand on `main`.
It then looks up a successful Bloem CI push run on `main` for that commit and
stops if there is none. Runs queue in the group `bloem-image-main` and are
never cancelled.

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

- It publishes no `build-<n>` or short-SHA tag and no arm64 image. Its last
  step, added in `6d0adcfe9` (2026-10-27), points `latest` at the image it
  just pushed, but only if that commit is still `main`'s head; a late run for
  an older commit leaves `latest` for the newer commit's own run, so `latest`
  never moves backwards. If the newest commit's CI fails, `latest` stays on
  the last image published while its commit was the head. `docker-compose.yml`
  defaults `SILO_IMAGE` to `latest`; deployments that must be attributable pin
  a full-SHA tag
  ([deployment guide](overlays/wiki/deployment/docker.md#container-image-selection)).
- It does not deploy. Deployment is a separate, manual step, recorded in
  [`docs/operations/`](../operations/).
- The image's Go build stage uses the Dockerfile's floating `golang:1.26` tag
  (Silo's choice), not the exact patch `go.mod` names, which the CI jobs use.

## Tools on the Go runners

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
  the parser is fixed.
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

## Go patch

Silo's setup action (`.github/actions/setup-go`) installs the newest Go 1.26
patch. Bloem's lint, contract-check, test and scenario jobs then install
exactly the patch `go.mod` names (step `Use the Go patch go.mod requires`;
`go 1.26.10` at `e852f92bf`; added `d72f5884f`, October 9, 2026). The tenant
and native authentication jobs install that patch directly. A security
release therefore takes effect when `go.mod` moves, not when runner images
catch up. `go tool govulncheck` reads the standard library of that patch, so a
new standard-library advisory fails the Go lint job until `go.mod` names the
fixed patch.

## Upstream sync merges

Two Bloem workflows, added on August 26, 2026 (`af2be39f1`), merge Silo into
Bloem without anyone having to notice that Silo moved. Bloem CI is the gate:
nothing reaches `main` unless Bloem CI passed on the exact merge.

### The daily sync: `upstream-sync.yml`

It runs daily at 05:20 UTC. GitHub can start scheduled runs hours late; from
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

### The merge gate: `upstream-sync-merge.yml`

It runs whenever a Bloem CI run completes, re-run attempts included. It acts
only if that run was for a pull request on a branch named `upstream-sync/*`.
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

### The sync token

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

## PostgreSQL version

Every PostgreSQL service in `bloem-ci.yml` runs the image `docker-compose.yml`
deploys (`pgvector/pgvector:pg18`), and `TestCIPostgresServicesMatchTheDeploymentMajor`
in `internal/api` keeps them equal (`9f7a9a3ee`, October 14, 2026). PostgreSQL
18 reports an `ON DELETE RESTRICT` violation as SQLSTATE 23001, where 17
reports 23503, so a 17 service hides behavior production has.

## Mixed library location pins

`scripts/ci/bloem-library-locations.txt` lists the required mixed-location and
native audiobook regressions. `make test-bloem-library-locations` runs them
through the same runner as `make test-db-pins`, so a missing, skipped or
failing test fails the run. The Go database regressions job runs it last,
against its own `bloem_storage_test_ci` database and a Redis service. The
tests need:

- the `integration` build tag, which the target sets;
- a migrated database whose name starts with `bloem_storage_test_`, which the
  tagged tests clone as a template. Packages therefore run one at a time;
- `SILO_TEST_REDIS_URL=redis://127.0.0.1:65421/0` on a disposable Redis;
- a `$HOME` whose ancestors are not group- or world-writable, because the
  native S3 state directory checks refuse unsafe ancestors;
- FFmpeg on `PATH`.

Locally, create a disposable database with that prefix and run
`go run ./cmd/bloem-testdb` against it. Then run the target with both URLs
set.

## Tests that read the workflow

Go tests in `internal/api` parse `bloem-ci.yml`, so some workflow edits fail
Go test:

- `TestCIPostgresServicesMatchTheDeploymentMajor`: every PostgreSQL service
  runs the deployed major.
- `TestBloemFoundationCIRequiresDisposablePostgres`: Tenant identity and
  Tenant stores run against a disposable pgvector PostgreSQL, and each Tenant
  stores shard passes the matrix's shard count to `go-test-shard.sh`.
- `TestCIChangedLineLintNeedsNoRepositoryCredential`: changed-line lint works
  from the credential-free checkout.
