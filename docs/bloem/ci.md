# Bloem CI additions

What Bloem adds to Silo's CI workflow (`.github/workflows/ci.yml`) and why.
Commands assume the repository root is the cwd.

## Tools on the Go runners

`scripts/ci/bloem-install-test-tools.sh` installs what the Go jobs need beyond
libvips:

- **Go test:** ripgrep, which `scripts/generate-bloem-storage-proto_test.sh`
  uses to match the generator's output.
- **Go test:** FFmpeg and ripgrep. The native audio and video tests generate
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
upstream-sync pull request only after CI succeeded on its exact head.

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

Every PostgreSQL service in `ci.yml` runs the image `docker-compose.yml`
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
