# Bloem CI

Bloem's CI is its own workflow, `.github/workflows/bloem-ci.yml` ("Bloem CI").
Silo's `.github/workflows/ci.yml` is kept as Silo's file with one line added:
its single job, a call to Silo's reusable `trusted-linux-ci.yml` from
`Silo-Server/silo-server@main`, runs only in Silo's repository. That reusable
workflow pulls its tools from Silo's main branch, not from the Silo revision
Bloem has merged, and knows none of Bloem's gates, so on Bloem it would check
the wrong thing. Skipping it costs a one-line seam; keeping Bloem's jobs in
Silo's file cost an 880-line one that every upstream CI change conflicted with.
Bloem Image and the upstream sync merge wait on "Bloem CI" by name.
Commands assume the repository root is the cwd.

## Jobs

"Bloem CI" carries the jobs Bloem's copy of `ci.yml` ran, apart from the
workflow name and concurrency group, with the slowest split across runners:

| Job | What it gates |
|---|---|
| Prune old Go caches | Keeps the newest Go cache per job on the default branch. |
| Tenant identity (disposable PostgreSQL) | Tenant, access-group and profile-tenancy migrations and stores, race-checked resourcetenancy, access and policy, the v1 tenancy and OPA acceptance suites, and the multitenant administration release gate. |
| Tenant stores race (1-4/4, disposable PostgreSQL) | `internal/tenancy` under the race detector, its tests split four ways. |
| Native authentication (1-3/3, disposable PostgreSQL) | `internal/auth` with and without the race detector, its tests split three ways, and its vet (shard 1). |
| Go lint | gofmt, `go vet ./...`, govulncheck, changed-line lint (`scripts/bloem-lint-changed-ci.sh`) and the full-tree router recovery lint. |
| Go contract checks | Settings bindings, client DTOs, playback fixtures, route inventory, migration ledger, scenario catalogs, offline routes, the API v2 OpenAPI, contract diff and fixtures, and the seam ledger. |
| Go test | `make test-go` on a database `cmd/bloem-testdb` prepared. |
| Go database pins | The database pins (`make test-db-pins`) and the upstream database regression gates. |
| Go database regressions | Watch Party and history-import race tests and the mixed library location pins. |
| Scenario executor | `make test-scenarios`. |
| Web (build, checks) | ESLint and format, typecheck and build, the launch bundle budget, the web settings binding and v2 types, and `verify-local-paths`; uploads `frontend-dist-<sha>`, which Bloem Image publishes. |
| Web test (1-4/4) | Vitest in four shards. |

The split jobs each run against their own PostgreSQL.
`scripts/ci/go-test-shard.sh PKG N I` prints the `-run` pattern for shard I of
N, dealing a package's tests round-robin so every test runs exactly once;
`scripts/ci/go-test-shard_test.sh` checks that.

Silo's `TestLogPartitionsMatchPartmanOnNonUTCServer` checks an eight-day
America/New_York window, which crosses the 2026-11-01 daylight-saving change, so
"Go test" fails on every branch until about November 2, 2026.

## Tools on the Go runners

`scripts/ci/bloem-install-test-tools.sh` installs what the Go jobs need beyond
libvips:

- **Go test, Go database pins, Go database regressions:** ripgrep, which `scripts/generate-bloem-storage-proto_test.sh`
  uses to match the generator's output.
- **The same three jobs:** FFmpeg and ripgrep. The native audio and video tests generate
  fixtures with real FFmpeg; the two native audiobook tests fail without it, and
  about twenty other packages' FFmpeg tests skip.

FFmpeg is Jellyfin's build, the family the runtime image ships. CI pins
`jellyfin-ffmpeg7` 7.1.4-3, while the image ships `jellyfin-ffmpeg8` (the
Dockerfile's `JELLYFIN_FFMPEG_VERSION`). The gap is deliberate:

- FFmpeg 8 prints a stream's display matrix as `Display Matrix: rotation of N
  degrees`. FFmpeg 7 printed `displaymatrix: rotation of N degrees`.
- Silo's input parser in `internal/mediasample` reads only the FFmpeg 7
  spelling. Under FFmpeg 8 it misses the rotation, so trickplay sheets for
  rotated (phone) video get landscape tile heights, and
  `TestGenerateRotatedManifestEndToEndDB` fails.
- That parser is Silo-owned code. The fix is a one-line change upstream (accept
  both spellings). Once it is merged, move the CI pin to the Dockerfile's
  version.
- This is a production bug, not only a CI gap: the shipped image runs FFmpeg 8,
  so rotated video gets wrong trickplay tiles in production until the parser
  is fixed.
- `TestBloemCIFFmpegPinTracksTheDisplayMatrixParse` in `internal/mediasample`
  ties the pin to that reason. While the parser misses FFmpeg 8's spelling it
  skips and names the bug. Once the parser reads both spellings it fails until
  the install script's FFmpeg major matches the Dockerfile's.

Everything else that uses FFmpeg passes on 8.1.3 too, including the HDR
software tone-map acceptance test. Its fixture tags frames through `setparams`,
because FFmpeg 8 ignores `-color_trc` and related options when the frames
carry their own color properties.

## Go patch

Silo's setup action installs the newest Go 1.26. Bloem's lint, contract-check,
test and scenario jobs then install exactly the patch `go.mod` names (`Use the Go
patch go.mod requires`), so a
security release takes effect when `go.mod` moves, not when runner images catch
up. `go tool govulncheck` reads the standard library of that patch: a new
standard-library advisory fails the Go lint job until `go.mod` names the fixed
patch.

## Upstream sync merges

`.github/workflows/upstream-sync.yml` runs daily. It merges `upstream/main` into
an `upstream-sync/<date>-<sha>` branch, checks that it builds, pushes the branch
and opens a pull request: a normal one for a clean merge, a draft carrying the
conflict markers otherwise. `.github/workflows/upstream-sync-merge.yml` merges an
upstream-sync pull request only after Bloem CI succeeded on its exact head.

Both workflows push and merge with the `UPSTREAM_SYNC_TOKEN` repository secret, a
personal access token with contents, pull requests and workflows write on this
repository, and fall back to `GITHUB_TOKEN` without it:

- GitHub does not start workflows for events raised by `GITHUB_TOKEN`, so a
  pull request it opens runs no CI until someone pushes to it or reopens it.
- `GITHUB_TOKEN` can never push or merge a commit that changes
  `.github/workflows`, whatever the job's permissions. Without the secret, a sync
  that brings in such a Silo commit fails at its push.

The sync's checkout keeps no credential. The push supplies the token for that
one command, so it is not on disk while the merged upstream tree builds.

## PostgreSQL version

Every PostgreSQL service in `bloem-ci.yml` runs the image `docker-compose.yml`
deploys (`pgvector/pgvector:pg18`), and `TestCIPostgresServicesMatchTheDeploymentMajor`
in `internal/api` keeps them equal. PostgreSQL 18 reports an `ON DELETE RESTRICT`
violation as SQLSTATE 23001, where 17 reports 23503, so a 17 service hides
behavior production has.

## Mixed library location pins

`scripts/ci/bloem-library-locations.txt` lists the required mixed-location and
native audiobook regressions. `make test-bloem-library-locations` runs them
through the same runner as `make test-db-pins`, so a missing, skipped or
failing test fails the run. The Go test job runs it after the upstream database
gates. The tests need:

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
