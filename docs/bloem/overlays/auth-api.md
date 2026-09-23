# Bloem overlay: Authentication API

Bloem additions and overrides for the upstream Silo document [`docs/auth-api.md`](../../auth-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Account passwords”. **Bloem replaces** the corresponding upstream passage with:

The password managed by this API belongs to the login account. Household profiles use
that account login; Bloem's optional direct-profile credentials are a separate contract
described below. Self-service password changes are restricted to the
active primary profile. An admin account may also change its password before selecting a profile,
but selecting a secondary profile removes that authority. API keys and impersonation sessions can
never change an account password.

---

**Location:** section “Ordinary v2 authentication”. **Bloem replaces** the corresponding upstream passage with:

Invited signup commits invite consumption and account creation together. With the
PostgreSQL profile provider, the optional default profile joins that transaction.
Both transactional account entrypoints check provider support before inserting an
account, provisioning membership or opening a profile store. Nil, unsupported and
SQLite providers cannot create a default profile through those transactions; rejection
does not create SQLite files. The returned store is checked independently. PostgreSQL
and the notification decorator preserve transactional support. Profileless requests
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
