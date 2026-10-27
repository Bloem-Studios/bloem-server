# Bloem overlay: Canonical Settings API

**What this overlays.** Silo's [`docs/settings-api.md`](../../settings-api.md).
Bloem adds a tenancy note to “Collections use the `{items}` envelope” and a
paragraph to the bridge's “Request identity headers”. It replaces nothing.
Bloem serves Silo's settings contract, the `/api/v2` settings operations and the
frozen `/api/v1` settings bridge, including the bridge's headers, scopes, write
mirror, atomic shortcuts and device registry, as Silo documents them. An
*overlay* is a Bloem-owned page that records Bloem's additions to, or
replacements of, sections of a Silo-owned document. The Silo file stays exactly
as Silo wrote it. See the [documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem is multi-tenant, with accounts in *organizations*,
so it records how Silo's server-wide administrator defaults behave across
organizations. Bloem also has *direct-profile sessions*, which sign in straight
to one household profile with that profile's own credential. Their
default-deny route allowlist dates from `16baf5558` (2026-08-14). Bloem first noted its settings
differences in the Silo file in August 2026 and moved them here in `47ea7c889`
(2026-09-23). Until `b017bfe84` (2026-10-26) this page also described Bloem's
Idempotency-Key lifecycle receipts on the settings admin routes. That system
is gone, and Silo's `settings_values.go` and `settings_values_admin.go` are
Silo's bytes again.

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/direct_profile_routes.go`). Since the previous review
(Silo `6de6d5e5e`), Silo changed the document once, in `93585220e` (#3136,
2026-10-26): the stored values one `resolveEffectiveSettings` or
`listSettingValues` response returns are bounded at 16 MiB. That applies to
Bloem unchanged.

Index: [Bloem documentation](../README.md).

---

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
