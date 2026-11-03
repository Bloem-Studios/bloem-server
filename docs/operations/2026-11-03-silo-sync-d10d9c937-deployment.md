# Bloem deployment — November 3, 2026: Silo through `d10d9c937`

Production has run `main` at `dc4f67200dee0d35d6dbb1f5d1eec5ff6cda8f74` since 16:13:05 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `aa9e17d90`, which the
[`a5325887c` receipt](2026-11-03-silo-sync-a5325887c-deployment.md) recorded. Since
`aa9e17d90` production gained:

- `dde06d221`: the previous deployment's receipt.
- `eaa421c14`: the receipt for the storage ebook placeholder ISBN deployment, and the rule
  explained in the plain part of the native storage page.
- `c9bec2467`: the merge of Silo `main` from `a5325887c` through `d10d9c937` (November 3,
  14:15 UTC), one Silo commit with no migration. Silo's #3396 moves the parsing of a
  metadata provider's declared capability (content levels, default priorities and
  enablement, lookup and rating sources, chain placement) unchanged from
  `internal/metadata/chain.go` into `internal/metadata/capability_metadata.go`. Nothing
  changes for users.
- `460cdddae`: regenerated client DTOs (the server revision stamp only).
- `1772a6fe5`: the status rows for the batch.
- `dc4f67200`: `main` merged into the batch after `eaa421c14` landed during its checks.

## Merge resolutions

None. The merge had no conflicts and touched no seam: Bloem has no lines in either metadata
file. No new seam; the seam ledger stays at 309. No new wire types, so the client coverage
and digest are unchanged.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-dc4f67200` (image `sha256:91665e9427d8bbe6883a91ffa3b60196155396116343d8bf7ab71147fae55d14`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `ee7f860b822ac9ff622eaca59f63fa22bd9e18da5626ec1d64fae4a66b46b7f8` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-dc4f67200`. Database restore point of
  the same name at LSN `468/6D14BA40`. Segment `00000003000004680000006D`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `aa9e17d90`. One transcode process was
  running inside the server, and the stop took 37 seconds (exit 0). No migration ran; 756 are
  applied. The server answered ready at 16:13:05, 45 seconds after the stop began, and was
  healthy at 16:13:05 with no restarts. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `dc4f6720`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-dc4f67200`, healthy, and the server lists both as enabled and healthy on
  `dc4f67200`. Their previous compose files are kept with suffix `.bak-pre-dc4f67200`.
- **Old images.** The script removed `release-4d2b68167` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. `main` moved once
during the checks (`eaa421c14`, documentation only); after merging it the checks passed 25 of
25 again. No test failed, so no flake was rerun.

## Bloem CI

Run 41668274388 passed on `dc4f67200` at its first attempt.

## Rollback

Start `bloem-server:release-aa9e17d90` again and set the transcode nodes back from their
`.bak-pre-dc4f67200` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is, and the Silo change has no behavior to
undo. Restoring the database to the restore point together with the container snapshot also
rolls back, but discards everything written since the restore point, taken just before the
16:12 restart.
