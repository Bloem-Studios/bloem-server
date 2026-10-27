# Bloem native API security foundation

> **Provenance.** Bloem addition: the tenant foundation (organizations, memberships,
> platform ownership, administrative contexts and the native namespace) that Bloem
> layers over Silo's single-tenant account, profile and session model. Written with that
> foundation in `c02d2445b` (2026-08-13). Later changes that matter: v2 socket tickets
> bound to tenant authority (`9ab044057`, `6d52b1a0d`, 2026-10-09/11); account policy
> back on Silo's `users` table with one membership per account (`c60b7008e`,
> 2026-10-26); lifecycle receipts removed (`b017bfe84`, 2026-10-26); Silo's
> session-to-account token binding and playback ownership rules applied to Bloem's paths
> (Silo merge `68070f04b`, 2026-10-27). **Status:** current at `e852f92bf`, except the
> [Rollback](#rollback) procedure, which applies only to the August 2026 foundation
> release. Silo's own authentication model is described in [API contract](api-contract.md)
> and [external sign-in](external-sign-in.md).

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
  [native client surface](bloem-client-surface.md).

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
    "progress_sync_v1"
  ]
}
```

`features` and `feature_tokens` are not two spellings of one list. `features`
is the fixed object of identity-model booleans this document has always
published; `feature_tokens` is the open, versioned allowlist a client matches
against, and every later capability lands there. Both grow additively.

Direct-profile login is implemented on `/api/v1/auth/profile-login` when its dependencies
are wired; the capability boolean reflects that wiring. The default-deny direct-session
route allowlist still rejects Silo v2 and the native Bloem namespace (apart from the
audiobook and ebook request viewer routes), so the embedded browser must use account login. Household credential status/set/clear is available through
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
  account's own organization: its one active membership, which is the default
  organization for upgraded accounts. Direct-profile sessions instead revalidate their explicit
  organization, membership and revisions; they cannot be projected into another tenant.
- Administrative context JWTs live separately from account sessions, expire
  within 15 minutes, and bind exactly one Platform or Organization authority.
  Browsers retain the token in memory only; persistent storage contains at most
  the selected non-secret context key.
- Session checks are bound to the account the credential names. Since the Silo
  merge `68070f04b` (2026-10-27; Silo #3140), Silo's `ActiveSession` takes that
  account, and so do Bloem's preview-aware lookups (`ActivePreviewSession`,
  `ActiveSessionAuthority` in `internal/auth/bloem_preview_principal.go`), which the
  auth middleware and the plugin router use, the administrative context middleware
  (`internal/api/middleware/admin_context.go`) and native video's login-session check
  (`internal/api/bloem_native_video.go`). A token whose login session belongs to another
  account is refused.
- Playback progress and stop belong to the acting profile, not just the account. Silo's
  `ownsPlaybackSession` (#3133) guards v1 progress and stop, and Bloem's durable stop
  (`internal/api/handlers/bloem_playback_stop.go`) applies the same rule, so a sibling
  profile on the same account cannot stop another profile's playback
  (`TestBloemBridgeStopRefusesASiblingProfile`).
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

Setup runs inside Silo's `UserRepository.ClaimInitialSetup` transaction, which takes
the deployment-wide `InitialSetupAdvisoryLock` and re-counts accounts after acquiring
it, so a waiting contender finds the winner's account and gets
`ErrSetupAlreadyComplete`. Bloem's `SetupInitialUserInTransaction`
(`internal/auth/bloem_service.go`) creates the account, the optional profile, the
ownership activation and the login session in that one transaction; failure before
commit rolls all of it back. (Until `b017bfe84`, 2026-10-26, a separate lifecycle-receipt
path wrapped setup in its own transaction.) The Bloem ownership
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

## Account provisioning and login policy

Organization-specific account creation creates the membership and assigns the account a
default access group only in the selected organization. It must not silently add a
default-organization membership; an account holds at most one membership
(`organization_memberships_one_per_account`, since `c60b7008e`). The membership
provisioner can adopt the exact active, same-role row seeded by identity creation in the
same transaction; it cannot reactivate an invited or suspended row, change its role, or
increment its revisions as a side effect.
Organization provisioning uses `FOR NO KEY UPDATE`: concurrent seeded memberships already
hold foreign-key `KEY SHARE` locks, so upgrading both to `FOR UPDATE` would deadlock.
Provisioning does not change organization keys, and competing organization updates remain
serialized. No transaction retry substitutes for that lock compatibility.

Password-login responses resolve the authenticated account's default-organization viewer
policy before reporting `download_allowed`. The login request cannot select a tenant via
headers or an ambient profile context. Missing viewer authority reports downloads as
unavailable without preventing account login for administrative-context selection. This
response is informational: subsequent resource requests still enforce their own authority.

The v1 account-password capability and mutation routes preserve profileless account
administration. A selected profile first resolves tenant authority, then passes the shared
ownership/PIN viewer guard. Direct-profile sessions remain rejected before either lookup.

Logout revokes a login session, not an API key. The shared handler refuses API-key or
sessionless claims before invoking session revocation; valid account and admitted
direct-profile sessions retain self-logout. v1 invitation acceptance hides unknown,
expired, revoked and consumed invitations behind `404 not_found`; a pending invitation's
duplicate-account conflict remains `409 already_used`.

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
available; native organization-bound requests do not.

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

> **Historical.** This procedure belongs to the August 2026 tenant-foundation release.
> At `e852f92bf` several later Bloem migrations are forward-only and refuse `Down` (for
> example `20261013161500_remove_bloem_compatibility.sql` and
> `20261026003000_bloem_account_policy_on_users.sql`), so a current deployment rolls back
> by restoring a backup taken before the migration, as the deployment receipts describe
> (for example [October 27](../operations/2026-10-27-main-deployment.md#rollback)).

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
4. confirm every native organization/platform administrative route requires the
   matching short-lived context and advertises only implemented features;
5. resolve ownership ambiguity, if present; and
6. retain the pre-migration backup until the rollback window is explicitly
   closed.

The OPA composition, database acceptance, exact local commands, and failure
response guidance are in [OPA tenant authorization](opa-tenant-authorization.md).

## Native Live TV and Xtream authority

Live TV administration is distinct from organization/platform administrative context.
It requires authenticated account authority, the acting-administrator gate, the
account's selected primary profile and any required PIN proof. General profileless
account administration does not imply profileless Live TV access. Direct-profile
credentials and signed delivery proofs cannot create providers or manage guides.

[Xtream integration](xtream-live-tv.md) keeps provider credentials encrypted with
tuner/origin binding and no plaintext fallback. Only fixed live/XMLTV endpoints
are requested; redirects and provider-supplied alternate source/artwork URLs are
refused. The server retains opaque channel references and feeds encoders through
owned pipes, not credential-bearing URLs. UI writes capture account/profile/PIN
identity and require explicit reconciliation after uncertain outcomes, never replay.
`xtream_supported` is installed support, not a grant or proof of encryption readiness.

Mounted authority, storage and encoder regressions are separate from the
[September 19 deployment smoke checks](../operations/2026-09-19-xtream-deployment.md).
Neither certifies actual provider playback, every browser or the unfinished
scenario suite. No authorization or secret-disclosure rule was relaxed for CI.

## Native seasonal delivery and events authority

`seasonal_viewer_v1` identifies the authenticated native `/ambience` route only when
all required resolvers are mounted. It requires a verified profile in the current
active tenant. A verified legacy viewer in the initializing default organization
may receive public packs during Silo-compatible switching; this does not activate
that organization or permit organization-targeted packs. Public packs and
current-organization packs are filtered with active membership in the same database read as their contents. Public login branding never
includes organization-targeted packs. Direct-profile admission remains default-deny.

The Bloem v2 events adapter captures the original authenticated principal and resolved
tenant in the ticket store's optional, server-owned `SocketIdentity.AuthorityBinding`.
The contextless upgrade and periodic checks revalidate that same session, account
incarnation, organization, membership, revisions, profile ownership, role, device and
impersonator. They cannot choose a new tenant. Shared single-use consumption, origin,
protocol, expiry, PIN, access fingerprint and revocation checks remain in force.
Ordinary Silo-compatible account/OAuth JWTs omit device identity. When such a
legacy session delegates a ticket, the adapter captures the stored session device
once and compares it on every subsequent check. Explicit tenant or device claims
never use this fallback, and client headers cannot change the captured device.
Unbound older tickets fail closed. The binding is not accepted from or exposed to clients.

Playback-control v2 tickets use the same Bloem authority adapter. Authenticated
ticket creation captures the original tenant; the public WebSocket upgrade
restores and revalidates that binding before the shared handler checks playback
session, profile and installation ownership. The shared handler's periodic
checks use the same validator. Resolving a fresh tenant from only the account
and profile could retarget authority; accepting the request without tenant
context instead rejects otherwise valid control tickets. Neither is permitted.
The wire protocol, one-use ticket consumption, Origin checks, PIN verification,
access fingerprint, ownership and revocation rules remain unchanged.

Disposable rebuilt-browser acceptance received a real v2 events `hello` and kept the
socket healthy for 18 seconds, beyond its 15-second authority recheck. The same run
completed home/seasonal HTTP reads without runtime errors. Current-organization seasonal
delivery and exclusion from public branding also passed; foreign-tenant and concurrent
retargeting exclusion are covered separately by database regressions.
