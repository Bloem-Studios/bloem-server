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

---

## Implementation notes (S-1) — shipped on `feat/s1-alerts`

Everything below is additive; the v1 route-contract goldens are unchanged (the new routes mount
only when the notification system is wired, which the golden fixtures do not do).

### Storage

Migration `20260901120000_system_alert_notifications.sql`:

- `notification_deliveries` gains `body jsonb` (the AlertBody, NULL for release types),
  `expires_at timestamptz` (denormalised from `body.expires_at` by the delivery repository — the
  single writer — so list/sync can filter with a plain predicate), `dismissed_at timestamptz`, and
  `announcement_id text` (FK → `notification_announcements`, `ON DELETE SET NULL`). The episode-row
  CHECK is untouched. Partial unique `(profile_id, announcement_id)` makes re-fanout idempotent.
- `notification_announcements`: one row per admin compose (`type`, `body`, `targeting`,
  `created_by`, `recipient_count`, `created_at`, `withdrawn_at`).

### Delivery types and body

`system.alert` (operational; bypasses the profile master toggle) and `system.announcement`
(informational; honours `notification_preferences.enabled`). `NormalizeAlertBody` validates at
write time: title required (≤120 chars), body ≤2000, `severity ∈ {info, warning, critical}`
(default `info`), `deeplink`/`cta.url` must be an `https://` URL, a `bloem://` app deeplink, or
an app path starting with a single `/` (`javascript:`, `data:`, protocol-relative `//`, plain
`http:` and every other scheme are rejected), `image_url` `https://` only, `cta` needs both
`label` and `url`, `expires_at` must be in the future, and **`dismissible` is forced `false`
when `severity=critical`**.

Exposure note (accepted): `deeplink` / `cta.url` are admin-authored absolute URLs, so the
exposure is "an admin can send users anywhere"; the server vets the scheme, not the destination.

### Wire shape (one shape for REST inbox, ws snapshot, `notification.created`)

`DeliveryRowPayload` grows, all `omitempty` and only present on system rows:

```json
{"title": "…", "body": "…", "severity": "info|warning|critical", "deeplink": "…",
 "image_url": "…", "dismissible": true, "cta": {"label": "…", "url": "…"},
 "expires_at": "RFC3339", "dismissed_at": "RFC3339"}
```

Release rows omit every one of these keys (including `dismissible`). Generic webhooks carry the
same object under `alert`; Discord embeds and web push/APNs display derive title/body/link from
it (`BuildNotificationDisplay` categories `system_alert` / `system_announcement`, thread
`announcement:{id}`).

### Client routes

- `GET /notifications`, `GET /notifications/sync`: expired rows are **always** filtered
  server-side; dismissed rows are filtered unless `?include_dismissed=1`. The ws snapshot
  (`RecentUnread`) and `unread-count` apply both filters. Account-channel digests (email, Discord
  DM) skip expired rows too.
- `GET /notifications/{id}` applies the not-expired predicate too: a withdrawn-then-read row is
  expired, so a client holding a stale snapshot gets 404 when it re-fetches it by id.
- `POST /notifications/{id}/dismiss` → 204 (idempotent), 404 unknown/other profile/expired, 409
  `not_dismissible` when the stored body says `dismissible=false` (every critical alert). Dismiss
  never touches `read_at`. Emits ws `notification.dismissed` `{id, profile_id}`.
- `GET /notifications/capability` adds `"announcements": true`,
  `"supported_types": [episode.available, request.fulfilled, request.approved, request.declined,
  webhook.auto_disabled, system.alert, system.announcement]`, `"dismiss": true`.

### Admin routes (`/admin/notifications/announcements`, beside the server-channel CRUD)

- `POST` body: `{"type": "system.alert|system.announcement" (default announcement),
  "body": AlertBody, "targeting": {"audience": "all|role|organization|library|explicit",
  "role"?, "organization_id"?, "library_id"?, "user_ids"?, "profile_ids"?}}` → 201 with the
  announcement (`recipient_count` included); 400 on body/targeting validation, 422
  `no_recipients` when the audience resolves to nobody. Disabled accounts are never targeted;
  `organization` uses active `organization_memberships`; `library` resolves each profile's access
  scope (`AllowedLibraryIDs` / unrestricted); `explicit` expands `user_ids` to all their profiles
  and adds `profile_ids` individually.
- Opt-out (accepted design): for `system.announcement` the resolved audience is filtered
  against `notification_preferences.enabled` **before** the announcement row is stored, so
  `recipient_count` and the withdraw set exclude opted-out profiles and no per-profile audit
  trace of the skip exists. `system.alert` skips this filter entirely.
- Fan-out: `System.dispatchOperationalBatch` — the announcement row, every inbox row, and every
  webhook / web push / APNs-FCM outbox attempt commit in **one transaction**; post-commit each
  row goes through the shared `MultiDispatcher` (ws `notification.created` per recipient, channel
  senders). Every enabled webhook of a recipient profile receives system rows.
- `GET` → `{"announcements": [...]}` newest first (withdrawn included, `withdrawn_at` set).
- `DELETE /{id}` = **withdraw**: in one transaction, stamps `withdrawn_at`, deletes the
  announcement's **unread** delivery rows (pending outbox attempts cascade away, so undelivered
  pushes are cancelled), and sets `expires_at = now()` on **read** rows so every feed stops
  showing them while the read history survives. Emits ws `notification.withdrawn`
  `{id, profile_id}` per affected row. Idempotent; 404 for unknown ids.

### Admin create contract (source of truth for the garden client)

Request, copied from the passing handler test
`TestAdminAnnouncementsCreateReturnsCreatedWithRecipientCount`
(`internal/api/handlers/admin_announcements_test.go`). `type` defaults to `system.announcement`;
`library_id` is an int, `user_ids` is `[]int`, `profile_ids` is `[]string` (profile ids are
strings everywhere in this server — the code wins over the earlier `[]int` draft); `severity`
defaults to `info`; `dismissible` is a bool (forced `false` at `critical`).

```json
POST /api/v1/admin/notifications/announcements
{
  "type": "system.alert",
  "body": {
    "title": "Maintenance",
    "body": "Tonight",
    "severity": "warning",
    "deeplink": "bloem://settings/status",
    "image_url": "https://cdn.example/banner.png",
    "dismissible": true,
    "cta": {"label": "Status", "url": "https://status.example"},
    "expires_at": "2030-01-01T00:00:00Z"
  },
  "targeting": {
    "audience": "role",
    "role": "admin"
  }
}
```

`targeting` variants: `{"audience":"all"}` · `{"audience":"role","role":"admin|user"}` ·
`{"audience":"organization","organization_id":"<uuid>"}` · `{"audience":"library","library_id":5}` ·
`{"audience":"explicit","user_ids":[1,2],"profile_ids":["p1","p2"]}`.

Response `201 Created` (the announcement; top-level `id`, body echoed after normalization,
`created_by` = calling admin's user id, `withdrawn_at` null until withdrawn):

```json
{
  "id": "01J...ULID",
  "type": "system.alert",
  "body": {"title": "Maintenance", "body": "Tonight", "severity": "warning",
           "deeplink": "bloem://settings/status", "image_url": "https://cdn.example/banner.png",
           "dismissible": true, "cta": {"label": "Status", "url": "https://status.example"},
           "expires_at": "2030-01-01T00:00:00Z"},
  "targeting": {"audience": "role", "role": "admin"},
  "created_by": 42,
  "recipient_count": 3,
  "created_at": "2026-09-01T12:00:00Z",
  "withdrawn_at": null
}
```

Errors: `400 bad_request` (body/targeting validation, message names the field), `422
no_recipients`, `503 unavailable` (notification system not wired).

`GET /api/v1/admin/notifications/announcements` → `{"announcements": [<announcement>, ...]}`
newest first. `DELETE /api/v1/admin/notifications/announcements/{id}` → `204` (withdraw,
idempotent), `404 not_found`.
