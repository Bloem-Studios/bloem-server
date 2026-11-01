# Bloem overlay: Authentication API

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to or replaces in Silo's [`docs/auth-api.md`](../../auth-api.md), which stays
> exactly as Silo wrote it. The section-by-section changes are in the
> [For developers](#for-developers) section.

Silo's authentication page explains how people sign in, how passwords and login
sessions work, and how the server protects them. Bloem signs people in the same
way. It differs where Bloem has something Silo does not: profiles with a sign-in of
their own, and organizations.

![Four things Bloem adds to signing in: a profile with its own sign-in, the domain check that refuses a sign-in on another organization's domain and names where to sign in, a provider sign-in that stays on the domain it started on, and a sign-in its organization revoked, which ends so the app asks to sign in again](../../architecture/images/overlay-auth/sign-in-additions.svg)

## What Bloem changes

- **A profile can have its own sign-in.** The household's main profile can give a
  profile an email address and password of its own, a **direct-profile
  credential**. Signing in with it goes straight to that one profile, and the
  session reaches only a short list of routes.
- **Every account belongs to one organization.** Creating an account also makes it
  a member of an organization.
- **Each organization signs in on its own address.** An organization can be served
  on its own domain. Signing in on another organization's domain is refused, and
  the message says where to sign in instead. An organization without a domain
  signs in at the server's public address. A sign-in or sign-out through the
  sign-in provider (Authentik) stays on the domain it started on. A domain decides
  how Bloem looks, never what someone may see.
- **A sign-in its organization revoked ends.** When an organization revokes an
  account, a sign-in that started before that is ended, and the app asks the
  person to sign in again.
- **Bloem's own password checks wait their turn.** When the server is too busy to
  check a password, Bloem's own sign-ins answer "try again shortly", as Silo's do.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Direct-profile credential, profile sign-in | `POST /api/v1/auth/profile-login`; managed at `/api/bloem/v1/profile-credentials/{id}` (capability `profile_credential_management_v1`) |
| The domain check | `auth.SignInDomainCheck`, `orgdomains.Directory.SignInCheck`, refusal `409 wrong_organization_domain` |
| A provider sign-in staying on its domain | `orgdomains.Directory.OAuthBaseURL`, `auth.OAuthHandler.SetRequestBaseURL` |
| A sign-in its organization revoked | `apimw.ErrSessionRevokedByOrganization`, answered `401 session_invalid` (`session_expired` on `/api/v2`) |
| Waiting their turn | Silo's admission limit, `internal/auth/passwordhash` |

## For developers

### Where this overlay came from

Bloem's direct-profile login (`POST /api/v1/auth/profile-login`) has had a
default-deny route allowlist since `16baf5558` (2026-08-14). Account creation also
provisions an organization membership. Bloem first documented these in the Silo
file in September 2026 and moved the text here in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/bloem_auth_ext.go`,
`bloem_profile_credentials.go`, `admin_tenant_members.go`,
`bloem_admin_platform.go`, `internal/auth/account_provisioner.go`). That review
rewrote the busy-refusal note, which had described Bloem's lifecycle-receipt
handlers, removed in `b017bfe84` (2026-10-26), and the period before
`bb92c4cfd` (2026-10-25), which put Bloem's own password hashing under Silo's
admission limit.

This rewrite added the organization-domain sections (organization domains landed
from `b95a3dda9` through `59616c4af`, 2026-11-01) and the revoked-sign-in section
(`25129bcbd`, 2026-11-01). From `3e2d306b2` through `63ef34b23` Silo changed
`docs/auth-api.md` three times: `9b77105e5` (#3021, 2026-10-28) verifies the
profile and acting admin on plugin routes; `5a205566a` (#3075, 2026-10-31) adds
the hourly request quota per login session; and `7ee9615f2` (#2925, 2026-10-31)
lets a v2 password change sign out the account's other devices. All three apply to
Bloem as written, and none changes a passage this overlay points at.

### Section by section

---

**Location:** section “Account passwords”, the first paragraph. **Bloem replaces** it with:

The password managed by this API belongs to the login account. Household profiles use
that account login; Bloem's optional direct-profile credentials are a separate contract
described below. Self-service password changes are restricted to the
active primary profile. An admin account may also change its password before selecting a profile,
but selecting a secondary profile removes that authority. API keys and impersonation sessions can
never change an account password.

---

**Location:** section “Login sessions on v2”, subsection “Access tokens after a role
change”, at its end. **Bloem adds:**

#### A sign-in its organization revoked

An organization-scoped session revocation bumps the membership's
`security_revision`, which stales every token carrying that revision. An
account-login session (`auth_sessions.profile_id IS NULL`) carries none and picks
its profile per request with `X-Profile-Id`. The trigger from migration
`20260923160000_bloem_org_session_revocations` records the revocation time per
account and organization, and the viewer resolver
(`internal/api/bloem_org_session_revocation.go`) refuses a profile selection by a
session created before it.

Since `25129bcbd` (2026-11-01) that refusal ends the session and the viewer-access
gate answers `401 session_invalid` (`session_expired` on `/api/v2`), the answer of
any ended session (`internal/api/middleware/bloem_viewer_session.go`). The
session's refresh is then refused as revoked, which is the answer clients sign
out on. Before, the gate answered `404 "Profile not found"` and left the session
alive, so the web app kept refreshing it and showed a broken home page with no
sign-in prompt. The refusal, `apimw.ErrSessionRevokedByOrganization`, still wraps
`access.ErrProfileNotFound`, so callers outside the gate keep refusing the
profile. Only sessions are checked: an API key has no `auth_sessions` row, and a
direct-profile session is already bound to its profile and revision.

---

**Location:** section “Sign-in rate limits”, subsection “When the server is too
busy to check a password”, after the paragraph beginning “The refusal is `503`
with `Retry-After: 1`: v2 `dependency_unavailable`, v1 error code…”. **Bloem adds:**

Since `bb92c4cfd` (2026-10-25), Bloem's own password hashing and checks run under
Silo's admission limit (`internal/auth/passwordhash`). The following answer a
busy refusal with Silo's `503 service_unavailable` and `Retry-After`:

- direct-profile login (`POST /api/v1/auth/profile-login`);
- setting or rotating a profile credential (hashing the new credential);
- tenant-member commands under `/api/v1/admin/tenants/{tenant_id}/members`,
  which also hash their command receipt.

Two account-password re-checks also run under the limit, but any failure there,
busy included, answers `503` without `Retry-After`: the check before a
profile-credential write (`unavailable`) and the check before an organization
ownership transfer (`tenant_unavailable`). v1 setup, signup and invitation
acceptance run Silo's handlers again since `b017bfe84` (2026-10-26).
`POST /api/v1/admin/users` creates the account through Bloem's
`bloemCreateUser`, which maps a busy refusal the same way. All of them answer
as described above.

---

**Location:** section “Sign-in attempts per name”, after the paragraph beginning
“Every password sign-in also spends a budget kept for the name it signs in with…”.
**Bloem adds:**

Direct-profile login (`POST /api/v1/auth/profile-login`) spends no per-name budget.
Its `profile_login` budget, like `profile_credentials` on the credential-management
routes, has no endpoint limit configured. Only the general per-address, account
and global request budgets apply, without the connection-address cap on forwarded
clients described under [sign-in rate limits](../../auth-api.md#sign-in-rate-limits).

---

**Location:** section “External sign-in”, subsection “OAuth sign-in flows”, after
the paragraph beginning “The frozen v1 routes share the login policy…” (the end of
the subsection). **Bloem adds:**

#### Organization domains at sign-in

Each organization can be served on its own domains
([organization domains](../../architecture/bloem-organization-domains.md);
`internal/orgdomains`, migration `20261101055208_bloem_organization_domains`,
capability `organization_domains_v1`). A domain decides presentation, never
access: an account belongs to one organization, and the tenant middleware takes
that organization from the account or session, whatever domain the request
arrived on.

**The domain check.** Every sign-in ends by creating a session, and
`Service.SetSignInDomainCheck` puts `Directory.SignInCheck` in front of the
session repository (`internal/auth/bloem_sign_in_domain.go`). When the request's
domain is registered to another organization than the account's, the sign-in is
refused before a session exists with `auth.OrganizationDomainError`. Password
sign-in (v1 and v2 `login`) and direct-profile login answer
`409 wrong_organization_domain`, "This account signs in at
https://<primary domain>." (`internal/api/handlers/bloem_sign_in_domain.go`); the
address is the server public URL when the account's organization has no primary
domain. `/api/v2` renders it as `conflict`, and the sign-in page shows the detail.
It is 409, not 403, because v2 renders a 403 at sign-in as `permission_denied`,
which the sign-in page reports as a disabled account. An organization without a
domain signs in at the server public URL, and its accounts are let in on that
URL's host even when the host is registered to another organization. Requests on
unregistered hosts (IP addresses, LAN names) and impersonation sessions are never
refused.

**Provider sign-in and sign-out.** A web sign-in through an OAuth provider
(Authentik) stays on the organization domain it started on: the provider sends the
browser back to that domain's callback, and the browser finishes on that domain's
completion page, where its cookies are (`Directory.OAuthBaseURL`, installed with
`auth.OAuthHandler.SetRequestBaseURL`). A provider sign-out started on an
organization domain returns the browser to that domain's `/login`. So each domain
needs, at the provider, both callbacks,
`https://<domain>/api/v1/auth/oauth/<installation>/callback` and
`https://<domain>/api/v2/auth/oauth/<installation>/callback`, and, when provider
sign-out is on, `https://<domain>/login` as a post-logout redirect URI. On any
other host the flow keeps the public URL and bounces there, as Silo's text above
describes. `internal/auth/oauth_handler.go` is a declared seam for this: one field
and five call sites (the public-origin check, the callback address, the completion
page twice, and the sign-out's return address).

Limits: an app's (native) sign-in through a provider keeps the public URL; a
provider sign-in refused on another organization's domain gets the OAuth handler's
generic failure, not the address message; and a device-code sign-in creates its
session through the device login service, so the domain check does not see it.

---

**Location:** section “Ordinary v2 authentication”, the paragraph beginning “Invited
signup commits invite consumption…”. **Bloem replaces** it with:

Invited signup commits invite consumption and account creation together. With the
PostgreSQL profile provider, the optional default profile joins that transaction.
Both transactional account entrypoints check provider support before inserting an
account, provisioning membership or opening a profile store. Nil, unsupported and
SQLite providers cannot create a default profile through those transactions
(`ErrTransactionalProfileUnavailable`); rejection does not create SQLite files.
PostgreSQL and the notification decorator preserve transactional support. Profileless requests
keep their existing behavior; no cross-store transaction or backend conversion is implied.

---

**Location:** section “Ordinary v2 authentication”, after the paragraph beginning
“Apple and Android adoption must be verified against each client's…”. **Bloem adds:**

#### Bloem household profile credentials

When `profile_credential_management_v1` is advertised, the embedded web exposes
status, set/rotate and disable at `/api/bloem/v1/profile-credentials/{id}`. These
GET/PUT/DELETE operations require a non-impersonated account session and the existing
household/PIN authority. Writes additionally require the enabled local account password
and a positive `expected_revision`, compared under the profile row lock. A stale write
returns `409 credential_revision_conflict` and preserves newer credentials and direct
sessions. Successful rotation or disable revokes direct sessions for that profile.

This is separate from account-password changes and admin-context authority. Direct
sessions, API keys and SSO-only reauthentication cannot manage profile credentials.
Browser direct-profile login and shared-device profile pairing remain unsupported;
the existing direct-session allowlist is unchanged. See the
[native contract](../../bloem-api-reference.md#embedded-web-workflow-adapters) for fields
and errors. Account-device activation remains a separate sign-in protocol.

#### September 30 upstream integration

Upstream account password-reset links and temporary-password flows are described
in [password resets](../../architecture/password-resets.md). The setup owner flag
belongs to the account; it does not replace Bloem organization ownership or
primary-profile authority. Account policy lives on `users`, as in Silo, since
`c60b7008e` (2026-10-26); an organization membership only links the account to
its one organization. Bloem keeps its organization-scoped session revocation and
carries those Silo account fields through its own service integration. See
[adapter boundaries](../../architecture/bloem-upstream-adapters.md) and the
[deployment record](../../operations/2026-09-30-upstream-deployment.md).

#### October 27 integration: tokens bound to their session's account

Silo `d8d053f75` (#3140), merged into Bloem in `68070f04b` (2026-10-27), binds a
token to its session's account: `ActiveSession` takes the account the
credential names. Bloem's own session lookups pass that account too:
the preview-aware `ActivePreviewSession` and `ActiveSessionAuthority`, the
middleware's `sessionAuthority`, the native-video session check
(`ActiveSessionRole`) and the tenant administrative context. So the
plugin router, preview, tenant-admin and native-video paths keep Silo's
binding. A direct-profile session still answers with the user role inside
Silo's query.

#### October 7 integration: credential checks and network identity

Bloem `c87b44545228c93f909275c0b5c4fc1d6a289e1b` includes Silo through
`74158b4a8`. The upstream distinction between invalid credentials and an
unavailable session store also applies to Bloem's direct-profile refresh
adapter. A missing credential service or a store/transport failure while
revalidating the current profile returns an unavailable error without revoking
the session. A mismatched retained binding or confirmed revoked subject still
ends it. The shared HTTP contract uses `503` with `Retry-After` (v1
`service_unavailable`, v2 `dependency_unavailable`); clients retain their
credentials and retry with bounded backoff. This does not broaden the
direct-profile route allowlist or grant access to native administration.

Linking a network-only identity provider, such as the Tailscale provider,
retains the account's local password. A confirmed refusal by an enabled network
provider can still block local sign-in except for a break-glass account; a
later positive provider check clears that refusal. An installation with OIDC
or LDAP bindings follows the external-provider password policy instead.
See [external sign-in](../../architecture/external-sign-in.md) for the upstream
policy and [native auth authority](../../bloem-api-reference.md#auth-model-account-viewer-and-administrative-authority)
for Bloem's account, viewer and administrative boundaries.

Native-storage management requires the existing signed administrative context
and rechecks current session/resource authority. Source ownership is separate
from organization membership and source-use entitlement. Read the
[storage architecture](../../architecture/bloem-native-storage.md) for the
tenancy checks; successful deployment probes are not proof of provider
admission.

### Related pages

- [Organization domains](../../architecture/bloem-organization-domains.md): how a
  domain picks its organization, branding and email links, and how to add one.
- [How organizations work](../../architecture/bloem-organizations.md).
- [Security foundation](../../architecture/bloem-security-foundation.md): session
  revocation and direct-profile sessions.
- [Settings API overlay](settings-api.md): the direct-profile session's settings
  routes.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_auth.py` into
`docs/architecture/images/overlay-auth/`. Redraw it there, not by hand.
