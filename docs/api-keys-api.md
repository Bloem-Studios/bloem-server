# API Keys API

> **API lifecycle:** this documents the frozen alpha `/api/v1` surface. Silo serves it through one
> pre-1.0 bridge release and then retires it; Silo 1.0's stable native API is `/api/v2`. See
> [the native API contract](architecture/api-contract.md).

API keys are long-lived credentials for scripts and integrations. A key is a
string with an `sa_` prefix and is sent the same way as a JWT access token:

```
Authorization: Bearer sa_your_api_key_here
```

A key always acts as the user who owns it: the account's role and permissions
still apply, so an admin-only route needs a key owned by an admin account.

## Scopes

By default a key is **unscoped** and can reach every route its owner can. A key
created with `scopes` is an allowlist credential instead: the auth middleware
admits it only to the routes those scopes name and answers `403` everywhere
else, including routes added after the key was issued.

Scopes only narrow. They never grant, and they never bypass the owner's role
check. `admin:users` on a key owned by a non-admin account still cannot manage
users.

Scoped keys are also refused the writes that would let them trade the allowlist
for an unscoped **admin** session. The boundary is the admin role, not the
credential: provisioning and managing ordinary accounts is in scope.

| Attempted write on `/api/v1/admin/users` | Result |
|------------------------------------------|--------|
| `POST` with `role: "admin"` | `403 insufficient_scope` |
| `PUT` with `role: "admin"` | `403 insufficient_scope` |
| `PUT` with `password` or `role` when the target account is currently an admin | `403 insufficient_scope` |
| `POST` with `password` and a non-admin `role` | allowed |
| `PUT` with `password` when the target account is not an admin | allowed |

Unscoped keys and JWT sessions are unaffected.

Discover the scopes a server understands with the capability endpoint below
rather than sniffing the server version.

---

## Self-service endpoints

The management endpoints below (create, list, delete) require a **JWT access
token**; authenticating them with an API key returns `403`, because a key may
not mint or enumerate keys. The capability endpoint is the exception: it is a
static catalog, so any authenticated caller may read it.

### List the available scopes

```
GET /api/v1/api-keys/scopes
```

Feature detection for API key scopes. Requires authentication; needs no
particular role. Note that a *scoped* key is refused here like anywhere else
outside its allowlist.

```json
{
  "scopes": [
    {
      "name": "admin:users",
      "description": "Manage user accounts: create, list, read, update, and delete users and read their profiles. Cannot create or modify admin accounts."
    },
    {
      "name": "admin:access-groups:read",
      "description": "Read access groups and their policies."
    }
  ]
}
```

A server that predates scopes has no such route and answers `404`; treat that
as "no scope support" and create unscoped keys.

### Create a key

```
POST /api/v1/api-keys
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `label` | string | yes | Human-readable name for the key. |
| `scopes` | string[] | no | Scope names from the capability endpoint. Omitted, `null`, or `[]` creates an unscoped key. Duplicates are removed and the list is sorted; an unknown scope is a `400`. |

Returns `201` with the key record. **The full `key` value is returned only on
creation. Store it from this response; list endpoints cannot recover it.**

```json
{
  "id": 12,
  "user_id": 7,
  "label": "ci",
  "key": "sa_1f0c…",
  "rate_tier": "standard",
  "scopes": ["admin:users"],
  "created_at": "2026-08-19T12:00:00Z",
  "last_used_at": null
}
```

`scopes` is always an array; `[]` means unscoped.

### List your keys

```
GET /api/v1/api-keys
```

Returns an array of metadata objects, newest first (creation time, then ID).
Each object contains `id`, `user_id`, `label`, `key_prefix`, `rate_tier`,
`scopes`, `created_at`, and `revision`, plus `last_used_at` when known.
The `key` field is absent. `key_prefix` contains the first 11 characters of a
key in the generated format, or an empty string for other legacy formats.
`revision` advances when configuration changes; authentication activity does
not change it. Existing credentials remain valid.

### Delete a key

```
DELETE /api/v1/api-keys/{id}
```

Returns `204`. Deleting a key you do not own returns `404`.

---

## Admin endpoints

These require an admin account.

### List every key

```
GET /api/v1/admin/api-keys
```

Same metadata fields as the personal list, plus `username` for the owning
account. This response never includes the full credential.

### List one user's keys

```
GET /api/v1/admin/users/{userId}/api-keys
```

Returns the same metadata fields as the personal list, without `username`.

### Create a key for a user

```
POST /api/v1/admin/api-keys
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `label` | string | yes | Human-readable name for the key. |
| `user_id` | integer | no | Owning account; defaults to the calling admin. |
| `scopes` | string[] | no | Same validation as the self-service endpoint. |

Returns `201` with the full key record, using the same creation-only secret
disclosure as the personal endpoint.

### Change a key's rate tier

```
PUT /api/v1/admin/api-keys/{id}/tier
```

Body: `{"tier": "standard"}` or `{"tier": "elevated"}`. Any other value is a
`400`.

### Delete any key

```
DELETE /api/v1/admin/api-keys/{id}
```

Returns `204`.

## V2 admin lifecycle

The v2 admin editor exposes the following operations under `/api/v2`:

| Method | Path | Result |
|---|---|---|
| GET | `/admin/api-keys/capabilities` | Availability, supported scopes and tiers, and editor support |
| GET | `/admin/api-keys` | Bounded metadata collection with opaque cursor continuation |
| GET | `/admin/api-keys/{id}` | Canonical metadata and a strong `ETag` |
| POST | `/admin/api-keys` | `201` creation response containing the full key and canonical `Location` |
| PUT | `/admin/api-keys/{id}/tier` | Conditional tier update returning canonical metadata and `ETag` |
| DELETE | `/admin/api-keys/{id}` | Conditional deletion returning `204` without a body |

These operations require acting-admin authority. Mutations retain the demo
restriction; reads do not. Scoped API keys
cannot access credential management; unscoped keys retain the owning account's
access. Personal key management uses the separate account-scoped operations below.

IDs use JSON strings. Canonical metadata excludes the full key, usage timestamps,
and the owner's display name. The collection adds usage and owner display fields.
Only the creation response contains the full credential. Save it then; creation
must not be retried automatically after an uncertain response.

The list accepts `limit` (1–200, default 50) and `cursor`. It orders by creation
time descending, then ID descending. The cursor is bound to the acting account,
profile, and page size. Continue using the returned cursor; a changed scope or
invalid cursor requires a fresh first page. The list has no exact total.

The tier update body is `{"rate_tier":"standard"}` or
`{"rate_tier":"elevated"}`. Read the canonical resource before editing and send its captured tag in
`If-Match`. A missing precondition returns `428`; a stale tag returns `412` with
the current tag. `If-Match: *` explicitly permits changing the current resource.
Canonical reads support conditional requests, including `304` for an unchanged
`If-None-Match` tag. Authentication usage and no-op tier edits do not invalidate
configuration tags. Successful deletion returns no validator.


## V2 personal lifecycle

Personal key management operates on the login account, without requiring a household
profile. The v1 endpoints remain frozen during the bridge.

| Method | Path | Result |
|---|---|---|
| GET | `/api/v2/api-keys/scopes` | Availability and supported scopes |
| GET | `/api/v2/api-keys` | Metadata collection with opaque cursor continuation |
| POST | `/api/v2/api-keys` | `201` response containing the credential once |
| DELETE | `/api/v2/api-keys/{id}` | Owner-only revocation returning `204` |

Listing, creation, and revocation require JWT authentication; API-key credentials
receive `403`. Scope discovery retains its availability to unscoped API keys.
Creation and revocation retain the demo restriction; listing and scope discovery
do not. An unavailable store reports
`available: false` in scope discovery and `503` for management operations.

Create with `{"label":"Script","scopes":[]}`. Omitting scopes creates an unscoped
key. Explicit nulls and unknown fields are rejected. The account comes from the
login session; a caller cannot choose another owner. Store the returned credential
securely: creation is non-retryable after an uncertain response. List responses
contain metadata only, with string IDs and optional usage timestamps.

The list accepts `limit` (1–200, default 50) and `cursor`, ordered by creation time
and ID descending. Cursors bind to the account and page size, independently of the
selected profile. Revocation checks ownership in the database delete. Missing keys
and keys belonging to another account both return `404`; retrying revocation has
no additional effect. This self-service revocation does not require `If-Match`.

The current web, Apple, and Android clients have no personal key-management
consumer to migrate. Jellyfin credential endpoints keep their separate protocol.
