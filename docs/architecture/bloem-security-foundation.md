# Bloem native API security foundation

Bloem retains the Silo-compatible `/api/v1` account login, profile picker,
PIN unlock, token refresh, and administrative projection. The native
`/api/bloem/v1` namespace is a separate additive boundary. Its discovery and
administration surface exposes:

- public `GET /api/bloem/v1/capabilities`; and
- public `GET /api/bloem/v1/server/identity`; and
- authenticated `GET /api/bloem/v1/organizations`; and
- administrative context exchange and management routes under
  `/api/bloem/v1/admin`; and
- the native client surface documented in
  [v2 client surface](bloem-client-surface.md).

The capability response is the source of truth. It advertises legacy v1
compatibility, organization membership discovery, and tenant-bounded media
scope, alongside the media types this build serves and the feature tokens the
clients match against. The following is an illustrative subset, not an exhaustive token
list; consult the runtime response and [API reference](../bloem-api-reference.md):

```json
{
  "api": "bloem/v1",
  "identity_schema": 1,
  "features": {
    "legacy_silo_v1": true,
    "organization_memberships": true,
    "tenant_bounded_media_scope": true,
    "direct_profile_login": true,
    "shared_device_pairing": false,
    "delegated_admin_roles": false
  },
  "media_types": ["movie", "series", "episode", "audiobook", "ebook", "manga"],
  "feature_tokens": [
    "playback_plan_v3",
    "neutral_playback_v3_contract_v1",
    "layout_aware_passthrough",
    "playback_route_diagnostics",
    "device_quirks_v1",
    "seek_reanchor_v1",
    "output_change_v1",
    "direct_stream_resume_v1",
    "plan_source_duration_v1",
    "declared_event_channels",
    "watch_document_v1",
    "device_pairing_v1",
    "progress_sync_v1",
    "lifecycle_idempotency_v1"
  ]
}
```

`features` and `feature_tokens` are not two spellings of one list. `features`
is the fixed object of identity-model booleans this document has always
published; `feature_tokens` is the open, versioned allowlist a client matches
against, and every later capability lands there. Both grow additively.

`lifecycle_idempotency_v1` is present only when the shared lifecycle
coordinator is wired. Once the rollout is finalized,
`lifecycle_idempotency_required_v1` is also advertised and registered lifecycle
mutations without `Idempotency-Key` fail with `428`. The public probe remains
available if its rollout-phase read fails: it keeps the support token, omits the
required token, and still answers `200`. Client key/retry behavior and the
`409`/`503` contract are documented in the
[native API reference](../bloem-api-reference.md#shared-lifecycle-mutation-idempotency-compatible-and-native-surfaces).

Direct-profile login is implemented on `/api/v1/auth/profile-login` when its dependencies
are wired; the capability boolean reflects that wiring. The default-deny direct-session
route allowlist still rejects Silo v2 and the native Bloem namespace, so the embedded
browser must use account login. Household credential status/set/clear is available through
`/api/bloem/v1/profile-credentials/{id}` when `profile_credential_management_v1` is advertised.
It requires a non-impersonated account login, existing household/PIN permission, and the
current local account password for writes. Direct sessions and API keys cannot manage
these credentials; SSO-only reauthentication is not implemented. Credential changes
require a positive `expected_revision`, checked under the profile row lock, then rotate
revisions and revoke direct sessions. Stale writes return `409 credential_revision_conflict`
without changing the newer credentials or sessions. The browser clears form secrets after attempts
and does not keep them in query/mutation caches.

Shared-device pairing and delegated administrative roles remain unimplemented and their
booleans remain false. The initial organization authority is the broad,
structured `organization_admin` role; organization administrators cannot
upload, edit, or activate Rego. Clients must not infer features from version
strings. `/api/v10/*` is not an alias and returns 404.

## Security invariants

- Existing v1 JWTs remain valid and carry no organization authority.
- Legacy account requests ignore organization-selection headers and project into the
  default organization. Direct-profile sessions instead revalidate their explicit
  organization, membership and revisions; they cannot be projected into another tenant.
- Administrative context JWTs live separately from account sessions, expire
  within 15 minutes, and bind exactly one Platform or Organization authority.
  Browsers retain the token in memory only; persistent storage contains at most
  the selected non-secret context key.
- Native organization-bound middleware takes selection only from validated
  session claims, then rechecks the current organization, membership, policy
  revision, and security revision before attaching tenant context.
- Missing, suspended, hidden, ambiguous, foreign, or stale tenant state fails
  closed without disclosing whether a hidden organization exists.
- Organization listing occurs before selection. It returns only the account's
  active memberships in active organizations and omits owners, member counts,
  and other organizations.
- V1 retains the legacy account ceiling for profile-less default-organization
  requests. A selected profile resolves its canonical access group from the
  profile's required organization-qualified assignment; a group from another
  organization never resolves. Deleting a non-default group reassigns its
  profiles to that organization's default group in the deletion transaction.
- Media visibility is bounded before catalog SQL runs. An organization may see
  its own folders and platform-owned folders with an active explicit
  entitlement. Ownership and entitlement establish availability only; access
  groups, profile restrictions, disabled-library settings, and custom policy
  may narrow that set but cannot widen it.
- Missing tenant facts, stale revisions, unavailable entitlement state, policy
  errors, malformed or undefined decisions, and evaluation timeouts fail
  closed. Hidden, foreign, and non-entitled resources are not disclosed.

## Clean setup

On a database with no users, migrations create one initializing default
organization and unassigned platform security row. Initial setup performs one
protected sequence:

1. create the account;
2. provision its active default-organization membership;
3. create the optional default profile with its organization identity;
4. atomically assign platform and organization ownership and activate the
   organization; and
5. create the session and tokens.

Account/profile creation and ownership activation share a transaction; failure before
commit rolls it back. Login-session issuance begins only after commit. The Bloem ownership
adapter and notification-decorated user store must preserve the transactional extensions;
unsupported backing stores fail closed, never emulate a separate commit. When a default
profile is requested, both account transaction entrypoints check the provider's
transactional capability before any account or membership insert or user-store lookup.
This prevents filesystem side effects from opening an unsupported SQLite store before
the PostgreSQL transaction rejects the request. The returned store's transactional
writer is still checked independently. Requests without a default profile keep their
existing provisioning behavior. Verify:

```sql
SELECT owner_account_id, policy_revision, ownership_resolution_required
FROM platform_security;

SELECT id, slug, status, owner_account_id, policy_revision, is_default
FROM organizations;

SELECT organization_id, account_id, status, legacy_role, security_revision
FROM organization_memberships
ORDER BY organization_id, account_id;
```

Exactly one default organization must exist. Its owner and the platform owner
must be the setup account; both organization and membership must be active.
Protected activation accepts only an enabled account whose legacy account role
and organization membership role are both `admin`; ordinary, disabled, invited,
or suspended accounts cannot win an ownership race.

## Upgrade behavior

An upgrade with exactly one enabled legacy administrator automatically assigns
that account as platform and default-organization owner. Disabled admins do not
create ambiguity. Existing users receive active memberships, profiles retain
their IDs and policy fields, and existing access groups/profiles attach to the
default organization. Unassigned profiles are backfilled to their
organization's default group, after which every profile has exactly one
canonical group. Existing media folders become platform-owned and the
default platform catalog is materialized as active default-organization
entitlements so upgraded v1 users retain their prior library visibility.

When multiple enabled legacy administrators exist, migrations deliberately set
`ownership_resolution_required=true`, leave both owners null, and keep the
default organization initializing. V1 login and profile switching remain
available; native organization-bound v2 requests do not.

### Resolve multiple-admin ambiguity

Back up the database, stop write traffic, and choose one enabled legacy admin
after out-of-band identity verification. In one `psql` transaction, substitute
the chosen integer account ID for `CHOSEN_ACCOUNT_ID`:

```sql
BEGIN;

SELECT id, username, email, enabled, role
FROM users
WHERE id = CHOSEN_ACCOUNT_ID
FOR UPDATE;

SELECT singleton, owner_account_id, ownership_resolution_required
FROM platform_security
FOR UPDATE;

SELECT id, owner_account_id, status, is_default
FROM organizations
WHERE is_default
FOR UPDATE;

UPDATE platform_security
SET owner_account_id = CHOSEN_ACCOUNT_ID,
    ownership_resolution_required = false,
    policy_revision = policy_revision + 1,
    updated_at = now()
WHERE singleton
  AND owner_account_id IS NULL
  AND ownership_resolution_required;

UPDATE organizations
SET owner_account_id = CHOSEN_ACCOUNT_ID,
    status = 'active',
    policy_revision = policy_revision + 1,
    updated_at = now()
WHERE is_default
  AND owner_account_id IS NULL
  AND status = 'initializing';

UPDATE organization_memberships AS membership
SET status = 'active',
    legacy_role = 'admin',
    security_revision = security_revision + 1,
    updated_at = now()
FROM organizations
WHERE organizations.is_default
  AND membership.organization_id = organizations.id
  AND membership.account_id = CHOSEN_ACCOUNT_ID;

COMMIT;
```

Abort rather than commit unless the chosen user is enabled, has legacy role
`admin`, both protected updates affect exactly one row, and the membership
update affects exactly one row. Re-run the verification queries afterward.

## Rollback

Rollback is allowed only before operators or later phases start writing
non-default organizations, organization-specific profile group assignments,
resource ownership, or entitlements that cannot be represented by v1. Take a
tested backup, stop Bloem, and roll back the application binary and schema
together. Keeping a new binary against the old schema is unsupported.

From a matching source checkout with the deployment environment file:

```sh
make migrate-status ENV_FILE=/path/to/deployment.env
make migrate-down-to VERSION=20260812163547 ENV_FILE=/path/to/deployment.env
```

The complete rollback crosses the access-group and resource-tenancy migrations
before removing `platform_security`, `organizations`,
`organization_memberships`, and the additive organization/profile group
columns. It restores the global access-group name constraint. It does not
change legacy users, profiles, account-level access-group assignments,
passwords, sessions, or watch state. Verify those legacy counts and sampled
rows before starting the previous binary. Do not use this rollback after the
representability boundary above has been crossed; restore the tested backup
instead.

## Release gate

Before enabling native tenant administration in an environment:

1. run migration up/down/up tests on a disposable database;
2. run the v1 compatibility suite for setup, login, profile list, PIN unlock,
   admin projection, and refresh;
3. confirm legacy account login tokens do not acquire administrative-context authority,
   and direct-profile sessions retain their explicit tenant binding;
4. confirm every native administrative route requires the matching short-lived
   context and advertises only implemented features;
5. resolve ownership ambiguity, if present; and
6. retain the pre-migration backup until the rollback window is explicitly
   closed.

The OPA composition, database acceptance, exact local commands, and failure
response guidance are in [OPA tenant authorization](opa-tenant-authorization.md).

## Native seasonal delivery and events authority

`seasonal_viewer_v1` identifies the authenticated native `/ambience` route only when
all required resolvers are mounted. It requires a verified profile in the current
active tenant. Public packs and current-organization packs are filtered with active
membership in the same database read as their contents. Public login branding never
includes organization-targeted packs. Direct-profile admission remains default-deny.

The Bloem v2 events adapter captures the original authenticated principal and resolved
tenant in the ticket store's optional, server-owned `SocketIdentity.AuthorityBinding`.
The contextless upgrade and periodic checks revalidate that same session, account
incarnation, organization, membership, revisions, profile ownership, role, device and
impersonator. They cannot choose a new tenant. Shared single-use consumption, origin,
protocol, expiry, PIN, access fingerprint and revocation checks remain in force.
Unbound older tickets fail closed. The binding is not accepted from or exposed to clients.

Disposable rebuilt-browser acceptance received a real v2 events `hello` and kept the
socket healthy for 18 seconds, beyond its 15-second authority recheck. The same run
completed home/seasonal HTTP reads without runtime errors. Current-organization seasonal
delivery and exclusion from public branding also passed; foreign-tenant and concurrent
retargeting exclusion are covered separately by database regressions.
