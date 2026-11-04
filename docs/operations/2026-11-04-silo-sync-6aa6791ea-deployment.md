# Bloem deployment — November 4, 2026: Silo through `6aa6791ea`

Production has run `main` at `baba53141b42dd83a00126e580e6c845a622a53c` since 03:40:26 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `479b27576`, which the
[`d5d00264d` receipt](2026-11-04-silo-sync-d5d00264d-deployment.md) recorded. Since
`479b27576` production gained:

- `bbac357da`: the previous deployment's receipt.
- `6c8d9fc7d`: the merge of Silo `main` from `d5d00264d` through `6aa6791ea` (November 4,
  02:39 UTC), eight Silo commits with no migration. Silo's #3469 detects NVENC through the
  WSL GPU device `/dev/dxg`, still confirmed by the FFmpeg smoke encode. Silo's #3443 gives
  mixed libraries their own sidebar icon and lines up library icons when a library has
  pins. Silo's #3467 links the Discord developer portal in the notification setup guide.
  Silo's #3442 lets a scan join an active run without a duplicate-key error. Silo's #3438
  clears the resume position when a Jellyfin client marks a rewatched item played. Silo's
  #3448 keeps the scroll position when switching episodes in the carousel. Silo's #3428
  lets Jellyfin clients direct-play video files with no audio track. Silo's #3159 makes
  `GET /api/v2/catalog/search/capabilities` report `provider: postgres` while Meilisearch
  is unreachable, instead of failing.
- `3b67e5506`: regenerated client DTOs, whose headers now name the merge commit; no wire
  type changed.
- `baba53141`: the status rows for the batch.

## Merge resolutions

No conflicts. Git merged Silo's changes to three seams, `internal/playback/gpudetect.go`,
`internal/playback/gpudetect_test.go` and `web/src/components/AppSidebar.tsx`, beside
Bloem's own lines there: the caller-expiry check before a hardware probe, the probe test
fixture, the preview sidebar switch and the Live TV link. Bloem needed no fix. No new seam;
the seam ledger stays at 308.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-baba53141` (image `sha256:c2b8d9c3357cac0548d4d3e678935416ca4e3d920ba55218af9a4ffa740d3b5d`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `c7ff616aeef4d9c8fb1d6d74bec432262d7ec14c29e6f5a47098d39bee50335d` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-baba53141`. Database restore point of
  the same name at LSN `475/C7BE33D0`. Segment `0000000300000475000000C7`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `479b27576`. Two transcode processes
  were running inside the server, and the stop took 38 seconds (exit 0). No migration ran;
  757 are applied. The server answered ready at 03:40:26, 46 seconds after the stop began,
  and was healthy at the same time with no restarts. Its runtime configuration fingerprint
  is the same as before.
- `/api/v2/system/info` reports `baba5314`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-baba53141`, healthy, and the server lists both as enabled and healthy on
  `baba53141`. Their previous compose files are kept with suffix `.bak-pre-baba53141`.
- **Old images.** The script removed `release-dc4f67200` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. No test failed, so no flake was recorded. `main` did not move during the checks.

## Bloem CI

Run 41766548515 passed on `baba53141` at its first attempt.

## Rollback

Start `bloem-server:release-479b27576` again and set the transcode nodes back from their
`.bak-pre-baba53141` compose files; the deploy script's rollback does both. The batch has no
migration, so the previous image runs on the database as it is. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards everything
written since the restore point, taken just before the 03:39 restart.
