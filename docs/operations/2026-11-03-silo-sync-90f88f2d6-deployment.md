# Bloem deployment — November 3, 2026: Silo through `90f88f2d6`

Production has run `main` at `4d2b68167078f32311f997e8a4f82273340bfd8e` since 01:06:17 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `4f7cfbc9b` and adds:

- `9bcb8d421`: the merge of Silo `main` from `bf53b6545` through `90f88f2d6` (November 3,
  00:17 UTC), one Silo commit with one migration. Silo's #3240 lists a movie on the calendar
  on its first home release, digital or disc, instead of its theatrical date, and gives the
  event a `digital` or `physical` badge on `/api/v2` (`/api/v1` drops those badges). The
  dates come from the metadata provider and are kept current on every refresh; a corrected
  match replaces them. Migration `20261029101739_add_movie_home_release_dates.sql` adds the
  nullable `media_items.digital_release_date` and `physical_release_date` columns and the
  concurrent index `idx_media_items_movie_home_release_date`.
- `dea57c4af`: regenerated client DTOs, which only take the new server revision.
- `4d2b68167`: the status rows for the batch.
- The commit pushed since `4f7cfbc9b`: the previous deployment's receipt (`f5e1991dd`).

## Merge resolutions

None. The merge had no conflicts and needed no Bloem fix. It touched four seams only on
Silo's own lines (`contracts/api/v2/openapi.json`, `internal/apiv2/home.go`,
`internal/models/media.go`, `web/src/api/v2/schema.ts`), and Bloem's batch item upsert picks
up the two new columns through Silo's `writeItem`. The seam ledger stays at 309.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-4d2b68167` (image `sha256:aa5e3528a73fa554352e5ce0f008a5d4bab1258eb64cb784e2ba3e13c0854394`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `c75f5fdfbd6422a5daaac60b7060aec66a7e9643758558f8bdebad81d90d1f94` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-4d2b68167`. Database restore point of
  the same name at LSN `455/16221820`. Segment `000000030000045500000016`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `4f7cfbc9b`. One transcode was
  running inside the server, and the stop took 37 seconds (exit 0). One migration ran, from
  01:06:05 to 01:06:09; 755 are applied. The server answered ready at 01:06:17, 49 seconds
  after the stop began, and was healthy at 01:06:21 with no restarts. Its runtime
  configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `4d2b6816`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-4d2b68167`, healthy, and the server lists both as enabled and healthy on
  `4d2b68167`. Their previous compose files are kept with suffix `.bak-pre-4d2b68167`.
- **Old images.** The script removed `release-46354baee` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. No test failed, so
no flake was rerun.

## Bloem CI

Run 41555071738 passed on `4d2b68167` at its first attempt.

## Rollback

Start `bloem-server:release-4f7cfbc9b` again and set the transcode nodes back from their
`.bak-pre-4d2b68167` compose files; the deploy script's rollback does both. The migration
only added two nullable columns and an index, which the previous image neither reads nor
writes, so the previous image runs on the database as it is. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards everything
written since the restore point, taken just before the 01:05 restart.
