# Bloem Xtream deployment — 2026-09-19

## Outcome

Bloem deployed commit **`418a18b7d4ae7060ebc574eb882bc525e7e1a4c2`** on
2026-09-19 at 21:26 UTC. The code was committed and pushed to `origin/main` before
cutover. Later documentation commits do not change the deployed application revision.
This is a dated record of one deployment, not a claim that every installation,
container tag or client has upgraded.

The deployed build includes the Xtream implementation and contract follow-through
in `23ee4c1aca52c538bd6ffb791b3d32e074336925`, plus three transport-error capitalization
corrections in `418a18b7d`. It was built from a clean checkout of that exact revision.
The repository Dockerfile was used with separately recorded build-stage resource
bounds (`GOMAXPROCS=2`, `GOFLAGS=-p=2`, `GOWORK=off`). Its local Docker image ID is
`sha256:feef6516526fb7a6ffad91a86e2c14c8512a8cab05ec0b39fffa1d852bc9b3c6`;
this is not a published registry manifest digest.

Only the application service and its `SILO_IMAGE` setting changed. Redis, existing
credentials, mounts, ports and devices were preserved. The measured cutover window
was **14.073 seconds**, from 21:26:01.583355 to 21:26:15.656342 UTC; this is not a
recovery-time objective or a full outage measurement.

At post-deployment verification:

- The container was healthy with zero restarts.
- HTTP health/readiness, setup-complete server identity, web assets, capabilities,
  Jellyfin and main-listener Audiobookshelf probes passed.
- Protected native routes, including Xtream creation, refused unauthenticated requests.
- Chromium rendered the login page with title `Sign In · Bloem`, without page errors
  or server failures. No credentials were submitted in that browser smoke test.
- No tuners or Xtream providers were configured. This deployment did not test a real
  provider, decoded playback or authenticated provider administration.

Host addresses, configuration, encryption keys, database archives, browser artifacts
and raw logs remain in protected operator records, not this repository.

## Database and recovery evidence

A fresh **full PostgreSQL backup** completed before cutover. The migration rehearsal
restored the complete schema and every row of 22 affected or foreign-key-dependent
tables from that backup, then applied both migrations with the exact candidate image:

| Migration | Effect |
| --- | --- |
| `20260919095615_bloem_policy_library_ids_bigint` | Widens eight policy-library arrays while retaining policy authority and rollback snapshots. |
| `20260919133349_bloem_xtream_sources` | Adds encrypted Xtream credential storage and physical-connection leases. |

The successful rehearsal used an isolated internal network without provider or
production egress. An earlier scratch attempt without any network interface failed
Sonyflake initialization before migration; that failure was retained, not counted as
a pass. The corrected image's rehearsal completed at 20:24:34.870350 UTC.

This was a **scoped restored-backup migration rehearsal**, not a new full-catalog
restore or RTO drill. A separate earlier full-archive restore and integrity check
remain historical recovery evidence; they did not rehearse these two migrations.

Production writers were quiesced before migration. Other database clients would
have blocked cutover rather than being killed. Before/after checks confirmed:

- Selected application-table row hashes and array bounds were preserved;
  Goose migration history was checked separately for the new versions.
- Policy trigger definitions, enable modes and comments were preserved.
- All eight policy-library arrays became `bigint[]`.
- The new credential and lease tables were empty; no invalid indexes remained.
- The latest migration was `20260919133349`.

Replacing the application container recycled its database pools. No production Down
migration or backup restore was performed. The previous image's `--migrate-only`
path accepted the additive schema during rehearsal; that proves bootstrap
compatibility, not every older runtime workflow or support for Xtream sources.

## CI result and approved exception

[CI run 35467142414](https://github.com/Bloem-Studios/bloem-server/actions/runs/35467142414)
for the deployed revision **failed**: `internal/scenariocatalog/executor` exceeded
its 20-minute package timeout. The stack showed fixture seeding with bcrypt while
the scenario catalog was progressing; it did not establish a deadlock. There were
no assertion-failure markers, but unfinished scenarios are not passes.

The other **189 Go packages passed**. Build, formatting, vet, vulnerability scan,
changed-line lint, generated-artifact and contract guards passed, as did the web,
tenant-identity, compatibility-foundation and compatibility-Compose jobs. The
separate Discord notification workflow failed and remains unresolved.

The maintainer explicitly approved deploying this exact revision despite this
specific timeout. The exception and evidence hashes were recorded before cutover;
backup, exact-image rehearsal, writer-quiescence, data-preservation and rollback
gates remained in force. **The approval did not turn CI green, certify the unfinished
suite or grant a standing exception for future changes.** No broad local suite was
rerun to support the deployment.

The [contract adjudications](../architecture/bloem-contract-adjudications.md) retain
frozen source catalogs and route snapshots. Their focused passing packets are
separate evidence, not a substitute for the timed-out ordinary gate. See
[scenario runtime](../architecture/scenario-acceptance.md#runtime-budget-and-fixture-lifetime)
for the remaining execution-budget issue.

## Upgrade and rollback boundaries

For another installation, repeat its own backup, rehearsal and migration checks;
this deployment record is not permission to skip them. Read the
[policy-array requirements](../architecture/core-id-range.md#apply-and-rollback) and
[Xtream fleet requirements](../architecture/xtream-live-tv.md#deployment-and-acceptance).
Quiesce writers, allow table/index rewrite headroom and adequate migration time,
and recycle application pools. Upgrade every API/worker binary before configuring
Xtream providers. Back up the existing `SECRET_KEY` separately from the database.

The previous application image and protected configuration are retained for
**image/config-first rollback**, keeping the additive schema and data. Do not run
automatic Down migrations or restore an archive over live data. If settings changed
after cutover, do not blindly overwrite them with an older configuration snapshot.

The previous image is `0366a7b64cbcaf69783f14d30b0f8f6a02b78b91`. Older binaries must
not administer Xtream sources. Before rolling back after provider creation, review
active streams, recordings and new provider state and arrange an explicit
compatibility plan; do not delete that state merely to make rollback possible.
Policy-array Down refuses out-of-range values, and Xtream Down refuses remaining
provider, guide, credential or lease state. A database restore is a separately
authorized recovery operation with possible loss of post-backup changes.

## Remaining acceptance

- Resolve the scenario executor timeout without exclusions, weaker assertions or
  reduced authentication cost.
- Repair the separate notification-workflow prerequisite.
- Exercise an actual provider, authenticated setup/guide/removal workflows and
  decoded raw/HLS/DVR media through the normal network and authority guards.
- Complete keyboard/accessibility, Safari and prolonged-playback acceptance.
- Exercise multi-replica owner loss and cleanup; no seamless failover is claimed.

The [web coverage matrix](../architecture/bloem-web-feature-coverage.md) and
[completion handoff](../architecture/bloem-web-completion-handoff.md) distinguish
implemented features, historical acceptance and these outstanding checks.
