# Bloem deployment — November 4, 2026: Silo through `4e3020a26`

Production has run `main` at `28bdc197bfe53b30e1e875f7ae91cb14ec72617b` since 22:01:09 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `c2b43286e`, deployed by hand
at 16:37 UTC with no receipt of its own; the last receipt, for
[Silo through `e3af98b06`](2026-11-04-silo-sync-e3af98b06-deployment.md), recorded
`5d5371d6c`. Since `5d5371d6c` production gained:

- `9ccb0cfc0`: that deployment's receipt.
- `1e6cd18b7`, `b9ae95d76` and `c2b43286e`: web fixes, deployed by hand before this batch
  (`b9ae95d76` at 16:17 UTC, then `c2b43286e`). A row-window move that was left stranded
  lands, catalog sweeps wait for a whole first read, each held first read gets its own
  deadline, and a desktop row window stays inside what Chrome draws, capped by the pixel
  ratio.
- `73cbf3b3a`: the merge of Silo `main` from `e3af98b06` through `4e3020a26` (November 4,
  20:45 UTC), six Silo commits with no migration. Silo's #3507 ranks box sets whose names
  open with the query first, so "harry" finds Harry Potter before Dirty Harry. Silo's #3501
  keeps a deep library grid short enough for the browser to draw, the same fix Bloem
  carried as a temporary seam. Silo's #3489 declares the device headers on the v2 sign-in
  operations, so an over-long device id is refused with 422 instead of being dropped.
  Silo's #3481 reports MDBList's own reason when a list feed refuses. Silo's #3418 runs the
  request router's connection test before the options probe reports a request server
  connected. Silo's #3419 cuts an admin curated list to its item limit on capped aggregate
  section reads.
- `e718ce7cf`: the library grid's temporary seam dropped, because Silo's
  `web/src/components/ItemGrid.tsx` now matches Bloem's byte for byte.
- `c136ee65a`: regenerated client DTOs, whose headers now name the new commit; no wire type
  changed.
- `28bdc197b`: the status rows for the batch.

## Merge resolutions

No conflicts. Git merged Silo's changes to five seams beside Bloem's own lines:
`contracts/api/v2/openapi.json` and `web/src/api/v2/schema.ts` (the device headers),
`internal/requests/service.go` (the connection test, which Bloem's audiobook lane guard and
connection check do not trigger), and `internal/api/handlers/home_views_service.go` and
`library_views_service.go` (the curated-list cut, which runs after Bloem's home section
transform). Bloem needed no fix. No new seam came with the batch; the seam ledger went from
317 to 316 entries, eight of them temporary web fixes that go when Silo merges its own
versions.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-28bdc197b` (image `sha256:803a58ea9a52002c1e325ac10c43ee9f5eec9d40e1308beccf2cd40a8fab0b7e`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `2a2949da45ddeaf108244a1c05b5df7995fb9dd46e78735e79d3f80cfba32f9e` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-28bdc197b`. Database restore point of
  the same name at LSN `48A/D8144D80`. Segment `000000030000048A000000D8`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `c2b43286e`. One transcode process
  was running inside the server, and the stop took 91 seconds (exit 0). No migration ran;
  762 are applied. The server answered ready at 22:01:09, 100 seconds after the stop began,
  and was healthy at the same time. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `28bdc197`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-28bdc197b`, healthy, and the server lists both as enabled and healthy on
  `28bdc197b`. Their previous compose files are kept with suffix `.bak-pre-28bdc197b`.
- **Old images.** The script removed `release-405ff9dcc` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. `main` did not move during the checks.

## Bloem CI

Run 41914606652 on `28bdc197b` failed its first two attempts, each on different Go tests:

| Attempt | Job | Test | Alone |
| --- | --- | --- | --- |
| 1 | Go test (3/4) | `internal/apiv2` `TestBloemThemeGrantHTTPRevocation` | passed alone |
| 1 | Go test (1/4) | `internal/scanner` `TestNativeProbeExitedWrapperCannotLeavePipeOwnerRunning` | passed alone |
| 2 | Go test (3/4) | `internal/apiv2` `TestDirectDownloadLinkDelivery/tampered` (Silo's test) | passed alone |

The batch stopped for the owner, who approved one more rerun of the failed job. The third
attempt passed, and the deploy used that result.

## Rollback

Start `bloem-server:release-c2b43286e` again and set the transcode nodes back from their
`.bak-pre-28bdc197b` compose files; the deploy script's rollback does both. The batch has no
migration, so the previous image runs on the database as it is. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards everything
written since the restore point, taken just before the 21:59 restart.
