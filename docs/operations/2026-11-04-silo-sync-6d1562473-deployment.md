# Bloem deployment — November 4, 2026: Silo through `6d1562473`

Production has run `main` at `6604bfd441e31ea65299e4a859c97e4149fd1b7f` since 22:47:38 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `28bdc197b`, recorded in the
receipt for [Silo through `4e3020a26`](2026-11-04-silo-sync-4e3020a26-deployment.md). Since
`28bdc197b` production gained:

- `7acb3168a`: that deployment's receipt.
- `a4aa3611a`: the merge of Silo `main` from `4e3020a26` through `6d1562473` (November 4,
  21:26 UTC), one Silo commit with no migration. Silo's #3514 moves the web player to
  mediabunny 1.62.1, which restores the stripped Matroska header on each frame of a laced
  block, so episodes whose DTS audio uses mkvmerge's lacing with header stripping decode in
  the browser. It adds a `dts-laced-stripped.mka` fixture to the FFmpeg audio decoder tests.
- `aaf390389`: regenerated client DTOs, whose headers now name the new commit; no wire type
  changed.
- `6604bfd44`: the status rows for the batch.

## Merge resolutions

Two seams conflicted on the same hunk. In `web/package.json` and the importers section of
`web/pnpm-lock.yaml`, Silo's bumped `mediabunny` line sat next to Bloem's `mpegts.js`
dependency, which the Live TV player uses. The merge took Silo's `mediabunny` lines verbatim
and kept Bloem's `mpegts.js` lines on their own lines after them; `pnpm install
--frozen-lockfile` accepts the result, and Silo's decoder tests pass on it. Bloem needed no
fix. No new seam came with the batch; the seam ledger stays at 316 entries, eight of them
temporary web fixes that go when Silo merges its own versions.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-6604bfd44` (image `sha256:092df6c0a8756a74fccb6c6bce3b21b968ddf47deca6c0b9af89e62be9798364`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `e20f8e507c06905ada227dc069e60da3806bc20b0725a1a4fc471634b6701fbd` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-6604bfd44`. Database restore point of
  the same name at LSN `48B/A213C568`. Segment `000000030000048B000000A2`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `28bdc197b`. One transcode process
  was running inside the server, and the stop took 36 seconds (exit 0). No migration ran;
  762 are applied. The server answered ready at 22:47:38, 45 seconds after the stop began,
  and was healthy at the same time. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `6604bfd4`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-6604bfd44`, healthy, and the server lists both as enabled and healthy on
  `6604bfd44`.
- **Old images.** The script removed `release-0edcd6e49` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. No test needed a rerun alone. `main` did not move during the checks.

## Bloem CI

Run 41925440317 on `6604bfd44` passed at its first attempt, and the deploy used that result.

## Rollback

Start `bloem-server:release-28bdc197b` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The batch has no migration, so the previous image runs
on the database as it is. Restoring the database to the restore point together with the
container snapshot also rolls back, but discards everything written since the restore point,
taken just before the 22:46 restart.
