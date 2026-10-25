# Bloem overlay: Canonical Settings API

Bloem additions and overrides for the upstream Silo document [`docs/settings-api.md`](../../settings-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

Bloem serves Silo's settings contract, `/api/v2` settings operations and frozen
`/api/v1` settings bridge, including the bridge's headers, scopes, write mirror,
atomic shortcuts and device registry, as upstream documents them. The points
below are where Bloem differs.

**Location:** section “Collections use the `{items}` envelope”, after the paragraph beginning “A definition marked `admin_default`…”. **Bloem uses Silo's behavior, with this tenancy note:**

An administrator default is a server setting (`defaults.<key>`), not an
organization setting. Bloem's tenant-aware viewer resolvers apply the same value
for every organization, and the `scope: "default"` event reaches subscribed
viewers in every organization. There is no per-organization administrator default.

**Location:** section “Bridge: the frozen `/api/v1` settings routes”, subsection “Request identity headers”, after the paragraph beginning “Authenticated settings routes use the active account and profile…”. **Bloem adds:**

A Bloem direct-profile session (`POST /api/v1/auth/profile-login`) is bound to
its token's profile; naming another profile answers `403`. Its default-deny
route allowlist admits the settings contract, effective, device-override,
explicit-value and `nav.shortcuts/item` routes, but not the account-wide
`/settings/` list or `/settings/{key}`. An explicit-value request at
`scope=account` answers `403 forbidden`. The session reaches no `/api/v2`
settings operation.

**Location:** section “Bridge: the frozen `/api/v1` settings routes”, subsection “Admin projection”, after the list of admin routes. **Bloem adds:**

The admin `PUT` and `DELETE` routes are registered account-lifecycle mutations.
When `GET /api/bloem/v1/capabilities` advertises `lifecycle_idempotency_v1`, send one
stable `Idempotency-Key` per logical edit and reuse it for an exact retry. The
header becomes mandatory when `lifecycle_idempotency_required_v1` is also
advertised. This is separate from the self-service settings API's
`X-Silo-Mutation-Id`; negotiation, replay, and error behavior are documented in
the [native API reference](../../bloem-api-reference.md#shared-lifecycle-mutation-idempotency-compatible-and-native-surfaces).
It covers only these `/api/v1` routes, not the v2 `setAdminUserSettingValue` and
`deleteAdminUserSettingValue` operations.

The section's example then becomes:

```http
PUT /api/v1/admin/users/42/settings/values/ui.card_presentation?scope=profile_client&profile_id=main&client_family=tv
Authorization: Bearer <admin-token>
Idempotency-Key: <stable-uuid-for-this-intent>
Content-Type: application/json

{"value":{"poster_size":"large","caption":"artwork"}}
```
