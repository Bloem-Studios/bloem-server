# Bloem deployment — November 4, 2026: watched removals from watch-sync plugins

Production has run `main` at `66b56baa30dd8d7d3e0470ade4d18e8c4cb93928` since 22:55:17 UTC.
It was deployed by hand at the owner's request ("go go go") with the merge bot's deploy
script and `--skip-ci-check`; Bloem CI runs on the commit in parallel. It replaces
`6604bfd44`, recorded in the receipt for
[Silo through `6d1562473`](2026-11-04-silo-sync-6d1562473-deployment.md). Since `6604bfd44`
production gained:

- `a6230c24c`: that deployment's receipt.
- `66b56baa3`: watched removals from watch-sync plugins
  ([how removing a title in a tracker clears its watch history](../architecture/bloem-watch-sync-removals.md)).
  A plugin on Bloem Plugin SDK v0.28.0 can report that a person removed an item's watches;
  Bloem removes that connection's profile's watch history, watched marks and resume points
  of the movie, episode or every episode of a series, up to the removal time, and keeps
  anything watched or resumed later. Three temporary seams in `internal/watchsync` carry
  the hooks (seam ledger 316 to 319). No migration.

It went out with Pastime schema 63 (deployed at 22:37 UTC), which records a removal when a
connected profile deletes a movie or show, and the Pastime plugin 0.4.0 (installation
updated at 22:58 UTC), which passes those removals on together with Pastime's favourite,
watchlist and rating removals.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-66b56baa3` (image
  `sha256:a1721c73c819f47080115bc2b93aa94ed75b342b3e5c7e2836f7fd5b2cfe401d`), and loaded it
  on both hosts. The binary `/usr/local/bin/silo` has SHA-256
  `25c22973ffd45169bea7ba3141ad1bf7db93a4e5e2a19a3dda36c9dfb3ff81b8` on both.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-66b56baa3`. Database restore point of
  the same name at LSN `48B/D84CBD48`, in segment `000000030000048B000000D8`.
- **Restart.** The script required the live image to be `6604bfd44`. The stop took 37
  seconds (exit 0). No migration ran. The server answered ready at 22:55:17 and was healthy
  at 22:55:18, 46 seconds after the stop began. Its runtime configuration fingerprint is the
  same as before, and no error lines were logged after the start.
- **Transcode nodes.** Both now run `release-66b56baa3`, healthy, and the server lists both
  as enabled and healthy on `66b56baa3`.
- **Old images.** The script removed `release-5d5371d6c` on both hosts and on the build
  machine; `release-8065e5746` stayed on the build machine because a container still uses it.

## Checks before the push

`go test ./internal/watchsync/...` passed, including the five new tests in
`bloem_watched_removal_test.go`; changed-line lint (golangci-lint v2.15.0) found nothing;
`scripts/verify-seams.sh`, `make verify-local-paths` and
`scripts/bloem/draw-pictures.py --check` passed. The commit was rebased onto `a6230c24c` and
the watched-removal tests rerun before the push.

## Rollback

Start `bloem-server:release-6604bfd44` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The commit has no migration, so the previous image
runs on the database as it is. A previous image reads a watched removal as a watched state
with no plays and reports it as a sync warning, changing nothing. Restoring the database to
the restore point together with the container snapshot also rolls back, but discards
everything written since the restore point, taken just before the 22:54 restart.
