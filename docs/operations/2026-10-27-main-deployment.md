# Bloem Server deployment — October 27, 2026

This receipt records the deployment of `main` with Silo through `3e2d306b2`.
Production had run `d064644a8` since 06:41 UTC on October 26.

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
