# Bloem deployment — November 3, 2026: Silo through `a5325887c`

Production has run `main` at `aa9e17d90cd1dcb9512c97d347ee1110a3f57384` since 15:23:38 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `040b55f10`, which production
ran before this deployment: that commit carries the placeholder ISBN fix below and went live
outside the merge bot, without a receipt of its own, after the
[`bb1ee4352` receipt](2026-11-03-silo-sync-bb1ee4352-deployment.md) recorded `3a3fae193`.
Since `3a3fae193` production gained:

- `2dcd1aea3`: the previous deployment's receipt.
- `962a3f1a9` and `040b55f10`: storage ebooks that share a placeholder ISBN (such as
  Bookwarehouse's `0000000000000`) are no longer merged into one catalog item; they group by
  title and author instead.
- `7c6025490`: the merge of Silo `main` from `bb1ee4352` through `a5325887c` (November 3,
  13:39 UTC), one Silo commit with no migration. Silo's #3434 keeps box sets out of library
  grids: a box set now shows in search (when the library's `box_sets_in_search` is on), on
  its own page and as a row on a movie page. Library browse lists each movie, and the
  `group=box_set` browse option is gone from `listCatalogItems`, `queryCatalogItems` and the
  letter index.
- `98a137f25`: regenerated client DTOs and the re-pinned client digest (see below).
- `aa9e17d90`: the status rows for the batch.

## Merge resolutions

None. The merge had no conflicts. It touched four seams: `contracts/api/v2/openapi.json`,
`internal/apiv2/libraries.go`, `web/src/api/types.ts` and `web/src/api/v2/schema.ts`. Bloem's
lines there carry storage-source and preview fields, in different hunks from Silo's box set
change. No Bloem code used the box set grid, and Bloem's preview catalog wrapper keeps
`Browse`'s unchanged signature. No new seam; the seam ledger stays at 309.

The batch removes wire shape: `CatalogItem` loses `box_set`, the catalog list query loses
`group`, and `internal/apiv2.CatalogBoxSet` is no longer reached from a registered client
root. `98a137f25` gives `CatalogBoxSet` a coverage reason beside its box set siblings (it still
reaches clients through the box set reads) and re-pins the client digest. Silo recorded the
removal in its own `contracts/api/v2/breaking-approvals.json`; Silo's removals table in
`docs/architecture/v1-scope.md` stays as Silo wrote it.

## Procedure and result

- **First attempt.** The first run of the deploy script stopped at its build step, before
  any change to production: the merge bot's worktree had no installed web dependencies to
  copy into the build. After installing them (`pnpm install --frozen-lockfile`, lockfile
  unchanged), the second run deployed.
- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-aa9e17d90` (image `sha256:cc435c5973901b222362864b9d6d6660018a4336578c7f9f0c4dff2dc870baa6`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `af32329c27c6e68ca490018d7b31ad241769a606f906daaadc327d2941b44549` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-aa9e17d90`. Database restore point of
  the same name at LSN `467/578CF30`. Segment `000000030000046700000005`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `040b55f10`. No transcodes were
  running inside the server, and the stop took 36 seconds (exit 0). No migration ran; 756 are
  applied. The server answered ready at 15:23:38, 42 seconds after the stop began, and was
  healthy at 15:23:40 with no restarts. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `aa9e17d9`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-aa9e17d90`, healthy, and the server lists both as enabled and healthy on
  `aa9e17d90`. Their previous compose files are kept with suffix `.bak-pre-aa9e17d90`.
- **Old images.** The script removed `release-4f7cfbc9b` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. No test failed, so
no flake was rerun.

## Bloem CI

Run 41650914542 passed on `aa9e17d90` at its first attempt.

## Rollback

Start `bloem-server:release-040b55f10` again and set the transcode nodes back from their
`.bak-pre-aa9e17d90` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is, and it brings back box set cards in
library grids. Restoring the database to the restore point together with the container
snapshot also rolls back, but discards everything written since the restore point, taken just
before the 15:22 restart.
