# Bloem documentation

Index of Bloem-owned documentation in this tracking fork of Silo Server. Silo-owned files
(anything that also exists upstream) stay as close to upstream as possible, so Bloem
material lives in Bloem-owned files listed here. The code is the source of truth.

## Start here

- [Bloem Server front page](../../.github/README.md) — what Bloem is, deployment checkpoint, quick start. The root `README.md` is upstream Silo's.
- [Integration changelog](CHANGELOG.md) — upstream changes and Bloem adapters.
- [Upstream merge reduction verification](../operations/2026-10-05-merge-reduction.md) — owned tenant modules, restored shared files and measured merge surface.
- [Presentation plugin cutover verification](../operations/2026-10-05-presentation-plugin-cutover.md) — worker extraction, focused checks and acceptance limits.
- [October 5 integration verification](../operations/2026-10-05-upstream-integration.md) — Silo build 1116, fresh checks, and inherited test limitations.
- [October 2 integration verification](../operations/2026-10-02-upstream-integration.md) — imported revision, regressions, and validation gaps.
- [September 30 deployment](../operations/2026-09-30-upstream-deployment.md) — exact revision and verification limits.
- [Fork provenance](../../FORK.md) — tracking-fork policy, upstream merges, what Bloem adds.
- [Contributing to Bloem](../../.github/CONTRIBUTING.md) — the root `CONTRIBUTING.md` is upstream Silo's.
- [Agent instructions](../../BLOEM_AGENTS.md) — read explicitly alongside the upstream `AGENTS.md` / `CLAUDE.md`; these owned rules take precedence.

## Bloem editions of upstream documents

For these, Bloem's text differs throughout; read the Bloem edition instead of the upstream file.

- [docs/non-goals.md](overlays/non-goals.md) (upstream: [docs/non-goals.md](../non-goals.md))
- [Docker deployment](overlays/wiki/deployment/docker.md) — maintained Bloem guide; the former upstream wiki page was removed.
- [Operator and viewer index](overlays/wiki/index.md) — Bloem's guide index, alongside the current [Silo reference index](../README.md).

## Overlays on upstream documents

Each overlay lists, by section, what Bloem adds to or replaces in the upstream document of the
same path. Read the upstream document together with its overlay.

- [DEVELOPMENT.md](overlays/DEVELOPMENT.md) — overlay on [DEVELOPMENT.md](../../DEVELOPMENT.md)
- [docs/admin-api.md](overlays/admin-api.md) — overlay on [docs/admin-api.md](../admin-api.md)
- [docs/api-docs.md](overlays/api-docs.md) — overlay on [docs/api-docs.md](../api-docs.md)
- [docs/architecture/api-contract.md](overlays/architecture/api-contract.md) — overlay on [docs/architecture/api-contract.md](../architecture/api-contract.md)
- [docs/architecture/core-id-range.md](overlays/architecture/core-id-range.md) — overlay on [docs/architecture/core-id-range.md](../architecture/core-id-range.md)
- [docs/architecture/deterministic-content-id.md](overlays/architecture/deterministic-content-id.md) — overlay on [docs/architecture/deterministic-content-id.md](../architecture/deterministic-content-id.md)
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
- [docs/realtime-api.md](overlays/realtime-api.md) — overlay on [docs/realtime-api.md](../realtime-api.md)
- [docs/release-versioning.md](overlays/release-versioning.md) — overlay on [docs/release-versioning.md](../release-versioning.md)
- [S3 storage notes](overlays/s3-storage-setup.md) — maintained additions to the current [blob-storage architecture](../architecture/blob-storage.md).
- [docs/settings-api.md](overlays/settings-api.md) — overlay on [docs/settings-api.md](../settings-api.md)
- [Artwork storage](overlays/wiki/admin/artwork-storage.md) — Bloem campaign/seasonal storage alongside the current [blob-storage architecture](../architecture/blob-storage.md).

## Guides (wiki)

- [Bloem Server Admin Guide](../wiki/admin-guide.md)
- [Entitlement Templates](../wiki/admin/entitlement-templates.md)
- [Bloem User Guide](../wiki/user-guide.md)

## Architecture and invariants

- [Silo playback merge impact on Bloem clients](../architecture/2026-08-24-silo-server-merge-client-impact.md)
- [Upstream reconciliation — 2026-09-08](../architecture/2026-09-08-upstream-reconciliation.md)
- [Server admin announcements](../architecture/admin-announcements.md)
- [Admin remote control of clients — invariants](../architecture/admin-remote-control.md)
- [Campaign and seasonal asset storage](../architecture/bloem-campaign-asset-storage.md)
- [Presentation plugins](../architecture/bloem-presentation-plugins.md) — local worker execution, host authority and section extension boundaries.
- [Upstream integration adapters](../architecture/bloem-upstream-adapters.md) — migration filesystem, profile-aware interfaces and embedded-web boundaries.
- [Bloem native client surface](../architecture/bloem-client-surface.md)
- [Bloem media ownership across API replicas](../architecture/bloem-cluster-media.md)
- [Bloem contract adjudications](../architecture/bloem-contract-adjudications.md)
- [Bloem native API security foundation](../architecture/bloem-security-foundation.md)
- [Embedded-web completion handoff](../architecture/bloem-web-completion-handoff.md)
- [Bloem Server web feature coverage](../architecture/bloem-web-feature-coverage.md)
- [Delivery boundary enforcement](../architecture/delivery-boundary-enforcement.md)
- [Header-Authenticated Media Client Rollout](../architecture/header-authenticated-media-rollout.md)
- [Live TV client access](../architecture/live-tv-client-access.md)
- [Multitenant administration](../architecture/multitenant-administration.md)
- [Native client subtitle delivery](../architecture/native-subtitle-delivery.md)
- [OPA tenant authorization operations](../architecture/opa-tenant-authorization.md)
- [Playback overlays](../architecture/playback-overlays.md)
- [Resource tenancy foundation](../architecture/resource-tenancy-foundation.md)
- [Seasonal scheduling](../architecture/seasonal-scheduling.md)
- [Server A–Z architecture evaluation](../architecture/server-a-z-architecture-evaluation-2026-08-23.md)
- [Server-First Client Remediation Roadmap](../architecture/server-first-client-remediation-roadmap-2026-08-23.md)
- [Xtream live TV providers](../architecture/xtream-live-tv.md)

## API references and specs

- [Bloem Server — Native /api/bloem/v1 Reference](../bloem-api-reference.md)
- [Playback overlays API](../playback-overlays-api.md)
- [Bloem Private Plugin Inventory](../plugin-fork-inventory.md)
- [Admin Remote Control of Clients (S-5)](../specs/admin-remote-control.md)
- [api-v2 fork readiness](../specs/api-v2-fork-readiness.md)
- [Client DTO Generator (R-19)](../specs/client-dto-generator.md)
- [Client Engagement Spec — alerts, promotions, ambience](../specs/client-engagement.md)
- [LAN service advertisement (`_bloem._tcp`)](../specs/lan-service-advertisement.md)

## Operations and deployment records

- [Bloem upstream deployment — 2026-09-30](../operations/2026-09-30-upstream-deployment.md)
- [Bloem Xtream deployment — 2026-09-19](../operations/2026-09-19-xtream-deployment.md)
- [Bulk policy cohorts release and canary runbook](../operations/bulk-policy-cohorts-runbook.md)
- [Bulk policy cohorts](../operations/bulk-policy-cohorts.md)
- [Jellyfin/Emby and Audiobookshelf Compatibility](../operations/compatibility-applications.md)
- [Entitlement templates](../operations/entitlement-templates.md)
- [Header-Authenticated Media Rollout Handoff](../operations/header-authenticated-media-rollout-handoff.md)

## Live TV provenance

- [Live TV tuner discovery (HDHomeRun + Dispatcharr)](../livetv-tuner-discovery.md)
- [prairie-source-manifest.tsv](../livetv/prairie-source-manifest.tsv)

## Upstream documents that still carry Bloem edits

The playback schema files below remain seams (`contracts/seams.txt`) because tooling reads them.
The retired `playback.proxy_policy` explanation now lives only in the
[Bloem v1 overlay](overlays/architecture/v1-scope.md#retired-bloem-playback-proxy-policy);
Silo's `v1-scope.md` no longer carries that fork-only row.

- `docs/design/schemas/playback-v3/v3/**` — playback schema and fixtures validated by `make verify-playback-fixtures`.
