# Bloem Server deployment — October 27, 2026

This receipt records the deployment of `main` with Silo through `3e2d306b2`.
Production had run `d064644a8` since 06:41 UTC on October 26
([previous receipt](2026-10-26-stage-a-deployment.md#follow-up-deployment-account-provisioning-0641-utc)).
It also records the follow-up deployment of `276becf4a`, two PostgreSQL
configuration changes the same morning, the afternoon deployment of
`7a3b59027`, the evening deployments of `bee8a7728` and `aeeea2357`, and the
deployment of `9cc80cd33` on the morning of October 28. It is the newest
deployment receipt.

## How these deployments are done

This section explains the procedure the receipt refers to. The facts are in
the sections below.

- **Where the image comes from.** After Bloem CI passes on a push to `main`,
  the [Bloem Image workflow](../bloem/ci.md#bloem-image) builds that exact
  commit and publishes `ghcr.io/bloem-studios/bloem-server:<full sha>`. The
  image is copied to the application host with `docker save | docker load`
  and tagged `bloem-server:release-<short sha>`, the tag production runs. Both
  deployments here used images CI published. Earlier deployments used images
  built by hand ([October 26](2026-10-26-main-deployment.md#build)).
- **What is backed up first.** Each backup is named after the release
  (`pre-main-<short sha>`):
  - a snapshot of the container the application runs in;
  - a named PostgreSQL restore point, a marker in the write-ahead log (WAL)
    that point-in-time recovery can stop at. It is usable only once the WAL
    segment that holds it has been archived, so the receipt records when that
    happened;
  - when migrations run, a dump of the tables they write to.
- **What the restart script guards.** The restart is done by an operator
  script that is not part of this repository. It refuses to run unless the
  live image is the expected previous release, so it cannot replace some
  other deployment by mistake. It stops the server, applies any pending
  migrations while the server is stopped ("offline"), and starts the new
  image. The receipt then compares the container's runtime fingerprint (its
  configuration and mounts) with the one before the restart, and checks
  readiness, health, migrations and the logs.
- **How to roll back.** Whether a rollback needs the database restored depends
  on whether the schema changed:
  - migrations ran (`422a51fed`): they are forward-only, so restore the
    database to the restore point together with the container snapshot.
    Starting the old image on the new schema is not a rollback.
  - no migrations ran (`276becf4a`): start the previous release image again.
  - the PostgreSQL configuration changes name their own backup files.
- **What the checks show.** The server started on the new revision, healthy,
  with the expected migrations and no error lines, and active streams
  continued. "Silo switching is enabled" means the database can still be
  handed back to Silo's server
  ([deployment guide](../bloem/overlays/wiki/deployment/docker.md#switching-between-silo-and-bloem)).
  None of this is client or playback acceptance.

## Deployed source and artifacts

| Item | Verified value |
| --- | --- |
| Application source on published `main` | `422a51fed3eb8f7ae3ff395b7d055078f97168d4` |
| System-info server version (`/api/v2/system/info`) | `422a51fe` |
| Previous application source | `d064644a8` |
| Runtime image | `bloem-server:release-422a51fed`, loaded from `ghcr.io/bloem-studios/bloem-server:422a51fed3eb8f7ae3ff395b7d055078f97168d4` (`sha256:53b3cc8cd4e688b81d4956932066f91f897870dcd1c0b08a056da58164c28a06`) |
| Server binary SHA-256 | `18953af97ec402857cdd863069e4638d01fe705c93ef2067d373d4d45e26fdea` |
| Migrations applied in this deployment | 3, forward-only; zero pending |
| Applied migration count | 725 |
| Maximum migration version | `20261026191107` |

The three migrations:

- `20261026133552_markers_share_by_default_existing_installs` (Silo): keeps
  marker sharing on for an existing install; production now has
  `markers.share_by_default = true`;
- `20261026170000_bloem_cohort_coherence_ignores_deleted_libraries`: a library
  a cohort group lists can be deleted;
- `20261026191107_allow_full_range_pq_download_tone_map` (Silo): the
  download tone-map check accepts full-range PQ sources; the one stored
  artifact passed the revalidation.

## What changed

Two Silo syncs, 49 Silo commits after `6de6d5e5e`:

- through `8b84639ed`: non-UTF-8 file names, intro marker and scanner fixes,
  web fixes, and the end of Bloem's temporary font extraction backport;
- through `3e2d306b2`: mostly security hardening. Tokens are bound to their
  session's account, also on Bloem's preview, plugin, tenant admin and native
  video paths; v1 progress and stop, and Bloem's durable stop, refuse another
  profile; signed stream and download tokens stay out of logs; account-level
  downloads check profile access; the plugin cache refuses a directory another
  local user controls; image, settings, playlist and webhook inputs are
  bounded. Features: AI providers connected once and chosen per job, the
  embedding provider chosen from AI Services, and playback starting on an
  audio track the client plays without conversion.

Also on `main`: a DST-safe window for Silo's partition timezone test, and
Bloem's cohort-coherence fix.

## Build

This is the first production image built and published by CI: Bloem CI on
`main` passed (attempt 2; attempt 1 lost a cohort entitlements test to a
library another package in its shard deleted), and the Bloem Image workflow
published the pinned image. Its runtime configuration (environment,
entrypoint, command, user, ports, healthcheck, volumes) is identical to
`release-d064644a8`. It was moved to the application host with
`docker save | docker load` and tagged `bloem-server:release-422a51fed`.

## Procedure and result

Before the restart:

- container snapshot `pre-main-422a51fed`;
- database restore point `pre-main-422a51fed` at LSN `3B8/C9441610`; the WAL
  segment holding it was archived at 06:58:00 UTC with no archive failures;
- a dump of `server_settings` and `download_artifacts`, the tables the
  migrations write to.

The restart script refused to run unless the live image was `d064644a8`. It
stopped the server in 9 seconds, applied the three migrations offline in about
a second, and started the new image. The server was ready 16 seconds after the
stop began, healthy with no restarts. The runtime fingerprint did not change.

After start:

- ready and health returned 200 locally; ready and the site returned 200
  publicly;
- zero pending migrations;
- Silo switching is enabled;
- 128 accounts and 128 memberships;
- active streams continued (one FFmpeg process before, three after);
- no ERROR or panic lines in the first minutes; the warnings were cold-cache
  "slow section fetch", ambiguous air-date links and one re-queued download
  artifact.

## Rollback

All three migrations are forward-only. To go back, restore the database to the
restore point above, with the container snapshot, rather than starting the old
image on the new schema.

## Follow-up: `276becf4a`

At 07:55 UTC `main` moved to `276becf4a365203d754b3547130951fd79e9cd83`
(system info `276becf4`). Since `422a51fed` it changes only the web admin
console, where a context refused before its first render now recovers instead
of keeping the refused context, plus a test-only database for the entitlements
package and this receipt. It has no migrations and no server code changes.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-276becf4a`, loaded from `ghcr.io/bloem-studios/bloem-server:276becf4a365203d754b3547130951fd79e9cd83` (`sha256:75fd2e3e12e4a6bce4b57dd0f8e94d72633c5909a5cf6fda1212eaba52b80364`) |
| Server binary SHA-256 | `115443118122a6972593a01c03f8f2e4d5ca634194c73fd6c064da4b03c73542`, the same in the published image and on the application host |
| Migrations | none applied; 725 applied, maximum `20261026191107`, zero pending |

Bloem CI on `main` passed on attempt 2. On attempt 1 the job VM for Go test
shard 2 powered off mid-run: its runner service was stopped, and the unit's
stop hook shuts the VM down. The VM used 16.8 GB of its 24 GB and the node had
memory to spare. The VM was destroyed before it could be inspected, so what
stopped the runner is not known. The image's runtime configuration is
identical to `release-422a51fed`.

Before the restart: container snapshot `pre-main-276becf4a`, and database
restore point `pre-main-276becf4a` at LSN `3B8/F45BF910`, whose WAL segment
was archived at 07:54:27 UTC with no archive failures.

The restart script refused to run unless the live image was `422a51fed`. It
stopped the server in 6 seconds, found no migrations to apply, and started
the new image. Ready returned 200 12 seconds after the stop began, and the
server was healthy with no restarts. The runtime fingerprint did not change.
Ready and health returned 200 locally, and ready and the site returned 200
publicly. Active streams continued (one FFmpeg process before, two after).
There were no ERROR or panic lines.

Three warnings in the first minute need no action for this deployment:

- the ebook metadata sources rate-limited enrichment searches;
- `PutBucketCors` on the private assets bucket returned 403 "not entitled";
- the server reports that its auto-tuned Postgres settings need a Postgres
  restart, but the database has no setting pending a restart.

To roll back, start `bloem-server:release-422a51fed` again: the schema did
not change.

## Follow-up: PostgreSQL tuning sized for the database container

Bloem tunes PostgreSQL with `ALTER SYSTEM` on every start (`POSTGRES_TUNE=on`,
`POSTGRES_TUNE_MEMORY=64GB`). The database runs in its own container with 12
cores and 64 GiB, but `POSTGRES_TUNE_CPUS` was never set, so the tuner counted
the application container's 32 cores. At 08:13 UTC the application's `.env`
gained `POSTGRES_TUNE_CPUS=12` and `POSTGRES_TUNE_STORAGE=nvme` (the database
disk is NVMe), and the server was recreated on the same image: 11 seconds of
downtime, healthy with no restarts, ready 200 locally and publicly, no ERROR
or panic lines. The runtime fingerprint changed only because the environment
did. Backups: `.env.bak-tune-cpus-20261027T081254Z` beside the `.env`, and
a copy of `postgresql.auto.conf` taken at `20261027T081254Z` on the
database container.

| Setting | Before | After |
| --- | --- | --- |
| `max_parallel_workers` | 32 | 12 |
| `max_worker_processes` | 32 | 12 (after the 08:18 restart below) |
| `work_mem` | `114390kB` | `134816kB` |
| `effective_io_concurrency` | 200 | 1000 |
| `autovacuum_max_workers` | 5 | 3 (default) |
| `io_workers` | 8 | 3 (default) |

The memory-based settings (`shared_buffers` 16 GB, `effective_cache_size`
48 GB) did not change. Every setting except `max_worker_processes` took effect
on reload.

The start-up warning "postgres restart required to finish applying auto-tuned
settings" appears on every start: the tuner lists every restart-only setting
it writes, whether or not the running value already matches. Before this
change PostgreSQL had no setting pending a restart. After it, only
`max_worker_processes` was pending.

At 08:18 UTC both containers were restarted in order so the application never
ran without its database: the application container was shut down (11
seconds), the database container rebooted (PostgreSQL accepting connections
23 seconds later), then the application container started. The site was down
for 51 seconds in total. PostgreSQL now runs `max_worker_processes = 12` with
no setting pending a restart. The streaming standby (which keeps
`max_worker_processes = 32`, at least the primary's value as a hot standby
requires) resumed streaming at once. WAL archiving continued with no failures,
and the monitor reconnected. The server came back healthy with no restarts on
`276becf4`, ready and health returned 200 locally and publicly, both Redis
instances answered, and there were no ERROR or panic lines.

At 08:28 UTC auto-tuning was turned off (`POSTGRES_TUNE=off`), as the
deployment guide advises for an external database. The tuned values stay in
`postgresql.auto.conf` on the database container. The `POSTGRES_TUNE_CPUS`,
`POSTGRES_TUNE_MEMORY` and `POSTGRES_TUNE_STORAGE` lines stay in the `.env` as
a record of what the values were sized for, and are used again only if tuning
is turned back on. If the database container's cores, memory or disk change,
re-tune it by hand. The server was recreated on the same image: 10 seconds of
downtime, healthy with no restarts, ready 200 locally and publicly. It logged
no tuning lines. `postgresql.auto.conf` was not rewritten (same checksum and
modification time), and all 20 tuned settings were unchanged. Backup:
`.env.bak-tune-off-20261027T082745Z`.

## Follow-up: `7a3b59027`

At 13:06 UTC production moved to `main` at
`7a3b5902756ebfb8eb4abc341cdbbd810d368a65` (system info `7a3b5902`). Since
`276becf4a` it brings:

- the standalone proxy's source check, which now resolves an account through
  its own organization (`a6d342390`; it matters only on servers running
  `SILO_MODE=proxy`);
- the native OpenAPI checked against every handler (`8fe96aadc`, `67429b073`),
  and tenant retirement without the member service answering
  `503 tenant_unavailable`;
- three handler fixes (`7a3b59027`): organization invitations accept only the
  organization's own access groups and libraries, a committed direct-account
  entitlement apply is no longer answered as `409 confirmation_target_changed`,
  and a repeated Live TV release no longer moves the session's end or rewrites
  its history;
- CI, image-workflow and documentation changes. There are no migrations.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-7a3b59027`, loaded from `ghcr.io/bloem-studios/bloem-server:7a3b5902756ebfb8eb4abc341cdbbd810d368a65` (`sha256:075949f9ca8480fb95ba2f5d3a04ba842654cfc9ca516ae3b75f4020b1a394e8`) |
| Server binary SHA-256 | `56af0ee4692298f8eac15d314bebce54ad3fb073d3dd4540b7f208c648ed959a`, the same in the published image and on the application host |
| Migrations | none applied; 725 applied, maximum `20261026191107`, zero pending |

Bloem CI on `main` passed on the first attempt, and Bloem Image published the
image and moved `latest` to it. The image's runtime configuration is identical
to `release-276becf4a`.

Before the restart: container snapshot `pre-main-7a3b59027`, and database
restore point `pre-main-7a3b59027` at LSN `3B9/BC3F0BF8`, whose WAL segment
was archived at 13:06:06 UTC with no archive failures. The standby was
streaming.

The restart script refused to run unless the live image was `276becf4a`. It
stopped the server in 6 seconds, found no migrations to apply, and started
the new image. Ready returned 200 12 seconds after the stop began, and the
server was healthy with no restarts. The runtime fingerprint did not change.
Ready and health returned 200 locally, and ready, health and the site returned
200 publicly. Active streams continued (one FFmpeg process before, two after).
One download being prepared when the server stopped was re-queued, as the
download worker does for an artifact whose output is missing. There were no
ERROR or panic lines, and no tuning lines (tuning stays off). The only other
warnings were the known `PutBucketCors` 403 on the private assets bucket and
an HDR10+ backfill that will read one file again on its next pass.

To roll back, start `bloem-server:release-276becf4a` again: the schema did
not change.

## Follow-up: `bee8a7728`

At 22:27 UTC production moved to `main` at
`bee8a77280d1e44a764ef7de086216de6a3c22d5` (system info `bee8a772`). Since
`7a3b59027` it brings the rest of the OpenAPI review's status fixes
(`bee8a7728`, see the [changelog](../bloem/CHANGELOG.md)) and the receipt
above. There are no migrations and no web changes.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-bee8a7728`, loaded from `ghcr.io/bloem-studios/bloem-server:bee8a77280d1e44a764ef7de086216de6a3c22d5` (`sha256:ff7501ae61560f25ac1230053400abae2155c043362f221ab0359d98c0e60981`) |
| Server binary SHA-256 | `ce08b7c03346beca3d881f64da793d37b09cd60ecd92ae576d8f3475ba2ec021`, the same in the published image and on the application host |
| Migrations | none applied; 725 applied, maximum `20261026191107`, zero pending |

Bloem CI on `main` passed on attempt 2. On attempt 1 the database-contracts
job lost two Silo recommendation tests that this commit does not touch and
that pass locally; the rerun of that job passed. Bloem Image then published
the image. Its runtime configuration is identical to `release-7a3b59027`.

Earlier that evening the application and database containers moved to
another Proxmox node, with a backup first. The container snapshots named in
the sections above (`pre-main-422a51fed`, `pre-main-276becf4a`,
`pre-main-7a3b59027`) were deleted for the move and no longer exist; the
database restore points are unaffected. The streaming standby stayed on the
old node.

Before the restart: container snapshot `pre-main-bee8a7728`, and database
restore point `pre-main-bee8a7728` at LSN `3BA/B66718A0`, whose WAL segment
was archived at 22:27:02 UTC with no archive failures. The standby was
streaming.

The restart script refused to run unless the live image was `7a3b59027`. It
stopped the server in 6 seconds, found no migrations to apply, and started
the new image. Ready returned 200 12 seconds after the stop began, and the
server was healthy with no restarts. The runtime fingerprint did not change.
Ready and health returned 200 locally, and ready, health and the site returned
200 publicly. Active streams continued (one FFmpeg process before, two after).
An unauthenticated administrative request is now refused as `401` with
`Cache-Control: no-store`. There were no ERROR or panic lines and no tuning
lines. The warnings were the same three as at `7a3b59027`: the `PutBucketCors`
403 on the private assets bucket, the HDR10+ backfill that will read one file
again, and the same download artifact re-queued by the restart.

To roll back, start `bloem-server:release-7a3b59027` again: the schema did
not change.

## Follow-up: `aeeea2357`

At 23:52 UTC production moved to `main` at
`aeeea23578d2ca1e822d99e20b0874b3818bf60e` (system info `aeeea235`). Since
`bee8a7728` it brings Silo through `73a6ae6a3`, merged in `7c431d470`
([changelog](../bloem/CHANGELOG.md)), and `golang.org/x/net` v0.62.0
(`aeeea2357`), which fixes two advisories the vulnerability scan began
reporting that evening (GO-2026-6866 and GO-2026-6860). It has one
migration and web changes.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-aeeea2357`, loaded from `ghcr.io/bloem-studios/bloem-server:aeeea23578d2ca1e822d99e20b0874b3818bf60e` (`sha256:e390807c5c17f56ea2fb8652d825ad20a672647358c4b3916e4870d06eb163b5`) |
| Server binary SHA-256 | `e88d5750a30f94bf5ee0a9c466979782f7d27e29e111880eecaf3bffd7863079`, the same in the published image and on the application host |
| Migrations | 1 applied, forward-only; 726 applied, maximum `20261027002724`, zero pending |

The migration, `20261027002724_retire_card_caption_choice` (Silo), resets
every stored `ui.card_presentation` caption to `title_metadata`. Production had
11 such settings, 4 with another caption; after the migration none do.

Bloem CI on `main` passed on the first attempt, and Bloem Image published the
image. Bloem CI on the merge commit before it had failed: the vulnerability
scan found the two advisories, and the contract checks lost their runner's
DNS. The image's runtime configuration is identical to `release-bee8a7728`.

Before the restart:

- container snapshot `pre-main-aeeea2357`;
- database restore point `pre-main-aeeea2357` at LSN `3BA/E0F94410`; the WAL
  segment holding it was archived at 23:52:04 UTC with no archive failures,
  and the standby was streaming;
- a dump of `user_setting_values`, the table the migration writes to, and
  `goose_db_version`.

The restart script refused to run unless the live image was `bee8a7728`. The
server took 37 seconds to stop (6 seconds at the previous two restarts) and
exited cleanly. The migration applied offline in 5 ms, and the new image
started. Ready returned 200 43 seconds after the stop began, and the server
was healthy with no restarts. The runtime fingerprint did not change. Ready
and health returned 200 locally, and ready, health and the site returned 200
publicly. Active streams continued (one FFmpeg process before, two after).
There were no ERROR or panic lines and no tuning lines. The first home page
after the start logged slow section fetches (7.7 seconds in all) while the
caches were cold, with no further slow-section warnings in the minutes after.
The HDR10+ backfill warning is the known one.

The migration is forward-only. To roll back, restore the database to the
restore point above together with the container snapshot. The dump restores
the four rewritten captions on their own.

## Follow-up: `9cc80cd33`

At 07:28 UTC on October 28 production moved to `main` at
`9cc80cd33150921bd07c105a04d12b15f3e61689` (system info `9cc80cd3`). Since
`aeeea2357` it brings Silo through `564b906b8`, merged in `5fc698915`
([changelog](../bloem/CHANGELOG.md)): the Requests Discover page an admin lays
out, and series artwork, dates and ratings on API v2 episode cards. It has one
migration and web changes.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-9cc80cd33`, built on the operator's workstation, revision label `9cc80cd33150921bd07c105a04d12b15f3e61689` |
| Server binary SHA-256 | `2212b8784aa3bca3ed4a827cbb3d0400ff9ca628089a6244574a51738ddfc24e`, the same in the built image and on the application host |
| Migrations | 1 applied, forward-only; 727 applied, maximum `20261027182504`, zero pending |

The migration, `20261027182504_request_discover_rows` (Silo), creates the
empty tables `request_discover_layout` and `request_discover_list_snapshots`
and changes no existing table. With no layout stored, the Discover tab shows
Silo's default rows.

**This image was not published by Bloem Image.** The deployment was asked for
while Bloem CI on `main` was still running, so the image was built locally
before CI finished. It used the steps the Bloem Image workflow uses: a
`git archive` of `9cc80cd33`, the web bundle Bloem CI's Web (build) job had
already produced for that commit (artifact
`frontend-dist-9cc80cd33150921bd07c105a04d12b15f3e61689`), and the workflow's
build arguments and labels. At that point Bloem CI's web build and contract
checks had passed. The merge had passed locally: the Go suite, the 377
database pins, the library-location tests, `go vet` and lint of the changed
lines. The image's runtime configuration is identical to
`release-aeeea2357`.

Before the restart:

- container snapshot `pre-main-9cc80cd33`;
- database restore point `pre-main-9cc80cd33` at LSN `3BE/4C860190`; the WAL
  segment holding it was archived at 07:28:17 UTC with no archive failures,
  and the standby was streaming;
- a dump of `goose_db_version`. The migration writes to no existing table.

The restart script refused to run unless the live image was `aeeea2357`. It
stopped the server in 9 seconds, applied the migration offline in about a
second, and started the new image. Ready returned 200 15 seconds after the
stop began, and the server was healthy with no restarts. The runtime
fingerprint did not change. Ready and health returned 200 locally, and ready,
health and the site returned 200 publicly. Active streams continued (two
FFmpeg processes before and after). Both new tables exist and are empty. There
were no panic lines and no tuning lines. The one ERROR line was the
recommendation embeddings job: the Gemini embedding API answered `402`
because the AI provider account's prepaid credit is used up. That needs a
top-up or another embedding provider, not a code change. The warnings were
the cold-cache slow section fetches on the first home page (10 seconds in
all), the known `PutBucketCors` 403 on the private assets bucket and the known
HDR10+ backfill.

The migration is forward-only. To roll back, restore the database to the
restore point above together with the container snapshot.
