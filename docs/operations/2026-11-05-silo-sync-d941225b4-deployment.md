# Bloem deployment — November 5, 2026: Silo through `d941225b4`

Production has run `main` at `a9f230203c328af14ed374344aac65d5c52d39e3` since 14:25:10 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `38156af18`, recorded in the
receipt for [Silo through `2d5f22e7d`](2026-11-05-silo-sync-2d5f22e7d-deployment.md). Since
`38156af18` production gained:

- `4aa7a4395`: that deployment's receipt.
- `2f04bcd2a`: the merge of Silo `main` from `2d5f22e7d` through `d941225b4` (November 5,
  13:36 UTC), one Silo commit with no migrations: a series' season and episode pages opened
  from a library keep that library in their links and episode lists, and an episode opens
  under a library that holds only its series, while Play and watch-together still pick from
  every library (Silo #2944).
- `a9f230203`: the status rows for the batch.

No client DTO, fixture, catalogue or scenario pin changed; the regenerated client DTOs
differed only in their revision stamp, so they were left as they were.

## Merge resolutions

One file conflicted, and its hunk took Silo's side verbatim:

- `web/src/pages/ItemDetail/SeasonContent.test.tsx`: Silo's three new library-scoping tests
  replace the "does not pass rating props to ActionBar" test, a Silo test Silo had pruned in
  #1869 that an earlier Bloem resolution kept. Bloem's query-client wrapper and its other
  lines outside the conflict stay.

Silo's other seam in the batch, `internal/catalog/access_filter.go`, merged cleanly: Silo's new
`SeriesLibraryCoversEpisodes` field sits beside Bloem's own `PlaybackDenied` and
`DownloadDenied` lines. The field is off by default and only Silo's native v2 reads turn it
on; Bloem's tenancy still limits the libraries a viewer can reach. Bloem needed no fix, and the
seam ledger stays at 320 entries.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-a9f230203` (image `sha256:e7c831bd0b2ca921d92a4a292825128a59e43516a40e3f45f77b8c8ae8d16523`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `ea4d806967e2476eedc80f00e34315effb363b44c2724704602c2b1b202b22c3` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-a9f230203`. Database restore point of
  the same name at LSN `49D/259DDC98`. Segment `000000030000049D00000025`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `38156af18`. One transcode process
  was running inside the server, and the stop took 7 seconds (exit 0). No migrations ran;
  765 are applied. The server answered ready at 14:25:10 and healthy at 14:25:12, 13 seconds
  after the stop began until ready. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `a9f23020`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-a9f230203`, healthy, and the server lists both as enabled and healthy on
  `a9f230203`.
- **Old images.** The script removed `release-66b56baa3` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. The ItemDetail and query-hook web tests (173 files) and the `internal/catalog`,
`internal/apiv2` and `internal/api/handlers` Go packages passed after the merge. No test
needed a rerun alone. `main` did not move during the checks.

## Bloem CI

Run 42041720456 on `a9f230203` passed at its first attempt, and the deploy used that result.

## Rollback

Start `bloem-server:release-38156af18` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The batch has no migrations, so the previous image
runs on the database as it is. Restoring the database to the restore point together with the
container snapshot also rolls back, but discards everything written since the restore point,
taken just before the 14:24 restart.
