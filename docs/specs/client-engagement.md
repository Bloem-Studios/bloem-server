# Client Engagement Spec — alerts, promotions, ambience

Server-driven content pushed down to the client apps: (A) admin-authored alert notifications,
(B) advertising/promotional cards, (C) seasonal ambience effects (e.g. halloween pumpkins,
animated snow). Status: DRAFT for owner approval. Evidence: repo review 2026-09-01 (paths cited
inline are current main, `38caf1ab`).

## Design principle: capability-gated, dialect-agnostic clients

Clients (bloem-android-v3, bloem-apple-v3) must run against BOTH the upstream-compat v1 dialect
and bloem v2. Only bloem servers implement this spec. Therefore:

- Every feature here is discovered via **capability payloads**, never via version/dialect
  sniffing. A server that doesn't advertise a capability gets a client with the feature fully
  dormant (no probes, no error noise).
- Every wire change is **additive and optional** — the v1 route-contract guard
  (`test(api): guarantee the v1 route contract can only ever widen`) stays green.
- Clients keep the existing forward-compat contract: unknown notification types render as a
  generic row; unknown home section types are ignored; unknown ambience effect names are ignored.

## A. Alert notifications (S-1)

Reuses ~90% of the existing pipeline (inbox REST + ws `notifications` channel + push relay).

1. **New delivery types** beside `internal/notifications/release_types.go:14`:
   `system.alert` (operational, severity-tiered) and `system.announcement` (informational,
   admin-authored).
2. **Structured body**: new nullable `body jsonb` column on the delivery table
   (migration; the existing CHECK at `20260611100000_profile_release_notifications.sql:105-109`
   constrains episode rows only): `{title, body, severity: info|warning|critical, deeplink?,
   image_url?, dismissible: bool, cta?: {label, url}, expires_at?}`.
3. **Wire**: `DeliveryRowPayload` (`internal/notifications/dispatcher.go:31-48`) grows the same
   optional fields (+`dismissed_at`). One shape serves REST inbox, ws snapshot, and realtime
   dispatch — honor the lockstep comment at `:29-30`.
4. **Dismiss ≠ read**: `POST /notifications/{id}/dismiss`; list/sync queries filter expired and
   (optionally) dismissed rows.
5. **Admin compose**: `POST/GET/DELETE /admin/notifications/announcements` next to the server-
   channel CRUD (`internal/api/router.go:3430-3439`). Targeting: all users | role | organization |
   library-entitled | explicit user/profile list. Fan-out via `System.DispatchOperational`
   (`internal/notifications/operational_dispatch.go:26`) → durable inbox + webhooks + web push +
   APNs/FCM outbox in one transaction, `notification.created` on the existing ws channel.
6. **Health bridge (S-4, may ship later)**: thin adapter from `internal/nodepool/health.go` /
   `internal/nodemetrics` into the dispatcher; severity policy decides admin-only vs all-users.
7. **Capability**: `capabilityResponse` (`internal/api/handlers/notifications.go:301-339`) grows
   `{"announcements": true, "supported_types": [...], "dismiss": true}`.

## B. Promotions / advertising (S-2)

New small subsystem; delivery rides existing surfaces.

1. **`promotions` table**: id, org/tenant scope (nullable = deployment-wide), `surfaces[]`
   (`home`, `detail`, `pre_playback`), placement hints, title/subtitle/image/deeplink/CTA,
   priority, `starts_at/ends_at`, targeting predicate (same dimensions as A.5), `dismissible`.
   Admin CRUD: `/admin/promotions`.
2. **Home delivery**: new `SectionType` `promoted` in `internal/sections/types.go:13-51` with a
   resolver in the fetcher — promo cards flow through the existing `/home/layout` +
   `/home/sections` contract. Old clients ignore the unknown section type; capability-aware v3
   clients render it. Card items carry free-form fields (not media items) — the section item
   union grows an optional `promo` variant.
3. **Detail/pre-playback delivery**: `GET /promotions?surface=detail&content_id=…` (and
   `surface=pre_playback`). Detail responses have no extensibility slot today; a separate fetch
   keeps the v1 contract untouched.
4. **Dismissals**: generalize `validHomeSurface` (`internal/api/handlers/home_dismissals.go:133-135`)
   to accept promo surfaces — the existing per-profile dismissal store handles
   "don't show this again" with no new table.
5. **Capability**: home/sections capability (or the notifications capability block) grows
   `{"promotions": {"surfaces": ["home","detail","pre_playback"]}}`.
6. Impressions/click accounting: OUT OF SCOPE v1 (note for later; a `POST /promotions/{id}/event`
   stub is cheap if wanted).

## C. Ambience — seasonal effects (S-3)

Not a notification: a scheduled presentation directive. Smallest piece.

1. **`ambience` block** on the branding payload (`internal/api/handlers/branding.go:30-40`,
   unauthenticated — effects work on the login screen too) AND echoed on the authenticated
   capability payload:
   `{"effect": "snow"|"pumpkins"|"hearts"|"fireworks"|…, "starts_at", "ends_at",
     "intensity": 0.0-1.0, "surfaces": ["all"|"home"|"login"]}`
   (array of windows; server evaluates schedule, clients also honor the window bounds locally).
2. **Admin CRUD**: `/admin/ambience` — schedule windows per effect; org-scoped or deployment-wide.
3. **Client contract**: effect names are an open set — clients render effects they know, ignore
   ones they don't. Intensity is a hint. Accessibility: clients must expose a local
   "reduce effects" toggle that wins over the server.

## Delivery order

S-1 alerts (cheapest, unlocks admin→user messaging) → S-3 ambience (tiny, high delight) →
S-2 promotions (new subsystem) → S-4 health bridge. Each is an independent PR-sized package with
route-contract tests; all changes additive.

## Client-side counterpart

The client contract and work-package integration live in the client repo:
`bloem-android-v3/docs/plan/05-engagement-spec.md` (and its apple counterpart when that program
starts). Contract summary clients rely on: capability blocks above; optional
`DeliveryRowPayload` fields; `POST /notifications/{id}/dismiss`; `GET /promotions`; `promoted`
section type; `ambience` windows on branding/capability.

---

## AMENDMENTS from the owner's Engagement.dc.html design (2026-09-01)

1. §C Ambience is a seasonal PACK, not an effect name: `{effect_id, window: {starts_at, ends_at},
   intensity, assets: {banner_url?, sprites?: [urls]}}`. Server ships artwork for asset-based
   seasons (banner + cut-out sprites); named effects (snow) are client-rendered; unknown
   effect_id + assets → generic artwork renderer; unknown + no assets → ignored. One registry
   entry per season, no per-season server logic.
2. §A alert body: `dismissible` is server-set and FORCED false at severity=critical; the dismiss
   endpoint is distinct from read (dismiss hides the banner, the inbox row remains until read).
   List/sync MUST filter expired rows server-side — clients never see stale alerts.
3. §B promotion items carry 16:9 artwork (never poster aspect), `kicker`, `headline`, `cta`,
   `deeplink`, and per-item dismiss. Pre-playback placement contract: the client always keeps
   "continue to content" as the default/focused action; the server cannot request a timer or
   forced wait (no such fields exist).
4. Authoring note (garden split still pending): whatever authors campaigns must be able to attach
   the artwork assets (banner/sprites/16:9 cards) — asset upload is part of the authoring surface.
