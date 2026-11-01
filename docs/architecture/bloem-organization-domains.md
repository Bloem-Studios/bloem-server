# Organization domains and branding

Each organization can be served on its own domains, under its own name, logo and colours.
Its emails link to its primary domain, and an account signing in on another organization's
domain is told where to sign in instead. The code is `internal/orgdomains`. The platform
routes are in `internal/api/handlers/bloem_organization_domains.go`, and the sign-in check is
in `internal/auth/bloem_sign_in_domain.go`.

## Rules

- **A domain decides presentation, never access.** An account belongs to one organization
  (`organization_memberships_one_per_account`). The tenant middleware takes that
  organization from the account or the session, whatever domain the request arrived on.
  A domain only chooses the branding shown and the address links use.
- **Registered hosts only.** A host maps to an organization only when it is in
  `bloem_organization_domains`. A stored host is lower case and has at least one dot, with
  no scheme, port or path. A request host is compared lower case, with its port removed.
- **Trusted proxies only.** The request's host is `X-Forwarded-Host` from a trusted proxy,
  else `Host`. A client that is not a trusted proxy cannot pick an organization by
  sending `X-Forwarded-Host`.
- **The default organization is the server.** It uses the server's own branding and public
  URL, and the branding routes refuse it (`409 default_organization`). It may register
  domains: an account of another organization is then refused there.
- **Another organization never shows the server's brand.** An organization's unset
  branding value is Bloem's built-in default on its domains, not the server's. The server's
  branding is the default organization's brand.

## How a request gets its organization

`orgdomains.WrapListener` wraps the whole listener in `cmd/silo/main.go`. That covers the
API router and the page shell, which `cmd/silo/root_handler.go` serves outside the API
router.

The wrapper reads the host through `(*clientip.Resolver).RequestHostOf`, which changes
nothing on the request. It must not run `clientip.Middleware`: that rewrites
`r.RemoteAddr`, and the API router's own client-address pass would then lose the trusted
proxy's forwarded host. The organization is stored with `WithRequestOrganization`.

`orgdomains.Directory` holds every registered domain and organization branding value in
memory. After `DirectoryTTL` (30 seconds) it keeps serving the old map while one background
read replaces it, so a slow database never holds requests up. After a write on the same node
it reads again in the next request, so the write shows at once. Other nodes see a write
within about 30 seconds. A failed read keeps serving the previous map.

## Branding and email links

Silo reads presentation values through a settings store that receives the request context.
`orgdomains.SettingsOverlay` wraps that store in two places:

- `branding.NewService` in `cmd/silo/main.go`. This covers `/api/v2/theme/branding`, the
  page shell and manifest (`internal/server/frontend.go`) and the branding assets.
- The invitation and password-reset services in `internal/api/router.go`, with
  `OverlayOptions{PublicURL: true}`. `server.public_url` there answers
  `<scheme>://<primary domain>`, so links follow the organization.

The overlay answers the keys in `branding.PresentationSettingKeys()`. It picks the
organization from `OrganizationFor`, in this order:

1. one named with `WithOrganization`;
2. the authenticated request's tenant;
3. the request's domain.

A write goes to an organization only when its context carries `WithBrandingWrite`. Every
other write is the server's, even one made on an organization's domain. That lets Silo's
own branding settings page keep editing the server's branding.

Organization values live in `bloem_organization_branding`, one row per `branding.*` key.
Uploads go through Silo's own `branding.Service.UploadAsset`. References are content hashes
in the shared asset store, and deleting an organization's asset only clears its row.

## Signing in on another organization's domain

Every sign-in creates a session through the auth service's session repository.
`Service.SetSignInDomainCheck` puts `Directory.SignInCheck` in front of it.

When the request's domain is registered to another organization than the account's, the
sign-in is refused before a session exists, with `auth.OrganizationDomainError`. The
password and profile sign-in handlers answer `409 wrong_organization_domain`,
"This account signs in at https://<primary domain>." That address falls back to the server
public URL when the account's organization has no primary domain. `/api/v2` renders it as
`conflict`, and the sign-in page shows the detail.

It is 409, not 403: v2 renders a 403 at sign-in as `permission_denied`, which the sign-in
page reports as a disabled account.

Not refused:

- requests on unregistered hosts, such as IP addresses and LAN names, so an
  administrator can always reach the server;
- impersonation sessions.

## Signing in through an OAuth provider (Authentik)

A web sign-in through a provider stays on the organization domain it started on. The
provider sends the browser back to that domain's callback, and the browser finishes on
that domain's completion page, where its cookies are. `Directory.OAuthBaseURL` names the
base, and `auth.OAuthHandler.SetRequestBaseURL` installs it.

`internal/auth/oauth_handler.go` is a declared seam for this: one field and four call
sites. They are the public-origin check, the callback address, and the completion page
twice. On any other host the flow keeps the public URL and bounces there, as in Silo.
The provider must know each domain's callback.

## Managing domains and branding

The platform routes sit under `/api/bloem/v1/admin/platform/tenants/{id}`:
- `domains`: list, add, set the primary with `PUT …/domains/{host}/primary`, and remove;
- `branding`: read and update, and upload or delete an asset with
  `PUT|DELETE …/branding/assets/{kind}`.

They take a Platform administrative context or an administrator's unscoped API key. An API
key records a domain as `garden`, anything else as `platform`. The capability is
`organization_domains_v1`. The platform administrator's organization page has Domains and
Branding panels.

An organization's first domain is its primary. Removing the primary makes the oldest
remaining domain primary.

## A new domain, step by step

1. Point DNS at the ingress and issue a certificate for the host.
2. Route the host to Bloem through a trusted proxy that forwards the client's host.
3. For sign-in through Authentik on that domain, register its callback in the provider:
   `https://<domain>/api/v1/auth/oauth/<installation>/callback` and
   `https://<domain>/api/v2/auth/oauth/<installation>/callback`.
4. Register the domain, in the organization page or from the storefront.

## Limits

- **Authentik:**
  - Signing out through the provider returns to the server's public URL.
  - App (native) sign-in through a provider keeps the public URL.
  - A provider sign-in refused on another organization's domain gets the OAuth
    handler's generic failure, not the address message.
- **Phone and TV apps:** the device sign-in page (`/activate`) and the server address the
  apps are told (`/api/v2/system/identity`) stay on the server's public URL. A device-code
  sign-in creates its session through the device login service, so the domain check does
  not see it.
- **Invitations from the platform:** an invitation follows the inviting administrator's
  organization. One a platform administrator sends into another organization links to the
  default domain.
- **Jellyfin and email sender:** the Jellyfin address and the email sender are server-wide.
- **Page shell cache:** it keeps one rendered page keyed by branding. With several brands
  it re-renders when the brand changes, which is correct but slower.
- **Silo switching:** a second organization ends it (see
  [Silo backend switching](silo-backend-switching.md)).
