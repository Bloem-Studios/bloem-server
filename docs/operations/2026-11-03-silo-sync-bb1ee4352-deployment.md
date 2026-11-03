# Bloem deployment — November 3, 2026: Silo through `bb1ee4352`

Production has run `main` at `3a3fae1936188d6fd6f08e46fd98bca778faf4b8` since 05:17:31 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `0fc66436b` and adds:

- `ff0a75fd1`: the merge of Silo `main` from `391e90910` through `bb1ee4352` (November 3,
  04:24 UTC), one Silo commit with no migration. Silo's #3436 makes library scans and the
  real-time monitor skip every hidden (dot-prefixed) folder below a library root, as Jellyfin
  does, alongside the NAS and OS housekeeping folders. A skipped folder is never read, so one
  the scan cannot open no longer makes the scan partial; a scoped scan inside a hidden folder
  reads nothing, and a skipped podcast scope no longer widens to its whole show.
- `dbe43fa54`: Bloem's native storage scan skips the same hidden folders (see below).
- `470c0a812`: regenerated client DTOs, which only take the new server revision.
- `3a3fae193`: the status rows for the batch and the native storage page.
- The commit pushed since `0fc66436b`: the previous deployment's receipt (`e40ffadd5`).

## Merge resolutions

None. The merge had no conflicts. It touched one seam, `internal/scanner/scanner.go`, where
Silo's change and Bloem's lines sit in different hunks.

Silo replaced `isIgnoredDirectoryPath` with `SkippedDirName`. Bloem's native storage scan
(`internal/scanner/storage_movie_identity.go`, a Bloem file) mirrors Silo's walk and still
called the removed function, so the merge did not build. `dbe43fa54` makes
`StorageMovieFileSkipped` and `StorageDirectorySkipped` call `SkippedDirName`, so native
storage libraries skip the hidden folders the filesystem walk now skips. Its new test rows for
hidden folders outside Silo's old name list fail with a lookup in Silo's remaining housekeeping
map. No new seam; the seam ledger stays at 309.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-3a3fae193` (image `sha256:a6453ec4ea73168ffd8c0d45ba8aed8cda651af148547ee5a7530183e6b3fb40`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `5a3a084202be2486a86c9e009e42efcf6f39c095f37e62610d2469397c756fbc` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-3a3fae193`. Database restore point of
  the same name at LSN `45A/64F1B468`. Segment `000000030000045A00000064`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `0fc66436b`. Two transcodes were
  running inside the server, and the stop took 40 seconds (exit 0). No migration ran; 756 are
  applied. The server answered ready at 05:17:31, 48 seconds after the stop began, and was
  healthy at the same moment with no restarts. Its runtime configuration fingerprint is the
  same as before.
- `/api/v2/system/info` reports `3a3fae19`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-3a3fae193`, healthy, and the server lists both as enabled and healthy on
  `3a3fae193`. Their previous compose files are kept with suffix `.bak-pre-3a3fae193`.
- **Old images.** The script removed `release-e3a096b20` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. No test failed, so
no flake was rerun.

## Bloem CI

Run 41580988646 passed on `3a3fae193` at its first attempt.

## Rollback

Start `bloem-server:release-0fc66436b` again and set the transcode nodes back from their
`.bak-pre-3a3fae193` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is. A scan by the new image marks files
under hidden folders missing; after a rollback the previous image's next scan finds them
again. Restoring the database to the restore point together with the container snapshot also
rolls back, but discards everything written since the restore point, taken just before the
05:16 restart.
