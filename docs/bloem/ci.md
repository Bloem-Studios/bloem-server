# How Bloem CI works

Every change to Bloem Server is checked before it can be trusted, a deployed
commit gets a public server image, and Silo's work comes in as ordinary merges.
This page walks through what runs and when, how the server image is published,
where the jobs run, how Silo's changes land, and what to do when a run fails.
Bloem's CI, image, release and upstream-sync workflows are Bloem additions; the
image and release workflows are disabled. Silo's own `ci.yml` stays as Silo wrote it
apart from one line. The technical rules are in the
[For developers](#for-developers) section.

## What runs, and when

![Two flows. Every change: a pull request, a push to main, the Monday run or a manual run starts Bloem CI. No workflow builds an image: when a commit that passed is deployed, Bloem's deploy tool, outside GitHub Actions, builds its image and publishes it once the deploy passes its checks, as release-<sha9>, the full commit SHA, latest and a dated tag; Bloem Image and Bloem Release are disabled. Every day, only when switched on (off by default): the upstream sync merges Silo into a branch and opens a pull request, Bloem CI checks it, and the merge gate lands it on main](../architecture/images/bloem-ci/workflows.svg)

Bloem has five workflows of its own. Three of them still run:

1. **Bloem CI** checks a change. It runs on every pull request, on every push to
   `main`, every Monday morning, and when someone starts it by hand. Its jobs run
   side by side: Go tests, lint, contract checks, database checks, the web build
   and the web tests. A newer run for the same branch cancels an older one.
2. **Upstream sync** merges Silo's latest work into a new branch every morning and
   opens a pull request, when it is switched on. It is off by default
   ([below](#how-silos-work-comes-in)).
3. **Upstream sync merge** lands that pull request on `main` once Bloem CI has passed
   on exactly the merge it tested. It is switched off together with the sync.

The other two have been disabled on GitHub since 2026-11-26 and no longer run:

4. **Bloem Image** used to build the **server image**, the package a server runs,
   for every push to `main` that passed Bloem CI, and moved the `edge` tag to it.
5. **Bloem Release** used to point `latest` and a dated tag at one of those images
   after a deploy.

None of the workflows builds an image now, and none deploys a server. Deploying is
a separate step that a person or a script starts, and the deploy builds the image
([below](#how-the-server-image-is-published)).

## How the server image is published

A server image is built only when a commit is deployed. Bloem's deploy tool, which
runs outside this repository and outside GitHub Actions, normally deploys only a
commit that passed Bloem CI:

1. It builds the image for that commit and runs it on Bloem's own production.
2. It checks that production is healthy on the new build. If it is not, it rolls
   back and publishes nothing.
3. Once the deploy has passed, it pushes the image to Bloem's private registry, then
   copies that same image to the public `ghcr.io/bloem-studios/bloem-server`. There
   it gets four tags: `release-` with the first nine characters of the commit, the
   full commit, `latest`, and a dated tag such as `2026.11.26` (`2026.11.26.2`, then
   `.3`, for later releases the same day).

So `latest` follows what Bloem's production runs, and a commit that is never
deployed gets no image. `edge`, which Bloem Image moved for every passing commit,
stays at `9fa2dad01`, the last image it published. Anyone who pulls `edge` should
switch to `latest` or a dated tag.

## Where the jobs run

![Three questions decide where a Bloem CI job runs: whether self-hosted runners are switched on, whether the run is on main, and whether a pull request comes from a listed member. A yes on main or from a member goes to a disposable runner; anything else runs on GitHub's ubuntu-24.04](../architecture/images/bloem-ci/runners.svg)

A job runs on one of two kinds of machine:

- **GitHub's runners**, the standard `ubuntu-24.04` machines GitHub provides.
- **Disposable runners**, Bloem's own machines. Each one runs a single job and is
  then thrown away, so nothing from one job can reach the next.

Bloem's own runners take the runs on `main` and the pull requests of listed
members, once a repository setting switches them on. Every other run, such as a
pull request from someone else, uses GitHub's runners. The service that starts the
disposable runners applies the same rule from its own settings, so the two must
change together.

Bloem's CI is moving to Bloem's own forge, Forgejo. Until it has moved, every push
to `main` also runs a copy of Bloem CI on Forgejo, on disposable runners of the same
sizes. That copy only checks: what counts, for deploys too, is still the GitHub run
([details](#the-forgejo-copy)).

## How Silo's work comes in

Bloem is a **tracking fork**: it takes in Silo's work as ordinary merges instead of
copying it by hand ([how Bloem keeps up with Silo](../architecture/bloem-following-silo.md)).
Each batch of Silo commits is merged into `main` by a merge bot that runs outside
GitHub Actions, and the rules are the same for a person doing it by hand:

1. **Silo's side wins every conflict**, word for word. Bloem's change goes on its own
   lines or in its own file, with a test where Silo's version would break Bloem.
2. **A review list** names the seams, Bloem files, overlays, pages and migrations the
   batch touches, so each can be read ([below](#the-merge-bot)).
3. **Fast local checks** run first: the build, the contract and verify checks and
   changed-line lint ([local gate](#local-gate)).
4. **Push, then wait for Bloem CI**, which runs the full suite on that exact
   commit. A merge counts as landed only when Bloem CI passes on it.

When Bloem CI stays red, or when keeping Bloem working would need an edit to one of
Silo's lines, the batch stops for a maintainer and no new batch starts.

The daily GitHub sync and its merge gate are still in the repository, switched off,
so two automatic mergers never race. A repository administrator turns both on by
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
  quickly, the second cancels the first run, and only the second commit gets a
  full result.

## Example: two pushes in quick succession

This example is made up.

1. At 10:00 Ana pushes a fix to `main`. Bloem CI starts on a disposable runner.
2. At 10:04 she pushes a typo fix. A new Bloem CI run starts for `main`, and the
   10:00 run is cancelled.
3. At 10:30 the second run passes. No image is built: the commit only becomes
   eligible to deploy.
4. At 16:00 a maintainer deploys `main`. The deploy builds the image for the 10:04
   commit, which contains both changes, and once production passes its checks the
   image is published and `latest` moves to it.
5. The 10:00 commit never gets an image of its own, and neither would the 10:04
   one if a newer commit had been deployed instead.

## Who can change what

| Who | What they manage |
| --- | --- |
| A repository administrator | The repository variables that switch on and route to the disposable runners (`BLOEM_SELF_HOSTED`, `BLOEM_CI_MEMBERS`), the one that switches the upstream sync on (`BLOEM_UPSTREAM_SYNC`), and the secrets for the sync token and Discord (`UPSTREAM_SYNC_TOKEN`, `DISCORD_WEBHOOK_URL`). |
| A maintainer | Answering or releasing a stopped Silo batch, and starting every deployment, which publishes the image. |
| The merge bot, outside this repository | Merging each Silo batch, pushing it to `main`, and its one re-run of a failed CI run. |
| Anyone with push access | Bloem's workflow files and scripts, re-runs, pushing a checked Silo merge to `main`, and merging a resolved sync pull request by hand. |
| The runner controller, outside this repository | Starting a fresh disposable runner for each job. Its own settings are the authority for routing. |
| Silo | Its own workflow files, which Bloem keeps exactly as Silo wrote them except one line of `ci.yml`. |

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| Bloem CI | `.github/workflows/bloem-ci.yml`, workflow name "Bloem CI" |
| The Forgejo copy | `.forgejo/workflows/bloem-ci.yml`, generated by `scripts/ci/compare-workflows.py` |
| Bloem Image, disabled | `.github/workflows/bloem-image.yml`, workflow name "Bloem Image" |
| Bloem Release, disabled | `.github/workflows/bloem-release.yml`, workflow name "Bloem Release" |
| The deploy tool | `bloem-deploy`, outside this repository; it builds, deploys and publishes the image |
| The merge bot | runs outside this repository; it uses `scripts/bloem/silo-merge-review.py` and `scripts/bloem/local-gate.py` |
| Review list | `scripts/bloem/silo-merge-review.py` |
| Local gate | `scripts/bloem/local-gate.py` |
| Upstream sync | `.github/workflows/upstream-sync.yml` |
| Upstream sync merge, the merge gate | `.github/workflows/upstream-sync-merge.yml` and `scripts/verify-upstream-sync-merge.sh` |
| The switch for both | the repository variable `BLOEM_UPSTREAM_SYNC`; `true` turns them on, anything else keeps them off |
| Disposable runner | a self-hosted runner with the labels `bloem-linux`, `bloem-run-<run id>-<attempt>` and `bloem-job-<job id>` |
| GitHub's runners | `ubuntu-24.04` (Bloem CI) and `ubuntu-latest` (the upstream sync and its merge gate) |
| Server image | `ghcr.io/bloem-studios/bloem-server:<full commit SHA>`, `release-<sha9>`, `latest` and dated tags such as `2026.11.26`, published by the deploy tool for each verified deploy; public. `edge` stays at `9fa2dad01` |
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
  the seam ledger, [`contracts/seams.txt`](../../contracts/seams.txt), and
  `make verify-seams` fails on a change the ledger does not list.
- An **overridden Silo line** is a line of Silo's that Bloem changes or removes
  inside a seam. Each one is recorded in
  [`contracts/silo-line-changes.json`](../../contracts/silo-line-changes.json),
  and `make verify-silo-lines` fails when Bloem overrides a line the record does
  not hold, including a Silo line a merge changed under an existing override.
  It also fingerprints every Silo function in which a Bloem line returns early,
  and fails when Silo changes one, so Bloem's line is checked against Silo's new
  code. The record may only shrink.
- A **disposable runner** is a self-hosted runner that runs one job and is not
  reused. See [Where jobs run](#where-jobs-run).

### Workflows

| Workflow | File | Runs on | What it does |
|---|---|---|---|
| Bloem CI | `bloem-ci.yml` | Pull requests; pushes to `main` and `apiv2`; Mondays at 04:23 UTC; manual | Runs every Bloem gate ([Jobs](#jobs)) |
| Bloem Image | `bloem-image.yml` | Disabled on GitHub since 2026-11-26 | Nothing. It published `ghcr.io/bloem-studios/bloem-server:<full sha>` and moved `edge` ([record](#bloem-image-disabled)) |
| Bloem Release | `bloem-release.yml` | Disabled on GitHub since 2026-11-26 | Nothing. It pointed `latest` and a dated tag at a commit's image ([record](#bloem-release-disabled)) |
| Upstream sync | `upstream-sync.yml` | Daily at 05:20 UTC, but its job runs only when `BLOEM_UPSTREAM_SYNC` is `true`; manual, with a dry run, always | Merges Silo's `main` on a branch and opens a pull request |
| Upstream sync merge | `upstream-sync-merge.yml` | Any Bloem CI run completing; acts only when `BLOEM_UPSTREAM_SYNC` is `true` | Merges a sync pull request whose exact head passed Bloem CI |
| CI | `ci.yml` | As in Silo | Nothing on Bloem: its one job runs only in Silo's repository ([below](#silos-ciyml)) |

The other files in `.github/workflows/` are Silo's and stay as Silo wrote them.
None of them tests or publishes Bloem:

- Silo's `docker.yml` ("Docker Image") publishes only after a successful "CI"
  run, and on Bloem every "CI" run concludes `skipped`, so Silo's image job is
  skipped too.
- The reusable `trusted-linux-build.yml` and `omnibus-build.yml`, which build
  Silo's images, run only in Silo's repository.
- `macos.yml` and `release.yml` run only when started by hand.
- `claude.yml`, `discord-commits.yml` (with `reusable-discord-commits.yml`),
  `v1-proposal-labeler.yml` and the contribution and evidence gates react to
  comments, issues and pushes; they build and test nothing.

### Silo's `ci.yml`

Bloem's CI is its own workflow, `.github/workflows/bloem-ci.yml` ("Bloem CI").
Silo's `.github/workflows/ci.yml` stays as Silo wrote it, with one added line: its
single job, a call to Silo's reusable `trusted-linux-ci.yml` from
`Silo-Server/silo-server@main`, runs only when
`github.repository == 'Silo-Server/silo-server'`. That reusable workflow takes its
tools from Silo's `main` branch, not from the Silo revision Bloem has merged, and
it knows none of Bloem's gates, so on Bloem it would check the wrong thing.
Skipping it costs a one-line seam. Keeping Bloem's jobs in their own file avoids
the conflicts a large edit to `ci.yml` would cause on every upstream CI change.
The upstream sync merge waits on "Bloem CI" by name, as the disabled Bloem Image
did.

### Triggers and cancellation

Bloem CI runs on every pull request, on pushes to `main` and to `apiv2` (the
API v2 integration branch), every Monday at 04:23 UTC, and by hand.

Runs share the concurrency group `bloem-ci-<event>-<ref>` with
`cancel-in-progress: true`. A newer run for the same event and ref cancels
the older one, on `main` too. A cancelled run is not a success, so its commit
has no passing run to deploy from.

No job `needs` another, so all of them start together. Every matrix uses
`fail-fast: false`, so one failing shard does not cancel the others. The
workflow's token is read-only (`contents: read`); only "Prune old Go caches"
gets `actions: write`, to delete caches.

### Where jobs run

Bloem CI's jobs carry the same runner routing as Silo's `trusted-linux-ci.yml`.
Every job except "Prune old Go caches" has this `runs-on` expression, with its own
job id in the last label. It is shown here on several lines for reading; the
workflow has it on one line:

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
not recorded in this repository.

The disposable runners are started fresh for each job by a runner controller
outside this repository. A runner accepts only a job that carries its own run's
`bloem-run-<run id>-<attempt>` label and its `bloem-job-<job id>` label, and it is
not reused afterwards. The controller applies the same routing rule from its own
configuration and is the authority, so the workflow expression and the controller
must be changed together. All shards of a matrix job ask for the same
`bloem-job-<job id>` label.

What differs by runner:

- The disposable runner image already has libvips and a warm Go cache. The libvips
  install exits early when `pkg-config --exists vips` succeeds. The Actions
  caches are restored and saved only on GitHub-hosted runners
  (`runner.environment == 'github-hosted'`): the Go build and module caches,
  the libvips packages, the golangci-lint analysis cache, and the web check,
  TypeScript and pnpm caches. Two caches are used on both: the golangci-lint
  binary, and the Web test job's pnpm store.
- On a runner with a tmpfs at `/dev/shm` that has 4 GiB free, the Go test
  shards put the tests' temporary files there (`TMPDIR`) and Go's build files
  on disk (`GOTMPDIR`). A synced write on a slow disk can take 0.2 to 0.45 s,
  longer than some of Silo's file-sync deadlines. Each shard prints where its
  temporary files live and how long one synced write takes.
- "Keep the Go build cache on disk" runs only on GitHub-hosted runners, where
  Silo's setup action puts the Go build cache on a tmpfs that Bloem's suite
  outgrows.
- "Prune old Go caches" always runs on `ubuntu-24.04`, and only for runs on
  the default branch that are not pull requests.

Upstream sync and Upstream sync merge run on GitHub's `ubuntu-latest`, as Bloem
Image and Bloem Release did before they were disabled. No GitHub runner, GitHub's or
Bloem's own, builds the server image now.

### The Forgejo copy

Bloem's CI is moving from GitHub Actions to Forgejo Actions on
`git.bloem-studios.com`. Until the move, a copy of Bloem CI runs there in shadow:
the GitHub repository is mirrored to Forgejo every hour, and Forgejo runs
`.forgejo/workflows/bloem-ci.yml` on each mirrored push to `main`. GitHub ignores
`.forgejo/`, and Forgejo reads only `.forgejo/workflows` once that directory
exists, so neither forge runs the other's file, and Silo's workflows never run on
Forgejo. The deploy tool and the merge bot still read only GitHub's runs.

The copy is generated, never edited by hand. After changing `bloem-ci.yml`, run:

```sh
python3 scripts/ci/compare-workflows.py --write
```

and commit both files. `make verify-local-gate` runs the script without
`--write`, so Go contract checks fail on both forges when the copy is stale, and
print the difference. The script's docstring lists every substitution, and the
script stops with an error when the GitHub workflow uses something it cannot
translate. In short, the copy:

- runs on pushes to `main` only, and drops "Prune old Go caches", which prunes
  GitHub's Actions cache;
- expands the YAML anchors, because Forgejo refuses an alias that names an anchor
  in another job;
- gives every job the plain label `bloem-job-<job id>`, served by a VM of the same
  size as on GitHub;
- starts the job's services itself with `docker run`, with their ports on
  `127.0.0.1` only, waits for their health checks, and removes them in a final
  step; Forgejo's host-mode runners start no services;
- in every job that sets up Go, moves the checkout, right after checking out, to
  `/opt/actions-runner/_work/bloem-server/bloem-server` and leaves a link to it
  in the workspace. That is where the runner template warms Go's build cache, and
  Go's build cache keys a package by its directory, while Forgejo gives every job
  a new random workspace. The job's last step, "Report the Go build cache", prints
  the directory Go built in and how many cache entries the job added (misses) and
  read again (hits). Web test, which has no Go, keeps the workspace: setup-node's
  pnpm cache skips files whose real path lies outside the workspace, and fails;
- uses Forgejo's fork of `upload-artifact`, and sends the contract diff's token to
  the Forgejo server rather than to github.com.

Forgejo leaves `runner.environment` unset, so every step for GitHub-hosted runners
is skipped there, as on Bloem's self-hosted GitHub runners.

### Jobs

| Job | What it gates |
|---|---|
| Prune old Go caches | Keeps the newest Go cache per job on the default branch. |
| Tenant identity (disposable PostgreSQL) | Tenant, access-group and profile-tenancy migrations and stores, race-checked resourcetenancy, access and policy, the tenancy and OPA acceptance suites, the initial setup atomicity tests (`SILO_INITIAL_SETUP_ATOMIC_DSN`), and the multitenant administration release gate. |
| Tenant stores race (1-4/4, disposable PostgreSQL) | `internal/tenancy` under the race detector, its tests split four ways. |
| Native authentication (1-3/3, disposable PostgreSQL) | `internal/auth` with and without the race detector, its tests split three ways, and its vet (shard 1). |
| Go lint | gofmt, `go vet ./...`, govulncheck, changed-line lint (`scripts/bloem-lint-changed-ci.sh`) and the full-tree router recovery lint (`make lint-router-recovery`). |
| Go contract checks | Settings bindings, client DTOs, playback fixtures, route inventory, the API v2 OpenAPI, the Bloem native OpenAPI and API catalogue, the v2 contract diff and fixtures, the seam ledger, the record of overridden Silo lines, the local gate's coverage of this workflow (`make verify-local-gate`), the Go test shards' database-list skip (`make verify-db-list-skip`), and their retried tests (`make verify-retried-tests`). |
| Go test (1-4/4) | `make test-go-shard` (`make test-go` for one shard's packages) on a database `cmd/bloem-testdb` prepared, its packages split four ways, four at a time per database. Silo's database-list tests are left to Go database pins (below). |
| Go database pins | The database pins (`make test-db-pins`, with the listed flaky pins retried) and the upstream database regression gates. |
| Go database regressions | Watch Party and history-import race tests, the Redis-backed native video grant and cover cache tests, and the library location and native storage pins (`scripts/ci/bloem-library-locations.txt`, built with `-tags=integration`). |
| Web (build, checks) | ESLint and format, typecheck and build, the launch bundle budget, the web settings binding and v2 types, and `make verify-local-paths`, which first runs the upstream sync merge helper's tests. On a push to `main` or `apiv2`, the build part still uploads `frontend-dist-<sha>`, which only the disabled Bloem Image read. |
| Web test (1-4/4) | Vitest in four shards, with the listed flaky tests retried alone. |

Go lint builds golangci-lint from source at the pinned `GOLANGCI_LINT_VERSION`,
because a released binary refuses a Go newer than the one it was built with. It
caches the binary and reuses it only when it was built with the current toolchain.

Locally, `make lint-changed` and `make lint` run on the Go version `go.mod`
names (the `LINT_GOTOOLCHAIN` variable in `Makefile.bloem`), the same version CI
builds with, whatever Go the machine has by default. golangci-lint's
staticcheck builds IR for the standard library, and under a newer default
toolchain it can panic in `buildir` before reporting anything. The pin applies
only to those two targets and only when `GOTOOLCHAIN` is not already set; set
`GOTOOLCHAIN` (for example to `local`) to choose another one, or
`LINT_GOTOOLCHAIN` to change the version for lint alone. The first run may
download that toolchain.

Changed-line lint reports only findings on lines that changed in the push or
pull request and that differ from Silo's copy of the file, so Silo's own lint
debt does not fail Bloem. An upstream sync merge is linted only on its
conflict-resolution lines.

The Go test shards skip Silo's database-list tests, the ones named in
`scripts/ci/db-pins.txt` and `scripts/ci/db-contracts.txt`. Silo's own
`make test-go` has no database, so Silo runs these only through
`scripts/ci/dbpins`, and some assume nothing else writes to the database while
they run: the marker population sync walks every eligible file in it, and fails
when another package's files appear mid-test. Go database pins runs both lists on
a fresh database, as Silo does, and the runner gives each package its own copy of
that database, so the shards pass `-skip` with the pattern
`scripts/ci/bloem-db-list-skip.py` prints. The script refuses a list name that
is also a test in a package the lists do not name, because `-skip` matches
names, not packages, and that test would then run nowhere.

The shards also skip the few tests listed in `scripts/ci/bloem-retried-tests.txt`
and then run each one on its own, in its package's shard, with up to two retries:
one run and two more if it fails, and the step fails when all three do. The list
holds only tests that fail under the shards' load and pass alone, added by a
maintainer, one test name per line with its reason and evidence; never a package or
a pattern. `scripts/ci/bloem-retried-tests.py` prints the `-skip` names and runs
the retries, refuses a listed name that is missing or also a test in another
package, and is checked by `make verify-retried-tests`; the local gate runs the
shards the same way. Silo's test code stays as Silo wrote it.

The same list covers Go database pins. A listed test that is also in a database
list is that job's, not a shard's:
`python3 scripts/ci/bloem-retried-tests.py db-pins` runs `make test-db-pins` on
copies of both lists without it, then runs it alone through the same runner
(`make test-db-contracts` with a one-test list), so a missing or skipped test
still fails, with the same two retries. A test that passes on a retry leaves a
warning on the run's summary. The local gate runs the job the same way.

The web test shards have their own list, `scripts/ci/bloem-retried-web-tests.txt`,
one Vitest test per line as Vitest names it in a failure
(`<file> > <describe> > <test>`). Every test still runs in its shard:
`python3 scripts/ci/bloem-retried-tests.py web` runs `make test-web` with a JSON
report, and only when every failed test is listed does it run each one alone
(`vitest run <file> -t` with its exact name) up to two more times; the report must
show the test ran and passed. Any other failure, a file that fails outside its tests,
or a missing report fails the shard as before. An empty list retries nothing.

The split jobs each run against their own PostgreSQL.
`scripts/ci/deal.sh N I` deals lines round-robin to N shards so each lands in
exactly one: Go test deals `make go-test-packages` with it, and
`scripts/ci/go-test-shard.sh PKG N I` deals a package's tests to print a `-run`
pattern. `scripts/ci/go-test-shard_test.sh` checks both. CI does not run that
test; run it by hand with `bash scripts/ci/go-test-shard_test.sh`.

The Go contract checks job byte-compares the Bloem native OpenAPI artifact
(`make verify-bloem-openapi`, `contracts/api/bloem/v2/openapi.json`) and
[the API catalogue](api-catalogue.md) generated from it
(`make verify-bloem-api-catalogue`) with a fresh generation.
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
authentication). One more phase, `compat`, runs only when `--only` names it: the
[Silo client compatibility check](../../contracts/client/silo-clients/README.md),
which is not a CI job. Commands assume the repository root is the cwd:

```sh
python3 scripts/bloem/local-gate.py run                     # every phase, compared with origin/main
python3 scripts/bloem/local-gate.py run --base <sha> --only go,scenarios
python3 scripts/bloem/local-gate.py list                    # every step and its command
eval "$(python3 scripts/bloem/local-gate.py services up)"   # the databases alone, for debugging
python3 scripts/bloem/local-gate.py services down "$GATE_SERVICES_ID"
```

- It starts one disposable `pgvector/pgvector:pg18` with CI's
  `max_connections=300` and `max_locks_per_transaction=512`, and with `fsync`,
  `synchronous_commit` and `full_page_writes` off, as CI's test databases are,
  so their commits do not slow the tests' own file syncs; and one Redis, on
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
  gains a `make` target or a `go test` the gate does not run, or when
  [the Forgejo copy](#the-forgejo-copy) is stale.
- For a Silo merge, run `--only build,verify,lint` with `--base` set to the `main`
  the merge started from; Bloem CI then runs the full suite on the pushed commit.

### Failures and re-runs

Apart from the few listed tests the Go and web test jobs retry ([above](#jobs)),
nothing in the workflows retries a failed test or job. When a job fails for a
reason that has nothing to do with the change, such as a flaky test or a lost
runner, re-run the run with GitHub's **Re-run failed jobs**
(`gh run rerun <run id> --failed`). That starts attempt 2 of the same run.
The failed jobs run again, and the jobs that passed keep their attempt-1
results. Because the disposable-runner labels include the attempt number, re-run
jobs ask for `bloem-run-<run id>-2` and get new runners. When the attempt
completes, Upstream sync merge fires again and acts on the run's new conclusion.

A re-run is for failures that are not about the change. A test that fails
again, or fails often, is a bug to fix. Typical causes seen so far:

- **A shared test database.** Libraries are global, so a test that compares
  library sets fails when another package in the same shard adds or deletes one.
  Give the package its own database (`bloemtestdb.RunIsolatedSuite`).
- **A lost runner.** A runner that powers off mid-run leaves no cause to inspect;
  re-run the failed jobs.
- **Daylight saving.** PostgreSQL adds `interval '8 days'` at wall-clock time on a
  session in a non-UTC zone, so across a clock change that is eight days and an
  hour. Write test windows in hours (`interval '192 hours'`), which PostgreSQL
  adds as absolute time.

### Server images

The deploy tool, `bloem-deploy`, builds and publishes every server image. It runs
outside this repository and outside GitHub Actions, on Bloem's build machine, and
refuses a commit without a successful Bloem CI run unless the operator passes
`--skip-ci-check`. For the commit it deploys it:

1. Exports the commit with `git archive`, builds the web app with `vite build`, and
   builds a `linux/amd64` image with Buildx from Silo's `Dockerfile`, passing
   `--build-context frontend_dist=<that build>/web/dist`. The labels and build
   arguments are the ones Bloem Image used: `org.opencontainers.image.revision`,
   `.created` and `.source`, and `BUILD_REVISION=<sha>`, `BUILD_DIRTY=false` and
   `BUILD_DATE`, so the server's system info reports the revision. The local tag is
   `bloem-server:release-<sha9>`.
2. Deploys it and verifies production. A deploy that fails verification is rolled
   back and publishes nothing.
3. After a verified deploy, pushes the image to Bloem's private registry as
   `release-<sha9>` and `<full sha>`, reading each tag's digest back.
4. Copies that manifest with `crane copy` to `ghcr.io/bloem-studios/bloem-server`, so
   both registries hold one digest, as `release-<sha9>`, `<full sha>`, a dated tag
   and `latest`, and reads every tag back.

The public tags:

- `<full sha>` and `release-<sha9>` name one deployed commit's image and never move.
- The dated tag is the deploy's UTC day, `YYYY.MM.DD`; a second, different release
  that day gets `.2`, then `.3`. A dated tag that already holds another image is never
  moved, and a retry for the same image reuses its tag.
- `latest` moves only on a verified deploy. Publishing an older deploy again
  (`bloem-deploy --publish --sha <sha>`) leaves `latest` alone when a later build has
  gone live or that sha was rolled back, so it never moves backwards.
- `edge` is not touched. It stays at `9fa2dad01`, Bloem Image's last image.
- `.env.bloem.example` sets `SILO_IMAGE` to `latest` for
  `docker-compose.advanced.yml`; deployments that must be attributable pin a dated or
  full-SHA tag
  ([deployment guide](overlays/wiki/deployment/docker.md#container-image-selection)).

Both pushes are best effort: a failed push changes neither the deploy's outcome nor
production, and a failed private push skips the public copy, which reads from it.
Publishing is retried with `bloem-deploy --publish --sha <sha>`.

What a published image is not:

- It has no `build-<n>` tag and no arm64 variant.
- It is Bloem's server image only, not Silo's all-in-one image, which bundles
  PostgreSQL, Redis and Meilisearch.
- Its Go build stage uses the Dockerfile's floating `golang:1.27` tag (Silo's
  choice), not the exact patch `go.mod` names, which the CI jobs use.

### Bloem Image (disabled)

`.github/workflows/bloem-image.yml` ("Bloem Image") is disabled on GitHub since
2026-11-26; the file stays in the repository as a record. It ran after a successful
Bloem CI push run on `main`, downloaded that run's `frontend-dist-<sha>` web bundle,
built and pushed `ghcr.io/bloem-studios/bloem-server:<full sha>` with the labels and
build arguments above, and moved `edge` to it while the commit was still `main`'s
head. It built an image for every passing `main` commit, about three a day, most never
deployed. The deploy tool's build replaced it.

### Bloem Release (disabled)

`.github/workflows/bloem-release.yml` ("Bloem Release") is disabled on GitHub since
2026-11-26; the file stays as a record. Started by hand or by the deploy tool after a
verified deploy, it waited for Bloem Image's image for the commit and pointed `latest`
and a dated tag at it with `docker buildx imagetools create`. The deploy tool's
public copy now sets the same tags by the same rules.

### Tools on the Go runners

`scripts/ci/bloem-install-test-tools.sh` installs what the Go jobs need
beyond libvips. Go test, Go database pins and Go database regressions call it
with `ffmpeg`.

- **FFmpeg:** the native audio and video tests generate fixtures with real
  FFmpeg. The native audiobook regressions fail without it, and FFmpeg tests
  elsewhere skip. The script adds FFmpeg to `PATH` for later steps.
- **ripgrep:** the script can still install it, for
  `scripts/generate-bloem-storage-proto_test.sh`, which matches the
  generator's output with `rg`. No CI step or Go test runs that script, so CI
  does not ask for ripgrep.

FFmpeg is Jellyfin's build, the family the runtime image ships. CI pins
`jellyfin-ffmpeg7`, while the image ships `jellyfin-ffmpeg8` (the Dockerfile's
`JELLYFIN_FFMPEG_VERSION`). The gap is deliberate:

- FFmpeg 8 prints a stream's display matrix as `Display Matrix: rotation of N
  degrees`. FFmpeg 7 printed `displaymatrix: rotation of N degrees`.
- Silo's input parser in `internal/mediasample` (`probe.go`) reads only the
  FFmpeg 7 spelling. Under FFmpeg 8 it misses the rotation, so trickplay
  sheets for rotated (phone) video get landscape tile heights.
- That parser is Silo-owned code. The fix is a one-line change upstream
  (accept both spellings). Once Silo merges it, move the CI pin to the
  Dockerfile's version.
- This affects the shipped image too, which runs FFmpeg 8: rotated video gets
  wrong trickplay tiles until the parser is fixed.
- `TestBloemCIFFmpegPinTracksTheDisplayMatrixParse` in `internal/mediasample`
  ties the pin to that reason. While the parser misses FFmpeg 8's spelling, the
  test skips and names the bug. Once the parser reads both spellings, it fails
  until the install script's FFmpeg major matches the Dockerfile's.

The HDR software tone-map acceptance test passes on FFmpeg 8 too. Its fixture tags
frames through `setparams`, because FFmpeg 8 ignores `-color_trc` and related
options when the frames carry their own color properties.

### Go patch

Silo's setup action (`.github/actions/setup-go`) installs the newest Go 1.27
patch. Bloem's lint, contract-check, test and scenario jobs then install
exactly the patch `go.mod` names (step `Use the Go patch go.mod requires`). The
tenant and native authentication jobs install that patch directly
(`actions/setup-go` with `go-version-file: go.mod`). A security release
therefore takes effect when `go.mod` moves, not when runner images catch up.
`go tool govulncheck` reads the standard library of that patch, so a new
standard-library advisory fails the Go lint job until `go.mod` names the fixed
patch.

### Upstream sync merges

A merge bot takes in every Silo batch. The two GitHub workflows that can also merge
Silo are switched off, because two automatic mergers must never race.

#### The merge bot

It runs outside GitHub Actions, and nothing in this repository starts it. It uses
two scripts from this repository: `scripts/bloem/silo-merge-review.py` and
`scripts/bloem/local-gate.py`. For each batch, which is exactly the Silo commit it
found, it works on a branch `merge/upstream-<sha9>` from `origin/main`:

1. **Start.** A batch starts twice a day, when Silo's tip is not in Bloem's `main`
   and that tip is at least 10 minutes old. Only one batch runs at a time. If the
   commit is already in `main`, the batch ends with nothing to do.
2. **Merge.** `git merge --no-ff --no-commit <batch>`; each conflicted hunk takes
   Silo's side verbatim, Bloem's behavior goes back on Bloem's own added lines or in
   a Bloem-owned file or field, and a Silo deletion wins. When keeping Bloem working
   would need a change to one of Silo's lines, the batch stops. The merge commit is
   titled "Merge Silo upstream through <sha9>".
3. **Review.** `python3 scripts/bloem/silo-merge-review.py --base <old main> --silo <batch>`
   lists the seams the batch touches, Bloem files beside or calling changed Silo code,
   overlays to bring in line, Bloem pages that mention changed files, and the
   batch's migrations. Each item gets a line saying why Bloem still works or what
   breaks; a break is fixed on Bloem's own lines with a test that fails first.
4. **Regenerate and document.** `make client-dtos`, `make playback-fixtures`, the API
   catalogue, coverage reasons in `contracts/client/v1/coverage.json` with
   `make client-digest`; then the overlays and Bloem pages the merge affects, with
   pictures redrawn, and the status paragraph in
   [how Bloem keeps up with Silo](../architecture/bloem-following-silo.md).
5. **Fast checks.** `python3 scripts/bloem/local-gate.py run --base <old main> --only build,verify,lint`.
   A failed test is rerun alone once; a real failure gets at most three fix
   attempts. If `main` moved meanwhile, it is merged in and the fast checks run
   again; a third move stops the batch.
6. **Push and wait.** `git push origin HEAD:main`, never forced. The bot waits for
   the Bloem CI run on that exact commit. A failed run is rerun once, failed jobs
   only (`gh run rerun <id> --failed`), unless `main` has moved on, since a re-run
   would cancel the newer push's run. A run cancelled by a newer push is followed to
   the new tip after checking it still contains the pushed commit. Red twice, a run
   that never finishes, or a `main` that lost the commit stops the batch. Nobody
   pushes an empty commit or starts CI by hand to turn it green.

A batch also stops when a test or check will not pass and the cause cannot be
fixed. A stopped batch blocks new batches until a maintainer answers or releases it.
Migrations and active streams are not stop reasons.

#### The switch for the GitHub sync

The two workflows below merge Silo into Bloem without anyone having to notice that
Silo moved, with Bloem CI as the gate: nothing reaches `main` unless Bloem CI passed
on the exact merge. Each job's `if:` requires the repository variable
`BLOEM_UPSTREAM_SYNC` to be `true`, and it is unset. A scheduled sync run therefore
starts and skips its job; a sync started by hand, dry run included, still runs and
opens its pull request; and the merge gate skips every Bloem CI completion, so a
sync pull request is merged by a person. The rest of this section describes both
workflows as they run when switched on.

#### The daily sync: `upstream-sync.yml`

When switched on, it runs daily at 05:20 UTC. GitHub can start scheduled runs hours
late. It can also be started by hand with `dry_run`, which reports what would happen
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
     merge by hand once Bloem CI passes.
6. Post a summary to Discord if `DISCORD_WEBHOOK_URL` is set.

Nothing in it force-pushes or writes to `main`.

#### The merge gate: `upstream-sync-merge.yml`

It runs whenever a Bloem CI run completes, re-run attempts included. It acts
only when the switch is on and that run was for a pull request on a branch named
`upstream-sync/*`.
It checks out the default branch, never the pull request's head, so a pull
request cannot change the helper that judges it. It then fetches Silo's
`main` and runs `scripts/verify-upstream-sync-merge.sh`. The helper merges only if
all of these hold:

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
write on this repository. Without it they fall back to `GITHUB_TOKEN`:

- GitHub does not start workflows for events raised by `GITHUB_TOKEN`, so a
  pull request it opens runs no CI until someone pushes to it or reopens it.
  The pull request body says so.
- `GITHUB_TOKEN` can never push or merge a commit that changes
  `.github/workflows`, whatever the job's permissions. Without the secret, a
  sync that brings in such a Silo commit fails at its push.
- A merge pushed with the token starts Bloem CI on `main`.

The sync's checkout keeps no credential. The push supplies the token for that
one command, so it is not on disk while the merged upstream tree builds.

A sync pull request is opened by the token's account. It runs on disposable
runners only if that login is in `BLOEM_CI_MEMBERS`
([Where jobs run](#where-jobs-run)).

### PostgreSQL version

Every PostgreSQL service in `bloem-ci.yml` runs `pgvector/pgvector:pg18`, the
major Bloem deploys. `TestCIPostgresServicesMatchTheDeploymentMajor` in
`internal/api` keeps them equal: it reads `ARG PG_MAJOR=` from
`omnibus/Dockerfile`, the PostgreSQL major Silo's all-in-one image bundles, and
requires every CI service to run `pgvector/pgvector:pg<major>`. Bloem's own
`docker-compose.advanced.yml` runs `pgvector/pgvector:pg18` as well. PostgreSQL
18 reports an `ON DELETE RESTRICT` violation as SQLSTATE 23001, where 17
reports 23503, so a 17 service hides behavior a real deployment has.

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
  Silo batch and the rule that Silo wins every conflict.
- [FORK.md](../../FORK.md): fork provenance and merge policy.
- [Docker deployment guide](overlays/wiki/deployment/docker.md): choosing and
  pinning an image.
- [Release versioning overlay](overlays/release-versioning.md): Bloem's image
  tags and releases.
- [Developing Bloem Server](overlays/DEVELOPMENT.md): local test databases and
  the checks to run before pushing.

The pictures are SVG files under `docs/architecture/images/bloem-ci/`, drawn by
`scripts/bloem/pictures/bloem_ci.py` through `scripts/bloem/draw-pictures.py`.
Redraw them there, not by hand.
