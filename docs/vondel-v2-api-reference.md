# Vondel Server — Native /api/v2 Reference

A wire-level reference for `vondel-server`'s native `/api/v2` API surface — the Vondel-specific
extensions layered on top of the frozen, Silo-compatible `/api/v1` projection documented
separately (see [Silo API Reference](https://github.com/Vondel-Media/silo-api-reference), also
mirrored at `vondel-android/docs/silo-api-reference.md`). Compiled 2026-08-20 by reading
`vondel-server`'s actual Go source directly — route tables, handlers, and the domain packages
each handler calls into — cross-checked against real usage in `vondel-android` and `vondel-apple`.

**This is a private, proprietary API.** Unlike `/api/v1`, none of this surface is meant to be
Silo-compatible or externally stable — `vondel-server`'s own `AGENTS.md` calls the whole repo "a
VERY EARLY WIP" and explicitly welcomes sweeping changes. Treat this document as a snapshot of
2026-08-20's implementation, not a frozen contract — re-derive it from source before relying on
any exact field name or error code in new work.

## What `/api/v2` actually is

`/api/v2` is not a versioned reimplementation of `/api/v1` — it's additive. `mountV2` in
`internal/api/router_v2.go` builds everything here independently of the `/api/v1` tree ("so a
native route can be added, changed or removed without touching the projection that upstream Silo
clients depend on"), and splits into two genuinely different surfaces:

- **System & Client** (`GET /api/v2/capabilities`, `GET /api/v2/organizations`,
  `POST /api/v2/admin/session`, plus the client-facing routes mounted by `router_v2_client.go`:
  server identity, native Watch home/item/search, native progress sync, person detail) — reachable
  by an ordinary authenticated user, no organization/admin context required. This is the part the
  native mobile/TV clients actually call.
- **Admin** (everything under `/api/v2/admin`) — a genuine multi-tenant SaaS administration
  surface: per-organization admin (groups, entitlements, invitations, policy-decision audit),
  cross-organization platform admin (organization lifecycle, memberships), compatibility-app
  lifecycle management (Jellyfin/Audiobookshelf-protocol companion services), and org-wide people
  administration (bulk account/profile operations via async jobs). Confirmed via grep of both
  native client repos: **none of the Admin surface is called by either native client** — it is
  admin-console/platform-operator-only.

## Table of contents

1. [System & Client](#system--client-native-apiv2)
2. [Organization Administration](#organization-administration-native-apiv2admin)
3. [Platform Administration & Compatibility](#platform-administration--compatibility-native-apiv2admin)
4. [People Administration](#people-administration-native-apiv2adminorganizationpeople)

## Known gaps / follow-ups surfaced while compiling this doc

- **`POST /api/v2/sync/progress` is not called by either native client today**, despite offering a
  richer per-item result vocabulary (`updated`/`ignored`/`error`) than the `/api/v1` equivalent —
  `vondel-android`'s `HttpProgressSyncSource.kt` explicitly targets `/api/v1/sync/progress`
  instead, and no `sync/progress` call of any version exists in `vondel-apple`. Worth a deliberate
  decision on whether clients should migrate to the v2 endpoint, not treated as fixed here.
- Two independent optimistic-concurrency revision counters exist in the Organization Administration
  surface (a group/invitation mutation checks the org's `tenant.PolicyRevision`; an entitlement
  mutation checks that entitlement row's own `security_revision`) — a real footgun for any future
  API consumer, worth normalizing if this surface grows a real client.
- `GET /admin/organization/policy-decisions` only exposes `cursor`/`decision_name`/`limit` query
  filters even though the underlying repository also supports `user_id`, `allowed`, and a
  `from`/`to` time range — those three filters exist in the store but aren't reachable via this
  route today.

---

## System & Client (native /api/v2)

Source: `internal/api/router_v2.go` (`mountV2Routes`), `internal/api/router_v2_client.go`
(`v2ClientSurface`), and the handlers each mounts: `internal/api/handlers/v2_system.go`,
`internal/api/handlers/v2_admin_session.go`, `internal/api/handlers/server_identity.go`,
`internal/api/handlers/watch.go` + `internal/watchdoc/{document,compose}.go`,
`internal/api/handlers/v2_progress.go` (shares request/response types and helpers with
`internal/api/handlers/progress.go`), `internal/api/handlers/person_detail.go`.

This section covers everything `mountV2Routes` mounts **outside** `/api/v2/admin/*` — the public
system probes, the admin-context session exchange (which itself is not an admin-only route: any
authenticated account calls it to *become* admin-scoped), and the whole native client surface
(`v2ClientSurface`) that a television or phone app talks to for its own library view. The
`/api/v2/admin/*` tree (organization overview, groups, entitlements, policy explain, platform
administration) is a separate reference.

### Auth model: two different authorities on one `/api/v2` tree

`router_v2_client.go`'s package doc and `newV2ClientSurface`/`mount` comments spell this out
explicitly, and it is worth stating precisely because it is easy to get backwards:

- **The admin tree** (`/api/v2/admin/*`, mounted by the rest of `mountV2Routes` not covered here)
  requires a **tenant-selected session** — `apimw.AdminContextMiddleware`, fed by a short-lived
  token minted from `POST /api/v2/admin/session` (documented below), which carries organization,
  membership and policy/security revision claims.
- **The native client surface** (`server/identity`, `watch/*`, `sync/progress`, `persons/{id}`)
  **deliberately does not** require that tenant-selected session. Quoting the source comment on
  `v2ClientSurface`: *"A viewer's session is not tenant-selected — no login endpoint mints
  organization, membership or revision claims — so these routes take the same legacy tenant
  projection the v1 tree uses, and derive their media scope from the viewer's own profile
  instead."* Concretely, the authenticated client-surface route group in `mount()` uses:
  - `s.auth.RequireAuth` — the ordinary account bearer-token check (same as `/api/v1`), **not**
    `adminMW.Require`.
  - `optionalLegacyTenant(s.tenant)` — explicitly *not* `tenantMW.RequireV2`, because `RequireV2`
    would 401 every native-client request (no login endpoint mints the claims it needs). The doc
    comment: *"Organization-bound routes still must"* use `RequireV2` — client-surface routes are
    the deliberate exception, not an oversight.
  - `s.viewer.RequireViewerAccess` (when a viewer resolver is wired) — resolves the profile's
    library restrictions, content-rating ceiling and playback-quality ceiling.
  - `apimw.RequireProfile` — requires the `X-Profile-Id` header (see below); the comment notes the
    demo-mode guard is skipped here on purpose because its blocklist is written in `/api/v1` path
    prefixes and already exempts playback progress, so it would be a no-op on `/api/v2` anyway.
- `GET /api/v2/server/identity` sits **outside** that authenticated group — it is the one public,
  unauthenticated probe a client calls before it holds any credentials.

**Every `/api/v2` client-surface dependency is optional at construction.** `newV2ClientSurface`
wires each handler only if its backing store exists (`deps.DB`, `deps.UserStoreProvider`,
`deps.FileRepo`, `deps.PersonRepo`, `deps.Config`, …); a route whose handler could not be built is
left **unmounted** rather than mounted and answering emptily — a client sees `404`, not a
misleadingly-empty `200`. `mount()`'s own comment on the identity route is explicit about the
policy for the one route mounted unconditionally: a `404` says "this server doesn't have this
route", a `503` says "this server has it and cannot answer right now, retry" — the code goes out
of its way to always be able to return the latter for identity specifically.

**`X-Profile-Id` header.** Every authenticated client-surface route requires it
(`middleware.RequireProfile`); a request without it gets `400 {"error":"bad_request","message":"X-Profile-Id header is required"}` before the handler runs. A direct-profile session (one already
bound to a single profile) supplies its own profile and the header becomes optional/validated
against it instead — see `bindDirectProfile` in `internal/api/middleware/profile.go`.

---

### System

#### GET /api/v2/capabilities

**Purpose.** The public, unauthenticated build-capability probe — "what does this server do", not
"what may this caller do". Never fails: `HandleCapabilities` performs no I/O and always returns
`200`.

**Auth.** None. Mounted directly on `/api/v2`, ahead of any auth middleware.

**Request.** No body, no params.

**Response** `200` — `v2CapabilitiesResponse`:

```jsonc
{
  "api": "v2",
  "identity_schema": 1,
  "features": {
    "legacy_silo_v1": true,               // always true today
    "organization_memberships": true,     // always true today
    "tenant_bounded_media_scope": true,   // always true today
    "direct_profile_login": false         // tracks whether POST /auth/profile-login is wired (V2SystemHandler.SetDirectProfileLoginAvailable)
  },
  "media_types": ["movie", "series", "episode", "audiobook", "ebook", "manga"],
  "feature_tokens": [
    "watch_document_v1",
    "device_pairing_v1",
    "progress_sync_v1",
    "declared_event_channels",
    "/* plus every token playback.ServerFeaturesV3() publishes */"
  ]
}
```

Field notes:
- `media_types`: the fixed vocabulary of `mediaTypesServed` — the item types *this build* can
  serve at all (a property of the software, not of what an operator's libraries happen to
  contain). A type absent from the list means the build cannot serve it; a client should not infer
  anything from a type's absence about a specific server's content.
  - Note: `movie`/`series`/`episode`/`audiobook`/`ebook`/`manga` are the raw `media_items.type`
    vocabulary values (`internal/api/handlers/domain_consts.go`); there is no separate "video"
    grouping value here even though `catalog.IsValidMediaScope` accepts one internally.
- `feature_tokens`: an **additive-only allowlist** clients match against and ignore unknown
  entries from. `watch_document_v1` is the schema token for the Watch documents below;
  `progress_sync_v1` covers `POST /api/v2/sync/progress`'s per-item `updated`/`ignored`/`error`
  result vocabulary; `device_pairing_v1` covers TV pairing (protocol details on
  `/auth/device/capability`, documented in the v1 reference); `declared_event_channels` covers the
  events-websocket declared-channel handshake (details on `/events/capability`). The rest of the
  list is whatever `playback.ServerFeaturesV3()` currently publishes — check that function for the
  live set; this document does not duplicate the v1 Watch & Playback section's playback-capability
  catalogue.
- `identity_schema` is a bare integer (currently always `1`), unrelated to `api_versions` on
  `GET /api/v2/server/identity`.

**Errors.** None — the handler cannot fail.

**Clients.** `vondel-apple`'s `ServerIdentityProbe.swift` calls `/api/v2/capabilities` (constant
`Endpoints.capabilities`) alongside identity, as part of server-discovery/compatibility probing.
No confirmed `vondel-android` call to this specific path was found in the source tree searched
(android's compatibility probing lives in `SiloCompatibilityProbe.kt`, which targets `/api/v1`
surfaces).

---

#### GET /api/v2/organizations

**Purpose.** Lists the calling account's **active** organization memberships, each paired with its
organization row — the list a client (in practice, the admin/tenant web UI, not a native TV/phone
app) presents so the user can pick which organization to mint an admin-context session for via
`POST /api/v2/admin/session`.

**Auth.** `authMW.RequireAuth` (ordinary bearer-token account auth — not an admin context token).
If the server has no tenant/auth wiring at all (`authMW == nil`), the route instead answers a fixed
`503` for every caller (see Errors).

**Request.** No body, no params.

**Response** `200` — `v2OrganizationListResponse`:

```jsonc
{
  "organizations": [
    {
      "id": "018f...-uuid",
      "slug": "acme",
      "name": "Acme Media",
      "default": false,
      "membership_id": "018f...-uuid",
      "membership_role": "admin",        // tenancy.Membership.LegacyRole, free-form legacy role string
      "policy_revision": 3,
      "security_revision": 1
    }
  ]
}
```

The handler filters membership rows to `membership.Status == tenancy.MembershipActive` and
`membership.AccountID == caller`, then drops any organization it cannot resolve as
`tenancy.OrganizationActive` with a non-nil owner and matching ID — a membership pointing at a
missing/inactive/mismatched organization is silently excluded, never surfaced as a partial or
error entry.

**Errors.**
- `401 {"error":"unauthorized"}` — no valid claims (should not normally be reachable given
  `RequireAuth`, but the handler re-checks).
- `503 {"error":"tenant_unavailable","message":"Tenant authorization is unavailable"}` — no
  organization store wired (`h.organizations == nil`), a membership-list read failed, or an
  organization lookup failed mid-list. Also the fixed response the route answers when `authMW` is
  nil server-wide (in that fallback case even auth is skipped — every request, authenticated or
  not, gets this same `503`).

**Clients.** Not observed called from `vondel-android` or `vondel-apple` native-client source
(grepped both repos for `/api/v2/organizations`). This is an admin/tenant-web-UI surface, not part
of the TV/phone Watch experience.

---

#### POST /api/v2/admin/session

**Purpose.** Exchanges the caller's ordinary authenticated account session for a separate,
short-lived **administrative context token** — scoped either to the whole platform
(`AdminScopePlatform`) or to one organization (`AdminScopeOrganization`) the caller has an active
admin membership in. This is the "no login endpoint mints organization/membership/revision claims"
gap `router_v2_client.go`'s doc comment refers to: this endpoint is what *does* mint them, for
callers who explicitly ask for admin context — it is a separate elevation step, not something a
native Watch client goes through.

**Auth.** `authMW.RequireAuth`. If the server has no session handler wired (`session == nil`, e.g.
tenancy support absent), the route answers a fixed unavailable response instead of routing to a
real handler.

**Request body** (`adminContextSessionRequest`, max 16 KiB, unknown fields rejected):

```jsonc
{
  "scope": "platform" | "organization",   // auth.AdminScope
  "organization_id": "uuid"               // required (and must parse as UUID) iff scope == "organization"; must be OMITTED/empty for scope == "platform"
}
```

The decoder uses `DisallowUnknownFields` and explicitly checks for trailing JSON after the object
(a second `Decode` must hit `io.EOF`) — a body with extra top-level values is rejected as
malformed, not merely truncated-read.

**Response** `200` — `adminContextSessionResponse`:

```jsonc
{
  "access_token": "<opaque admin-context JWT>",
  "expires_at": "2026-08-20T12:34:56Z",   // RFC3339, from the minted token's own claims
  "context": {
    "key": "platform" | "organization:<uuid>",
    "scope": "platform" | "organization",
    "organization_id": "uuid",            // omitted for platform scope
    "membership_id": "uuid",              // omitted for platform scope
    "name": "Platform" | "<organization name>",
    "status": "active" | "<organization.Status>",
    "authority": "platform_admin" | "organization_admin",
    "policy_revision": 3,                 // omitted (zero value) for platform scope
    "security_revision": 1                // omitted (zero value) for platform scope
  }
}
```

Scope-specific behavior:
- **`platform`**: `organization_id` must be empty in the request (`400` otherwise). The caller must
  be a platform admin (`auth.PlatformAdminAuthorizer.IsPlatformAdmin`) or the request is refused.
  Minted claims carry `Scope: AdminScopePlatform` and no organization/membership/revision fields.
- **`organization`**: `organization_id` must parse as a UUID. The handler resolves tenancy context
  (`AdminContextSessionResolver.Resolve`), loads the membership, and requires all of: resolved
  account/organization/membership IDs match the request and the caller, `PolicyRevision > 0` and
  `SecurityRevision > 0`, and `membership.Status == tenancy.MembershipActive` — any mismatch is a
  stale-authorization `401`, not a `403` or `404`, on the theory that the caller's locally-cached
  authorization state (not their identity) is what's wrong. The organization itself must resolve to
  `tenancy.OrganizationActive`. `authority` becomes `"platform_admin"` if the caller is *also* a
  platform admin (platform admins act with elevated authority even inside one organization's
  context), otherwise the caller's own membership must carry `LegacyRole == "admin"` or the request
  is refused with `403`.

**Errors.**
- `400 {"error":"invalid_request", ...}` — malformed/oversized/unknown-field body; platform scope
  with a non-empty `organization_id`; organization scope with a missing/unparsable
  `organization_id`; unrecognized `scope` value.
- `401 {"error":"unauthorized"}` — no valid account claims.
- `401 {"error":"authorization_state_stale","message":"Tenant authorization state is stale"}` —
  resolved tenancy context and the stored membership disagree (see above).
- `403 {"error":"insufficient_platform_authority"}` — platform scope requested by a non-platform-admin.
- `403 {"error":"insufficient_organization_authority"}` — organization scope requested by a caller
  whose membership role is not `admin` and who is not a platform admin.
- `404 {"error":"not_found","message":"Organization not found"}` — via
  `tenancy.ErrOrganizationNotFound` / `ErrOwnershipResolutionRequired` / `ErrMembershipNotFound`
  paths (organization or membership rows do not resolve as expected).
- `403 {"error":"organization_suspended"}` — via `tenancy.ErrTenantSuspended` during resolve.
- `503 {"error":"tenant_unavailable", ...}` — handler not fully wired, token mint/parse failure, or
  any other tenancy-store read error. Also the fixed response when `session == nil` server-wide.

**Clients.** Not observed called from `vondel-android` or `vondel-apple` native-client source. This
is the admin-elevation surface for the tenant/admin web UI, not the TV/phone Watch experience.

---

### Server Identity

#### GET /api/v2/server/identity

**Purpose.** The public, unauthenticated "which server is this, and can I log into it yet" probe a
client calls before it holds any credentials — the first request of server discovery/pairing, and
distinct in both scope and source from `GET /api/v1/health` (health's `server_name`/`server_id`
come from Jellyfin-compatibility configuration and are deliberately left untouched by this
endpoint; the two must not be conflated even though both can carry a "server name"). Mounted
unconditionally, even when no database is configured — see Errors for why the failure mode is
`503`, never `404`, in that case.

**Auth.** None.

**Request.** No body, no params.

**Response** `200` — `serverIdentityResponse`, with `Cache-Control: no-store` (the identifier is
stable but `setup_complete` flips exactly once, so caching it would risk offering first-run setup
on an already-configured server):

```jsonc
{
  "status": "ok",
  "server_id": "<stable per-deployment identifier, from serverid.Resolver>",
  "server_name": "<branding.Service name, or branding.DefaultServerName if unbranded>",
  "api_versions": [1, 2],           // serverAPIMajorVersions — fixed constant, both API majors are always mounted
  "setup_complete": true            // !NeedsSetup(ctx) — false until the deployment's first account exists
}
```

**Errors.** Every failure mode answers **`503 {"error":"unavailable","message":"Server identity is not available"}`**, with the identifier deliberately omitted from the body rather than
substituted with a per-process or freshly-generated value — the source comment is explicit that
inventing an identifier here would be worse than a client falling back to its own stored origin
string, because it would silently re-key state the client already has stored against this server.
Triggers: no identity resolver or setup reporter wired (nil DB), the identity resolver failed, or
the setup-state read failed.

**Clients.** Called by both. `vondel-apple`: `ServerIdentityProbe.swift` (`Endpoints.identity =
"/api/v2/server/identity"`). `vondel-android`: `ServerIdentityProbe.kt`
(`"$origin/api/v2/server/identity"`). Both use it as the first step of server
discovery/compatibility probing before any authenticated call.

---

### Watch (native)

Mounted only when `deps.DB`, `deps.UserStoreProvider` and `deps.FileRepo` are all present
(`newV2ClientSurface`); a deployment without a catalog/media-file store simply does not have these
routes at all. All three endpoints sit inside the authenticated, viewer-scoped, profile-scoped
group described under **Auth model** above — every document returned is filtered to what one
profile may see, and carries that profile's own progress.

Every response is a `watchdoc.Document` (`schema: "watch_document_v1"`), sent with
`Cache-Control: private, no-store` (a profile-scoped, progress-bearing document is never cached or
revalidated by an intermediary). **This is a genuinely different document shape from
`/api/v1/catalog` and the v1 recommendation/section endpoints** — it is not a v2 alias for
paginated browse. A Watch document composes, in one response: the visible library window (or one
item's full detail, or search results), that profile's resume progress for the items named, and a
single server-chosen `featured_content_id` — an editorial choice the *server* makes once per
document rather than something each client re-derives.

**`Document` wire shape** (`internal/watchdoc/document.go`, JSON field names are exact):

```jsonc
{
  "schema": "watch_document_v1",
  "snapshot": "2026-08-20T12:00:00Z",             // RFC3339, time.Now().UTC() at composition
  "featured_content_id": "abc123",                // omitted only when Items is empty
  "items": [ /* DocumentItem[] */ ],
  "progress": [ /* DocumentProgress[] */ ]
}
```

`DocumentItem`:

```jsonc
{
  "kind": "movie" | "series" | "episode",         // watchdoc.KindMovie/KindSeries/KindEpisode — NOT the raw media_items.type vocabulary (no audiobook/ebook/manga; Watch is video-only)
  "content_id": "abc123",
  "title": "…",

  "year": 2024,                                   // omitempty
  "runtime_seconds": 5400,                        // omitempty; catalog minutes * 60
  "rating": "PG-13",                               // omitempty
  "overview": "…",                                 // omitempty
  "genres": ["Action"],                            // omitempty
  "keywords": ["heist"],                           // omitempty
  "poster_url": "https://…",                       // omitempty; always a resolved, directly-fetchable URL, never a stored path

  // series-only
  "season_count": 3,                               // omitempty
  "seasons": [ { "season_number": 1, "title": "…" } ],  // omitempty; populated only on ComposeItem (detail), and only from seasons whose episodes survived filtering

  // episode-only — required TOGETHER, never partial
  "series_id": "series-abc",                       // omitempty (but required when kind == episode)
  "season_number": 1,                              // omitempty (required, >= 1, when kind == episode)
  "episode_number": 4,                              // omitempty (required, >= 1, when kind == episode)

  "file_id": 981,                                  // omitempty; the id a client sends to POST /api/v1/playback/start. Zero is NEVER emitted — an item with no playable file is dropped from the document entirely (home/search), or kept with file_id omitted (the one item ComposeItem was asked for, and a series' shell)
  "featured": true,                                // omitempty; marks the item named by the document's top-level featured_content_id

  // detail-only (ComposeItem / GET /watch/items/{content_id}) — never populated on home or search documents
  "chapters": [ { "index": 0, "title": "…", "start_seconds": 0, "end_seconds": 120.5 } ],  // omitempty
  "intro_start_seconds": 60.0,                     // omitempty; emitted together with intro_end_seconds or not at all
  "intro_end_seconds": 90.0,                        // omitempty
  "cast": [ { "person_id": "42", "name": "…", "character": "…", "photo_url": "https://…" } ],  // omitempty; root item only, never on an episode
  "crew": [ { "person_id": "17", "name": "…", "job": "Director", "photo_url": "https://…" } ],  // omitempty; root item only
  "editions": [                                     // omitempty; populated on ComposeItem, for EVERY item it names (root and, for a series, every episode) when that item has 2+ playable files — never for a single-file item, since that would just restate file_id
    {
      "file_id": 981, "resolution": "1080p", "codec_video": "hevc", "codec_audio": "eac3",
      "hdr": true, "container": "mkv", "duration_seconds": 5423.2,
      "edition_key": "…", "edition_label": "Director's Cut"
    }
  ]
}
```

`DocumentProgress`:

```jsonc
{
  "content_id": "abc123",       // the movie, or the series an episode row belongs to — never an episode id here
  "episode_id": "ep-9",         // omitempty; present only when this row is attributed to an episode of content_id's series
  "position_seconds": 245.3,
  "duration_seconds": 5400.0,
  "completed": false,
  "updated_at": "2026-08-20T12:00:00.123456789Z"   // RFC3339Nano
}
```

A progress row is emitted **only** for a `content_id` the document's `items` array already lists —
a restricted or unlisted item cannot be named through the progress array even if a stored row for
it exists (`watchdoc.readProgress`). A row also needs `duration_seconds > 0` and a non-zero
`updated_at` to be emitted at all — rows that cannot describe a resume point are dropped, not sent
with zero/garbage values.

**Featured-item rule** (`chooseFeatured`, closed and server-owned — layout/rendering of "featured"
is entirely the client's decision): the most-recently-added **movie** the profile has not
completed; if none qualifies, the first item in the document's own order (added-time descending,
content-id ascending on ties — a strict total order, so the choice is deterministic for the same
store contents). `GET /watch/items/{content_id}` overrides this: the requested item is always the
featured one (as long as it made it into `items` at all).

#### GET /api/v2/watch/home

**Purpose.** The first frame of a TV/phone home screen: up to `watchHomeItemLimit` = **100** most
recently added movies/series the profile may see, unioned with whatever movies/series the profile
has progress on (even if older than that 100-item window) via a second bounded read of the
profile's `watchRecentProgressRows` = **200** most-recently-updated progress rows — otherwise a
long-tenured viewer's Continue Watching titles could fall entirely outside the 100-item recency
window and the document would have nothing to resume. An episode progress row is resolved back to
its series for this purpose. This is a **window, not a paginated catalog** — there is no
`?cursor=`/`?offset=` on this endpoint; a client that wants to browse the full library still uses
`/api/v1/catalog`.

**Auth.** `RequireAuth` + `optionalLegacyTenant` + (if wired) `RequireViewerAccess` + `RequireProfile` — see Auth model above. `X-Profile-Id` header required.

**Request.** No query params.

**Response** `200` — a `Document` as above, `items` capped per the window/union logic, `progress`
covering the union's requested content ids, `featured_content_id` chosen by the rule above.

**Errors.**
- `400 {"error":"bad_request","message":"X-Profile-Id header is required"}` — should not normally
  reach the handler given `RequireProfile`, but the handler re-checks.
- `500 {"error":"internal_error","message":"Failed to compose the Watch home document"}` — any
  composition-layer read failure.
- `503 {"error":"unavailable","message":"Watch documents are not available"}` — Watch not wired
  (nil reader) — the route itself is unmounted in that case, so this path is effectively dead code
  guarding against future wiring mistakes rather than a live response.

**Clients.** Both. `vondel-apple`: `HTTPWatchCatalogSource.swift` (`Endpoints.home =
"/api/v2/watch/home"`). `vondel-android`: `HttpWatchCatalogSource.kt` (`HOME_PATH =
"/api/v2/watch/home"`); its own file comment states *"The native client surface lives on
`/api/v2`; `/api/v1` is a frozen Silo-compatible projection."*

---

#### GET /api/v2/watch/items/{content_id}

**Purpose.** The detail document for one movie or series: for a series, every surviving episode in
ascending season/episode order plus season summaries; for a movie, the item itself. Unlike the home
document, this is the one place chapters/skip-intro markers, cast/crew, and multi-edition file
choices are populated (see the `DocumentItem` shape above) — a home or search document never pays
for per-item data nothing on that screen renders.

**Auth.** Same authenticated group as `/watch/home`.

**Path param.** `content_id` (string, required — `400` if blank after trim).

**Response** `200` — a `Document` whose `items` names the requested item (root — with
`file_id` **omitted**, not dropped, when the item itself has no playable file; the detail screen
still renders with Play unavailable) plus its episodes for a series, `featured_content_id` always
equal to `content_id` when the item survived composition, `progress` for every content id named.

**Errors.**
- `400 {"error":"bad_request","message":"content_id is required"}`.
- **`404 {"error":"not_found","message":"Item not found"}`** — deliberately the same answer whether
  the item does not exist at all or exists but the profile's access predicates exclude it
  (`watchdoc.ErrItemNotFound`). This is intentional, per the source comment: distinguishing the two
  would let a restricted profile probe for the existence of content it may not see. Also returned
  for a content id that resolves to a non-movie/series kind (audiobook, ebook, manga, or a bare
  episode id) — Watch is video-only.
- `500 {"error":"internal_error","message":"Failed to compose the Watch item document"}`.
- `503` unavailable — same as home.

**Clients.** Both. `vondel-apple`: `HTTPWatchCatalogSource.swift` (`Endpoints.itemPrefix =
"/api/v2/watch/items/"`). `vondel-android`: `HttpWatchCatalogSource.kt` (`ITEM_PATH_PREFIX =
"/api/v2/watch/items/"`).

---

#### GET /api/v2/watch/search

**Purpose.** Server-side title search over the movies/series the profile may see, returned in the
search provider's own relevance order (never re-sorted by the composition layer), capped at
`watchSearchResultLimit` = **50** results. No pagination — a client wanting more is a client
wanting scroll pagination, which this endpoint does not offer (same "first frame" tradeoff as
`/watch/home`'s item limit).

**Auth.** Same authenticated group as `/watch/home`. Additionally requires a search provider to be
wired at all — see Errors.

**Query params.** `q` (string, required, trimmed — `400` if empty).

**Response** `200` — a `Document` with `items` in search-ranked (not added-time) order. Unlike
`/watch/home` and `/watch/items/{id}`, `ComposeSearchResults` builds the `Document` directly
instead of going through the shared `finishDocument` finisher, so **`featured_content_id` is always
omitted and `progress` is always an empty array `[]`** on this endpoint — the source comment is
explicit that search is "a results list, not a home screen." A client wanting resume-progress for a
searched item must fetch it via `/watch/items/{content_id}` or `/watch/home`. Detail-only fields
(chapters/cast/crew/editions) are never populated either — search results are always the terse item
shape, deduplicated by `content_id` (keeping the first, best-ranked occurrence).

**Errors.**
- `400 {"error":"bad_request","message":"q is required"}`.
- `503 {"error":"unavailable","message":"Search is not available"}` — no search provider configured
  (`h.searcher == nil`); this is checked before the query param, so a missing `q` on a
  searchless deployment still yields the searcher-unavailable answer first only if... actually the
  code checks `h.searcher == nil` **before** parsing `q`, so an empty `q` on a deployment with no
  search provider returns `503`, not `400`.
- `500 {"error":"internal_error","message":"Search failed"}` — search provider error.
- `500 {"error":"internal_error","message":"Failed to compose the Watch search document"}` —
  composition failure after a successful search.
- `503` unavailable (no reader) — same as home.

**Clients.** Both. `vondel-apple`: `HTTPWatchCatalogSource.swift` (`Endpoints.searchPath =
"/api/v2/watch/search"`). `vondel-android`: `HttpWatchCatalogSource.kt` (`SEARCH_PATH =
"/api/v2/watch/search"`).

---

### Sync (native)

#### POST /api/v2/sync/progress

**Purpose.** Batch offline-queue flush of playback progress — the same storage path, thresholds,
last-write-wins merge, taste-profile-staleness trigger and event fan-out as
`POST /api/v1/sync/progress`, but reported back to the caller in a **finer per-item status
vocabulary**. The v1 route reports a flat `"ok"` for every non-error row (frozen — real Silo clients
parse it and it cannot change under them); this v2 route distinguishes a row that was actually
**written** from one that was accepted but **discarded** by last-write-wins or the min-resume
floor, so a client can tell "landed" from "not landed" and stop retrying a position the server
genuinely never stored (the exact failure mode the sync protocol exists to prevent). Clients
feature-detect this vocabulary via the `progress_sync_v1` token on `/api/v2/capabilities` rather
than by version-sniffing the path.

**Auth.** Same authenticated group as the Watch routes: `RequireAuth` + `optionalLegacyTenant` +
(if wired) `RequireViewerAccess` + `RequireProfile`.

**Request body** (`syncProgressRequest`):

```jsonc
{
  "items": [
    {
      "media_item_id": "abc123",        // required per-item; a blank one is reported as a per-item error, not a request-level 400
      "position": 245.3,
      "duration": 5400.0,
      "force_overwrite": false,         // when true, unconditionally overwrites stored progress (store.SetProgress) regardless of LWW
      "updated_at": "2026-08-20T12:00:00Z"  // OPTIONAL RFC3339 CLIENT event time; presence selects the offline-queue LWW-merge path (store.SetProgressIfNewer) instead of the default live-heartbeat path (store.UpdateProgress)
    }
  ]
}
```

Three distinct write paths per item, chosen in this priority order:
1. **`updated_at` present** → offline-queued event. The client time is parsed as RFC3339 (a
   malformed value is a per-item `error`, not silently treated as "now" — treating it as now would
   let a stale offline event unfairly win a last-write-wins race); it is then clamped to at most
   `now + 2 minutes` (`progressClockSkew`) — a value further in the future than that is silently
   replaced with `now` (and logged server-side), never rejected. `store.SetProgressIfNewer` applies
   last-write-wins on this clamped event time; a `false` return (a newer stored event already won)
   is exactly what makes the result `"ignored"`, not an error.
2. **`force_overwrite: true`** (and no `updated_at`) → `store.SetProgress`, unconditional write,
   bypassing LWW.
3. **default** (neither of the above) → `store.UpdateProgress`, the ordinary live-heartbeat write
   path, still subject to the deployment's watched/min-resume threshold settings
   (`playback.watched_threshold`, `playback.min_resume_threshold` — read fresh per request from
   settings, default when unset/unparsable).

In every path, `userstore.ResolveProgressState(position, duration, thresholds)` first classifies
whether the position falls below the min-resume floor (`skip`) — a below-floor position is
never actually written even on the offline-queue path (`SetProgressIfNewer` is skipped entirely
when `skip` is true), and is reported `"ignored"`.

**Response** `200` — `syncProgressResponse`:

```jsonc
{
  "results": [
    { "media_item_id": "abc123", "status": "updated" },
    { "media_item_id": "def456", "status": "ignored" },
    { "media_item_id": "",       "status": "error", "error": "media_item_id is required" },
    { "media_item_id": "ghi789", "status": "error", "error": "updated_at must be RFC3339" },
    { "media_item_id": "jkl012", "status": "error", "error": "failed to update progress" }
  ]
}
```

`status` is exactly one of three values (constants `syncStatusUpdated`/`syncStatusIgnored`/`syncStatusError`):
- `"updated"` — the row was written to storage.
- `"ignored"` — accepted, but not written: either the min-resume floor discarded it, or a newer
  stored event won last-write-wins. **Not an error** — the client must not retry this item as a
  failure.
- `"error"` — rejected; `error` carries one of: `"media_item_id is required"`, `"updated_at must be
  RFC3339"`, or the generic `"failed to update progress"` (a store-layer failure).

Side effects that fire once per request (not per item) whenever at least one item was processed
(i.e., reached any non-empty-`media_item_id` write attempt, `updated`/`ignored` alike, but not
`error`): the profile's taste-profile is marked stale and a background refresh is requested
(`triggerProfileRefresh`), and a `"progress"` user-state event is published per processed item
(`publishUserStateEvent`) for real-time fan-out (e.g. to Watch Together / multi-device sync
listeners) via the events hub.

**Errors.**
- `400 {"error":"bad_request","message":"Invalid request body"}` — malformed JSON.
- `400 {"error":"bad_request","message":"At least one progress item is required"}` — empty `items`.
- `500 {"error":"internal_error","message":"Failed to access user store"}` — store-provider
  resolution failure.
- Otherwise `200` always, with per-item `error` entries inside `results` for item-level problems —
  a batch with some bad items and some good ones is not failed wholesale.

**Clients.** **Neither native client was found calling this v2 endpoint.** `vondel-android`'s
`HttpProgressSyncSource.kt` explicitly targets **`POST /api/v1/sync/progress`** — its own doc
comment reads: *"Pushes locally-recorded checkpoints to a Vondel server's `POST
/api/v1/sync/progress` — the real…"* (the v1 route, not this v2 one), despite the file living in
a module whose sibling watch/person sources are on `/api/v2`. `vondel-apple` has no
`sync/progress` call under any API version in the source tree searched — its
`WatchProgressCoordinator`/`WatchProgressStore` did not resolve to an HTTP call in this pass (it
may write through per-session `/api/v1/playback/{session_id}/progress` heartbeats instead, or be
local-only; not confirmed further here). **This v2 route currently appears unused by both
first-party native clients** — a real, worth-flagging gap between the richer per-item result
vocabulary this endpoint offers and what ships today.

---

### Persons

#### GET /api/v2/persons/{person_id}

**Purpose.** A cast/crew member's own detail page: bio/photo plus their filmography, capped at
`personFilmographyLimit` = **100** items (most recent by year first) among the movies/series the
requesting profile may see.

**Auth.** Same authenticated group as Watch/Sync above (`RequireAuth` + `optionalLegacyTenant` +
`RequireViewerAccess` (if wired) + `RequireProfile`). Note the source comment's distinction: a
person's **identity** (name, bio, photo) is not itself profile-scoped data — "anyone who can reach
the endpoint may read it" — but the **filmography** is filtered by the viewer's library/rating
scope exactly like Watch and browse, applied inside the same query as the person join
(`catalog.BrowseFilters.PersonID`) rather than filtered after the fact.

**Path param.** `person_id` — must parse as a positive integer (`400` otherwise).

**Response** `200` — `personDetailResponse`:

```jsonc
{
  "id": "42",
  "name": "…",
  "bio": "…",                      // omitempty
  "birth_date": "1970-01-15",      // omitempty; "2006-01-02" format, pointer (present only if known)
  "death_date": null,              // omitempty; same format, pointer
  "birthplace": "…",               // omitempty
  "photo_url": "https://…",        // omitempty; resolved "featured"-size URL, omitted when no photo path or path is "-"
  "filmography": [
    {
      "content_id": "abc123",
      "title": "…",
      "kind": "movie" | "series",   // the item's own catalog type, for routing a tap to the right detail screen
      "year": 2019,                 // omitempty
      "role": "Jane Doe" | "Director",  // omitempty; character name for an acting credit (models.PersonKindActor/GuestStar), else the credit's job title/kind
      "poster_url": "https://…"     // omitempty
    }
  ]
}
```

`filmography` is always present as an array (never `null`) — `[]` when the person has no visible
credits or `browse`/`roles` sources are unwired, sorted by year descending (the browse repository's
own "year" sort — not re-sorted client-side by this handler).

**Errors.**
- `400 {"error":"bad_request","message":"person_id must be a positive integer"}`.
- `404 {"error":"not_found","message":"Person not found"}` — no row (`pgx.ErrNoRows` or a nil
  result).
- `500 {"error":"internal_error","message":"Failed to load person"}` / `"Failed to load
  filmography"` — store errors.
- `503 {"error":"unavailable","message":"Person detail is not available"}` — no person source wired
  (route would actually be unmounted in that case — `deps.PersonRepo == nil` skips mounting
  `surface.persons` entirely — so this is a defensive path, not a live response).

**Clients.** Both. `vondel-apple`: `HTTPPersonSource.swift` (`personPrefix =
"/api/v2/persons/"`). `vondel-android`: `HttpPersonSource.kt` (`PERSON_PATH_PREFIX =
"/api/v2/persons/"`; file doc comment: *"One person's own page, read from a Vondel server's native
`GET /api/v2/persons/{person_id}`."*).

---

## Organization Administration (native /api/v2/admin)

Everything in this section is mounted under `r.Route("/admin", …)` in `mountV2Routes` (`internal/api/router_v2.go`, ~line 165), guarded by a single `r.Use(adminMW.Require)` that wraps the *entire* `/admin` subtree — every endpoint below, both Organization and Policy Explain, is behind it. There is no route-by-route auth variation to call out per endpoint; it is documented once here.

### The admin-context gate (`AdminContextMiddleware.Require`)

`/api/v2/admin/*` is deliberately **not** protected by the same bearer JWT used elsewhere in the API (`authMW.RequireAuth`, the account-session token). It requires a second, short-lived **administrative context token** — a distinct JWT type (`auth.AdminContextClaims`, `internal/auth/admin_context.go`) obtained by first calling `POST /api/v2/admin/session` (`AdminContextSessionHandler.HandleSession`) with a normal account session and getting back one of these tokens. That handoff itself is out of scope here; what matters for every endpoint below is what the token asserts and how `Require` re-checks it on every request:

- **Lifetime**: `auth.AdminContextTokenLifetime` = 15 minutes, fixed at mint time. There is no refresh for an admin-context token — a client mints a new one via `/admin/session` when it expires.
- **Scope** (`claims.Scope`): exactly one of two mutually exclusive kinds. `AdminScopePlatform` binds the token to an account only (no organization) — this is the *platform operator* context, used by the sibling `/admin/platform/...` and `/admin/organization/people` routes documented elsewhere, not by the endpoints in this file. `AdminScopeOrganization` binds the token to one exact `(account, organization, membership)` triple — this is the context every endpoint documented in this file requires.
- **What "organization admin" means here**: an `AdminScopeOrganization` token is only honored when `claims.EffectiveAuthority` is `"organization_admin"` (an active membership whose `LegacyRole == "admin"`) or `"platform_admin"` (a platform operator who has "entered" an organization's admin context — re-verified live against `auth.PlatformAdminAuthorizer.IsPlatformAdmin` on every request, not trusted from the token alone). Anything else — a non-admin membership, a stale/former admin — is rejected with `401 authorization_state_stale`, not `403`, because the token parsed fine but the authority it claims no longer holds.
- **Live revalidation, not just signature checking**: on every request, `Require` re-resolves the membership (`tenancy.Resolver.Resolve` + `AdminContextMembershipStore.GetMembership`) and compares `AccountID`, `OrganizationID`, `MembershipID`, `PolicyRevision`, and `SecurityRevision` against what the token asserts. Any mismatch — the membership was suspended, the org's policy or security revision moved, the membership row disappeared — invalidates the token immediately (`401 authorization_state_stale`), even though the JWT signature and expiry are still fine. A previously-admin token cannot be replayed after a demotion.
- **What lands in the request context**: `Require` puts two things on success — `middleware.SetAdminContextClaims` (the raw, server-validated `AdminContextClaims`) and `tenancy.WithContext` (the freshly re-resolved `tenancy.Context`, i.e. the current `OrganizationID`, `MembershipID`, `AccountID`, `PolicyRevision`, `SecurityRevision`). Every handler in this file additionally calls `requireV2OrganizationContext`, which re-derives both from context and rejects (`403 insufficient_organization_authority`) unless `claims.Scope == AdminScopeOrganization` and the claims and the resolved tenant context agree on account/organization/membership. In effect: an `AdminScopePlatform` token, even from a real platform admin, is refused by every endpoint in this file — Organization and Policy Explain are exclusively an organization-admin surface.
- **Not built at all** ⇒ `503`: if the server has no database, or the admin-context token service/resolver/membership store/platform authorizer couldn't be constructed, `adminMW` is `nil` and the whole `/admin` subtree answers `503 tenant_unavailable` for every route (`mountUnavailableAdminContextRoutes`). If `organizationHandler`/`explainHandler` specifically failed to construct (no DB) but `adminMW` did build, unmatched sub-paths fall through to a generic `404 not_found` "Administrative resource not found" from the subtree's catch-all.

**Optimistic concurrency.** Every mutating Organization endpoint (not Policy Explain, which is read-only) requires an `expected_revision` in the request body. Two different revisions are in play and callers must send the right one for the right resource — this is easy to get wrong:
- Group and invitation mutations check `expected_revision` against the **organization's** `tenant.PolicyRevision` (`requireOrganizationRevision`). A mismatch is `409 authorization_state_changed` with the current `policy_revision`.
- Library entitlement mutations check `expected_revision` against that **specific entitlement row's** `security_revision` (`organization_entitlements.security_revision`), not the org-wide policy revision. A mismatch is also `409 authorization_state_changed`, with `current_revision` reflecting the entitlement's own revision, sourced from the tenant context's `SecurityRevision` field in the response envelope (see the endpoint entries below for the exact shape).

**Clients**: grepped both `vondel-android` and `vondel-apple` for every literal path in this section (`organization/overview`, `organization/groups`, `organization/libraries`, `organization/entitlements`, `organization/invitations`, `organization/policy-decisions`) — zero references in either repository. This entire section (Organization + Policy Explain) is admin-console/web-only; no native client calls any endpoint documented here.

---

### Organization

Handler: `handlers.V2AdminOrganizationHandler` (`internal/api/handlers/v2_admin_organization.go`), constructed with four independent stores/repositories:

- `tenants` (`*tenancy.Store`) — organization identity and summary counts.
- `access.NewGroupStore(deps.DB)` — **access groups**: an organization's reusable permission/entitlement *templates* (playback quality ceiling, download rules, stream/transcode caps, allowed permissions, an assignable library subset) that profiles are assigned to. Distinct from a "role" — a group is a bundle of *content and playback restrictions*, not an administrative privilege level. Every organization has exactly one `is_default` group that new non-admin profiles land in automatically; it cannot be deleted (only replaced by promoting another group to default first).
- `resourcetenancy.NewStore(deps.DB)` — **entitlements**: the record of which shared/platform-owned libraries (`media_folders` owned by a `platform` resource owner) an organization has been granted access to, and at what status (`active`/`suspended`/`revoked`). This is a distinct concept from access groups: an entitlement governs whether the organization can see a library *at all*; a group governs what a given profile *inside* that organization may additionally do with the libraries it can see. `HandleListLibraries` returns the union of libraries the organization owns outright (`access_kind: "owned"`) and platform libraries it holds a live entitlement for (`access_kind: "entitled"`).
- `invitations.NewRepository(deps.DB)` — **the same `invitations` table and `models.Invitation` type used by the legacy `/admin/invitations` (v1) admin surface**, not a different invitation concept. Both are single-use, email-bound claim tokens carrying the access choices (`access_group_id`, `library_ids`, `create_profile`, `show_tour`) an admin made at send time, described in the `/api/v1` reference's "Invitations" section. What differs between the two surfaces: the v2 organization endpoints are explicitly organization-scoped (`ListForOrganization`/`CreateForOrganization` against a specific `OrganizationID`, vs. v1's implicit `COALESCE(..., default_organization_id())` projection), require the `expected_revision` optimistic-concurrency check described above, and — notably — **do not send email or return a `claim_url`/`email_sent`**: `HandleCreateInvitation` mints the token itself (`invitations.NewToken()`) and returns the raw `claim_token` directly in the response body, leaving delivery entirely to the caller. The v2 surface also has no resend or revoke/delete endpoint; only list and create are exposed here (revocation/resend remain v1-only, under `/admin/invitations/{id}`).

All five stores are satisfied by narrow interfaces defined in the same file (`V2OrganizationOverviewStore`, `V2OrganizationGroupStore`, `V2OrganizationResourceStore`, `V2OrganizationInvitationStore`); if the handler as a whole is nil or a specific store is nil, its endpoints answer `503 tenant_unavailable`/`"Tenant administration is unavailable"`.

Every endpoint below operates on `tenant.OrganizationID` — the organization is never a path parameter or query parameter; it comes entirely from the resolved admin-context token, so an org-admin token can only ever act on its own organization.

#### GET /api/v2/admin/organization/overview
- Purpose: summary counts for the admin's own organization (membership/profile/library/entitlement totals) — the admin console's organization dashboard.
- Auth required: organization-admin context (see gate above).
- Request: none.
- Response body (200):
```json
{
  "organization": {
    "id": "uuid",
    "slug": "string",
    "name": "string",
    "status": "initializing | active | suspended",
    "owner_account_id": 0,
    "policy_revision": 0,
    "is_default": true,
    "membership_count": 0,
    "active_membership_count": 0,
    "profile_count": 0,
    "library_count": 0,
    "entitlement_count": 0
  }
}
```
`owner_account_id` is `*int`, `omitempty`. `library_count` counts `media_folders` owned by resource owners scoped to this organization (not entitled platform libraries). `entitlement_count` counts non-`revoked` rows in `organization_entitlements`. Type: `tenancy.OrganizationSummary` (embeds `tenancy.Organization`).
- Errors: `404 not_found` (organization row missing — `tenancy.ErrOrganizationNotFound`), `503 tenant_unavailable`.
- Clients: none (admin-console only).

#### GET /api/v2/admin/organization/groups
- Purpose: lists every access group defined for the organization.
- Auth required: organization-admin context.
- Request: none.
- Response body (200):
```json
{
  "groups": [
    {
      "id": 0,
      "name": "string",
      "description": "string",
      "library_ids": [0],
      "max_playback_quality": "1080p | 2160p | \"\" (unrestricted)",
      "download_allowed": true,
      "download_transcode_allowed": true,
      "max_streams": 0,
      "max_transcodes": 0,
      "allowed_permissions": ["string"],
      "requests_allowed": true,
      "is_default": false,
      "member_count": 0,
      "created_at": "RFC3339",
      "updated_at": "RFC3339"
    }
  ]
}
```
`max_streams`/`max_transcodes` of `0` mean "no group-level cap." `library_ids` empty/absent means the group does not further restrict which libraries a profile can see beyond what the profile's account already allows. Type: `accessGroupResponse` (shared with the v1 `AccessGroupHandler`).
- Errors: `503 tenant_unavailable`.
- Clients: none.

#### POST /api/v2/admin/organization/groups
- Purpose: creates a new access group.
- Auth required: organization-admin context.
- Request body:
```json
{
  "name": "string (required, non-empty after trim)",
  "description": "string",
  "library_ids": [0],
  "max_playback_quality": "any | standard | 480p | 720p | 1080p | 4k | uhd | 2160p | 4320p",
  "download_allowed": true,
  "download_transcode_allowed": true,
  "max_streams": 0,
  "max_transcodes": 0,
  "allowed_permissions": ["string"],
  "requests_allowed": true,
  "is_default": false,
  "expected_revision": 0
}
```
`max_playback_quality` is normalized case-insensitively via `access.ParsePlaybackQualityPreset`: `""`/`"any"` → unrestricted, `"standard"`/`"480p"`/`"720p"`/`"1080p"` → canonical `"1080p"`, `"4k"`/`"uhd"`/`"2160p"`/`"4320p"` → canonical `"2160p"`; anything else is rejected. `download_allowed`, `download_transcode_allowed`, `requests_allowed` default to `true` when omitted; `max_streams`/`max_transcodes` default to `0` (uncapped) and must be `>= 0`; every `library_ids` entry must be a positive integer. `expected_revision` is checked against the **organization's** `policy_revision` (see optimistic-concurrency note above) — since a group doesn't exist yet, this is really "the org's policy state I last observed," not the group's own revision.
- Response body (201):
```json
{ "group": { "...": "accessGroupResponse, see GET .../groups above" }, "policy_revision": 0 }
```
- Errors: `422 validation_failed` (bad name, quality, library IDs, or negative stream/transcode caps — `fields` map names the offending field), `409 authorization_state_changed` (`current_revision` = the org's current `policy_revision`), `409 conflict` (duplicate group name — `access.ErrGroupDuplicate`), `503 tenant_unavailable`.
- Clients: none.

#### GET /api/v2/admin/organization/groups/{id}
- Purpose: fetches one access group by numeric ID, scoped to the organization.
- Auth required: organization-admin context.
- Request: none. `{id}` must be a positive integer or the route answers `404` before touching the store.
- Response body (200): `{ "group": { "...": "accessGroupResponse" } }`.
- Errors: `404 not_found` (bad/non-existent ID, or a group ID belonging to a different organization — indistinguishable from missing), `503 tenant_unavailable`.
- Clients: none.

#### PUT /api/v2/admin/organization/groups/{id}
- Purpose: partially updates an access group. Every field is optional — omitted fields are left unchanged; only `is_default`/`download_allowed`/etc. use pointer semantics to distinguish "not sent" from "sent false."
- Auth required: organization-admin context.
- Request body: same shape as create, but every top-level field is optional (`accessGroupUpdateRequest`), plus the required `expected_revision`:
```json
{
  "name": "string (optional; if present, must be non-empty after trim)",
  "description": "string",
  "library_ids": [0],
  "max_playback_quality": "string",
  "download_allowed": true,
  "download_transcode_allowed": true,
  "max_streams": 0,
  "max_transcodes": 0,
  "allowed_permissions": ["string"],
  "requests_allowed": true,
  "is_default": false,
  "expected_revision": 0
}
```
Same normalization/validation rules as create for any field that is present. `expected_revision` again checks the org's `policy_revision`, not a per-group revision — concurrent edits to *different* groups in the same org still race on this field.
- Response body (200): `{ "group": { "...": "accessGroupResponse" }, "policy_revision": 0 }`.
- Errors: `422 validation_failed`, `409 authorization_state_changed`, `404 not_found` (`access.ErrGroupNotFound`), `409 conflict` (duplicate name, or attempting to un-default the sole default group — `access.ErrGroupDuplicate`/`access.ErrDefaultGroupRequired`), `503 tenant_unavailable`.
- Clients: none.

#### DELETE /api/v2/admin/organization/groups/{id}
- Purpose: deletes a non-default access group; every profile assigned to it is reassigned to the organization's current default group in the same transaction.
- Auth required: organization-admin context.
- Request body: `{ "expected_revision": 0 }` (required, checked against the org's `policy_revision`).
- Response body (200) — note this endpoint returns `200` with a body, not `204`, because it reports the exact reassignment impact:
```json
{ "profiles_reassigned": 0, "default_group_id": 0 }
```
Type: `access.GroupDeletionImpact`. `default_group_id` is the group every reassigned profile now belongs to.
- Errors: `422 validation_failed` (missing/non-positive `expected_revision`), `409 authorization_state_changed`, `404 not_found` (group doesn't exist), `409 conflict` (the target *is* the default group — `access.ErrDefaultGroupRequired`; promote another group first), `503 tenant_unavailable`.
- Clients: none.

#### GET /api/v2/admin/organization/libraries
- Purpose: lists every library visible to the organization — both owned and entitled — for building the entitlement-management UI.
- Auth required: organization-admin context.
- Request: none.
- Response body (200):
```json
{
  "libraries": [
    {
      "folder_id": 0,
      "name": "string",
      "type": "string",
      "access_kind": "owned | entitled",
      "entitlement": {
        "id": "uuid",
        "organization_id": "uuid",
        "status": "active | suspended | revoked",
        "security_revision": 0
      }
    }
  ]
}
```
`entitlement` is present (`omitempty`) only when `access_kind` is `"entitled"` — organization-owned libraries never carry an entitlement object, because owning a library isn't mediated by one. Type: `resourcetenancy.LibraryProjection`.
- Errors: `503 tenant_unavailable`.
- Clients: none.

#### PUT /api/v2/admin/organization/entitlements/{folder_id}
- Purpose: flips an existing library entitlement between `active` and `suspended` (this endpoint cannot create a new entitlement or set `revoked` — revocation is the DELETE endpoint below).
- Auth required: organization-admin context.
- Request body:
```json
{ "expected_revision": 0, "status": "active | suspended" }
```
`expected_revision` here is checked against **the entitlement row's own `security_revision`** (`organization_entitlements.security_revision`), *not* the organization's `policy_revision` — this is the one mutation in this section that uses the other revision counter. `status` must be exactly `"active"` or `"suspended"`; anything else (including `"revoked"`) is a validation error.
- Response body (200):
```json
{ "entitlement": { "id": "uuid", "organization_id": "uuid", "status": "active | suspended", "security_revision": 0 } }
```
`security_revision` in the response is already incremented — use it as the next `expected_revision`.
- Errors: `422 validation_failed` (missing/non-positive `expected_revision`, or `status` not `active`/`suspended`), `409 authorization_state_changed` (`current_revision` here is the *tenant* context's `SecurityRevision`, returned via the shared `writeV2OrganizationError` conflict envelope — reload the libraries list to get the entitlement's fresh `security_revision`), `404 not_found` (no live — `active`/`suspended` — entitlement for that `folder_id` in this organization — `resourcetenancy.ErrResourceHidden`), `503 tenant_unavailable`.
- Clients: none.

#### DELETE /api/v2/admin/organization/entitlements/{folder_id}
- Purpose: revokes a library entitlement outright (sets `status='revoked'`, stamps `revoked_at`) — the organization permanently loses access to that platform library unless a new entitlement is granted later.
- Auth required: organization-admin context.
- Request body: `{ "expected_revision": 0 }` (required; checked against the entitlement's own `security_revision`, same as PUT above).
- Response body: `204 No Content`.
- Errors: `422 validation_failed`, `409 authorization_state_changed`, `404 not_found` (`resourcetenancy.ErrResourceHidden` — no live entitlement for that folder), `503 tenant_unavailable`.
- Clients: none.

#### GET /api/v2/admin/organization/invitations
- Purpose: lists every invitation (any lifecycle state) created for this organization, newest first.
- Auth required: organization-admin context.
- Request: none.
- Response body (200):
```json
{
  "invitations": [
    {
      "id": 0,
      "email": "string",
      "role": "string",
      "access_group_id": 0,
      "library_ids": [0],
      "create_profile": true,
      "show_tour": true,
      "note": "string",
      "invited_by": 0,
      "invited_by_name": "string",
      "status": "pending | accepted | expired | revoked",
      "expires_at": "RFC3339",
      "accepted_at": "RFC3339",
      "accepted_user_id": 0,
      "created_at": "RFC3339"
    }
  ]
}
```
Identical `invitationResponse` shape and `status` derivation (revoked > accepted > expired > pending, computed at read time) as the v1 `GET /admin/invitations/` response documented in the client-facing reference — see that entry for field-by-field detail. The only difference is this list is filtered to one organization explicitly rather than the legacy default-organization projection.
- Errors: `503 tenant_unavailable`.
- Clients: none.

#### POST /api/v2/admin/organization/invitations
- Purpose: creates an invitation scoped to this organization. Unlike the v1 `POST /admin/invitations/`, this endpoint never sends email — it hands the caller the raw claim token to deliver however it chooses.
- Auth required: organization-admin context.
- Request body:
```json
{
  "email": "string (required, valid RFC 5322 address)",
  "expected_revision": 0,
  "access_group_id": 0,
  "library_ids": [0],
  "create_profile": true,
  "show_tour": true,
  "note": "string"
}
```
`expected_revision` is checked against the organization's `policy_revision` (org-wide, not per-invitation). `access_group_id` is `*int64` (nullable). `create_profile`/`show_tour` are `*bool`, defaulting to `true` when omitted/null. Role is hardcoded to `"user"` server-side — there is no `role` field on this request (unlike v1's `createInvitationRequest`, which accepts one). The invitation is created with `invitations.DefaultTTL` = 7 days from now.
- Response body (201):
```json
{
  "invitation": { "...": "invitationResponse, see GET .../invitations above" },
  "claim_token": "string"
}
```
`claim_token` is the raw, single-use token (the server stores only its SHA-256 hex digest) — this response is the only place it is ever returned, and there is no `email_sent`/`claim_url` field at all: delivery is entirely the caller's responsibility.
- Errors: `422 validation_failed` (`email` fails `net/mail.ParseAddress` or contains surrounding whitespace/comments the parser stripped), `409 authorization_state_changed`, `503 tenant_unavailable` (also covers token-generation failure, which is treated as a generic tenant-unavailable error rather than a distinct code).
- Clients: none.

---

### Policy Explain

Handler: `handlers.V2PolicyExplainHandler` (`internal/api/handlers/v2_policy_explain.go`), backed by `policy.NewDecisionRepository(deps.DB)` reading the `policy_decisions` table. This is a **read-only audit/explainability log**: every time the OPA-based policy engine (`internal/policy`) evaluates one of three decision kinds — `silo.scope.decision` (effective viewer access scope), `silo.permission.decision` (route-level permission gates), or `silo.action.decision` (download/playback action gates) — the async `DecisionLogger` samples and persists the evaluation as one row here (`internal/policy/decisionlog.go`). "Policy Explain" is the admin-facing surface for reading those rows back and reconstructing *why* a specific access decision came out the way it did — not a live policy-evaluation or simulation endpoint (that's `internal/policy/simulate.go`, mounted elsewhere/not in scope here).

Both endpoints require the same organization-admin context described above (`requireV2OrganizationContext`), and both restrict results to `tenant.OrganizationID` — an org admin can only read their own organization's decision log, never another organization's or the platform-wide log.

Note the sampling caveat: whether a given real request produced a row here at all depends on server-side decision-log verbosity/sample-rate settings (`policy.SettingDecisionLogVerbosity`, default `"digest"`; `policy.SettingDecisionLogScopeSampleRate`, default 50%) and retention (`policy.SettingDecisionLogRetentionDays`, default 14 days, enforced by partition cleanup) — this log is a diagnostic sample, not a complete audit trail of every decision ever made.

#### GET /api/v2/admin/organization/policy-decisions
- Purpose: cursor-paginated list of policy decisions logged for the admin's organization, newest first.
- Auth required: organization-admin context.
- Query params: `cursor` (opaque, base64url-encoded `timestamp|id` pair from a previous page's `next_cursor`), `decision_name` (exact match against `silo.scope.decision`/`silo.permission.decision`/`silo.action.decision`), `limit` (integer 1–200; omitted/invalid-range → `422`, unset entirely defaults to 100 server-side). Note the handler exposes only these three filters from `policy.ListOptions` — `user_id`, `allowed`, and `from`/`to` time-range filtering exist in the repository/options struct but are **not** wired up to this endpoint's query string (only the internal, non-organization-scoped `List` path and other callers can use them).
- Response body (200):
```json
{
  "decisions": [
    {
      "id": 0,
      "timestamp": "RFC3339",
      "organization": { "id": "uuid", "membership_id": "uuid" },
      "subject": { "account_id": 0, "profile_id": "string" },
      "group": { "id": 0, "name": "string" },
      "library_ceiling": [0],
      "action": "string",
      "resource": {},
      "allowed": true,
      "reason_code": "string",
      "policy_versions": [ { "kind": "vendor | custom", "name": "string", "version": 0 } ]
    }
  ],
  "next_cursor": "string"
}
```
`next_cursor` (`omitempty`) is present only when more rows exist beyond `limit`. Field derivation, all from `explainPolicyDecision` reconstructing the response out of the stored `input_sample`/`result_sample` JSON snapshots plus the row's own columns — **not** a fixed, guaranteed-populated schema, since whether the underlying sample was captured at all (and at what verbosity) depends on the server-side decision-log settings described above:
  - `action` prefers `input.action`, falling back to the row's raw `decision_name` (e.g. `"silo.scope.decision"`) if the sampled input didn't carry an explicit action string.
  - `subject.account_id`/`subject.profile_id` prefer the sampled `input.user_id`/`input.profile_id`, falling back to the row's own `user_id`/`profile_id` columns.
  - `library_ceiling` reads `input.tenant_library_ids`, falling back to `input.library_ceiling`; empty/absent yields `[]`.
  - `resource` is the sampled `input.resource` object with every key matching `sensitiveDecisionKey` (case-insensitive substring match on `password`, `secret`, `token`, `credential`, `api_key`, `authorization`, `cookie`, `client_ip`, `device_id`, `session_id`, applied recursively into nested objects/arrays) replaced with the literal string `"[redacted]"` — this redaction happens server-side before the response is built, not just at display time. The same redaction is separately applied to any top-level `input` key by name, so a sensitive key can be redacted even if it never made it into `resource`.
  - `allowed`/`reason_code` prefer the sampled `result.allowed`/`result.reason_code`, falling back to the row's own `allowed` column (a `*bool`; `false`/zero-value if genuinely absent).
  - `policy_versions` always starts with one synthesized `{"kind":"vendor","version": <policy_generation>}` entry (the compiled vendor bundle's generation number), then appends any `kind:"custom"` entries found in the sampled `input.policy_versions` array (organization-authored policy overlays, if any were in effect for that decision).
- Errors: `422 validation_failed` (`limit` out of `1..200` range), `503 tenant_unavailable` (also covers a malformed `cursor` — decoded via `ErrDecisionNotFound`/generic decode failure, both folded into the same fallback response by `writeV2PolicyDecisionError`).
- Clients: none.

#### GET /api/v2/admin/organization/policy-decisions/{id}
- Purpose: fetches one policy decision row by numeric ID, scoped to the organization, for a detail/"explain" view.
- Auth required: organization-admin context.
- Request: none. `{id}` must be a positive integer or the route answers `404 not_found` before querying.
- Response body (200): `{ "decision": { "...": "PolicyDecisionExplanation, same shape as one item in the list above" } }`. Looked up with a `nil` timestamp hint, so when duplicate IDs exist across time-partitioned storage the newest row wins.
- Errors: `404 not_found` (no row with that ID in this organization — a foreign organization's decision ID is indistinguishable from a missing one, by design), `503 tenant_unavailable`.
- Clients: none.

---

## Platform Administration & Compatibility (native /api/v2/admin)

Source: `internal/api/router_v2.go` (`mountV2` / `mountV2Routes`, routes ~line 153-227),
`internal/api/handlers/v2_admin_platform.go`, `internal/api/handlers/v2_admin_compatibility.go`,
`internal/tenancy/types.go` + `internal/tenancy/admin_store.go`, `internal/compatapp/types.go`.

This is a native `/api/v2` surface — it is not part of the `/api/v1` Silo-compatible projection and
carries none of that surface's additive-only contract rules. Everything under `/api/v2/admin` is
gated first by `AdminContextMiddleware.Require` (the whole `/admin` subtree), and every route
documented here additionally re-checks its own authority inside the handler.

### Platform admin vs. organization admin — a real, enforced distinction

`/api/v2/admin` carries two structurally different kinds of administration, and the code
distinguishes them at more than one layer:

1. **Organization admin** (`/api/v2/admin/organization/*`, `/api/v2/admin/organization/people/*`
   — documented separately) — scoped to the single organization (tenant) named in the caller's
   admin-context token. Reachable by an organization's own `admin`-role member.
2. **Platform admin** (`/api/v2/admin/platform/*`, this document) — cross-organization. It can
   list, create, suspend, reactivate, and transfer ownership of *any* organization on the
   installation, and it administers the Compatibility Applications surface, which is also
   installation-wide rather than tenant-scoped.

The distinction is not just routing sugar. Getting an admin-context token at all requires calling
`POST /api/v2/admin/session` (`internal/api/handlers/v2_admin_session.go`) with a body naming a
`scope`:

- `{"scope":"platform"}` — only minted if `auth.PlatformAdminAuthorizer.IsPlatformAdmin` returns
  true for the caller's account, which in turn is just `account.Enabled && account.Role == "admin"`
  on the `users` table (`internal/auth/admin_context.go`). There is no separate "platform admin"
  flag distinct from the legacy account `role` column — every enabled account with `role: "admin"`
  qualifies. The minted token has `Scope: "platform"` and, deliberately, **no** organization,
  membership, policy-revision, or security-revision claims — `validateAdminContextClaims` rejects a
  platform-scope token that carries any of those.
- `{"scope":"organization","organization_id":"<uuid>"}` — resolves a specific membership; its
  `effective_authority` claim is `"organization_admin"` normally, or `"platform_admin"` if the same
  account also happens to be a platform admin. **This upgraded `effective_authority` string does
  not matter to the Platform/Compatibility handlers below** — they check `claims.Scope`, not
  `claims.EffectiveAuthority`. An organization-scoped token, even one minted for a platform admin
  with `effective_authority: "platform_admin"`, is rejected by every endpoint in this document with
  `403 insufficient_platform_authority`. A caller who is a platform admin and wants to use these
  routes must request a **separate** `scope: "platform"` admin-context session; the two session
  types are not interchangeable, and having both open at once (each is a distinct 15-minute JWT
  from `POST /admin/session`) is normal.

Both `V2AdminPlatformHandler` (`requirePlatform`/`requirePlatformMutation`) and
`V2AdminCompatibilityHandler` (`requirePlatformScope`/`requirePlatformMutation`) enforce this the
same way: pull `auth.AdminContextClaims` out of the request context (already validated once by
`AdminContextMiddleware.Require`, which itself re-checks `IsPlatformAdmin` against the *current*
account state on every request for platform-scope tokens) and require
`claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0`. Any other scope, or a missing
claims value (which cannot normally happen once `adminMW.Require` has run, but is defensive), gets
`403 insufficient_platform_authority`.

**Bearer token**: every request in this document carries `Authorization: Bearer <admin-context
token>` — the short-lived (max 15 minute) token from `POST /api/v2/admin/session`, not the
account's ordinary login access token.

### Common envelopes

**Validation error** (`422`), from `writeAdminValidation`:
```json
{
  "error": "validation_failed",
  "message": "Administrative request validation failed",
  "fields": { "<field_name>": "<human-readable reason>" }
}
```

**Generic error**, from `writeError` — used for `400`, `401`, `403`, `404`, `503`:
```json
{ "error": "<code>", "message": "<human-readable message>" }
```

**Optimistic-concurrency conflict** (`409`) — every mutation in this document takes an
`expected_revision` and fails this way if the record moved since the caller last read it:
```json
{
  "error": "authorization_state_changed",
  "message": "Authorization state changed; reload and retry",
  "current_revision": 0
}
```
The caller is expected to re-fetch (`GET .../organizations/{id}` or the equivalent list/detail
call), read the fresh revision, and retry with it as the new `expected_revision`.

**Request body limit / strict decoding**: every mutating Platform and Compatibility endpoint reads
its body through `decodeAdminPlatformJSON` — max 32 KiB, `DisallowUnknownFields()` (an unknown JSON
field is a `400 invalid_request`, not silently ignored), and a trailing-data check (a second JSON
value in the body, e.g. NDJSON, is also `400 invalid_request`).

---

### Platform

Handler: `handlers.V2AdminPlatformHandler`, built from a `*tenancy.Store` (satisfying
`V2AdminPlatformStore`) and an `auth.AccountCredentialVerifier` (satisfying
`AdminReauthenticationVerifier`, used only by ownership transfer). Mounted whenever `deps.DB != nil`
— i.e. in every real deployment with a database; there is no separate flag that disables it.

Only reachable by clients: **no** — confirmed by grepping both `vondel-android` and
`vondel-apple` for `admin/platform`, `platform/organizations`, and `platform/compatibility`; there
are zero matches in either repository. This surface is platform-operator / admin-console-only, not
called by any first-party native client.

#### Entitlement template administration

Source: `internal/api/handlers/entitlement_templates.go`. Every route below
requires a platform-scoped admin-context token. Mutation routes also require
the platform mutation authority enforced by the shared admin middleware.

These are **policy templates**, not the organization-library grants at
`/api/v2/admin/organization/entitlements/{folder_id}`. A template's library
selection narrows the libraries already available to the target; it does not
create a platform-library grant.

Policy wire shape:

```json
{
  "all_libraries": true,
  "library_ids": null,
  "playback_allowed": true,
  "max_streams": 3,
  "max_profiles": 5,
  "transcode_allowed": true,
  "max_transcodes": 1,
  "download_allowed": true,
  "download_transcode_allowed": true,
  "max_playback_quality": "1080p",
  "allowed_permissions": null,
  "requests_allowed": true
}
```

`all_libraries: true` or `library_ids: null` selects every available library
dynamically. An explicit array selects only those positive IDs.
`allowed_permissions: null` permits all access-group permissions; an explicit
array is an allowlist. Original and transcoded downloads are independent.

Template routes:

| Method and path | Contract |
| --- | --- |
| `GET /api/v2/admin/platform/entitlement-templates` | Returns `{ "templates": [...] }`. `?status=enabled` returns only enabled, non-archived templates; `?include_archived=false` omits archived history. |
| `POST /api/v2/admin/platform/entitlement-templates` | Creates revision 1 from `{key,name,enabled,policy}`; returns `201 {"template": ...}`. |
| `GET /api/v2/admin/platform/entitlement-templates/{key}` | Returns the latest revision, or the exact positive `?revision=N`. |
| `GET /api/v2/admin/platform/entitlement-templates/{key}/revisions` | Returns `{ "revisions": [...] }`; `/history` is an equivalent UI-oriented alias. |
| `POST /api/v2/admin/platform/entitlement-templates/{key}/revisions` | Appends a revision. Send `{expected_revision,name,enabled,policy}` or `{expected_revision,source_revision,name,enabled}` to copy historic policy as a new rollback revision. |
| `POST /api/v2/admin/platform/entitlement-templates/{key}/clone` | Creates a disabled template from `{source_revision,key,name}`; returns 201. |
| `POST /api/v2/admin/platform/entitlement-templates/{key}/archive` | Archives the latest revision using `{expected_revision}`. |

Template responses contain `key`, `name`, `revision`, `enabled`, `archived`,
`status` (`enabled`, `disabled`, or `archived`), `policy`, and `created_at`.
Revision conflicts return 409; invalid policies and revisions return 422.

Target detail and application routes:

| Target | Detail | Dry run | Apply |
| --- | --- | --- | --- |
| Organization | `GET /platform/organizations/{id}/entitlement` | `POST /platform/organizations/{id}/entitlement/dry-run` | `POST /platform/organizations/{id}/entitlement/apply` |
| Direct account | `GET /platform/accounts/{account_id}/entitlement` | `POST /platform/accounts/{account_id}/entitlement/dry-run` | `POST /platform/accounts/{account_id}/entitlement/apply` |

All paths in the table are relative to `/api/v2/admin`. The account routes are
also mounted at `/platform/users/{user_id}/entitlement...` as compatibility
aliases.

Dry-run request:

```json
{ "template_key": "standard", "template_revision": 1 }
```

The 200 response includes the full dry-run `result`, top-level
`template_key`, `template_revision`, `changed`, `changes`, `warnings`,
`expires_at`, and identical `confirmation_token` and legacy `dry_run_token`
aliases. The token expires after ten minutes and is bound to the actor, target,
exact revision, and a hash of the previewed state.

Apply request:

```json
{
  "template_key": "standard",
  "template_revision": 1,
  "confirmation_token": "signed-preview-token",
  "idempotency_key": "stable-client-command-id"
}
```

`dry_run_token` is accepted as a compatibility alias. The non-empty
idempotency key is limited to 200 characters. The 200 response includes
`organization_id`, optional `account_id`, `template_key`,
`template_revision`, `group_id`, `dry_run`, `changed`, optional
`profiles_moved`, the previous key/revision when present, and effective
`policy`. Replaying an identical command returns its stored response; reusing
the key for another payload conflicts. A stale or target-mismatched preview
must be discarded and repeated from dry-run.

Organization detail additionally returns `managed_default_group`,
`tenant_limits`, `library_ids`, `last_reconciled_at`, and
`audit_history_href`. Direct-account detail returns its organization/account
IDs, managed group, policy libraries, and last reconciliation. Organization
audit history is:

```text
GET /api/v2/admin/platform/organizations/{id}/entitlement/audit
```

and returns `{ "events": [...] }` in reverse chronological order.

#### `Organization` (wire shape used throughout)
```json
{
  "id": "uuid",
  "slug": "string",
  "name": "string",
  "status": "initializing | active | suspended",
  "owner_account_id": 0,
  "policy_revision": 0,
  "is_default": false
}
```
`owner_account_id` (`json:"owner_account_id,omitempty"`) is a pointer in Go and is omitted from the
JSON entirely when the organization has no owner yet (this happens during the `initializing`
status, before ownership resolution completes). `policy_revision` is the optimistic-concurrency
token for every organization-level mutation in this section (`expected_revision` in requests must
match it). `status` is one of exactly three values — `tenancy.OrganizationInitializing`,
`tenancy.OrganizationActive`, `tenancy.OrganizationSuspended` — there is no `deleted`/`archived`
status; suspension is the only reversible "off" state, done via `SetOrganizationStatus` (see
suspend/reactivate below), never a row delete.

`GET`-list and `GET`-single additionally wrap `Organization` in an `OrganizationSummary`, which adds
read-only counts (all derived server-side, not settable):
```json
{
  "id": "uuid", "slug": "string", "name": "string", "status": "active",
  "owner_account_id": 0, "policy_revision": 0, "is_default": false,
  "membership_count": 0,
  "active_membership_count": 0,
  "profile_count": 0,
  "library_count": 0,
  "entitlement_count": 0
}
```

#### `Membership` (wire shape used by the membership endpoints)
```json
{
  "id": "uuid",
  "organization_id": "uuid",
  "account_id": 0,
  "status": "invited | active | suspended",
  "legacy_role": "admin | user",
  "security_revision": 0
}
```
`security_revision` is the optimistic-concurrency token for membership mutations. List/get wrap
this as `MembershipSummary`, adding read-only `email` and `username` (looked up from the account,
not user-settable through this API):
```json
{
  "id": "uuid", "organization_id": "uuid", "account_id": 0,
  "status": "active", "legacy_role": "admin", "security_revision": 0,
  "email": "string", "username": "string"
}
```

`legacy_role` is literally the pre-tenancy binary role vocabulary (`"admin"`/`"user"`) carried
forward into the tenancy membership row — the name signals it is not the primary authorization
mechanism going forward, but it is still the field these endpoints read and write today.

---

#### `GET /api/v2/admin/platform/organizations`
- Purpose: list/search organizations across the whole installation (paginated, cursor-based).
- Auth required: platform admin-context token (`scope: "platform"`).
- Query params: `query` (substring/name search, optional), `status` (`initializing`|`active`|
  `suspended`, optional — anything else is `422` on the `status` field), `limit` (1-200, optional;
  server default applies when omitted — `422` on `limit` if out of range or non-numeric),
  `cursor` (opaque, from a previous page's `next_cursor`).
- Response body (200):
```json
{
  "organizations": [ /* OrganizationSummary, see above */ ],
  "next_cursor": "string (omitted when there is no further page)"
}
```
- Errors: `403 insufficient_platform_authority` (wrong/missing scope), `422 validation_failed`
  (`limit` or `status` out of range), `422 validation_failed` with `fields.cursor` = "is invalid"
  (malformed cursor — `tenancy.ErrInvalidCursor`), `503 tenant_unavailable` (store unavailable).
- Clients: none (platform/admin-console only — confirmed, see above).

#### `POST /api/v2/admin/platform/organizations`
- Purpose: create a new organization (tenant) and assign its initial owning account.
- Auth required: platform admin-context token.
- Request body:
```json
{
  "name": "string",
  "slug": "string",
  "owner_account_id": 0
}
```
`slug` must match `^[a-z0-9]+(?:-[a-z0-9]+)*$` (lowercase letters/digits, single hyphens, no
leading/trailing/double hyphen). `name` must be non-empty (after trimming). `owner_account_id` must
be `> 0`.
- Response body (201):
```json
{ "organization": { /* Organization, see above */ } }
```
- Errors: `422 validation_failed` (empty `name`, malformed `slug`, or `owner_account_id <= 0`, one
  entry per bad field), `409 organization_slug_conflict` (slug already taken —
  `tenancy.ErrOrganizationSlugConflict`), `422 validation_failed` with `fields.owner_account_id` =
  "must identify an enabled organization member" (`tenancy.ErrOwnerNotEligible` — the account exists
  but isn't eligible to own yet), `422 validation_failed` with `fields.account_id` = "must identify
  an existing account" (`tenancy.ErrAccountNotFound`), `403 insufficient_platform_authority`,
  `503 tenant_unavailable`.
- Clients: none.

#### `GET /api/v2/admin/platform/organizations/{id}`
- Purpose: fetch one organization's full administrative detail (the `OrganizationSummary` shape,
  including membership/profile/library/entitlement counts).
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID; a non-UUID value is `404 not_found`, not `400` — the
  handler treats a malformed ID the same as an unknown one so probing can't distinguish the two).
- Response body (200):
```json
{ "organization": { /* OrganizationSummary, see above */ } }
```
- Errors: `404 not_found` (unknown or malformed `id`), `403 insufficient_platform_authority`,
  `503 tenant_unavailable`.
- Clients: none.

#### `PATCH /api/v2/admin/platform/organizations/{id}`
- Purpose: rename an organization and/or change its slug.
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID).
- Request body:
```json
{
  "expected_revision": 0,
  "name": "string (optional)",
  "slug": "string (optional)"
}
```
`expected_revision` is required and must be `> 0`; it is checked against the organization's current
`policy_revision`. At least one of `name`/`slug` must be present. A present `name` must be
non-empty after trimming; a present `slug` must match the same slug pattern as create.
- Response body (200):
```json
{ "organization": { /* Organization, see above */ } }
```
- Errors: `422 validation_failed` (missing/invalid `expected_revision`, neither field present,
  empty `name`, or malformed `slug`), `404 not_found` (unknown organization), `409
  authorization_state_changed` (revision mismatch — response includes `current_revision`), `409
  organization_slug_conflict` (new slug already in use), `403 insufficient_platform_authority`,
  `503 tenant_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/organizations/{id}/suspend`
- Purpose: suspend an organization — the whole tenant's access is cut off (`tenancy.tenant_provisioning.go`
  treats `OrganizationSuspended` as the "frozen" state consulted elsewhere in the codebase, e.g.
  `TenantUserLimits.Frozen`).
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID).
- Request body:
```json
{ "expected_revision": 0 }
```
`expected_revision` required, `> 0`, checked against `policy_revision`.
- Response body (200):
```json
{ "organization": { /* Organization, see above; status: "suspended" */ } }
```
- Errors: `422 validation_failed` (`expected_revision` missing/non-positive), `404 not_found`,
  `409 authorization_state_changed`, `403 insufficient_platform_authority`, `503 tenant_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/organizations/{id}/reactivate`
- Purpose: the inverse of suspend — moves an organization's status back to `active`. Internally
  this is the exact same handler as suspend (`handleOrganizationStatus`), parameterized with
  `tenancy.OrganizationActive` instead of `tenancy.OrganizationSuspended`.
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID).
- Request body: `{ "expected_revision": 0 }` — same validation as suspend.
- Response body (200): `{ "organization": { /* Organization; status: "active" */ } }`.
- Errors: identical set to suspend, above.
- Clients: none.

#### `POST /api/v2/admin/platform/organizations/{id}/transfer-ownership`
- Purpose: reassign an organization's owning account to a different (already-member) account. The
  most sensitive mutation in this surface — it is the only endpoint here that additionally requires
  fresh password re-authentication of the calling platform admin, on top of the admin-context
  token.
- Auth required: platform admin-context token **and** password re-authentication (see below).
- Path params: `id` (organization UUID).
- Request body:
```json
{
  "expected_revision": 0,
  "owner_account_id": 0,
  "confirmed": true,
  "password": "string"
}
```
`expected_revision` required `> 0` (checked against `policy_revision`). `owner_account_id` required
`> 0`. `confirmed` must be `true` — a request with `confirmed: false` (or omitted) fails validation
before password is even checked, so a client cannot accidentally trigger this by a bare retry.
`password` is the calling platform admin's own current account password (not the target owner's) —
it is verified via `AdminReauthenticationVerifier.VerifyPassword(ctx, claims.AccountID, password)`.
An empty/missing `password`, or a handler built with no reauth verifier configured, both produce
`401 reauthentication_required` before the store is ever touched.
- Response body (200):
```json
{ "organization": { /* Organization, see above; owner_account_id now the new owner */ } }
```
- Errors: `422 validation_failed` (missing/invalid `expected_revision`, `owner_account_id`, or
  `confirmed` not true), `401 reauthentication_required` (empty password, or `VerifyPassword`
  returns `false`), `503 tenant_unavailable` (reauth verifier errored, or store unavailable),
  `404 not_found`, `409 authorization_state_changed`, `422 validation_failed` with
  `fields.owner_account_id` = "must identify an enabled organization member"
  (`tenancy.ErrOwnerNotEligible` — new owner isn't an eligible member, e.g. not `active`),
  `403 insufficient_platform_authority`.
- Clients: none.

#### `GET /api/v2/admin/platform/organizations/{id}/memberships`
- Purpose: list an organization's memberships (paginated, cursor-based).
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID).
- Query params: `limit` (1-200, optional), `cursor` (opaque, optional).
- Response body (200):
```json
{
  "memberships": [ /* MembershipSummary, see above */ ],
  "next_cursor": "string (omitted when there is no further page)"
}
```
- Errors: `422 validation_failed` (`limit` out of range), `422 validation_failed` with
  `fields.cursor` = "is invalid", `404 not_found` (unknown organization), `403
  insufficient_platform_authority`, `503 tenant_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/organizations/{id}/memberships`
- Purpose: add an account as a member of an organization.
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID).
- Request body:
```json
{
  "expected_revision": 0,
  "account_id": 0,
  "legacy_role": "admin | user",
  "status": "invited | active | suspended (optional)"
}
```
`expected_revision` required `> 0`, checked against the **organization's** `policy_revision` (not a
membership revision — membership creation is itself an organization-level mutation).
`account_id` required `> 0`. `legacy_role` must be exactly `"admin"` or `"user"`. `status` if
present must be one of the three membership statuses; if omitted the store defaults it to
`active` (`tenancy.MembershipActive`, per `admin_store.go`).
- Response body (201):
```json
{
  "membership": { /* Membership, see above */ },
  "organization": { /* Organization, see above — its policy_revision has advanced */ }
}
```
- Errors: `422 validation_failed` (bad `expected_revision`, `account_id`, or `legacy_role`, or
  invalid `status`), `404 not_found` (unknown organization), `409 authorization_state_changed`,
  `422 validation_failed` with `fields.account_id` = "must identify an existing account"
  (`tenancy.ErrAccountNotFound`), `409 membership_conflict` (`tenancy.ErrMembershipConflict` — e.g.
  the account is already a member, or the requested state conflicts with protected organization
  state), `403 insufficient_platform_authority`, `503 tenant_unavailable`.
- Clients: none.

#### `PATCH /api/v2/admin/platform/organizations/{id}/memberships/{membership_id}`
- Purpose: change a membership's role and/or status (e.g. promote to admin, suspend a member).
- Auth required: platform admin-context token.
- Path params: `id` (organization UUID), `membership_id` (membership UUID).
- Request body:
```json
{
  "expected_revision": 0,
  "legacy_role": "admin | user (optional)",
  "status": "invited | active | suspended (optional)"
}
```
`expected_revision` required `> 0`, checked against the **membership's** `security_revision` this
time (unlike creation, which checks the organization's `policy_revision`). At least one of
`legacy_role`/`status` must be present. Present `legacy_role` must be `"admin"` or `"user"`. Present
`status` must be one of the three membership statuses.

Server-side, demoting or suspending the organization's last active admin/owner membership is
rejected with `tenancy.ErrMembershipConflict` — the store enforces that an organization is never
left without an eligible admin via this path.
- Response body (200):
```json
{
  "membership": { /* Membership, see above */ },
  "organization": { /* Organization, see above */ }
}
```
- Errors: `422 validation_failed` (bad `expected_revision`, neither field present, invalid
  `legacy_role`/`status`), `404 not_found` (unknown organization or membership — both map to
  `tenancy.ErrMembershipNotFound`/`ErrOrganizationNotFound`), `409 authorization_state_changed`
  (the conflict response's `current_revision` here is the membership's `security_revision`,
  looked up fresh — see `writeStoreError`'s membership branch), `409 membership_conflict`
  (would leave the organization without an eligible admin, or otherwise conflicts with protected
  state), `403 insufficient_platform_authority`, `503 tenant_unavailable`.
- Clients: none.

---

### Compatibility Applications

Handler: `handlers.V2AdminCompatibilityHandler`, built from `deps.CompatApplications`
(`handlers.CompatibilityApplicationService`) and `deps.PublicURL`. Mounted only when
`deps.CompatApplications != nil`.

**What this actually is.** A "compatibility application" is *not* a Radarr/Sonarr-style third-party
metadata integration, and it is not a plugin in the `silo-plugin-sdk` sense. Per
`internal/compatapp/types.go`'s package doc: it is one of two specific, reviewed **companion
services** — `vondel-jellyfin` and `vondel-audiobookshelf` (`compatapp.KindJellyfin` /
`compatapp.KindAudiobookshelf`, run as separate Docker containers alongside the main server, per
the `docker-compose.<kind>.yml` overlay files `operatorCommands` references) — that speak a
*third-party client's native wire protocol* (Jellyfin's API, Audiobookshelf's API) on the front end
so existing Jellyfin/Audiobookshelf apps can point at a Vondel-managed library, while talking to
Vondel itself on the back end through a separate, closed, versioned **private compatibility API**
(`contracts/compat/v1/openapi.yaml`) authenticated with its own enrollment/credential system (this
package, `internal/compatapp`). This admin surface is the control plane for that trust
relationship: which companion instances exist, what capability slices of the private API each was
granted, and whether each is currently enabled, healthy, or revoked. It never touches an
application/media table directly and never talks to Docker — per the router's own mount comment,
"the handler consumes the lifecycle service; it never writes application tables and never touches
Docker," and per `operatorCommands`' comment, Vondel only *displays* the exact Docker/Compose
commands an operator (or their deployment controller) should run — install/update/rollback/remove —
it has no Docker socket and performs no container mutation itself.

**Mounted by default in real deployments.** `cmd/silo/main.go` wires
`deps.CompatApplications = handlers.NewCompatApplicationService(compatapp.NewService(pool))`
whenever the database connection pool (`pool`) is non-nil — i.e. in essentially every running
deployment with a database configured, the same condition most of the rest of the admin API depends
on. It is unmounted (all six routes below 404 through the generic `/admin/*` catch-all) only when
the server has no database pool at all — a degraded/no-DB boot mode, not a normal operational
toggle.

Only reachable by clients: **no** — same grep as Platform, above, found zero references to
`platform/compatibility` in either `vondel-android` or `vondel-apple`.

**Two reviewed kinds only**: `"jellyfin"` and `"audiobookshelf"` (`compatibilityKinds` map in the
handler, mirroring `compatapp.knownKinds`). Any other value is rejected before the lifecycle
service is even called.

**Six reviewed capabilities only** (`compatapp.Capability`, granted per enrollment, not
enumerated/validated by the admin handler itself beyond passing them through — the lifecycle
service enforces the closed set and rejects an unknown capability with
`ErrCompatibilityCapabilityUnknown`): `identity` (credential exchange, device trust, profile
discovery, PIN verification, profile switching), `catalog` (libraries/browse/search/detail/
metadata/artwork), `state` (progress, watched state, favorites, bookmarks, collections, playlists,
downloads), `playback` (playback planning, stream auth, cancellation, recovery, session/device
reporting), `livetv` (channels, guide, tuner availability, stream auth, DVR rules, recordings),
`events` (subject-filtered events with resumable cursors). A capability named `"service"`
(enrollment/renewal/health) is implicit to every enrolled application and deliberately not
independently grantable.

#### `CompatibilityApplication` (wire shape used throughout)
```json
{
  "instance_id": "string",
  "kind": "jellyfin | audiobookshelf",
  "state": "string",
  "enabled": true,
  "revoked": false,
  "healthy": true,
  "version": "string",
  "image_digest": "string",
  "api_range": { "min": "string", "max": "string" },
  "last_contact_at": "RFC3339 timestamp or null",
  "active_sessions": 0,
  "capabilities": ["identity", "catalog", "..."],
  "revision": 0
}
```
`revision` is the optimistic-concurrency token every mutating endpoint below takes as
`expected_revision`. The list/detail views returned by this handler additionally wrap every
application as:
```json
{
  "instance_id": "string", "kind": "jellyfin", "state": "string",
  "enabled": true, "revoked": false, "healthy": true,
  "version": "string", "image_digest": "string",
  "api_range": { "min": "string", "max": "string" },
  "last_contact_at": null, "active_sessions": 0,
  "capabilities": ["identity"], "revision": 0,
  "canonical_url": "string",
  "commands": {
    "install": "docker compose -f docker-compose.yml -f docker-compose.<kind>.yml up -d",
    "update": "docker compose ... pull vondel-<kind> && docker compose ... up -d vondel-<kind>",
    "rollback": "Pin the previous image digest for vondel-<kind> in docker-compose.<kind>.yml, then run: docker compose ... up -d vondel-<kind>",
    "remove": "docker compose ... rm -sf vondel-<kind>  # discard its disposable protocol state: docker volume ls -q --filter name='_vondel-<kind>-state$' | xargs -r docker volume rm"
  }
}
```
`canonical_url` is `{PublicURL}/audiobookshelf` for the Audiobookshelf companion, or bare
`{PublicURL}/` for Jellyfin (falls back to `https://vondel.example` if `PublicURL` is unset).
`commands` is purely informational text for an operator to copy/paste or for a deployment
controller to run — the server never executes any of it.

---

#### `GET /api/v2/admin/platform/compatibility/applications`
- Purpose: list every enrolled compatibility application instance and its current lifecycle state.
- Auth required: platform admin-context token (`scope: "platform"` — same check as the Platform
  section, `requirePlatformScope`, independent of the Platform handler's own check).
- Response body (200):
```json
{ "applications": [ /* compatibilityApplicationView, see above */ ] }
```
- Errors: `403 insufficient_platform_authority`, `503 compatibility_admin_unavailable` (lifecycle
  service errored or the handler/service is nil).
- Clients: none.

#### `POST /api/v2/admin/platform/compatibility/enrollments`
- Purpose: mint a one-time enrollment secret for a new companion instance, carrying a reviewed
  capability grant. The companion redeems this secret exactly once, at first boot, to obtain its
  permanent application identity and a renewable service credential.
- Auth required: platform admin-context token.
- Request body:
```json
{
  "kind": "jellyfin | audiobookshelf",
  "capabilities": ["identity", "catalog", "..."]
}
```
`kind` must be exactly `"jellyfin"` or `"audiobookshelf"`; anything else is rejected before the
service is called. `capabilities` is passed through to the lifecycle service, which validates each
name against the closed six-capability set (see above) and rejects an empty or unknown list.
- Response body (201) — **the only time the raw secret is ever returned**; response carries
  `Cache-Control: no-store`:
```json
{
  "enrollment": {
    "kind": "jellyfin",
    "secret": "string (one-time, never retrievable again)",
    "expires_at": "RFC3339 timestamp"
  }
}
```
The secret expires 15 minutes after issuance (`compatapp.EnrollmentTTL`) if never redeemed.
- Errors: `422 validation_failed` with `fields.kind` = "must be jellyfin or audiobookshelf" (bad or
  missing `kind`), `422 validation_failed` with `fields.capabilities` = "must name reviewed
  capabilities" (`ErrCompatibilityCapabilityUnknown` — an unrecognized or empty capability list),
  `403 insufficient_platform_authority`, `503 compatibility_admin_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/compatibility/applications/{instance_id}/enable`
- Purpose: turn an application instance on (it starts/continues serving its private-API traffic).
- Auth required: platform admin-context token.
- Path params: `instance_id` (the companion's self-reported instance identifier — a string, not a
  UUID path param the router validates; an unknown value reaches the service and comes back as
  `ErrCompatibilityApplicationNotFound`).
- Request body:
```json
{ "expected_revision": 0 }
```
Required, `> 0`, checked against the application's current `revision`.
- Response body (200):
```json
{ "application": { /* CompatibilityApplication, see above; enabled: true */ } }
```
- Errors: `422 validation_failed` with `fields.expected_revision` = "must be a positive revision",
  `404 not_found` (unknown `instance_id`), `409 authorization_state_changed` (revision mismatch;
  response includes `current_revision`), `403 insufficient_platform_authority`,
  `503 compatibility_admin_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/compatibility/applications/{instance_id}/disable`
- Purpose: turn an application instance off — same handler as enable
  (`handleSetEnabled`), parameterized `enabled=false`. A disabled application's requests against the
  private compatibility API are refused (`compatapp.ErrApplicationDisabled`), but its trust record
  and credential remain intact — this is reversible via `enable`, unlike revoke.
- Auth required: platform admin-context token.
- Path params: `instance_id`.
- Request body: `{ "expected_revision": 0 }` — same validation as enable.
- Response body (200): `{ "application": { /* CompatibilityApplication; enabled: false */ } }`.
- Errors: identical set to enable, above.
- Clients: none.

#### `POST /api/v2/admin/platform/compatibility/applications/{instance_id}/rotate-credential`
- Purpose: force-issue a fresh service credential for an application instance, invalidating its
  previous one. Sets `credential_rotated_at` on the underlying application record (companion
  self-renewal on normal use does not set this field — only an administrator-forced rotation does).
- Auth required: platform admin-context token.
- Path params: `instance_id`.
- Request body:
```json
{ "expected_revision": 0 }
```
Required, `> 0`, checked against `revision`.
- Response body (200) — **the only time the raw rotated secret is ever returned**; response
  carries `Cache-Control: no-store`:
```json
{
  "credential": {
    "secret": "string (one-time, never retrievable again)",
    "expires_at": "RFC3339 timestamp"
  },
  "application": { /* CompatibilityApplication, see above */ }
}
```
The credential is valid 15 minutes past its last successful use (`compatapp.CredentialTTL`) and
renews on each authenticated call; forced rotation immediately invalidates whatever credential the
running companion instance was using, so the operator is expected to redeploy/restart the companion
with the new secret promptly.
- Errors: `422 validation_failed` with `fields.expected_revision` = "must be a positive revision",
  `404 not_found`, `409 authorization_state_changed`, `403 insufficient_platform_authority`,
  `503 compatibility_admin_unavailable`.
- Clients: none.

#### `POST /api/v2/admin/platform/compatibility/applications/{instance_id}/revoke`
- Purpose: permanently withdraw trust from an application instance. Unlike disable, this is
  terminal — a revoked application cannot be re-enabled; the operator must enroll a new instance
  (a fresh `POST .../enrollments` + companion re-registration) to reconnect. The most destructive
  Compatibility endpoint, which is why it requires explicit confirmation in the body (though, unlike
  ownership transfer in the Platform section, it does **not** require password re-authentication).
- Auth required: platform admin-context token.
- Path params: `instance_id`.
- Request body:
```json
{
  "expected_revision": 0,
  "confirmed": true
}
```
`expected_revision` required `> 0`. `confirmed` must be `true` — omitted or `false` fails
validation before the lifecycle service is called.
- Response body (200):
```json
{ "application": { /* CompatibilityApplication, see above; revoked: true, enabled: false */ } }
```
- Errors: `422 validation_failed` (`expected_revision` missing/non-positive, and/or `confirmed` not
  `true` — both can appear together in `fields`), `404 not_found`, `409
  authorization_state_changed`, `403 insufficient_platform_authority`,
  `503 compatibility_admin_unavailable`. A revoke retried against an application that is *already*
  revoked (`compatapp.ErrApplicationRevoked`, from `internal/api/handlers/compatapp_adapter.go`'s
  `mapCompatApplicationError`) is deliberately folded into the same `409
  authorization_state_changed` conflict channel as a plain revision mismatch, carrying the
  application's current revision — "reload and retry" is the correct instruction either way,
  since the reloaded row shows `revoked: true` and the control is simply gone. It is not
  distinguished from a stale-revision conflict by error code; a client must inspect the reloaded
  application body (`revoked`) to tell the two apart.
- Clients: none.

---

### Audit trail

Every mutation in both sections above requires an `AdminMutationActor` in the request context
(`adminMutationRequestContext`, installed by the handler from the validated
`auth.AdminContextClaims` before calling into the store/service): `AccountID` (the calling admin),
`PlatformRole: "platform_admin"`, `AuthorityContext: "platform"`, and `RequestID` (from chi's
request-ID middleware, falling back to an `X-Request-ID` header). The `tenancy` store's
`adminMutationActor` helper rejects a call with `tenancy.ErrAdminAuditRequired` if any of those
three fixed fields don't match exactly — every Platform mutation is guaranteed to carry a
consistent, attributable actor into `internal/tenancy`'s audit log
(`recordOrganizationAudit`/`recordMembershipAudit`). The `CompatibilityApplicationService`
interface's doc comment states implementations should read the same actor via
`tenancy.AdminMutationActorFromContext`, though the compatibility handler itself does not enforce
that the way the tenancy store enforces it for Platform routes.

---

## People Administration (native /api/v2/admin/organization/people)

Source of truth read directly from `vondel-server` (`~/projects/vondel/vondel-server`):
route table `internal/api/router_v2.go` (`mountV2Routes`, people block ~lines 208-221),
handler `internal/api/handlers/v2_admin_people.go` (`V2AdminPeopleHandler`), service/store
`internal/adminpeople/service.go` (`adminpeople.Service`), and durable-job worker
`internal/adminpeople/worker.go` (`adminpeople.Worker`). Cross-checked against
`~/projects/vondel-android` and `~/projects/vondel-apple` — see **Clients** below.

This surface is **organization-admin-scoped bulk people management**, not household/self-service
profile management (that's the `/api/v1` `PUT /profiles/{id}` flow, documented separately —
see the note under `HandleUpdateProfile` for the precise difference). "People" here means the
combination of an organization membership (one row per account per organization,
`organization_memberships`) plus that account's household profiles (`user_profiles`) — an
org admin lists/searches/paginates across every member account and profile in their
organization, and can act on many of them at once through an asynchronous, durably-tracked
bulk-job pipeline.

### Auth (applies to every endpoint below)

All seven routes are mounted under `r.Route("/admin", func(r chi.Router) { r.Use(adminMW.Require) ... })`
inside `mountV2Routes`, then further gated per-handler by `V2AdminPeopleHandler.requireOrganization`
(`internal/api/handlers/v2_admin_people.go:215`). Concretely:

- **Bearer token**: a normal account bearer token first exchanged via `POST /api/v2/admin/session`
  for a short-lived (max 15 minute, `auth.AdminContextTokenLifetime`) signed **admin context
  token** (`internal/auth/admin_context.go`). `adminMW.Require` resolves that token into
  `middleware.AdminContextClaims` and a `tenancy.Context` on the request.
- `requireOrganization` then additionally requires: `claims.Scope == auth.AdminScopeOrganization`
  (i.e. the admin context was minted for a specific organization, not a platform-wide session),
  `claims.AccountID > 0`, `claims.OrganizationID != uuid.Nil`, and that the claims' `AccountID`,
  `OrganizationID`, and `MembershipID` all match the resolved `tenancy.Context` exactly. A
  platform-scoped admin context (`AdminScopePlatform`) is rejected here — `403
  insufficient_organization_authority` — even though `EffectiveAuthority` may separately be
  `"platform_admin"` (see **Actor authority** below, which is a distinct check applied only on
  mutations).
- Effective admin authority is `organization_admin` in the overwhelming common case: an
  organization-context admin token's `EffectiveAuthority` is `"organization_admin"` unless the
  underlying account is also a platform admin, in which case `AdminContextMiddleware` sets it to
  `"platform_admin"` (see `internal/api/middleware/admin_context.go:91`). Either authority passes
  `requireOrganization`; the distinction only resurfaces inside mutation actor
  revalidation (below).
- Missing/invalid auth → `403 {"error":"insufficient_organization_authority","message":"Organization administrator authority required"}`.
- If the people service could not be constructed at all (no DB/config wired) → `503
  {"error":"tenant_unavailable","message":"Tenant administration is unavailable"}` and the whole
  `/organization/people` subtree is unmounted (404 from the catch-all instead).

### Error envelope conventions

Handler errors funnel through `V2AdminPeopleHandler.writeError` (`v2_admin_people.go:229`), which
maps `adminpeople` sentinel errors to HTTP status/JSON:

| Go error | HTTP | Body |
|---|---|---|
| `adminpeople.ErrNotFound`, `adminpeople.ErrInvalidSelection` | 404 | `{"error":"not_found","message":"Administrative resource not found"}` |
| `adminpeople.ErrSelectionExpired` | 409 | `{"error":"selection_expired","message":"The immutable selection expired; create a new selection"}` |
| `adminpeople.ErrAuthorizationStateChanged` | 409 | `{"error":"authorization_state_changed","message":"Authorization state changed; reload and retry","current_revision":<int64>}` — `current_revision` is refetched live (the target person's `security_revision`) only when the request carried an `account_id`; otherwise omitted (0) |
| `adminpeople.ErrInvalidCursor` | 422 | `{"error":"validation_failed","message":"Administrative request validation failed","fields":{"cursor":"is invalid"}}` |
| `adminpeople.ErrInvalidFilter` | 422 | same shape, `fields:{"filters":"contain invalid values"}` |
| `adminpeople.ErrInvalidBulkAction` | 422 | same shape, `fields:{"request":"contains an invalid people mutation"}` |
| anything else (DB errors, service unavailable) | 503 | `{"error":"tenant_unavailable","message":"Tenant administration is unavailable"}` |

`writeAdminValidation` (used directly by the handler for request-shape errors before the service
is even called, e.g. bad `limit`/`group_id` query params or a missing `expected_revision`) uses
the same `422 validation_failed` envelope.

---

### Directory: list & inspect

#### GET /api/v2/admin/organization/people

- **Purpose**: paginated, filterable, sortable listing of every person (account + membership +
  household profiles) in the caller's organization. This is the browse/search surface the other
  endpoints (selections, per-account fetch) build on.
- **Auth**: organization admin context (see above).
- **Query parameters** (all optional; parsed by `adminPeopleFilterFromQuery`):
  - `query` — free-text substring match (case-insensitive), matched against the account's
    email, username, *or* any of the account's profile names (`ILIKE '%query%'`).
  - `status` — repeatable and/or comma-separated; each value is a `tenancy.MembershipStatus`:
    `invited` | `active` | `suspended`. Filters to memberships in the given status set. An
    unrecognized status is silently dropped only if empty after trim; a genuinely invalid
    (non-empty, unrecognized) value is caught later by the service as `ErrInvalidFilter` — in
    practice you should stick to the three known values.
  - `group_id` — repeatable and/or comma-separated; positive integers. Filters to accounts that
    have at least one profile whose `access_group_id` is in the set. Non-positive/unparseable →
    `422 validation_failed {"group_id":"must contain positive identifiers"}`.
  - `active_since` — RFC3339 timestamp. Filters to accounts whose membership *or* any profile was
    updated at/after this timestamp. Malformed → `422 validation_failed {"active_since":"must be RFC3339"}`.
  - `sort` — one of `name` (default), `email`, `recent_activity`. Anything else fails filter
    validation service-side (`ErrInvalidFilter` → `422 validation_failed {"filters":"contain invalid values"}`).
  - `limit` — 1–200, default 50. Out of range → `422 validation_failed {"limit":"must be between 1 and 200"}`.
  - `cursor` — opaque HMAC-signed cursor from a previous page's `next_cursor` (see below); must
    match the *exact same* canonicalized filter+sort or it's rejected as `ErrInvalidCursor` (404
    `not_found` is *not* returned here — cursor mismatch maps to `422 validation_failed
    {"cursor":"is invalid"}`).
- **Response body** (200):
```go
type Page struct {
    Items            []PersonSummary `json:"items"`
    NextCursor       string          `json:"next_cursor,omitempty"` // present iff another page exists
    ApproximateTotal int64           `json:"approximate_total"`     // COUNT(*) matching the filter, independent of pagination
}

type PersonSummary struct {
    OrganizationID   uuid.UUID                `json:"organization_id"`
    AccountID        int                      `json:"account_id"`
    Email            string                   `json:"email"`
    DisplayName      string                   `json:"display_name"`   // username, falling back to email
    MembershipID     uuid.UUID                `json:"membership_id"`
    MembershipStatus string                   `json:"membership_status"` // "invited" | "active" | "suspended"
    LegacyRole       string                   `json:"legacy_role"`       // organization_memberships.legacy_role, e.g. "admin"/"member"
    SecurityRevision int64                    `json:"security_revision"` // optimistic-concurrency token — see UpdateMembership/UpdateProfile
    LastActivity     time.Time                `json:"last_activity"`     // GREATEST(membership.updated_at, max(profile.updated_at))
    Profiles         []ProfileSummary         `json:"profiles"`
}

type ProfileSummary struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    GroupID   int       `json:"group_id"`
    GroupName string    `json:"group_name"`
    UpdatedAt time.Time `json:"updated_at"`
}
```
  `items` is capped by `limit`; sort/pagination is keyset (not offset) — ordered by
  `(lower(name_or_email), account_id)` ascending for `name`/`email` sort, or
  `(last_activity DESC, account_id DESC)` for `recent_activity`. The next cursor encodes the
  canonicalized filter (so a client can't swap filters mid-pagination) plus the last row's sort
  key, HMAC-signed with a key derived from `Config.Auth.JWTSecret` (`sha256(secret)` — see
  `adminpeople.NewService`).
- **Errors**: `422 validation_failed` (bad query params/filter/cursor), `503 tenant_unavailable`.
- **Clients**: none. `grep -rl "organization/people"` over `vondel-android` and `vondel-apple`
  finds no matches in either repo — this endpoint (and the whole People Administration surface)
  is not called by either native client. Presumed admin-console/web-only; no admin web UI source
  was inspected as part of this pass.

#### GET /api/v2/admin/organization/people/{account_id}

- **Purpose**: fetch a single person's full summary by account ID — the same shape as one item
  of the list response, used e.g. to refresh a detail view after a mutation.
- **Auth**: organization admin context.
- **Path params**: `account_id` — positive integer; non-numeric or `<= 0` → `404 not_found`
  (`adminPeoplePathAccount`).
- **Request body**: none.
- **Response body** (200):
```go
struct {
    Person adminpeople.PersonSummary `json:"person"`
}
```
  (same `PersonSummary` shape as above). Internally this is implemented as `List` with
  `Filter{Limit: 1}` plus an `account_id =` condition, so it's subject to organization scoping
  the same way — an account in a different organization, or with no membership row in this
  organization, resolves to zero rows and thus `ErrNotFound`.
- **Errors**: `404 not_found` (no such account/membership in this organization), `503 tenant_unavailable`.
- **Clients**: none (see above).

---

### Selections & Bulk Jobs

This is the non-obvious part of the surface, and it is a deliberate two-phase design: you first
**snapshot** a target set into an immutable, server-stored "selection," then separately submit a
**bulk action** referencing that selection, which is executed asynchronously by a background
worker rather than inline in the request. There is no way to submit a bulk action with an inline
filter — `POST /bulk-jobs` always requires a prior `selection_token`; `BulkAction.SelectionToken`
is validated non-empty and any action targeting a selection that fails to parse/load fails the
whole call (`adminpeople.ErrInvalidBulkAction` / `ErrInvalidSelection`).

#### What a "selection" actually is

`HandleCreateSelection` → `Service.CreateSelection` (`internal/adminpeople/service.go:506`):

1. Takes the same `Filter` shape as the list endpoint (query/status/group_ids/active_since/sort —
   **no `limit`/`cursor`**, since a selection always snapshots the *entire* matching set, capped
   at `maximumSelectionTargets = 10000` rows).
2. Opens a `pgx.RepeatableRead` transaction and, in that one transaction, both (a) queries up to
   10,001 matching account rows *with a per-row snapshot* of membership id/status/security
   revision, the account's `access_policy_revision`, and every one of that account's profiles
   (`id`, `access_group_id`, `updated_at`) as a JSON array, and (b) counts the *true* total
   matching the filter (which can exceed 10,000).
3. Persists one row to `admin_people_selections`: a random `uuid.New()` reference id, the
   canonicalized filter (JSON), `snapshot_at` (wall-clock time of the query, for audit — the
   comment in source is explicit that this is *not* used as a query cutoff, to avoid
   app/DB clock-skew races), the captured `account_ids` array, `matched_count`, `excluded_count`
   (`= true_total - min(true_total, 10000)`), an `expires_at` 15 minutes out (`selectionTTL`),
   and the full per-target JSON snapshot (`targets`).
4. Returns an HMAC-signed opaque token (`base64url(uuid_bytes) + "." + base64url(hmac)`, keyed
   off the same `sha256(JWTSecret)` key as pagination cursors) that encodes only the selection's
   UUID — the actual membership/profile data lives server-side, not in the token.

So: **a selection is a materialized, immutable, time-boxed (15-minute) snapshot of the exact set
of accounts (with their per-account/per-profile revision fingerprints at snapshot time) that
matched a filter**, capped at 10,000 accounts, referenced afterward by an opaque signed token —
not a saved/reusable named query, and not simply "the current live result of this filter." The
snapshot's per-row revisions are what the bulk executor later re-checks per-target to detect
concurrent modification (see below), which is the entire point of snapshotting rather than
re-querying at execution time.

##### POST /api/v2/admin/organization/people/selections

- **Purpose**: create the immutable selection described above.
- **Auth**: organization admin context.
- **Request body**:
```go
type adminPeopleFilterRequest struct {
    Query       string                     `json:"query"`
    Status      []tenancy.MembershipStatus `json:"status"`
    GroupIDs    []int                      `json:"group_ids"`
    ActiveSince *time.Time                 `json:"active_since"`
    Sort        string                     `json:"sort"`
}
```
  Same semantics as the list-endpoint filter (see above), minus pagination fields. `sort` still
  affects nothing observable about the selection itself (selections aren't paginated) but is
  part of the canonicalized filter identity used for idempotency (see bulk-job dedup below), so
  varying it produces a logically distinct selection even over the same query/status/group_ids.
- **Response body** (201):
```go
type Selection struct {
    Token     string    `json:"token"`      // opaque, pass as BulkAction.SelectionToken
    Matched   int64     `json:"matched"`    // count of accounts actually captured (<= 10000)
    Excluded  int64     `json:"excluded"`   // true_total - matched, i.e. rows beyond the 10000 cap
    ExpiresAt time.Time `json:"expires_at"` // snapshot_at + 15 minutes
}
```
- **Errors**: `422 validation_failed {"filters":"contain invalid values"}` (bad sort/status/group
  ids), `503 tenant_unavailable`.
- **Clients**: none (see above).

#### What a "bulk job" does, and its kinds

`BulkAction` (the `POST /bulk-jobs` request body) is:
```go
type BulkAction struct {
    SelectionToken string `json:"selection_token"` // required, references a live (unexpired) Selection
    Kind           string `json:"kind"`            // one of the three kinds below
    GroupID        *int   `json:"group_id,omitempty"` // required for assign_group, forbidden otherwise
}
```
Three kinds, validated by `validateBulkAction` (`service.go:708`):

- `"assign_group"` (`adminpeople.BulkAssignGroup`) — requires `group_id` (positive int, must
  exist as an `access_groups` row in the caller's organization or the whole job fails to enqueue
  with `ErrNotFound`). Effect **per target account**: sets `access_group_id = group_id` on
  *every one of that account's household profiles* that isn't already in that group. Accounts
  with zero profiles are skipped with reason `no_profiles`; accounts whose every profile is
  already in the target group are skipped with reason `already_applied`.
- `"suspend_memberships"` (`adminpeople.BulkSuspendMemberships`) — `group_id` must be absent.
  Sets the account's organization membership `status = 'suspended'` (and bumps both the
  membership's `security_revision` and the account's `access_policy_revision`) unless already
  suspended (`already_applied`) or the account is the organization's owner
  (`o.owner_account_id = m.account_id` → skipped with reason `protected_owner` — the owner can
  never be bulk-suspended).
- `"reactivate_memberships"` (`adminpeople.BulkReactivateMemberships`) — mirror of the above,
  sets `status = 'active'`; no owner protection (reactivating is always safe), skipped with
  `already_applied` if already active.

Anything else in `kind`, or `group_id` present on a non-`assign_group` kind, or missing/absent
`group_id` on `assign_group`, or an empty `selection_token` → `422 validation_failed
{"request":"contains an invalid people mutation"}`.

##### The actor: who is allowed to run a bulk job, and how it's re-validated

The caller's admin-context claims are captured into a `MutationActor` (`AccountID`, `Authority`
— `"organization_admin"` or `"platform_admin"`, `MembershipID`, `SecurityRevision`,
`PolicyRevision`, `RequestID`) at enqueue time via `adminPeopleMutationContext`
(`v2_admin_people.go:271`), snapshotted onto the job row (`admin_people_bulk_jobs`), and
re-validated (`resolveBulkActorSnapshot` at enqueue, `revalidateBulkActor` at every batch
execution) against live `organizations.policy_revision`, and either the account's global
`role='admin'`/`enabled` (platform-admin actors) or the caller's own organization membership
status/role/`security_revision` (organization-admin actors). Any mismatch — the org's policy
revision moved, the actor's own membership was itself suspended/demoted, or (platform admin) the
actor's account got disabled — fails the whole batch with `ErrAuthorizationStateChanged` and
marks the job `failed`. This means a bulk job can itself be aborted mid-flight if the *operator's
own* authority changed since the job was queued, independent of what happens to individual
targets.

##### POST /api/v2/admin/organization/people/bulk-jobs

- **Purpose**: enqueue (not execute inline) a bulk action against a previously created selection.
- **Auth**: organization admin context.
- **Request body**: `BulkAction` (above).
- **Behavior**:
  - Idempotency: the job is keyed by `(organization_id, selection_reference, action_key)` where
    `action_key = kind + ":" + group_id_or_empty`, guarded by a Postgres advisory transaction
    lock so concurrent duplicate submissions collapse onto one job. If a matching job already
    exists, the handler returns *that job's current status* (via `GetBulkJob`) rather than
    creating a second one, including if it has already completed.
  - Selection must not be expired: `!now.Before(selection.expires_at)` → `409 selection_expired`.
  - For `assign_group`, the target group must exist in the org, or `404 not_found`.
  - On success, persists an `admin_jobs` row (`status='queued'`, `job_type='organization_people_bulk'`)
    plus an `admin_people_bulk_jobs` row (the actor snapshot + selection reference + action) plus
    one `admin_people_bulk_targets` row per selection member (via `COPY`, so it's O(1) round
    trips for up to 10,000 targets), and an audit event (`people.bulk_job_created`). It returns
    immediately with `status: "queued"` — **the mutations have not happened yet**.
  - After the DB transaction commits, the handler calls `h.worker.Wake()` if a worker was wired
    (`deps.AdminPeopleWorker`) — a best-effort nudge to the background worker's wake channel to
    process sooner than its 30-second recovery-poll interval; the job runs regardless (the
    worker also polls periodically), this is purely a latency optimization and its absence
    (`worker == nil`, e.g. in a deployment without the worker enabled) does not stop the job from
    eventually being picked up **once a worker process does poll it** — a deployment where no
    `adminpeople.Worker` is running at all would leave jobs stuck `queued` indefinitely.
- **Response body** (201):
```go
type BulkResult struct {
    JobID           string         `json:"job_id"`
    Status          string         `json:"status"`           // "queued" | "running" | "completed" | "failed"
    ProgressCurrent int            `json:"progress_current"`  // targets processed so far (any terminal per-target state)
    ProgressTotal   int            `json:"progress_total"`    // = selection's Matched count
    Succeeded       int            `json:"succeeded"`
    Skipped         []RecordResult `json:"skipped"`
    Failed          []RecordResult `json:"failed"`
}
type RecordResult struct {
    AccountID int    `json:"account_id"`
    Reason    string `json:"reason"`
}
```
  On initial creation this is always `{status:"queued", progress_current:0, progress_total:<matched>, succeeded:0, skipped:[], failed:[]}`.
- **Errors**: `404 not_found` (bad selection reference, or missing assign-group target group),
  `409 selection_expired`, `422 validation_failed {"request":"contains an invalid people
  mutation"}`, `503 tenant_unavailable`.
- **Clients**: none (see above).

##### GET /api/v2/admin/organization/people/bulk-jobs/{job_id}

- **Purpose**: poll a bulk job's live status/progress — this is the *only* way to observe job
  completion; there is no webhook/push notification for job state in this package.
- **Auth**: organization admin context.
- **Path params**: `job_id` — opaque string id (`idgen.NextID()`-generated, not a UUID);
  empty/missing → `404 not_found`.
- **Response body** (200): same `BulkResult` shape as job creation, `status` reflecting current
  `admin_jobs.status`. Per-target `reason` strings you may see in `skipped`/`failed`:

  | Reason constant | Meaning |
  |---|---|
  | `already_applied` | target already had the requested end-state (already in the group / already suspended / already active) |
  | `no_profiles` | `assign_group` on an account with zero household profiles — nothing to reassign |
  | `not_found` | the target account/membership no longer exists in the organization |
  | `protected_owner` | `suspend_memberships` attempted on the organization's owner account — never allowed |
  | `mutation_failed` | the per-target DB mutation errored; that target's savepoint was rolled back and the job continues with the rest |
  | `authorization_state_changed` | the target's live membership/profile revisions no longer match what was captured in the selection snapshot (someone else modified this account since the selection was created) — that target is skipped, not retried |
  | `actor_authority_target` | the target account *is* the acting admin's own account — bulk operations always skip the actor's own row, even if it otherwise matches the selection |
- **Job lifecycle** (driven entirely by `adminpeople.Worker`, not by this GET):
  1. `queued` — row exists, no batch processed yet.
  2. `running` — worker has claimed a batch (`FOR UPDATE OF j` row lock, `SKIP LOCKED` on
     targets) and is processing up to `batchSize` (default 100, configurable via
     `WorkerOptions.BatchSize`) pending targets per pass, each in its own savepoint so one
     target's failure doesn't roll back the whole batch. `progress_current` increases as batches
     land; the worker re-wakes itself (its own `Wake()`) while any target remains
     `queued`/`running` so it drains the whole job across repeated passes without needing an
     external poke.
  3. `completed` — every target reached a terminal per-target status
     (`succeeded`/`skipped`/`failed`); `progress_current == progress_total`.
  4. `failed` — the *batch executor itself* errored (not an individual target — those go to
     `failed` in the `Skipped`/`Failed` arrays while the job stays `running`/`completes` normally);
     this happens for e.g. actor-authority revalidation failure. A `failed` job's `BulkResult`
     still carries whatever partial `succeeded`/`skipped`/`failed` counts had accumulated.
  Terminal jobs (`completed`/`failed`) are eventually garbage-collected: `Worker.Run`'s hourly
  cleanup tick deletes `admin_jobs`+cascaded rows older than `JobRetention` (default 24h) via
  `CleanupTerminalBulkJobs`, and separately purges expired (unused) selections via
  `CleanupExpiredSelections` on the same tick — so `GET .../bulk-jobs/{job_id}` on an old,
  completed job will eventually 404.
- **Errors**: `404 not_found` (unknown job id, or job belongs to a different organization),
  `503 tenant_unavailable`.
- **Clients**: none (see above).

---

### Per-account mutations

Both mutation endpoints require an `expected_revision` matching the person's *current*
`PersonSummary.security_revision` — classic optimistic concurrency. A stale revision fails with
`409 authorization_state_changed` and a `current_revision` field carrying the live value, so a
client can re-fetch and retry without a second round trip just to learn the new revision.

#### PATCH /api/v2/admin/organization/people/{account_id}/memberships/current

- **Purpose**: activate or suspend the target account's organization membership. The path says
  `.../memberships/current` — confirmed from the handler/service: an account has **exactly one**
  membership row per organization (`organization_memberships` is unique on
  `(organization_id, account_id)`), so "current" simply means "this account's one membership in
  this organization," not a selector among several. This is **not** a subscription/billing tier
  and **not** a role assignment beyond active/suspended — `legacy_role` (admin/member) is not
  settable through this endpoint at all. It is distinct from the platform-level `PATCH
  /api/v2/admin/platform/organizations/{id}/memberships/{membership_id}` surface (documented
  elsewhere), which operates platform-wide across organizations by explicit membership ID; this
  endpoint is organization-scoped, addressed by account, and limited to the active/suspended
  toggle.
- **Auth**: organization admin context.
- **Path params**: `account_id` — positive integer, 404 otherwise.
- **Request body**:
```go
struct {
    ExpectedRevision int64                    `json:"expected_revision"` // must be > 0 and equal the target's current security_revision
    Status           tenancy.MembershipStatus `json:"status"`            // "active" or "suspended" only
}
```
  Any other status value, or `expected_revision <= 0`, is rejected before the service is even
  called: `422 validation_failed {"request":"must include a current expected_revision and active
  or suspended status"}`.
- **Behavior**: if the requested status already matches the current one, this is a no-op success
  (revision is not bumped, no audit row). Otherwise updates `organization_memberships.status`,
  bumps `security_revision` (membership) and `access_policy_revision` (account), and records
  audit action `people.membership_updated`. The organization's owner account can never be
  suspended this way either (`protected` check mirrors the bulk-suspend rule) — attempting it
  returns `ErrInvalidBulkAction` → `422 validation_failed`.
- **Response body** (200):
```go
struct {
    Person adminpeople.PersonSummary `json:"person"` // freshly re-fetched, reflects the new status/revision
}
```
- **Errors**: `404 not_found` (no such account/membership), `409 authorization_state_changed`
  (stale `expected_revision`, with `current_revision`), `422 validation_failed` (bad status/owner
  protection), `503 tenant_unavailable`.
- **Clients**: none (see above).

#### PATCH /api/v2/admin/organization/people/{account_id}/profiles/{profile_id}

- **Purpose**: org-admin override that reassigns a **single specific household profile's** access
  group — reaching into *any* account in the organization, not just the caller's own.
- **Auth**: organization admin context.
- **Path params**: `account_id` (positive int, 404 otherwise), `profile_id` (opaque string id,
  404 if missing/empty).
- **Request body**:
```go
struct {
    ExpectedRevision int64 `json:"expected_revision"` // must be > 0, equal target account's security_revision
    GroupID          int   `json:"group_id"`          // must be > 0
}
```
  Missing/invalid either field → `422 validation_failed {"request":"must include a current
  expected_revision and group_id"}`.
- **Behavior**: verifies the `group_id` exists as an `access_groups` row in the caller's
  organization (`404 not_found` otherwise), locks the account's membership row and checks
  `expected_revision` against `security_revision` (`409 authorization_state_changed` on
  mismatch), then locks and updates that one `user_profiles.access_group_id` row (matched by
  `organization_id + user_id(account_id) + id(profile_id)` — `404 not_found` if no such profile
  exists under that account). If the profile is already in the requested group, it's a silent
  no-op (no revision bump, no audit row). Otherwise bumps both the membership's
  `security_revision` and the account's `access_policy_revision`, and records audit action
  `people.profile_group_updated`.
- **Response body** (200): same `{"person": PersonSummary}` shape as `UpdateMembership`, re-fetched
  after the change (so it reflects every profile on the account, not just the one touched).
- **Errors**: `404 not_found` (bad group id, bad account, or profile not under that account),
  `409 authorization_state_changed`, `422 validation_failed`, `503 tenant_unavailable`.
- **How this differs from `/api/v1` `PUT /profiles/{id}`**: the v1 endpoint is a
  self-service/household-manager path — auth is a plain account bearer token, callers may only
  ever act on profiles under their *own* account (a non-managing caller is further restricted to
  their own *active* profile matched via `X-Profile-Id`), and it can update a broad field set:
  name, avatar, PIN, playback quality/language/subtitle prefs, autoplay/skip toggles, and
  (household-manager only) `is_child`, `max_content_rating`, library restrictions,
  `max_playback_quality`. This v2 admin endpoint, by contrast, requires an organization-admin
  context token (not a plain user bearer token), can target **any account's** profile within the
  organization regardless of ownership, and can change **only `access_group_id`** — nothing else
  about the profile (name, avatar, content restrictions, playback prefs) is reachable through
  this route. It is a narrow, single-purpose org-access-control lever, not a general profile
  editor.
- **Clients**: none (see above).

---

### Summary: what's genuinely unclear / not fully determinable from this pass

- The admin web console (if any) that actually drives this API was not located/inspected as part
  of this task — only `vondel-android` and `vondel-apple` were checked, and both come back empty.
  If a web admin UI exists in a separate repo, it was out of scope here.
- Resolved: `cmd/silo/main.go` always constructs the worker (`adminpeople.NewWorker(adminPeopleService, adminpeople.WorkerOptions{})`,
  i.e. default options — 30s recovery interval, 1h cleanup interval, batch size 100) and starts
  its `Run` loop as a goroutine (`startAdminPeopleBackgroundWorker`) unconditionally at startup,
  then passes it into `Dependencies.AdminPeopleWorker`. So in this codebase's own binary, bulk
  jobs are never permanently stuck `queued` — the only caveat is that `mountV2` re-derives
  `peopleService` independently if `deps.AdminPeopleService` is nil, which in principle could let
  a test/embedding harness wire a handler without ever starting the shared worker; that's a
  latent risk in non-`cmd/silo` embedders, not in the shipped server.
