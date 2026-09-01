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
   `/home/sections` contract, **opt-in per request**: the section is delivered only when the
   client sends `promoted=1`. Old clients cannot be relied on to ignore an unknown section type
   (pre-S-2 and upstream-compat clients decode `section_type` as a plain string with no
   unknown-type drop), so without the parameter the home responses are identical to a build
   without S-2; capability-aware v3 clients learn support from the capability echo, opt in, and
   render the row. Card items carry free-form fields (not media items) — the section item union
   grows an optional `promo` variant.
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

---

## Implementation notes (S-3) — shipped on `feat/s3-ambience`

Everything below is additive; the v1 route-contract goldens are unchanged (the ambience routes
mount only when `Dependencies.Ambience` is wired, which the golden fixtures do not do; the new
fields on the branding and capability payloads are new keys only).

### Storage

Migration `20260901192510_ambience_packs.sql` creates `ambience_packs`: `id` (ULID),
`effect_id`, `starts_at`, `ends_at`, `intensity` (0.0–1.0, CHECK), `surfaces text[]`
(default `{all}`), `assets jsonb` (`{banner_url?, sprites?}`), `organization_id uuid` nullable
(FK → `organizations`, `ON DELETE CASCADE`; NULL = deployment-wide), `created_by`,
`created_at`, `updated_at`; CHECK `starts_at < ends_at`. One row per season; the server has no
per-season logic (`internal/ambience`).

### Validation (`ambience.Normalize`, at write time)

`effect_id` is a required lowercase slug (`^[a-z0-9][a-z0-9_-]{0,63}$`, open set — `snow`,
`pumpkins`, `halloween-2026`…); `window.starts_at`/`window.ends_at` are both required with
`starts_at < ends_at`; `intensity` defaults to `1.0` and must be within `[0, 1]`; `surfaces`
defaults to `["all"]`, vocabulary `{all, home, login}`, deduplicated, `all` absorbs the rest;
`assets.banner_url` and each `assets.sprites[]` entry (max 32) must be an `https://` URL with a
host or a server-served path `/api/v1/ambience/assets/<ref>` (`http:`, `data:`, `javascript:`,
protocol-relative and other app paths are rejected).

### Schedule evaluation

The registry reuses the sections seasonal clock (`recipes.Clock`; production `RealClock`, tests
`FixedClock`). A pack is active when `starts_at <= now < ends_at` (inclusive start, exclusive
end, evaluated in SQL against the clock's instant). Windows are still emitted so clients honour
the bounds locally.

### Wire shape (one shape on both payloads)

```json
{"id": "01J…", "effect_id": "snow",
 "window": {"starts_at": "RFC3339", "ends_at": "RFC3339"},
 "intensity": 0.5, "surfaces": ["all"], "assets": {"banner_url": "…", "sprites": ["…"]}}
```

- `GET /theme/branding` (unauthenticated) gains `"ambience": [<wire>, …]` — the active
  **deployment-wide** packs only (org-scoped packs never leak pre-login). Always present, `[]`
  when none or when the registry is not wired; registry errors degrade to `[]`.
- `GET /notifications/capability` (authenticated) gains the same `"ambience": [<wire>, …]` block
  — active deployment-wide packs plus the active packs of every **active** organization the
  calling account has an **active** membership in. Key present = capability exists, `[]` =
  supported but nothing active, key absent = dormant (registry not wired).
- Clients: unknown `effect_id` + `assets` → generic artwork renderer; unknown + no assets →
  ignore. Intensity is a hint; the local "reduce effects" toggle wins.

### Asset serving

`GET /ambience/assets/{ref}` (public, mounted with the registry): streams stored artwork with
`ETag`, `Cache-Control: public, max-age=31536000, immutable`, `nosniff`, and the branding asset
CSP. Refs are content-addressed (`<16 hex of sha256>.<png|webp|jpg|gif>`), so traversal or
foreign refs are 404. Storage is the public assets bucket (`S3Public`, key `ambience/<ref>`),
the same store branding uses; without S3 the registry still works with external `https://`
URLs and `storage_available` on the admin list says so.

### Admin routes (`/admin/ambience`, beside the S-1 announcements CRUD, same admin group)

- `GET /` → `{"packs": [<pack>, …], "storage_available": bool}` soonest window first.
- `POST /` → `201` with the pack (below). `PUT /{id}` is a full replacement with the same body →
  `200`. `DELETE /{id}` → `204`; `404 not_found` for unknown ids (stored artwork objects are
  left in place, same orphan policy as branding).
- `POST /assets` (standalone, no pack — the authoring side pushes artwork before any pack
  exists) → multipart `file` (PNG/WebP/JPEG/GIF, sniffed not trusted, ≤8 MiB; the request body
  is hard-capped at 9 MiB → `413 too_large`) with text fields `asset_id` (garden's uuid),
  `kind` (`campaign_card_16x9|season_banner|season_sprite`, else `400`), `checksum` (sha256 hex
  of the bytes, verified → `400` on mismatch) and `content_type` (ignored; bytes are sniffed)
  → `201` with the public URL at the top level and the stored asset under `asset` (second
  contract block below); `415 unsupported_image`, `503 unavailable` (no S3). **Idempotent on
  `asset_id`** (table `ambience_assets`: asset_id, kind, checksum, ref, content_type,
  size_bytes): the same `asset_id` + same checksum returns the existing URL without re-storing;
  same `asset_id` + different checksum replaces the object ref. Rows keep `kind` and `asset_id`
  beside the ref so packs and later campaigns can reference artwork by garden's id. All fields
  are optional for ad-hoc uploads (no `asset_id` = no registry row).
- `POST /{id}/assets` → same upload plus optional `slot` = `banner` (default; replaces) |
  `sprite` (appends, max 32, checked **before** the object is stored) → `201`
  `{"url": "/api/v1/ambience/assets/<ref>", "slot": "…", "pack": <pack>}`; the pack row is
  locked while `assets` is rewritten so concurrent attaches never lose entries; `415
  unsupported_image`, `413 too_large`, `503 unavailable` (no S3), `404 not_found`.
- `organization_id` naming an unknown organization is `400 bad_request`
  (`organization_id does not exist`), not a 500.
- Errors: `400 bad_request` (validation, message names the field), `503 unavailable` (registry
  not wired).

### Admin ambience contract (source of truth for the garden client)

Request and response copied from the passing handler test
`TestAdminAmbienceCreateReturnsCreatedPack` (`internal/api/handlers/admin_ambience_test.go`).
`intensity` and `surfaces` may be omitted (defaults `1.0` / `["all"]`); `organization_id` is a
uuid string or null; `assets` may be `{}`. Asset URLs returned by the upload endpoints are
server-relative (`/api/v1/ambience/assets/<ref>`) and are accepted as-is in `assets`.

```json
POST /api/v1/admin/ambience
{
  "effect_id": "halloween",
  "window": {"starts_at": "2026-10-24T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"},
  "intensity": 0.7,
  "surfaces": ["home", "login"],
  "assets": {
    "banner_url": "https://cdn.example/halloween/banner.png",
    "sprites": ["https://cdn.example/halloween/pumpkin.png"]
  },
  "organization_id": null
}
```

Response `201 Created` (`created_by` = calling admin's user id):

```json
{
  "id": "pack-1",
  "effect_id": "halloween",
  "window": {"starts_at": "2026-10-24T00:00:00Z", "ends_at": "2026-11-01T00:00:00Z"},
  "intensity": 0.7,
  "surfaces": ["home", "login"],
  "assets": {
    "banner_url": "https://cdn.example/halloween/banner.png",
    "sprites": ["https://cdn.example/halloween/pumpkin.png"]
  },
  "organization_id": null,
  "created_by": 42,
  "created_at": "2026-10-24T00:00:00Z",
  "updated_at": "2026-10-24T00:00:00Z"
}
```

Standalone artwork upload, copied from the passing handler test
`TestAdminAmbienceStandaloneUploadReturnsPublicURL` (same file). The request is the multipart
shape bloem-garden's `HTTPAssetTransport` sends (`internal/engagement/assetsync.go`); garden reads
the URL from top-level `url` (or `asset.url`).

```
POST /api/v1/admin/ambience/assets
Content-Type: multipart/form-data; boundary=…

asset_id=3f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f   (garden uuid; idempotency key)
kind=season_banner                               (campaign_card_16x9|season_banner|season_sprite)
checksum=<sha256 hex>                            (verified against the bytes)
content_type=image/png                           (ignored, the bytes are sniffed)
file=@banner.png                                 (required; Content-Type: image/png)
```

Response `201 Created` (`<hex>` = sha256 of the bytes, `<ref>` = its first 16 hex chars + ext):

```json
{
  "asset": {
    "asset_id": "3f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f",
    "kind": "season_banner",
    "ref": "<ref>.png",
    "url": "/api/v1/ambience/assets/<ref>.png",
    "checksum": "<hex>",
    "content_type": "image/png",
    "size_bytes": 71
  },
  "url": "/api/v1/ambience/assets/<ref>.png"
}
```

A retry with the same `asset_id` and `checksum` answers `201` with the identical body and stores
nothing new.

---

## Implementation notes (S-2) — shipped on `feat/s2-promotions`

Everything below is additive; the v1 route-contract goldens are unchanged (the promotion routes
mount only when `Dependencies.Promotions` is wired, which the golden fixtures do not do; the
`promoted` section type, the `promo` item variant and the capability key are new keys only).

### Storage

Migration `20260901200121_promotions.sql` creates `promotions`: `id` (ULID), `organization_id`
uuid nullable (FK → `organizations`, `ON DELETE CASCADE`; NULL = deployment-wide), `surfaces
text[]` (non-empty), `placement jsonb` (`{home_position?, detail_slot?, content_ids?}`),
`kicker`, `headline`, `subtitle`, `image_url`, `image_width`/`image_height` nullable,
`deeplink`, `cta jsonb` nullable (`{label, url}`), `priority`, `starts_at`/`ends_at` (CHECK
`starts_at < ends_at`), `targeting jsonb` (the S-1 `AnnouncementTargeting` shape), `dismissible`
(default true), `created_by`, `created_at`, `updated_at`. It also widens the
`user_home_item_dismissals` surface CHECK to `promo:home | promo:detail | promo:pre_playback`
so per-item promo dismissals reuse the existing per-profile dismissal store (no new table).
There are deliberately **no timer / forced-wait columns** (amendment 3).

### Validation (`promotions.Normalize`, at write time)

`surfaces` is required, vocabulary `{home, detail, pre_playback}`, deduplicated; `headline` is
required (≤120), `kicker` ≤40, `subtitle` ≤200; `image_url` is required and must pass the S-3
asset validator `ambience.IsAssetURL` (an `https://` URL with a host or a server asset path
`/api/v1/ambience/assets/<ref>` — amendment 4: campaign artwork is uploaded through the S-3
`POST /admin/ambience/assets` route with `kind=campaign_card_16x9`); `image_width`/`image_height`
must be given together, positive, and 16:9 within ±1% (`1920x1080`, `1600x900`, `1366x768` pass;
poster aspect is rejected); `deeplink` and `cta.url` follow the S-1 link rule
(`notifications.IsAppLink`: https, `bloem://`, app path — `http:`, `javascript:`, `data:`,
protocol-relative rejected); `cta.label` is required with a CTA (≤40); `starts_at`/`ends_at`
required with `starts_at < ends_at`; `targeting` is canonicalised by the S-1
`notifications.ValidateTargeting` (audience `all|role|organization|library|explicit`, side
fields outside their audience dropped); `placement.home_position` ≥ 0,
`placement.content_ids` ≤64 non-empty entries; `dismissible` defaults to `true`; unknown body
keys (e.g. a `wait_seconds`) are ignored, never stored, never echoed.

### Delivery evaluation (`promotions.Service.Active` / `ActiveHome`)

A promotion is delivered when `starts_at <= now < ends_at` (the sections seasonal clock,
`recipes.Clock`), the surface is listed, the row is deployment-wide or belongs to one of the
viewer's active organizations, the targeting matches the viewer (`role` = the account's `users.role`;
`organization` = active membership; `library` = the profile's allowed library ids, unrestricted
access matches; `explicit` = user id or profile id listed), `placement.content_ids` is empty or
contains the request's `content_id` (rows with `content_ids` are skipped when no `content_id`
is given), and the profile has not dismissed it on that surface. Order: `priority DESC,
starts_at, id`. Dismissal-store failures degrade to "nothing dismissed" (logged).

### Home delivery

`SectionType` `promoted` (`internal/sections/types.go`) resolves in
`Fetcher.fetchPromotedSection` (never cached: per-profile). `SectionWithItems.Promos` carries
the cards; `Items` stays empty. Delivery is **opt-in per request** with the query parameter
`promoted=1`, accepted on `/home/layout`, `/home/sections` and
`/home/sections/{id}/items`; absent or any other value means no `promoted` row is delivered —
synthetic or admin-pinned — and the responses are byte-identical to `main`
(`/home/sections/system-promoted/items` is then `404 not_found`). Admin section create/update
rejects `promoted` outside the `home` scope (`validatePromotedScope`), so no library surface can
ever carry a promoted row past the gate. When the request opts in,
`Dependencies.Promotions` is wired and the profile has active home cards,
`SectionHandler.maybeInjectPromoted` inserts a synthetic row
`{"id": "system-promoted", "section_type": "promoted", "title": "Promoted"}` at the first
card's `placement.home_position` (default `1`, clamped to the layout) unless the admin layout
already contains a `promoted` section (admins may create one through the sections CRUD to pin
its position). The cards resolved for placement ride on the resolved row
(`ResolvedSection.Promos`), so `/home/sections` evaluates `ActiveHome` once per request. On
the wire the section's `items[]` entries are the `promo` variant of the item union:
`{"content_id": "<promotion id>", "type": "promo", "title": "<headline>", "genres": [],
"keywords": [], "status": "", "promo": <card>}`. The opt-in contract is proven end to end over
the three handlers in `TestHomeEndpointsDeliverPromotedOnlyWhenOptedIn` (DB-backed) and at the
unit level in `TestMaybeInjectPromotedRequiresTheOptInParameter`.

### Client routes

- `GET /promotions?surface=detail|pre_playback&content_id=…` (profile-scoped, mounted beside
  `/home/dismissals`) → `{"surface": "detail", "promotions": [<card>, …]}` (`[]` when nothing
  applies). `surface=home` and unknown surfaces are `400 bad_request` (home rides on the section).
  `503 unavailable` when the service is not wired.
- Dismiss: `PUT /home/dismissals/promo:<surface>/<promotion id>` with body `{}`; undo with
  `DELETE` on the same path. `validHomeSurface` accepts `promo:home`, `promo:detail`,
  `promo:pre_playback`. Dismissals are per profile and per surface.
- Capability (`GET /notifications/capability`): `"promotions": {"surfaces": ["home", "detail",
  "pre_playback"]}` beside the S-1 and S-3 blocks; key absent = dormant. A client that sees the
  key opts in to the home row with `promoted=1` on the home endpoints.

### Admin routes (`/admin/promotions`, beside the S-1 announcements and S-3 ambience CRUD, same admin group)

- `GET /` → `{"promotions": [<promotion>, …], "surfaces": ["home","detail","pre_playback"]}`,
  highest priority first.
- `POST /` → `201` with the promotion (contract below). `PUT /{id}` is a full replacement with
  the same body → `200`. `DELETE /{id}` → `204`; `404 not_found` for unknown ids.
- `organization_id` naming an unknown organization is `400 bad_request`
  (`organization_id does not exist`). Errors: `400 bad_request` (validation, message names the
  field), `503 unavailable` (service not wired).
- Impressions / clicks: out of scope (nothing stored, no event route).

### Admin promotions contract (source of truth for the garden client)

Request and response copied from the passing handler test
`TestAdminPromotionsCreateReturnsCreatedPromotion` (`internal/api/handlers/admin_promotions_test.go`).
`placement`, `subtitle`, `image_width`/`image_height`, `deeplink`, `cta`, `priority`,
`targeting` (default `{"audience":"all"}`), `dismissible` (default `true`) and
`organization_id` (default null) may be omitted.

`POST /api/v1/admin/promotions`

```json
{
  "surfaces": ["home", "detail", "pre_playback"],
  "placement": {"home_position": 1, "detail_slot": "below_hero", "content_ids": ["movie-1"]},
  "kicker": "New this week",
  "headline": "The Bloem Winter Collection",
  "subtitle": "Ten films, one long weekend.",
  "image_url": "https://cdn.example/winter-16x9.jpg",
  "image_width": 1920,
  "image_height": 1080,
  "deeplink": "bloem://collection/winter",
  "cta": {"label": "Browse", "url": "/collections/winter"},
  "priority": 5,
  "starts_at": "2026-11-20T00:00:00Z",
  "ends_at": "2026-12-01T00:00:00Z",
  "targeting": {"audience": "all"},
  "dismissible": true
}
```

`201 Created`

```json
{"id":"01PROMO","organization_id":null,"surfaces":["home","detail","pre_playback"],"placement":{"home_position":1,"detail_slot":"below_hero","content_ids":["movie-1"]},"kicker":"New this week","headline":"The Bloem Winter Collection","subtitle":"Ten films, one long weekend.","image_url":"https://cdn.example/winter-16x9.jpg","image_width":1920,"image_height":1080,"deeplink":"bloem://collection/winter","cta":{"label":"Browse","url":"/collections/winter"},"priority":5,"starts_at":"2026-11-20T00:00:00Z","ends_at":"2026-12-01T00:00:00Z","targeting":{"audience":"all"},"dismissible":true,"created_by":42,"created_at":"2026-11-20T00:00:00Z","updated_at":"2026-11-20T00:00:00Z"}
```

### Promo card wire shape

One shape on both delivery paths, copied from the passing tests
`TestPromotionsListForwardsSurfaceContentAndViewer` and
`TestBuildSectionsResponseEmitsPromoItemVariant`; `subtitle`, `deeplink` and `cta` are
omitted when empty. The card carries **no timer, wait, skip or countdown field** — the tests
assert the key set — so the client always keeps "continue to content" as the default action
on the pre-playback surface.

`GET /api/v1/promotions?surface=detail&content_id=movie-1`

```json
{"surface":"detail","promotions":[{"id":"01PROMO","kicker":"New this week","headline":"The Bloem Winter Collection","subtitle":"Ten films, one long weekend.","image_url":"https://cdn.example/winter-16x9.jpg","deeplink":"bloem://collection/winter","cta":{"label":"Browse","url":"/collections/winter"},"dismissible":true}]}
```

The same card inside the home `promoted` section (`/home/sections?promoted=1`):

```json
{"id":"system-promoted","section_type":"promoted","title":"Promoted","featured":false,"item_limit":1,"total_count":1,"is_custom":false,"customized":false,"items":[{"content_id":"01PROMO","type":"promo","title":"The Bloem Winter Collection","genres":[],"keywords":[],"status":"","promo":{"id":"01PROMO","kicker":"New this week","headline":"The Bloem Winter Collection","subtitle":"Ten films, one long weekend.","image_url":"https://cdn.example/winter-16x9.jpg","deeplink":"bloem://collection/winter","cta":{"label":"Browse","url":"/collections/winter"},"dismissible":true}}]}
```
