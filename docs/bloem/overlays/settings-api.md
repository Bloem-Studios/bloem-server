# Bloem overlay: Canonical Settings API

This overlay covers Silo's [`docs/settings-api.md`](../../settings-api.md), which stays exactly as Silo wrote it.

Silo's settings page explains how apps read and change settings: the server's,
an account's, a profile's and a device's. Bloem serves Silo's settings contract as
Silo documents it: the `/api/v2` settings operations with their headers, scopes,
write mirror, atomic shortcuts and device registry. Bloem adds three notes, because Bloem has **organizations** and
**profiles with a sign-in of their own**.

## What Bloem changes

- **An administrator's default applies everywhere.** Silo lets an administrator
  set a default value for some settings. On Bloem that default is the server's,
  and it applies in every organization alike. There is no per-organization
  default.
- **A profile's own sign-in reaches no settings.** A session signed in straight to
  one profile reaches no settings route. Profile sign-in is dormant.
- **Each organization's domain shows its own brand.** On an organization's own
  domain, the public branding Silo serves before sign-in (name, logo, colours)
  is that organization's. Silo's own branding settings stay the server's.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| An administrator's default | a server setting `defaults.<key>`, for definitions marked `admin_default` |
| A profile's own sign-in | a direct-profile session (dormant: no sign-in route is mounted) |
| Its allowed routes | the default-deny allowlist in `internal/api/direct_profile_routes.go` |
| An organization's brand | `bloem_organization_branding`, served through `orgdomains.SettingsOverlay` |

## For developers

### Section by section

---

**Location:** section “Collections use the `{items}` envelope”, after the paragraph
beginning “A definition marked `admin_default`…”. **Bloem uses Silo's behavior,
with this tenancy note:**

An administrator default is a server setting (`defaults.<key>`), not an
organization setting. Bloem's tenant-aware viewer resolvers apply the same value
for every organization, and the `scope: "default"` event reaches subscribed
viewers in every organization. There is no per-organization administrator default.

---

**Location:** section “Public branding”, after the paragraph beginning “The web
client has one theme, Cinema Dark.”. **Bloem adds:**

#### Organization branding on organization domains

Silo's branding service reads its settings through `orgdomains.SettingsOverlay`
(`branding.NewService` in `cmd/silo/main.go`). On an organization's own domain,
or for a request authenticated in that organization, the keys in
`branding.PresentationSettingKeys()` (server name, login subtitle, accent colour,
default theme and the asset references) answer that organization's values from
`bloem_organization_branding`. That covers `GET /api/v2/theme/branding`,
`/api/v2/branding/assets/{kind}`, the page shell and
web app manifest, and the brand in emails. An unset value is Silo's built-in
default there, never the server's: the server's branding is the default
organization's brand. For the default organization, or no organization, every
key is the server's.

A write goes to an organization only when its context carries
`WithBrandingWrite`, as the platform routes
`/api/bloem/v2/admin/platform/tenants/{id}/branding` do (`PUT|DELETE
…/branding/assets/{kind}` for assets); those routes refuse the default
organization with `409 default_organization`. Every other write, including Silo's
own branding settings and asset uploads made on an organization's domain, is the
server's. See [organization domains](../../architecture/bloem-organization-domains.md).

---

**Location:** section “Settings semantics”, subsection “Request identity headers”, after
the paragraph beginning “Authenticated settings routes use the active account and
profile…”. **Bloem adds:**

A Bloem direct-profile session is bound to its token's profile. Its default-deny
route allowlist (`internal/api/direct_profile_routes.go`) admits no `/api/v2` settings
operation, so the session reaches no settings route. Profile sign-in itself is
dormant: no sign-in route is mounted.

### Related pages

- [Settings contract overlay](architecture/settings-contract.md): the Kotlin and
  Swift bindings Bloem commits.
- [Authentication overlay](auth-api.md): direct-profile sessions and sign-in on
  organization domains.
- [Organization domains](../../architecture/bloem-organization-domains.md).
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).
