# Vondel v10 security foundation

Vondel retains the Silo-compatible `/api/v1` account login, profile picker,
PIN unlock, token refresh, and administrative projection. The native
`/api/v10` namespace is a separate additive boundary. This phase exposes only:

- public `GET /api/v10/capabilities`; and
- authenticated `GET /api/v10/organizations`.

The capability response is the source of truth. Direct-profile login, shared
device pairing, and delegated administrative roles remain `false` until their
own reviewed phases ship. Clients must not infer features from version strings.

## Security invariants

- Existing v1 JWTs remain valid and carry no organization authority.
- V1 ignores organization headers and resolves only the default organization.
- V10 organization-bound middleware takes selection only from validated
  session claims, then rechecks the current organization, membership, policy
  revision, and security revision before attaching tenant context.
- Missing, suspended, hidden, ambiguous, foreign, or stale tenant state fails
  closed without disclosing whether a hidden organization exists.
- Organization listing occurs before selection. It returns only the account's
  active memberships in active organizations and omits owners, member counts,
  and other organizations.
- Profile organization/access-group columns are an additive shadow. V1 still
  enforces `users.access_group_id` until a later parity-proven cutover.

## Clean setup

On a database with no users, migrations create one initializing default
organization and unassigned platform security row. Initial setup performs one
protected sequence:

1. create the account;
2. provision its active default-organization membership;
3. atomically assign platform and organization ownership and activate the
   organization; and
4. create the session and tokens.

Failure before step 4 deletes the new account and issues no token. Verify:

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

## Upgrade behavior

An upgrade with exactly one enabled legacy administrator automatically assigns
that account as platform and default-organization owner. Disabled admins do not
create ambiguity. Existing users receive active memberships, profiles retain
their IDs and policy fields, and existing access groups/profiles attach to the
default organization.

When multiple enabled legacy administrators exist, migrations deliberately set
`ownership_resolution_required=true`, leave both owners null, and keep the
default organization initializing. V1 login and profile switching remain
available; native organization-bound v10 requests do not.

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

Rollback is allowed only before later phases start writing multi-organization
or profile-specific group state. Take a tested backup, stop Vondel, and roll
back the application binary and schema together. Keeping a new binary against
the old schema is unsupported.

From a matching source checkout with the deployment environment file:

```sh
make migrate-status ENV_FILE=/path/to/deployment.env
make migrate-down-to VERSION=20260811145848 ENV_FILE=/path/to/deployment.env
```

The down migration removes `platform_security`, `organizations`,
`organization_memberships`, and the additive organization/profile group
columns. It restores the global access-group name constraint. It does not
change legacy users, profiles, account-level access-group assignments,
passwords, sessions, or watch state. Verify those legacy counts and sampled
rows before starting the previous binary.

## Release gate

Before enabling v10 in an environment:

1. run migration up/down/up tests on a disposable database;
2. run the v1 compatibility suite for setup, login, profile list, PIN unlock,
   admin projection, and refresh;
3. confirm v1 tokens and payloads contain no tenant identity;
4. confirm v10 has no write routes and advertises only implemented features;
5. resolve ownership ambiguity, if present; and
6. retain the pre-migration backup until the rollback window is explicitly
   closed.
