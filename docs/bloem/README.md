# Bloem documentation

Index of Bloem-owned documentation in this tracking fork of Silo Server. A
**tracking fork** keeps Silo's Git history and merges Silo's `main` as it moves
(see [fork provenance](../../FORK.md)). Any Markdown file that also exists in
Silo's `main`, including the root `README.md`, `CONTRIBUTING.md`, `AGENTS.md`
and most of `docs/`, is Silo's and stays as Silo wrote it. Bloem's material
lives in the Bloem-owned files listed here; where Bloem differs from a Silo
document, an **overlay** records the difference. The code is the source of truth.

Every current Bloem page is written the same way, after
[How organizations work](../architecture/bloem-organizations.md): plain language and
pictures first, a made-up example where it helps, a "Words used in the code" table, and
a "For developers" section that holds every technical rule.
[Writing Bloem documentation](documentation-workflow.md) sets out that shape.

Pages marked *historical* are dated records: their facts are true to their date,
not to the current source, and they keep the form they were written in. The current
state is on the [front page](../../.github/README.md#current-state).

## Start here

- [Bloem Server front page](../../.github/README.md) — what Bloem is, where it comes from, current state, quick start. GitHub shows it in place of the root `README.md`, which is Silo's.
- [How Bloem forks Silo](../../FORK.md) — tracking-fork policy, the "Silo wins" rule, seams and overlays, what Bloem adds and removed, the current integration boundary.
- [Agent instructions](../../BLOEM_AGENTS.md) — Bloem's overrides of Silo's `AGENTS.md` / `CLAUDE.md`; they take precedence where the two differ.
- [Contributing to Bloem](../../.github/CONTRIBUTING.md) — repository ownership, AI disclosure and the validation commands; the root `CONTRIBUTING.md` is Silo's.
- [Integration changelog](CHANGELOG.md) — every Silo integration and Bloem change, newest first, with commits and seam counts.
- [Writing Bloem documentation](documentation-workflow.md) — Silo's manual/reference model, where each kind of Bloem change is documented, regeneration and verification commands.
- [How Bloem CI works](ci.md) — the Bloem CI jobs, the daily upstream sync, the published image's gate, runner placement and tool pins.
- [API catalogue](api-catalogue.md) — generated list of typed `/api/v2` and `/api/bloem/v1` operations and native documentation gaps.
- [API by topic](api/README.md) — every typed `/api/bloem/v1` operation, generated from the OpenAPI contract, one page per topic, with credentials, fields and error codes; plus the hand-written page for Bloem's remaining [`/api/v1` routes](api/v1-routes.md).

## How Bloem works: start here

The overview pages, one per area, for readers who don't work on the code. Each links the
more detailed pages of its area, and ends with a "For developers" section.

- [How organizations work](../architecture/bloem-organizations.md) — the platform, organizations, accounts and profiles; what someone can watch; a reseller's year.
- [How organization domains and branding work](../architecture/bloem-organization-domains.md) — each organization on its own domains, with its own branding and email links; refusing a sign-in on another organization's domain.
- [How plans, previews and upgrades work](../architecture/bloem-plans.md) — the built-in plans, how a plan combines with an account and its profiles, changing a plan safely, the storefront preview, the upgrade prompt.
- [How Live TV works](../architecture/bloem-live-tv.md) — sources, tuning in, recordings.
- [How storage sources work](../architecture/bloem-storage-sources.md) — libraries read from S3 and Bookwarehouse.
- [How requests work](../architecture/bloem-requests.md) — movie and series, audiobook and ebook requests.
- [How messages and promotions work](../architecture/bloem-messages-and-promotions.md) — announcements, campaign and playback cards, seasonal packs.
- [How apps connect](../architecture/bloem-apps.md) — the three entrances, signing in, remote control.
- [Switching between Silo and Bloem](../architecture/bloem-silo-switching-explained.md) — one database, two servers taking turns, both install layouts.
- [How Bloem keeps up with Silo](../architecture/bloem-following-silo.md) — the daily sync, "Silo wins", where Bloem keeps its own work.

The pictures are drawn by `scripts/bloem/draw-pictures.py`.

## Deployment receipts

Dated records of what production ran, how it was deployed and what was checked.
A receipt is not native-client or playback acceptance.

- [November 1 deployment: provider sign-out on its domain](../operations/2026-11-01-provider-sign-out-deployment.md) — current: `59616c4af` (a provider sign-out returns to the organization domain it started on, seams stay at 304, no migrations, 18 s restart, an image built locally before CI finished).
- [November 1 deployment: organization domains](../operations/2026-11-01-organization-domains-deployment.md) — `8a721cff2` (organization domains and branding, provider sign-in kept on its domain, the 304th seam, one migration adding two tables, 18 s restart, the server's address registered to the default organization, Garden `d9f77cc2`, an image built locally before CI finished).
- [November 1 deployment: Silo through `63ef34b23`](../operations/2026-11-01-silo-sync-63ef34b23-deployment.md) — `f14332386` (Silo through `63ef34b23`, and a sign-in its organization revoked is told to sign in again; no migrations, 105 s restart, an image built locally before CI finished).
- [November 1 deployment: off Silo's `/api/v1`](../operations/2026-11-01-off-silo-v1-deployment.md) — `e5cd1eb6a` (the storefront and the web app off Silo's `/api/v1`, native platform and viewer campaign routes, one migration that changed no rows, 46 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment through `32a2f63e1`](../operations/2026-10-31-silo-sync-32a2f63e1-deployment.md) — historical: `4edbee99d` (Silo through `32a2f63e1`, Silo's lines verbatim in the session-revoked hook and household check, 303 seams, one migration, 48 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment through `288f58200`](../operations/2026-10-31-silo-sync-288f58200-deployment.md) — historical: `29c23fc2c` (Silo through `288f58200`: password changes that can sign out other devices, PIN revocation checked on household writes, no migrations, 46 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment](../operations/2026-10-31-silo-sync-deployment.md) — historical: `8065e5746` (Silo through `07c05cad6`, seven additive migrations applied online beside the live server in 126 s, 52 s restart, an image built locally before CI finished).
- [October 30 upgrade prompt and new arrivals deployment](../operations/2026-10-30-upgrade-prompt-new-arrivals-deployment.md) — historical: `b99aa105c` (the stream-limit upgrade prompt and the new-arrivals list for storefronts, one migration, an image built locally before CI finished).
- [October 30 S3 all-buckets deployment](../operations/2026-10-30-s3-all-buckets-deployment.md) — historical: `8f4ba105c` (guided S3 setup and all-bucket sources with S3 plugin 0.4.0, no migrations).
- [October 29 deployment](../operations/2026-10-29-silo-sync-free-play-deployment.md) — historical: `38e587805` (Silo through `cc8514943` and preview free play, ten forward-only migrations, 17 min 41 s offline for Silo's marker backfill, an image built locally before CI finished), then `cc37f3900` (monitoring API keys open the events socket, no migrations), then `09dab0097` (AI subtitles from word-only speech-to-text responses, Chinese and Japanese without spaces and in shorter lines, no migrations).
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
- [Installing and running Bloem with Docker](overlays/wiki/deployment/docker.md) — maintained Bloem installation and operations guide; the former upstream wiki page was removed.
- [Bloem guides for operators and viewers](overlays/wiki/index.md) — Bloem's guide index, alongside the current [Silo reference index](../README.md).

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
- [Bloem S3 storage notes](overlays/s3-storage-setup.md) — maintained additions to the current [blob-storage architecture](../architecture/blob-storage.md).
- [docs/settings-api.md](overlays/settings-api.md) — overlay on [docs/settings-api.md](../settings-api.md)
- [Bloem artwork storage](overlays/wiki/admin/artwork-storage.md) — Bloem campaign/seasonal storage alongside the current [blob-storage architecture](../architecture/blob-storage.md).

## Guides (wiki)

- [Bloem Server admin guide](../wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback, access policy, Live TV, maintenance.
- [Entitlement templates](../wiki/admin/entitlement-templates.md) — operator guide to revisioned access-policy templates.
- [Bloem user guide](../wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests.

## Every page, by area

### Organizations, accounts and policy

- [How organizations work](../architecture/bloem-organizations.md) — start here: the platform, organizations, accounts and profiles in plain language, with six pictures.
- [How multitenant administration works](../architecture/multitenant-administration.md) — the Platform and Organization administrative contexts and what each manages.
- [How library ownership and grants work](../architecture/resource-tenancy-foundation.md) — resource owners and the first resource-isolation boundary.
- [How tenant authorization works](../architecture/opa-tenant-authorization.md) — how tenant facts, OPA and SQL scope catalog and playback reads.
- [How Silo backend switching works](../architecture/silo-backend-switching.md) — Silo and Bloem taking turns on one database: the flag, its invariants, the all-in-one procedure, and `make silo-switch-check` and `make silo-switch-check-all-in-one`.
- [How request rules apply to organizations and profiles](../architecture/bloem-request-policy.md) — verified profile quotas, scoped administrative usage and group limits on Silo's request service.
- [How retiring an organization and deleting a library stay safe](../architecture/bloem-lifecycle-safety.md) — atomic tenant retirement and library deletion.
- [How Bloem's security foundation works](../architecture/bloem-security-foundation.md) — the `/api/bloem/v1` discovery and administration boundary beside Silo's `/api/v1` login.
- [How Bloem's contract adjudications work](../architecture/bloem-contract-adjudications.md) — reviewed, pinned replacements for Silo scenario expectations Bloem deliberately differs from.
- [How Bloem's adapters over Silo work](../architecture/bloem-upstream-adapters.md) — migration filesystem, profile-aware interfaces and embedded-web boundaries over Silo's code.

### Native storage and media delivery

- [How native storage works](../architecture/bloem-native-storage.md) — experimental storage-source (S3, Bookwarehouse) ebook, movie and audiobook libraries: locations, scanning, covers and reading.
- [How stored audiobooks play](../architecture/bloem-native-audio-playback.md) — complete-book publication and pinned direct audio playback.
- [How stored movies play](../architecture/bloem-native-video-playback.md) — source admission, movie publication, execution ownership, worker HLS and supported surfaces.
- [How a trusted tool reads a stored file](../architecture/bloem-native-tool-input.md) — bounded pinned reads for trusted local tools, child lifetime and host admission.
- [How a scan reads a stored file](../architecture/bloem-native-scan-reader.md) — sealed scan pages, pinned entry reads and current scan authority.
- [How Bloem looks inside a stored file](../architecture/bloem-native-probe.md) — trusted ffprobe input, shared read budgets and process cleanup.
- [How playback works across several API servers](../architecture/bloem-cluster-media.md) — which node owns Live TV and native media sessions, on Silo's playback attempts and heartbeats.
- [How access is checked as content leaves the server](../architecture/delivery-boundary-enforcement.md) — library access rechecked where bytes or rows leave the server.
- [Storage source management UI design](../architecture/bloem-storage-source-management-ui-design.md) — historical: the October 15 design for adding and managing S3 and Bookwarehouse sources in the web admin.
- [Storage source management UI plan](../architecture/bloem-storage-source-management-ui-plan.md) — historical: the October 15 implementation plan for that design.

### Requests and preview

- [How audiobook requests work](../architecture/bloem-audiobook-requests.md) — request authority, independent quotas, identifier matching and notification scope.
- [How ebook requests work](../architecture/bloem-ebook-request-foundation.md) — default-off web/admin requests, Bookwarehouse fulfillment, durable recovery and linked-library completion.
- [How browse-only access and the preview work](../architecture/bloem-browse-only-content-policy.md) — protected preview principals, signed guest sessions, catalogue hygiene and native content-delivery boundaries.

### Announcements, promotions and seasonal presentation

- [How announcements work](../architecture/admin-announcements.md) — admin-authored messages, targeting and inbox rules.
- [How playback cards work](../architecture/playback-overlays.md) — chapter-triggered campaign cards during playback.
- [How seasonal packs are scheduled](../architecture/seasonal-scheduling.md) — annual ambience schedules evaluated in the selected timezone.
- [Where campaign and seasonal artwork is stored](../architecture/bloem-campaign-asset-storage.md) — campaign and seasonal uploads use the catalog Assets store (local or S3); public S3 is only a fallback.
- [How Bloem picks which campaigns and seasonal packs to show](../architecture/bloem-presentation-evaluators.md) — in-process promotions and ambience evaluation, host authority and section extension boundaries.

### Live TV

- [How Xtream Live TV providers work](../architecture/xtream-live-tv.md) — encrypted provider accounts, live channels, XMLTV, connection limits; no VOD or remote URLs.
- [How apps reach Live TV](../architecture/live-tv-client-access.md) — the `watch_live_tv` permission, capability discovery and tuner-session handling.
- [How Live TV finds tuners](../livetv-tuner-discovery.md) — HDHomeRun and Dispatcharr discovery and probing.
- [prairie-source-manifest.tsv](../livetv/prairie-source-manifest.tsv) — every Prairie Server source blob the Live TV subsystem imports or adapts.

### Clients, protocols and compatibility

- [Leaving Silo's `/api/v1`](../architecture/bloem-leaving-silo-v1.md) — the rule that Bloem adds nothing to Silo's `/api/v1`, what moved off it, and what still calls it.
- [How Bloem's native API is organized](../architecture/bloem-client-surface.md) — why Bloem-only features live under `/api/bloem/v1`, and the native document set.
- [How Bloem's SDKs and generated contracts fit together](../architecture/bloem-sdk-compatibility.md) — plugin SDK pins, generated client DTOs and consumer synchronization.
- [How Bloem runs Silo's Jellyfin and Audiobookshelf servers](../architecture/bloem-compatibility-removal.md) — Silo's Jellyfin and Audiobookshelf servers, their tenant wiring, and the removed companion gateway.
- [How Pastime tracking was removed](../architecture/bloem-pastime-removal.md) — forward-only cleanup migration and the rule that Pastime returns only as an ordinary plugin.
- [How remote control works](../architecture/admin-remote-control.md) — invariants for server-issued commands to client apps.
- [Header-authenticated media client rollout](../architecture/header-authenticated-media-rollout.md) — historical: superseded on October 9, when the deployment gate was removed.

### Embedded web

- [What the web app covers](../architecture/bloem-web-feature-coverage.md) — Bloem features in Silo's web app: implemented screens, authority boundaries, verification and limits.
- [Embedded-web completion handoff](../architecture/bloem-web-completion-handoff.md) — historical: the September 18–19 web batch and its remaining acceptance work.

### Historical evaluations and plans

- [Native client subtitle delivery](../architecture/native-subtitle-delivery.md) — historical: the September 8 adoption of Silo `658be10eb`'s subtitle artifact and timeline contract; the behaviour is now Silo's, described in Silo's [playback protocol v3](../architecture/playback-protocol-v3.md#8-track-identity-and-the-subtitle-ordinal-space).
- [Silo playback merge impact on Bloem clients](../architecture/2026-08-24-silo-server-merge-client-impact.md) — historical: the August 24 client impact audit.
- [Upstream reconciliation — 2026-09-08](../architecture/2026-09-08-upstream-reconciliation.md) — historical: the September 8 reconciliation and its deferred items.
- [Server A–Z architecture evaluation](../architecture/server-a-z-architecture-evaluation-2026-08-23.md) — historical: the August 23 architecture audit.
- [Server-first client remediation roadmap](../architecture/server-first-client-remediation-roadmap-2026-08-23.md) — historical: the August 23 roadmap that followed the audit.

## API references and specs

- [Bloem native API reference](../bloem-api-reference.md) — the maintained wire reference for Bloem's native extensions.
- [Playback cards: who calls which route](../playback-overlays-api.md) — campaign CRUD and the save-to-inbox endpoint for playback overlays.
- [How Bloem announces itself on a home network (`_bloem._tcp`)](../specs/lan-service-advertisement.md) — the opt-in DNS-SD service (`internal/lanadvert`) Bloem's clients browse for.
- [Bloem private plugin inventory](../plugin-fork-inventory.md) — historical snapshot of Bloem's plugin forks, with later dated notes.
- [Admin remote control of clients (S-5)](../specs/admin-remote-control.md) — historical: the working spec; the architecture page above is maintained.
- [api-v2 fork readiness](../specs/api-v2-fork-readiness.md) — historical: September preparation for Silo's `/api/v2` route-inventory gates.
- [Client DTO generator (R-19)](../specs/client-dto-generator.md) — historical: the design of the generated Kotlin/Swift contracts.
- [Client engagement spec](../specs/client-engagement.md) — historical: the September design for alerts, promotions and ambience.

## Operations runbooks

- [Managing entitlement templates](../operations/entitlement-templates.md) — platform-administrator operation of revisioned policy templates.
- [How bulk policy changes work](../operations/bulk-policy-cohorts.md) — reviewed policy changes for up to 10,000 snapshotted accounts.
- [Releasing bulk policy changes: the canary runbook](../operations/bulk-policy-cohorts-runbook.md) — releasing the cohort capability and validating it with one test account.
- [Header-authenticated media rollout handoff](../operations/header-authenticated-media-rollout-handoff.md) — historical: the August 24 handoff; the gate it describes was removed on October 9.

## Other Bloem-owned files

- [How audiobooks and podcasts work in Bloem](../../internal/audiobooks/README.md) — package notes for Silo's audiobook code and its Audiobookshelf-compatible server in Bloem.
- [Bloem's brand assets](../../assets/brand/README.md) — vector sources, palette and where the rendered icons live.

## Upstream documents that still carry Bloem edits

The playback schema files below remain seams (`contracts/seams.txt`) because tooling reads them.
The retired `playback.proxy_policy` explanation now lives only in the
[Bloem v1 overlay](overlays/architecture/v1-scope.md#retired-bloem-playback-proxy-policy);
Silo's `v1-scope.md` no longer carries that fork-only row.

- `docs/design/schemas/playback-v3/v3/**` — playback schema and fixtures validated by `make verify-playback-fixtures`.
