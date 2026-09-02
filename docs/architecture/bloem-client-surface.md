# Bloem native client surface

`/api/v1` is the Silo-compatible projection. `/api/bloem/v1` is the native Bloem
API. Anything this project invents for its own clients belongs on v2, because
a deployment must stay usable by upstream Silo clients that will never learn a
Bloem-only field, value, or route.

That rule is enforced, not merely written down:
`internal/api/v1_route_surface_test.go` walks the mounted route tree and
compares the `/api/v1` routes against golden lists generated from `origin/main`
(`internal/api/testdata/v1_routes_*.txt`). A route added to or removed from v1
fails with the route named. Regenerate the goldens only when a v1 change is
genuinely intended, and only after the change is agreed:

```bash
BLOEM_UPDATE_V1_ROUTE_GOLDEN=1 go test ./internal/api/ -run TestV1RouteSurface
```

## Routes

| Route | Auth | Notes |
| --- | --- | --- |
| `GET /api/bloem/v1/capabilities` | public | Build capabilities. Never fails. |
| `GET /api/bloem/v1/server/identity` | public | `server_id`, `server_name`, `api_versions`, `setup_complete`. `no-store`. |
| `GET /api/bloem/v1/watch/home` | account + profile | `watch_document_v1` home snapshot. |
| `GET /api/bloem/v1/watch/items/{content_id}` | account + profile | `watch_document_v1` item snapshot. |
| `GET /api/bloem/v1/watch/search` | account + profile | Profile-scoped Watch search. |
| `POST /api/bloem/v1/sync/progress` | account + profile | Per-item `updated` / `ignored` / `error`. |
| `GET /api/bloem/v1/persons/{person_id}` | account + profile | Profile-scoped person detail and visible filmography. |
| `GET /api/bloem/v1/music/status` | account + profile | Available music library IDs. |
| `GET /api/bloem/v1/music/artists` | account + profile | Artists page; requires an allowed `library_id`. |
| `GET /api/bloem/v1/music/artists/{id}` | account + profile | Artist and ordered albums; requires `library_id`. |
| `GET /api/bloem/v1/music/albums/{id}` | account + profile | Album and ordered tracks; requires `library_id`. |

The authenticated routes share one group: `RequireAuth`, the default-organization
tenant projection, the rate limiter, viewer-access resolution, and
`RequireProfile`. They deliberately do **not** use `tenantMW.RequireV2`, which
demands a tenant-selected session carrying organization, membership and revision
claims. No login endpoint mints those claims, so requiring it would refuse every
viewer. Organization-bound administrative routes still must use it.

These routes use ordinary account sessions and require `X-Profile-Id`.
Direct-profile sessions are limited by the router's `/api/v1` allowlist and
are rejected before the v2 profile middleware runs.

`feature_tokens` describe additive capabilities — including
`watch_document_v1`, `device_pairing_v1`, `progress_sync_v1`, and
`music_catalog_v1` — and clients must not use a server version for feature
detection. Most tokens are build-level. `lifecycle_idempotency_v1` is present
when the lifecycle coordinator is wired, while
`lifecycle_idempotency_required_v1` additionally reflects the current rollout
phase. Clients use a stable `Idempotency-Key` only when support is advertised,
and preserve the same key across bounded retries; the full status contract is
in the [v2 API reference](../bloem-api-reference.md#shared-lifecycle-mutation-idempotency-v1-and-v2).
A token does not prove that an unrelated dependency-conditional route is
mounted in a particular deployment; clients still handle the route's response.

## Server identity is its own endpoint

Identity is a sibling of the capability document rather than a section inside
it. The capability document must never fail: a probe that can itself be
unavailable leaves a client interpreting exactly the ambiguous state the probe
exists to replace. Its lifecycle-required token is a dynamic rollout fact whose
read failure is handled by omitting that token, while the response stays `200`.
Identity resolves the
`server.instance_id` setting through the database and legitimately answers
`503`, and its `setup_complete` flips exactly once, so it is served `no-store`.
Folding the two together would either make the capability probe fallible or
leave the identity fields silently absent.

`GET /api/v1/health` is untouched by this. Its `server_name` and `server_id`
still come from the Jellyfin-compatibility configuration and are still omitted
when that configuration is absent. They are not a scope key; `server_id` from
the identity endpoint is.

## Progress sync reports two vocabularies

`POST /api/v1/sync/progress` reports `ok` for every non-error item. That value
is ambiguous — a position under the min-resume floor and an offline event that
lost last-write-wins come back looking exactly like a stored row — but Silo
clients parse it, so it stays.

`POST /api/bloem/v1/sync/progress` takes the identical request body against the
identical store and reports `updated` (the row was written), `ignored` (accepted,
not written) or `error`. Only the reporting differs: both routes run the same
thresholds, the same last-write-wins merge, the same taste-profile refresh and
the same event fan-out.

## Response and dependency behavior

The native surface may add fields and routes without preserving an upstream
Silo wire shape. Clients must ignore response fields they do not understand.
The public identity route is always mounted. Other handlers are conditionally
assembled; a handler absent from the router yields `404`. Watch is mounted
when its reader dependencies exist. Its catalog reader is also always passed
to the Watch handler as the searcher, so a deployment without the optional
search provider returns a valid `200` search document with no items rather
than `503` when Watch itself is mounted.

## Wiring

`mountV2` builds everything it serves from `Dependencies` inside
`internal/api/router_v2.go` and `internal/api/router_v2_client.go`, rather than
receiving handlers assembled by the v1 tree. That independence is the point: a
native route can be added, changed or removed without editing
`internal/api/router.go` at all.
