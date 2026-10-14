# Bloem CI additions

What Bloem adds to Silo's CI workflow (`.github/workflows/ci.yml`) and why.
Commands assume the repository root is the cwd.

## Tools on the Go runners

`scripts/ci/bloem-install-test-tools.sh` installs what the Go jobs need beyond
libvips:

- **Go contract checks:** ripgrep. `scripts/verify-no-compat.sh` searches with
  `rg`. It refuses to run without it and fails on any `rg` error, so a missing
  tool can no longer pass the compatibility-removal gate unchecked.
  It reads every file under the searched paths, including hidden directories
  and files that ignore rules or a `RIPGREP_CONFIG_PATH` configuration would
  skip.
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

Everything else that uses FFmpeg passes on 8.1.3 too, including the HDR
software tone-map acceptance test. Its fixture tags frames through `setparams`,
because FFmpeg 8 ignores `-color_trc` and related options when the frames
carry their own color properties.

## Upstream sync merges

`.github/workflows/upstream-sync-merge.yml` merges an upstream-sync pull request
only after CI succeeded on its exact head. It does not run the
compatibility-removal gate itself: from its default-branch checkout the gate
would inspect `main` rather than the head, and only after the merge. The Go
contract checks job runs the gate on every pull request head instead, and
`TestUpstreamSyncMergeReliesOnCIForTheCompatibilityGate` in `internal/api` keeps
that job unconditional, with ripgrep installed first.

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
