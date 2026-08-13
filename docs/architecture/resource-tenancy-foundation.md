# Resource tenancy foundation

Migration `20260813090000_resource_tenancy_roots.sql` establishes Vondel's
first resource-isolation boundary. It does not enable organization-private
resource creation, add public API fields, or activate row-level security.

## Invariants

- `resource_owners` contains exactly one `platform` owner.
- Each organization has at most one `organization` owner.
- `media_folders.owner_id` and `plugin_installations.owner_id` are non-null and
  reference a typed owner.
- Existing roots are platform-owned.
- An organization-owned root can be accessed only by that exact organization.
- A platform-owned root is visible to an organization only through an active,
  typed row in `organization_entitlements`.
- An entitlement cannot target an organization-owned root because its
  `(root_owner_id, root_owner_kind)` foreign key requires owner kind
  `platform`.
- Composite foreign keys prevent a media-folder or plugin entitlement from
  naming a root whose stored owner differs from `root_owner_id`.
- Only one active or suspended entitlement may exist for an
  organization/root. Revoked rows remain as history.

Repository checks enforce these invariants before OPA. OPA may narrow access
in later phases, but it cannot manufacture an owner or entitlement.

## Default platform catalog

The migration creates the active organization-creation bundle
`default-platform-catalog` at revision 1. Revision 1 contains every root found
during the migration. The default organization receives one explicit active
entitlement per member, with `granted_by_service` set to
`resource-tenancy-migration`.

A bundle revision is immutable once applied. Changing `active_revision` does
not rewrite entitlements previously materialized from an older revision.
`Materializer.MaterializeDefaultBundle` locks the target organization and
active bundle, applies one frozen revision transactionally, and leaves existing
live entitlements unchanged.

## Compatibility-created roots

Legacy `/api/v1` create inputs do not contain ownership fields and remain
unchanged. During this expand phase:

- omitted root ownership defaults to the protected platform owner; and
- typed `AFTER INSERT` triggers grant the default organization direct active
  access to a newly created platform root.

These direct compatibility grants use
`granted_by_service='resource-root-compatibility'`. They do not silently add
the new root to an already-frozen bundle revision. Adding it to the default for
future organizations requires an explicit new bundle version and audited
activation.

No caller can use these compatibility paths to select an organization owner.
Organization-private library/plugin creation remains disabled until its later
design and review boundary.

## Verification queries

The following queries are read-only.

Exactly one platform owner:

```sql
SELECT count(*) AS platform_owner_count
FROM resource_owners
WHERE kind = 'platform';
```

Expected: `1`.

No root without an owner, and no missing owner reference:

```sql
SELECT 'media_folder' AS root_kind, count(*) AS invalid_count
FROM media_folders AS roots
LEFT JOIN resource_owners AS owners ON owners.id = roots.owner_id
WHERE roots.owner_id IS NULL OR owners.id IS NULL
UNION ALL
SELECT 'plugin_installation', count(*)
FROM plugin_installations AS roots
LEFT JOIN resource_owners AS owners ON owners.id = roots.owner_id
WHERE roots.owner_id IS NULL OR owners.id IS NULL;
```

Expected: both counts are `0`.

Migration coverage for revision 1 and the default organization:

```sql
WITH root_counts AS (
    SELECT
        (SELECT count(*) FROM media_folders) +
        (SELECT count(*) FROM plugin_installations) AS roots
), bundle_counts AS (
    SELECT count(*) AS members
    FROM entitlement_bundle_members AS members
    JOIN entitlement_bundles AS bundles
      ON bundles.id = members.bundle_id
     AND bundles.active_revision = members.bundle_revision
    WHERE bundles.is_organization_creation_default
), entitlement_counts AS (
    SELECT count(*) AS entitlements
    FROM organization_entitlements AS entitlements
    JOIN organizations AS organizations
      ON organizations.id = entitlements.organization_id
    WHERE organizations.is_default
      AND entitlements.status = 'active'
      AND entitlements.source_bundle_revision = 1
)
SELECT roots, members, entitlements
FROM root_counts, bundle_counts, entitlement_counts;
```

Immediately after migration, all three values must match. Later
compatibility-created roots receive direct entitlements but are intentionally
absent from frozen revision 1; use the following ongoing visibility check:

```sql
SELECT 'media_folder' AS root_kind, count(*) AS unentitled
FROM media_folders AS roots
JOIN resource_owners AS owners ON owners.id = roots.owner_id AND owners.kind = 'platform'
WHERE NOT EXISTS (
    SELECT 1
    FROM organization_entitlements AS entitlements
    JOIN organizations AS organizations
      ON organizations.id = entitlements.organization_id AND organizations.is_default
    WHERE entitlements.media_folder_id = roots.id
      AND entitlements.root_owner_id = roots.owner_id
      AND entitlements.status = 'active'
)
UNION ALL
SELECT 'plugin_installation', count(*)
FROM plugin_installations AS roots
JOIN resource_owners AS owners ON owners.id = roots.owner_id AND owners.kind = 'platform'
WHERE NOT EXISTS (
    SELECT 1
    FROM organization_entitlements AS entitlements
    JOIN organizations AS organizations
      ON organizations.id = entitlements.organization_id AND organizations.is_default
    WHERE entitlements.plugin_installation_id = roots.id
      AND entitlements.root_owner_id = roots.owner_id
      AND entitlements.status = 'active'
);
```

Expected: both counts are `0`.

Ledger failures or quarantine:

```sql
SELECT phase, root_kind, status, count(*)
FROM resource_tenancy_migration_ledger
GROUP BY phase, root_kind, status
ORDER BY phase, root_kind, status;
```

Every migration row should be `complete`.

## Rollback boundary

Before any organization-private root exists, stop the application and migrate
to the immediate predecessor:

```bash
make migrate-down-to VERSION=20260812190000
```

The Down migration removes only resource-tenancy functions, triggers, tables,
constraints, indexes, and root `owner_id` columns. It preserves all prior
media-folder and plugin-installation rows and columns.

After a later release enables organization-private resources, schema Down is
not a valid rollback. Roll back the application image and restore the named
predeployment database snapshot; never discard ownership rows to force the old
schema to accept private resources.

## Next boundary

The next slice moves library/catalog repository reads and writes onto required
typed tenant/service scopes and locks `/api/v1` parity. RLS remains disabled
until executable repository/RLS decisions match exactly.
