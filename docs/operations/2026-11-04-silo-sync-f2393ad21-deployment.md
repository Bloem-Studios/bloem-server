# Bloem deployment — November 4, 2026: Silo through `f2393ad21`

Production has run `main` at `341108c6eaa6f36539c55ced7b1c7b2b623ac06e` since 00:25:44 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `4939c4cfa`, which the
[`527e40436` receipt](2026-11-03-silo-sync-527e40436-deployment.md) recorded. Since
`4939c4cfa` production gained:

- `102d405ac`: the previous deployment's receipt.
- `a1a84b196`: the merge of Silo `main` from `527e40436` through `f2393ad21` (November 3,
  23:32 UTC), three Silo performance commits with no migration and no change to any answer.
  Silo's #3451 builds card badges without decoding each file's full track lists. Silo's
  #3458 skips work the item detail page never sent (trickplay manifests, marker thumbnails,
  literary-work summaries for films and series) and reads the viewer's favorite, watchlist,
  progress and playback preferences in one batch. Silo's #3459 runs a season's episode-list
  lookups at the same time on a lighter file query, reads card flags in one statement, and
  opens a transaction for library pages and search only when a second statement follows.
- `f377fcbce`: regenerated client DTOs (the server revision stamp only).
- `341108c6e`: the status rows for the batch.

## Merge resolutions

None. The merge had no conflicts. It touched one seam, `internal/scanner/file_repo.go`, which
merged automatically: Silo's new episode-list file query sits beside Bloem's
maintenance-scope lines, which are unchanged. The new episode-list query still carries every
field the access check and the unreadable flag read. Bloem's interest-tracking decorator
picks up Silo's forwards of the two new batched reads. No new seam; the seam ledger stays at
309. No new wire types, so the client coverage and digest are unchanged.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-341108c6e` (image `sha256:f8c7ab92d3ba6d01e02d2dc39e09e8b2546753b9107165fa49b8fe6343b516da`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `be407294a5dfa33cf30a0c3afc11cf9d87d59e15a26b3cf3e2f3b0ce3e7b1324` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-341108c6e`. Database restore point of
  the same name at LSN `471/B389C938`. Segment `0000000300000471000000B3`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `4939c4cfa`. One transcode process
  was running inside the server, and the stop took 36 seconds (exit 0). No migration ran;
  756 are applied. The server answered ready at 00:25:44, 43 seconds after the stop began,
  and was healthy at 00:25:46 with no restarts. Its runtime configuration fingerprint is the
  same as before.
- `/api/v2/system/info` reports `341108c6`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-341108c6e`, healthy, and the server lists both as enabled and healthy on
  `341108c6e`. Their previous compose files are kept with suffix `.bak-pre-341108c6e`.
- **Old images.** The script removed `release-3a3fae193` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. `main` did not move
during the checks. No test failed, so no flake was rerun.

## Bloem CI

Run 41744044879 passed on `341108c6e` at its first attempt.

## Rollback

Start `bloem-server:release-4939c4cfa` again and set the transcode nodes back from their
`.bak-pre-341108c6e` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is; going back only restores the slower
card, item detail and episode-list reads. Restoring the database to the restore point
together with the container snapshot also rolls back, but discards everything written since
the restore point, taken just before the 00:25 restart.
