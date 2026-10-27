# Playback overlays API

> **Provenance.** Bloem addition; Silo has no promotions or playback-overlay API. This
> page was added with timed playback overlays in `eaa0b3a6b` (2026-09-06) and revised
> for the embedded web authoring in `53534d9f0` (2026-09-18). Re-checked on 2026-10-27
> against Bloem `main` at `e852f92bf` (Silo through `3e2d306b2`). It is a pointer page:
> the wire contract lives in the documents linked below.

See [the playback overlay contract](architecture/playback-overlays.md) for delivery,
capability detection, placement fields, client lifetime rules and the authenticated
save-to-inbox endpoint (`POST /api/v1/promotions/{id}/save`, beside the viewer list
`GET /api/v1/promotions`; both need a selected profile).

Two authoring paths write the same promotions registry:

- Garden, Bloem's companion application outside this repository, uses the
  acting-admin CRUD under `/api/v1/admin/promotions` (`GET`/`POST /`,
  `PUT`/`DELETE /{id}`, mounted by `mountBloemLegacyAdminV1` in
  `internal/api/router_bloem_wiring.go`).
- The embedded web console's **Campaigns & Seasonal Packs** page uses
  `/api/bloem/v1/admin/platform/promotions` under a native platform administrative
  context (`internal/api/router_bloem_engagement.go`). Organization authority is
  refused. The registry has no revision locking: a later save overwrites an earlier one.

Campaign artwork uploads go through the ambience asset service, which stores them in
Silo's catalog **Assets** blob store (local disk or S3) and falls back to the public S3
client only when no Assets store is configured (`cmd/silo/bloem_main.go`). With no
store at all, uploads return `503` and an HTTPS artwork URL can still be authored. See
[campaign asset storage](architecture/bloem-campaign-asset-storage.md) and the
[native authoring contract](bloem-api-reference.md#platform-campaign-and-seasonal-authoring).
The page previously said uploads required public S3; that described the wiring
before `f293cce0d` (2026-09-23).

Server administrators keep the separate, basic announcements page (**Admin → Settings →
Announcements**), which needs neither Garden nor campaign authority.

This page records source behavior. Client rendering of playback overlays on the
native apps is tracked in the overlay contract, not here.
