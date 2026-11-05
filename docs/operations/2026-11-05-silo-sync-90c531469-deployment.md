# Bloem deployment — November 5, 2026: Silo through `90c531469`

Production has run `main` at `d69ce2aa86e960d16bb63136775f150cfa52f7fa` since 01:14:34 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `eeb919cb5`, recorded in the
receipt for [bulk marks imported as marks](2026-11-04-import-marks-deployment.md). Since
`eeb919cb5` production gained:

- `37786b1f8`: that deployment's receipt.
- `9d83bfbc4`: the merge of Silo `main` from `6d1562473` through `90c531469` (November 5,
  00:26 UTC), one Silo commit with no migration. Silo's #3517 keeps laced Matroska audio
  continuous in the browser decoder: a decoded frame that repeats the previous frame's
  container timestamp, or overlaps it by 20 ms or less, now starts where the previous frame
  ended, so the audio worklet no longer plays silence and stalls the picture about once per
  cluster. Real gaps still play as silence.
- `66def9b30`: regenerated client DTOs, whose headers now name the new commit; no wire type
  changed.
- `d69ce2aa8`: the status rows for the batch.

## Merge resolutions

The merge had no conflicts. The batch changes `web/src/player/engine/audioMix.ts`, its test
and `engine.worker.ts`, none of them a seam, and Bloem has no lines of its own in them. Bloem
needed no fix. No new seam came with the batch; the seam ledger stays at 316 entries, eight of
them temporary web fixes that go when Silo merges its own versions.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-d69ce2aa8` (image `sha256:adafd196ce4614ab12f8620cced77df57e5231770e616a2f1aeb660d6c83448d`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `d92eae9712442d079122f76acef6de0e9077e803d3995b270d9732f471046555` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-d69ce2aa8`. Database restore point of
  the same name at LSN `48E/6F415520`. Segment `000000030000048E0000006F`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `eeb919cb5`. One transcode process
  was running inside the server, and the stop took 37 seconds (exit 0). No migration ran;
  762 are applied. The server answered ready at 01:14:34, 45 seconds after the stop began,
  and was healthy at 01:14:38. Its runtime configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `d69ce2aa`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-d69ce2aa8`, healthy, and the server lists both as enabled and healthy on
  `d69ce2aa8`.
- **Old images.** The script removed `release-c2b43286e` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. No test needed a rerun alone. `main` did not move during the checks.

## Bloem CI

Run 41944857735 on `d69ce2aa8` passed at its first attempt, and the deploy used that result.

## Rollback

Start `bloem-server:release-eeb919cb5` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The batch has no migration, so the previous image runs
on the database as it is. Restoring the database to the restore point together with the
container snapshot also rolls back, but discards everything written since the restore point,
taken just before the 01:13 restart.
