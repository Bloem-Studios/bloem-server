# Bloem deployment — October 31, 2026: Silo through `07c05cad6`

Production has run `main` at `8065e5746e3ae857a12e16bda3285a731a63f03a` since 16:23 UTC.
The commit merges 68 Silo commits after `cc8514943`, through `07c05cad6`, under the
rule that Silo wins every conflict. The owner asked for a push and deploy. The image
was built locally without waiting for CI.

## What changed

Silo's main line through `07c05cad6`. The changes include:

- an hourly request quota per sign-in session and an admin API clients card (#3075)
- Home rows that read 20 cards at a time, with `item_limit` on the home layout and
  section reads (#3276)
- zstd and brotli response compression (#3268)
- permessage-deflate on every websocket (#3275)
- the 8h access-token default (#3250)
- admin egress split by proxy node (#3256)
- account last activity in its own table (#3243)
- two security fixes: scoped API keys are judged on the path the router serves
  (#3307), and administrators can no longer set the JWT signing keys (#3309)

The merge commit message records how each conflict was resolved. The seam ledger
dropped one entry, because the partition timezone test now matches Silo.

## Migrations

Seven Silo migrations, all additive. `20261028150609_audiobook_runtime_from_files`
recomputes every unlocked audiobook's runtime from its files. Production has 370,939
audiobook items, and a sample showed about three quarters needing a new value. A
500-item update, timed inside a transaction that was rolled back, ran at about 320
rows a second. At that rate the offline restart kit would have kept the server down for
about 15 minutes.

The owner chose to migrate online instead. The new image's `--migrate-only` ran beside
the live `b99aa105c` server with `SILO_MIGRATE_TIMEOUT=60m`, because the backfill
estimate was close to the 20-minute default deadline. It took 126 seconds from 16:20:00
UTC. The audiobook backfill took 2 min 1 s, and the `user_last_activity` backfill over
`activity_log` (about 5.7 million rows) took 2.7 s. 747 migrations are applied, none
pending. The live server stayed ready throughout. Afterwards the access-token default
read `8h` and `pg_stat_statements` was installed.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-8065e5746` and loaded on CT135 and the transcode node
  CT172. The binary `/usr/local/bin/silo` has SHA-256
  `43895f3bbffcec16429ff07ff93742c76636049540ff514f112cf730ea371756`.
- **Before any schema change:** container snapshot `pre-main-8065e5746`. Database
  restore point of the same name at LSN `40A/D38B4150`. Segment
  `000000030000040A000000D3`, which holds it, was archived after a WAL switch, with
  no archive failures.
- **Restart.** The kit's restart script required the live image to be `b99aa105c`. Two
  transcodes were running, and the stop took 45 seconds (exit 0). The kit's migration
  step found nothing pending. The server answered ready at 16:23:11, 52 seconds after
  the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `8065e574`. The new `/api/v2/admin/stats/api-clients` route answers 401
  without a credential. No error lines were logged after the start, and transcodes
  were running again. Most warnings were chapter thumbnail extractions that failed on
  individual files with "no packets". Silo did not change that code in this merge.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-8065e5746`, healthy. The API marked both nodes unhealthy while they
  restarted and recovered both at 16:24:07. Their previous compose files are kept with
  suffix `.bak-pre-8065e5746`.

## Verification before deploy

Done locally on the merge, at 8065e5746 and the commits folded into it:

- `go build`, `go vet` and `govulncheck`
- the full Go suite against a PostgreSQL sized like CI's
- Bloem CI's changed-lines lint, with no issues on Bloem lines
- every contract check
- web format, lint, typecheck, build, bundle budget and the full web test suite
- an upgrade from `main`'s previous schema

## Rollback

Start `bloem-server:release-b99aa105c` again. It ran beside the new schema for three
minutes during the online migration. The migrations only add tables, functions,
triggers and an extension, plus two data changes (audiobook runtimes and the 1h-to-8h
access-token default), so that image keeps working. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards
everything written since 16:19.
