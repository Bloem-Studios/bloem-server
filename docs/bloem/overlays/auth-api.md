# Bloem overlay: Authentication API

**What this overlays.** Silo's [`docs/auth-api.md`](../../auth-api.md). Bloem
replaces one paragraph each of “Account passwords” and “Ordinary v2
authentication”, and adds to “When the server is too busy to check a
password”, “Sign-in attempts per name” and “Ordinary v2 authentication”. An
*overlay* is a Bloem-owned page that records Bloem's additions to, or
replacements of, sections of a Silo-owned document. The Silo file stays
exactly as Silo wrote it. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem adds two things:

- *direct-profile credentials*: an optional login email and password for one
  household profile, which signs in straight to that profile
  (`POST /api/v1/auth/profile-login`; default-deny route allowlist since
  `16baf5558`, 2026-08-14);
- *organizations*: every account is a member of one organization, so account
  creation also provisions an organization membership.

Bloem first documented these in the Silo file in September 2026 and moved the
text here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/bloem_auth_ext.go`,
`bloem_profile_credentials.go`, `admin_tenant_members.go`,
`bloem_admin_platform.go`, `internal/auth/account_provisioner.go`). Silo has not
changed this document since the previous review (Silo `6de6d5e5e`). This review
rewrote the busy-refusal note. It had described Bloem's lifecycle-receipt
handlers, removed in `b017bfe84` (2026-10-26), and the period before
`bb92c4cfd` (2026-10-25), which put Bloem's own password hashing under Silo's
admission limit.

Index: [Bloem documentation](../README.md).

---

**Location:** section “Account passwords”, the first paragraph. **Bloem replaces** it with:

The password managed by this API belongs to the login account. Household profiles use
that account login; Bloem's optional direct-profile credentials are a separate contract
described below. Self-service password changes are restricted to the
active primary profile. An admin account may also change its password before selecting a profile,
but selecting a secondary profile removes that authority. API keys and impersonation sessions can
never change an account password.

---

**Location:** section “When the server is too busy to check a password”, after the paragraph beginning “The refusal is `503` with `Retry-After: 1`: v2 `dependency_unavailable`, v1 error code…”. **Bloem adds:**

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

**Location:** section “Sign-in attempts per name”, after the paragraph beginning “Every password sign-in also spends a budget kept for the name it signs in with…”. **Bloem adds:**

Direct-profile login (`POST /api/v1/auth/profile-login`) spends no per-name budget.
Its `profile_login` budget, like `profile_credentials` on the credential-management
routes, has no endpoint limit configured. Only the general per-address, account
and global request budgets apply, without the connection-address cap on forwarded
clients described under [sign-in rate limits](../../auth-api.md#sign-in-rate-limits).

---

**Location:** section “Ordinary v2 authentication”, the paragraph beginning “Invited signup commits invite consumption…”. **Bloem replaces** it with:

Invited signup commits invite consumption and account creation together. With the
PostgreSQL profile provider, the optional default profile joins that transaction.
Both transactional account entrypoints check provider support before inserting an
account, provisioning membership or opening a profile store. Nil, unsupported and
SQLite providers cannot create a default profile through those transactions
(`ErrTransactionalProfileUnavailable`); rejection does not create SQLite files.
PostgreSQL and the notification decorator preserve transactional support. Profileless requests
keep their existing behavior; no cross-store transaction or backend conversion is implied.

---

**Location:** section “Ordinary v2 authentication”, after the paragraph beginning “Apple and Android adoption must be verified against each client's…”. **Bloem adds:**

## Bloem household profile credentials

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

## September 30 upstream integration

Upstream account password-reset links and temporary-password flows are described
in [password resets](../../architecture/password-resets.md). The setup owner flag
belongs to the account; it does not replace Bloem organization ownership or
primary-profile authority. Account policy lives on `users`, as in Silo, since
`c60b7008e` (2026-10-26); an organization membership only links the account to
its one organization. Bloem keeps its organization-scoped session revocation and
carries those Silo account fields through its own service integration. See [adapter boundaries](../../architecture/bloem-upstream-adapters.md)
and the [deployment record](../../operations/2026-09-30-upstream-deployment.md).


## October 27 integration: tokens bound to their session's account

Silo `d8d053f75` (#3140), merged into Bloem in `68070f04b` (2026-10-27), binds a
token to its session's account: `ActiveSession` takes the account the
credential names. Bloem's own session lookups pass that account too:
the preview-aware `ActivePreviewSession` and `ActiveSessionAuthority`, the
middleware's `sessionAuthority`, the native-video session check
(`ActiveSessionRole`) and the tenant administrative context. So the
plugin router, preview, tenant-admin and native-video paths keep Silo's
binding. A direct-profile session still answers with the user role inside
Silo's query.

## October 7 integration: credential checks and network identity

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
