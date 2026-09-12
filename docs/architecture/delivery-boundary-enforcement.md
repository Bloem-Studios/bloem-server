# Delivery boundary enforcement

Native media delivery, the standalone proxy, and notification fanout now
recheck library access at the point where bytes or rows actually leave the
server, closing paths that previously trusted a session or a signed artifact
without asking whether the account behind it can still reach the library in
question.

## The existing ceiling (not built by this work)

The organization ceiling already existed before this change. `ViewerResolver`
loads the tenant's available media folders and the policy engine intersects
them into every resolved scope:

- `internal/policy/viewer_resolver.go:18` declares `TenantLibraryResolver`
  (`AvailableMediaFolderIDs`); `internal/policy/viewer_resolver.go:122` calls
  it inside `ResolveFacts` to get `tenantLibraryIDs` before the PDP runs.
- `internal/policy/vendor/scope.rego:68-70` (`tenant_bounded_libraries`)
  returns the tenant's own library set unmodified when the account is
  unrestricted, and `intersect(tenant_library_ids(i),
  effective.allowed_library_ids)` otherwise.
- Background paths that have no HTTP request get the same ceiling through
  `policy.TenantViewerResolver`, which resolves the subject's tenant itself
  rather than reading one off a request. `internal/api/router.go:2401` wires
  this resolver into progress-snapshot resolution.

The consequence is what makes the rest of this document possible:
`access.Scope.AllowedLibraryIDs` is already tenant-bounded everywhere it is
read. A delivery guard that rechecks library membership against that scope
is, by construction, also rechecking the tenant. Nothing added here needed
to re-derive organization membership — it only needed to make sure the scope
was actually being consulted at each delivery point.

## What this work added

Six paths never consulted the resolved scope at all. Each now rechecks
library membership immediately before serving:

- Native media delivery: file bytes, subtitles, subtitle fonts, HLS
  manifests and segments, and signed restart recipes
  (`internal/api/handlers/playback.go`, `internal/api/handlers/stream.go`,
  `internal/api/handlers/playback_library_access.go`).
- Managed-download subtitles (`internal/downloads/offline.go`).
- The standalone proxy's signed recipes, media grants, and download URLs
  (`internal/proxy/source_access.go`, wired from `cmd/silo/main.go`).
- Notification fanout and queued delivery re-reads
  (`internal/notifications/delivery_access.go`).

The delivery guard also had to be connected to a real scope on the one
request shape that skipped viewer-access resolution entirely: a bearer-less
`?st=` stream-token request (how a native player replays a `stream_url`,
since it cannot attach an `Authorization` header). Before this change,
`RequireViewerAccess` skipped on that path and `AccessFilterFromContext`
fell back to a zero-value filter, which the guard read as "no library
restriction" — revoking a library entitlement did not stop delivery.
`internal/api/middleware/viewer_access.go:107-145`
(`requireStreamTokenViewerAccess`) now resolves a real, organization-bounded
scope from the token's own `uid`/`pid` (lookup keys only, re-verified
against live policy) instead of skipping.

## Invariants

These are rules a future change must not break.

- **Library authority is read from PostgreSQL on every request, never
  cached as a token claim.** Grant mutations advance the organization's
  access revision; a delivery guard that trusted a claim instead of a fresh
  resolution would keep serving a revoked entitlement until the token
  expired.
- **The ceiling never widens a policy result; it only intersects.**
  `internal/policy/vendor/scope.rego:68-70` intersects, never unions, the
  tenant set with the effective policy result.
- **A delivery guard rechecks library membership only.** Quality selection
  stays at admission — a lower-resolution transcode must never be rejected
  because its source is higher resolution.
  `internal/catalog/access_filter.go:219` (`FileAllowedByLibraryScope`)
  takes a file and two library-ID sets, no quality parameter, by design;
  see the reasoning recorded at
  `internal/api/handlers/playback_library_access.go:33-35`.
- **Absence of a resolved scope means deny, not "unrestricted."** This is
  load-bearing: `RequireAuth`, `RequireViewerAccess`, and `TenantMiddleware`
  all return early for stream-token-authorized requests, so
  `internal/api/middleware/viewer_access.go` is what resolves the scope from
  the token's own claims on that path, and every guard in
  `internal/api/handlers/playback_library_access.go` calls `access.GetScope`
  directly and fails closed when `ok` is false, rather than trusting
  `AccessFilterFromContext`'s zero-value fallback alone.
- **An inaccessible source answers 404 and never confirms existence; an
  authority that cannot be consulted answers 503.**
  `internal/proxy/source_access.go:34-42` documents and
  `checkSourceAccess` (`internal/proxy/source_access.go:57-72`) implements
  this: `ErrSourceHidden` maps to 404, everything else — including a nil
  `SourceAccess` — maps to 503, matching the existing nil-grants/
  nil-loginSessions convention in `internal/proxy/server.go`.
- **The proxy checks ownership through an injected interface because it has
  no database and no resolved viewer scope.**
  `internal/proxy/source_access.go:31` defines `SourceAccess` as an
  interface in the same shape as `proxyGrantLookup`/`loginSessionValidator`;
  the concrete, DB-backed implementation lives in `cmd/silo/main.go` (where
  the pool this process opened already exists), composing
  `tenancy.Resolver` and `resourcetenancy.Store` the way the API's tenant
  middleware does. `internal/proxy` must not import `internal/tenancy` or
  `internal/resourcetenancy` directly.
- **Per-request rechecking is deliberate.** Checking only at manifest-fetch
  time would leave HLS segments unguarded until the next manifest reload;
  every account-scoped media route in the proxy — direct play, remux,
  transcode manifest, transcode segment, subtitles, download — calls
  `checkSourceAccess` independently.
- **Delivery-type eligibility is an explicit allowlist, not a negated
  exception, so a new type must classify itself.**
  `internal/notifications/delivery_access.go:18`
  (`accountLevelDeliveryTypes`) replaced a fallback that excluded only
  `request.fulfilled` by name — a new delivery type with no library and no
  item identity used to inherit eligibility silently.
  `TestEveryDeliveryTypeConstantIsClassified` parses every top-level
  `DeliveryType*` constant across the package's four declaring files and
  fails by name if a constant is missing from either
  `accountLevelDeliveryTypes` or the test's own
  `notAccountLevelDeliveryTypes` set.

## Explicitly not covered

- **Client-facing inbox reads are not bounded.** `GetByID`, `ListInbox`,
  `ListSync`, `RecentUnread`, `UnreadCount`, and `GetRowsByIDs` in
  `internal/notifications` carry no ownership predicate. A revoked-access
  row still appears in that viewer's own inbox and can expose the title and
  metadata of an item they can no longer reach.
- **Interest rows are not invalidated when an entitlement is revoked.**
  `internal/resourcetenancy` bumps `security_revision` on a grant change;
  `internal/notifications` never reads that revision. Stale
  `profile_series_interest` rows are excluded at read time by
  `deliveryAccessPredicate`, but they are never cleaned up — they persist
  indefinitely as dead rows.
- **The standalone proxy always checks against the account's default
  organization, not the organization the original request was scoped to.**
  `cmd/silo/main.go:480` resolves tenancy with `legacy=true` because proxy
  artifacts (signed recipes, grants, download URLs) carry no organization
  claim. For a single-org account this is exactly right; for a multi-org
  account, the proxy recheck can only ever validate against the default
  organization, never the one the original API request was scoped to.
- **The proxy guard costs multiple indexed Postgres round-trips per
  request** — a file lookup, a tenant resolution, and a resource-access
  check (`cmd/silo/main.go:469-494`) — repeated per HLS segment, since
  per-request rechecking is deliberate (see Invariants). The correct
  mitigation is a cache keyed on the organization's access revision,
  invalidated when it advances, not a time-based TTL — a TTL would
  reintroduce the stale-authority window this design rejects.
- **Rule 3's `episode_id`-only branch resolves at series granularity, not
  episode granularity.** It resolves episode→series and checks
  `media_item_libraries` rather than `episode_libraries`, which can
  diverge. This is unreachable today because no writer sets `episode_id`
  without also setting `series_id`; the assumption is called out in the
  code but not enforced.
- **Revalidation on long-lived sockets is periodic, not cancellation.** It
  cannot interrupt a response already in flight — a revoked entitlement
  stops the *next* request, not bytes already streaming.
- **Download artifacts are shared preparation jobs, not per-account
  permissions.** Revoking one account's access must not cancel an artifact
  another authorized account still needs; this work rechecks who may fetch
  an artifact, not who may keep it queued for preparation.
- **No restored-backup migration rehearsal has been done.** The revocation
  and revision-bump behavior described above has not been exercised against
  a database restored from a backup.
