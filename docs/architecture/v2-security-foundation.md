# Bloem v2 security foundation

Bloem retains the Silo-compatible `/api/v1` account login, profile picker,
PIN unlock, token refresh, and administrative projection. The native
`/api/v2` namespace is a separate additive boundary. Its discovery and
administration surface exposes:

- public `GET /api/v2/capabilities`; and
- public `GET /api/v2/server/identity`; and
- authenticated `GET /api/v2/organizations`; and
- administrative context exchange and management routes under
  `/api/v2/admin`; and
- the native client surface documented in
  [v2 client surface](v2-client-surface.md).

The capability response is the source of truth. It advertises legacy v1
compatibility, organization membership discovery, and tenant-bounded media
scope, alongside the media types this build serves and the feature tokens the
clients match against. Its exact response is:

```json
{
  "api": "v2",
  "identity_schema": 1,
  "features": {
    "legacy_silo_v1": true,
    "organization_memberships": true,
    "tenant_bounded_media_scope": true,
    "direct_profile_login": false,
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
[v2 API reference](../bloem-v2-api-reference.md#shared-lifecycle-mutation-idempotency-v1-and-v2).

Direct-profile login, shared-device pairing, and delegated administrative roles
are not implemented. The initial organization authority is the broad,
structured `organization_admin` role; organization administrators cannot
upload, edit, or activate Rego. Clients must not infer features from version
strings. `/api/v10/*` is not an alias and returns 404.

## Security invariants

- Existing v1 JWTs remain valid and carry no organization authority.
- V1 ignores organization headers and resolves only the default organization.
- Administrative context JWTs live separately from account sessions, expire
  within 15 minutes, and bind exactly one Platform or Organization authority.
  Browsers retain the token in memory only; persistent storage contains at most
  the selected non-secret context key.
- V2 organization-bound middleware takes selection only from validated
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

Failure before step 5 deletes the new account and issues no token. Verify:

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

Before enabling v2 in an environment:

1. run migration up/down/up tests on a disposable database;
2. run the v1 compatibility suite for setup, login, profile list, PIN unlock,
   admin projection, and refresh;
3. confirm v1 tokens and payloads contain no tenant identity;
4. confirm every v2 administrative route requires the matching short-lived
   context and advertises only implemented features;
5. resolve ownership ambiguity, if present; and
6. retain the pre-migration backup until the rollback window is explicitly
   closed.

The OPA composition, database acceptance, exact local commands, and failure
response guidance are in [OPA tenant authorization](opa-tenant-authorization.md).
