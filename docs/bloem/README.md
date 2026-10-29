# Bloem documentation

Index of Bloem-owned documentation in this tracking fork of Silo Server. A
**tracking fork** keeps Silo's Git history and merges Silo's `main` as it moves
(see [fork provenance](../../FORK.md)). Any Markdown file that also exists in
Silo's `main`, including the root `README.md`, `CONTRIBUTING.md`, `AGENTS.md`
and most of `docs/`, is Silo's and stays as Silo wrote it. Bloem's material
lives in the Bloem-owned files listed here; where Bloem differs from a Silo
document, an **overlay** records the difference. The code is the source of truth.

Pages marked *historical* are dated records: their facts are true to their date,
not to the current source. The current state is on the
[front page](../../.github/README.md#current-state).

## Start here

- [Bloem Server front page](../../.github/README.md) — what Bloem is, where it comes from, current state, quick start. GitHub shows it in place of the root `README.md`, which is Silo's.
- [Fork provenance](../../FORK.md) — tracking-fork policy, the "Silo wins" rule, seams and overlays, what Bloem adds and removed, the current integration boundary.
- [Agent instructions](../../BLOEM_AGENTS.md) — Bloem's overrides of Silo's `AGENTS.md` / `CLAUDE.md`; they take precedence where the two differ.
- [Contributing to Bloem](../../.github/CONTRIBUTING.md) — repository ownership, AI disclosure and the validation commands; the root `CONTRIBUTING.md` is Silo's.
- [Integration changelog](CHANGELOG.md) — every Silo integration and Bloem change, newest first, with commits and seam counts.
- [Documentation workflow](documentation-workflow.md) — Silo's manual/reference model, where each kind of Bloem change is documented, regeneration and verification commands.
- [Bloem CI](ci.md) — the Bloem CI jobs, the daily upstream sync, the published image's gate, runner placement and tool pins.
- [API catalogue](api-catalogue.md) — generated list of typed `/api/v2` and `/api/bloem/v1` operations and native documentation gaps.

## Deployment receipts

Dated records of what production ran, how it was deployed and what was checked.
A receipt is not native-client or playback acceptance.

- [October 29 deployment](../operations/2026-10-29-silo-sync-free-play-deployment.md) — current: `38e587805` (Silo through `cc8514943` and preview free play, ten forward-only migrations, 17 min 41 s offline for Silo's marker backfill, an image built locally before CI finished), then `cc37f3900` (monitoring API keys open the events socket, no migrations), then `09dab0097` (AI subtitles from word-only speech-to-text responses, Chinese and Japanese without spaces and in shorter lines, no migrations).
- [October 27 main deployment](../operations/2026-10-27-main-deployment.md) — historical: `422a51fed` (Silo through `3e2d306b2`, the first CI-built production image, three forward-only migrations), then `276becf4a` (the admin-context fix, no migrations), PostgreSQL tuning sized for the database container and then turned off, then `7a3b59027` (the proxy, OpenAPI and handler fixes, no migrations), then `bee8a7728` (the rest of the OpenAPI review's status fixes, no migrations), then `aeeea2357` (Silo through `73a6ae6a3` and `golang.org/x/net` v0.62.0, one migration), then on October 28 `9cc80cd33` (Silo through `564b906b8`, one migration, an image built locally before CI finished).
- [October 26 Stage A deployment](../operations/2026-10-26-stage-a-deployment.md) — historical: `7831034ea` (account policy on users, music and lifecycle receipts removed, three forward-only migrations), then `d064644a8`.
- [October 26 main deployment](../operations/2026-10-26-main-deployment.md) — historical: `d183b286f`, Silo through `6de6d5e5e`, 21 Silo migrations, a locally built image.
- [October 15 preview server deployment](../operations/2026-10-15-preview-server-deployment.md) — historical: published source, offline migrations, presentation workers since removed, public preview left disabled.
- [October 7 native storage deployment](../operations/2026-10-07-native-storage-deployment.md) — historical: `c87b44545`, Silo through `74158b4a8`, 407 seams.
- [September 30 upstream deployment](../operations/2026-09-30-upstream-deployment.md) — historical: exact revision and verification limits.
- [September 19 Xtream deployment](../operations/2026-09-19-xtream-deployment.md) — historical: `418a18b7d`, Xtream live providers, migration and rollback boundaries.

## Source integration records

All historical; the [changelog](CHANGELOG.md) carries every later integration.

- [October 10 upstream resolutions](upstream-merge-2026-10-10.md) — historical: conflict resolutions for download, thumbnail, metadata and web changes.
- [October 5 merge reduction](../operations/2026-10-05-merge-reduction.md) — historical: owned tenant modules, restored shared files, the ledger from 396 to 387 paths.
- [October 5 presentation plugin cutover](../operations/2026-10-05-presentation-plugin-cutover.md) — historical: the worker processes that [in-process evaluators](../architecture/bloem-presentation-evaluators.md) replaced on October 26.
- [October 5 integration verification](../operations/2026-10-05-upstream-integration.md) — historical: Silo build 1116, checks and inherited test limitations.
- [October 2 integration verification](../operations/2026-10-02-upstream-integration.md) — historical: imported revision, regressions and validation gaps.
- [September 24 handover](HANDOVER-2026-09-24.md) — historical: the September Silo sync, the 20-item fix program and the first seam-extraction round.

## Bloem editions of upstream documents

For these, Bloem's text differs throughout; read the Bloem edition instead of the upstream file.

- [docs/non-goals.md](overlays/non-goals.md) (upstream: [docs/non-goals.md](../non-goals.md)) — Live TV in scope, remote-URL ingestion still out.
- [Docker deployment](overlays/wiki/deployment/docker.md) — maintained Bloem installation and operations guide; the former upstream wiki page was removed.
- [Operator and viewer index](overlays/wiki/index.md) — Bloem's guide index, alongside the current [Silo reference index](../README.md).

## Overlays on upstream documents

Each overlay lists, by section, what Bloem adds to or replaces in the upstream document of the
same path. Read the upstream document together with its overlay.

- [DEVELOPMENT.md](overlays/DEVELOPMENT.md) — overlay on [DEVELOPMENT.md](../../DEVELOPMENT.md)
- [docs/admin-api.md](overlays/admin-api.md) — overlay on [docs/admin-api.md](../admin-api.md)
- [docs/api-docs.md](overlays/api-docs.md) — overlay on [docs/api-docs.md](../api-docs.md)
- [docs/architecture/api-contract.md](overlays/architecture/api-contract.md) — overlay on [docs/architecture/api-contract.md](../architecture/api-contract.md)
- [docs/architecture/core-id-range.md](overlays/architecture/core-id-range.md) — overlay on [docs/architecture/core-id-range.md](../architecture/core-id-range.md)
- [docs/architecture/invitations-onboarding.md](overlays/architecture/invitations-onboarding.md) — overlay on [docs/architecture/invitations-onboarding.md](../architecture/invitations-onboarding.md)
- [docs/architecture/observability.md](overlays/architecture/observability.md) — overlay on [docs/architecture/observability.md](../architecture/observability.md)
- [docs/architecture/playback-protocol-v3.md](overlays/architecture/playback-protocol-v3.md) — overlay on [docs/architecture/playback-protocol-v3.md](../architecture/playback-protocol-v3.md)
- [docs/architecture/scenario-acceptance.md](overlays/architecture/scenario-acceptance.md) — overlay on [docs/architecture/scenario-acceptance.md](../architecture/scenario-acceptance.md)
- [docs/architecture/secret-encryption.md](overlays/architecture/secret-encryption.md) — overlay on [docs/architecture/secret-encryption.md](../architecture/secret-encryption.md)
- [docs/architecture/settings-contract.md](overlays/architecture/settings-contract.md) — overlay on [docs/architecture/settings-contract.md](../architecture/settings-contract.md)
- [docs/architecture/streaming-write-deadline.md](overlays/architecture/streaming-write-deadline.md) — overlay on [docs/architecture/streaming-write-deadline.md](../architecture/streaming-write-deadline.md)
- [docs/architecture/v1-scope.md](overlays/architecture/v1-scope.md) — overlay on [docs/architecture/v1-scope.md](../architecture/v1-scope.md)
- [docs/auth-api.md](overlays/auth-api.md) — overlay on [docs/auth-api.md](../auth-api.md)
- [docs/invitations-api.md](overlays/invitations-api.md) — overlay on [docs/invitations-api.md](../invitations-api.md)
- [docs/libraries-api.md](overlays/libraries-api.md) — overlay on [docs/libraries-api.md](../libraries-api.md)
- [docs/realtime-api.md](overlays/realtime-api.md) — overlay on [docs/realtime-api.md](../realtime-api.md)
- [docs/release-versioning.md](overlays/release-versioning.md) — overlay on [docs/release-versioning.md](../release-versioning.md)
- [S3 storage notes](overlays/s3-storage-setup.md) — maintained additions to the current [blob-storage architecture](../architecture/blob-storage.md).
- [docs/settings-api.md](overlays/settings-api.md) — overlay on [docs/settings-api.md](../settings-api.md)
- [Artwork storage](overlays/wiki/admin/artwork-storage.md) — Bloem campaign/seasonal storage alongside the current [blob-storage architecture](../architecture/blob-storage.md).

## Guides (wiki)

- [Bloem Server Admin Guide](../wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback, access policy, Live TV, maintenance.
- [Entitlement Templates](../wiki/admin/entitlement-templates.md) — operator guide to revisioned access-policy templates.
- [Bloem User Guide](../wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests.

## Architecture and invariants

### Organizations, accounts and policy

- [Multitenant administration](../architecture/multitenant-administration.md) — the Platform and Organization administrative contexts and what each manages.
- [Resource tenancy foundation](../architecture/resource-tenancy-foundation.md) — resource owners and the first resource-isolation boundary.
- [OPA tenant authorization operations](../architecture/opa-tenant-authorization.md) — how tenant facts, OPA and SQL scope catalog and playback reads.
- [Silo backend switching](../architecture/silo-backend-switching.md) — Silo and Bloem taking turns on one database: the flag, its invariants and `make silo-switch-check`.
- [Bloem request policy](../architecture/bloem-request-policy.md) — verified profile quotas, scoped administrative usage and group limits on Silo's request service.
- [Bloem lifecycle safety](../architecture/bloem-lifecycle-safety.md) — atomic tenant retirement and library deletion.
- [Bloem native API security foundation](../architecture/bloem-security-foundation.md) — the `/api/bloem/v1` discovery and administration boundary beside Silo's `/api/v1` login.
- [Bloem contract adjudications](../architecture/bloem-contract-adjudications.md) — reviewed, pinned replacements for Silo scenario expectations Bloem deliberately differs from.
- [Upstream integration adapters](../architecture/bloem-upstream-adapters.md) — migration filesystem, profile-aware interfaces and embedded-web boundaries over Silo's code.

### Native storage and media delivery

- [Native storage](../architecture/bloem-native-storage.md) — experimental storage-source (S3, Bookwarehouse) ebook, movie and audiobook libraries: locations, scanning, covers and reading.
- [Native audiobook presentations](../architecture/bloem-native-audio-playback.md) — complete-book publication and pinned direct audio playback.
- [Native video playback](../architecture/bloem-native-video-playback.md) — source admission, movie publication, execution ownership, worker HLS and supported surfaces.
- [Native tool input](../architecture/bloem-native-tool-input.md) — bounded pinned reads for trusted local tools, child lifetime and host admission.
- [Native scan reader](../architecture/bloem-native-scan-reader.md) — sealed scan pages, pinned entry reads and current scan authority.
- [Native probe](../architecture/bloem-native-probe.md) — trusted ffprobe input, shared read budgets and process cleanup.
- [Bloem media ownership across API replicas](../architecture/bloem-cluster-media.md) — which node owns Live TV and native media sessions, on Silo's playback attempts and heartbeats.
- [Delivery boundary enforcement](../architecture/delivery-boundary-enforcement.md) — library access rechecked where bytes or rows leave the server.
- [Native client subtitle delivery](../architecture/native-subtitle-delivery.md) — subtitle artifact and timeline contract adopted from Silo `658be10eb`.
- [Storage source management UI design](../architecture/bloem-storage-source-management-ui-design.md) — historical: the October 15 design for adding and managing S3 and Bookwarehouse sources in the web admin.
- [Storage source management UI plan](../architecture/bloem-storage-source-management-ui-plan.md) — historical: the October 15 implementation plan for that design.

### Requests and preview

- [Audiobook requests](../architecture/bloem-audiobook-requests.md) — request authority, independent quotas, identifier matching and notification scope.
- [Ebook requests](../architecture/bloem-ebook-request-foundation.md) — default-off web/admin requests, Bookwarehouse fulfillment, durable recovery and linked-library completion.
- [Browse-only content policy](../architecture/bloem-browse-only-content-policy.md) — protected preview principals, signed guest sessions, catalogue hygiene and native content-delivery boundaries.

### Announcements, promotions and seasonal presentation

- [Server admin announcements](../architecture/admin-announcements.md) — admin-authored messages, targeting and inbox rules.
- [Playback overlays](../architecture/playback-overlays.md) — chapter-triggered campaign cards during playback.
- [Seasonal scheduling](../architecture/seasonal-scheduling.md) — annual ambience schedules evaluated in the selected timezone.
- [Campaign and seasonal asset storage](../architecture/bloem-campaign-asset-storage.md) — campaign and seasonal uploads use the catalog Assets store (local or S3); public S3 is only a fallback.
- [Presentation evaluators](../architecture/bloem-presentation-evaluators.md) — in-process promotions and ambience evaluation, host authority and section extension boundaries.

### Live TV

- [Xtream live TV providers](../architecture/xtream-live-tv.md) — encrypted provider accounts, live channels, XMLTV, connection limits; no VOD or remote URLs.
- [Live TV client access](../architecture/live-tv-client-access.md) — the `watch_live_tv` permission, capability discovery and tuner-session handling.
- [Live TV tuner discovery](../livetv-tuner-discovery.md) — HDHomeRun and Dispatcharr discovery and probing.
- [prairie-source-manifest.tsv](../livetv/prairie-source-manifest.tsv) — every Prairie Server source blob the Live TV subsystem imports or adapts.

### Clients, protocols and compatibility

- [Bloem native client surface](../architecture/bloem-client-surface.md) — why Bloem-only features live under `/api/bloem/v1`, and the native document set.
- [SDK compatibility and contract generation](../architecture/bloem-sdk-compatibility.md) — plugin SDK pins, generated client DTOs and consumer synchronization.
- [Protocol compatibility](../architecture/bloem-compatibility-removal.md) — Silo's Jellyfin and Audiobookshelf servers, their tenant wiring, and the removed companion gateway.
- [Pastime managed-tracking removal](../architecture/bloem-pastime-removal.md) — forward-only cleanup migration and the rule that Pastime returns only as an ordinary plugin.
- [Admin remote control of clients](../architecture/admin-remote-control.md) — invariants for server-issued commands to client apps.
- [Header-authenticated media client rollout](../architecture/header-authenticated-media-rollout.md) — historical: superseded on October 9, when the deployment gate was removed.

### Embedded web

- [Bloem Server web feature coverage](../architecture/bloem-web-feature-coverage.md) — Bloem features in Silo's web app: implemented screens, authority boundaries, verification and limits.
- [Embedded-web completion handoff](../architecture/bloem-web-completion-handoff.md) — historical: the September 18–19 web batch and its remaining acceptance work.

### Historical evaluations and plans

- [Silo playback merge impact on Bloem clients](../architecture/2026-08-24-silo-server-merge-client-impact.md) — historical: the August 24 client impact audit.
- [Upstream reconciliation — 2026-09-08](../architecture/2026-09-08-upstream-reconciliation.md) — historical: the September 8 reconciliation and its deferred items.
- [Server A–Z architecture evaluation](../architecture/server-a-z-architecture-evaluation-2026-08-23.md) — historical: the August 23 architecture audit.
- [Server-first client remediation roadmap](../architecture/server-first-client-remediation-roadmap-2026-08-23.md) — historical: the August 23 roadmap that followed the audit.

## API references and specs

- [Bloem Server — Native /api/bloem/v1 Reference](../bloem-api-reference.md) — the maintained wire reference for Bloem's native extensions.
- [Playback overlays API](../playback-overlays-api.md) — campaign CRUD and the save-to-inbox endpoint for playback overlays.
- [LAN service advertisement (`_bloem._tcp`)](../specs/lan-service-advertisement.md) — the opt-in DNS-SD service (`internal/lanadvert`) Bloem's clients browse for.
- [Bloem private plugin inventory](../plugin-fork-inventory.md) — historical snapshot of Bloem's plugin forks, with later dated notes.
- [Admin remote control of clients (S-5)](../specs/admin-remote-control.md) — historical: the working spec; the architecture page above is maintained.
- [api-v2 fork readiness](../specs/api-v2-fork-readiness.md) — historical: September preparation for Silo's `/api/v2` route-inventory gates.
- [Client DTO generator (R-19)](../specs/client-dto-generator.md) — historical: the design of the generated Kotlin/Swift contracts.
- [Client engagement spec](../specs/client-engagement.md) — historical: the September design for alerts, promotions and ambience.

## Operations runbooks

- [Entitlement templates](../operations/entitlement-templates.md) — platform-administrator operation of revisioned policy templates.
- [Bulk policy cohorts](../operations/bulk-policy-cohorts.md) — reviewed policy changes for up to 10,000 snapshotted accounts.
- [Bulk policy cohorts release and canary runbook](../operations/bulk-policy-cohorts-runbook.md) — releasing the cohort capability and validating it with one test account.
- [Header-authenticated media rollout handoff](../operations/header-authenticated-media-rollout-handoff.md) — historical: the August 24 handoff; the gate it describes was removed on October 9.

## Other Bloem-owned files

- [Audiobook and podcast support](../../internal/audiobooks/README.md) — package notes for Silo's audiobook code and its Audiobookshelf-compatible server in Bloem.
- [Bloem brand asset set](../../assets/brand/README.md) — vector sources, palette and where the rendered icons live.

## Upstream documents that still carry Bloem edits

The playback schema files below remain seams (`contracts/seams.txt`) because tooling reads them.
The retired `playback.proxy_policy` explanation now lives only in the
[Bloem v1 overlay](overlays/architecture/v1-scope.md#retired-bloem-playback-proxy-policy);
Silo's `v1-scope.md` no longer carries that fork-only row.

- `docs/design/schemas/playback-v3/v3/**` — playback schema and fixtures validated by `make verify-playback-fixtures`.
