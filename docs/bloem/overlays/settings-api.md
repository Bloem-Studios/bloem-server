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
