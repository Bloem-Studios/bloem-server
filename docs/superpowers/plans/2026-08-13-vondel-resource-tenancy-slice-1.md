# Vondel Resource Tenancy Slice 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish typed platform/organization resource ownership, versioned default bundles, explicit organization entitlements, and a deterministic backfill for existing media folders and plugin installations without changing `/api/v1` behavior.

**Architecture:** PostgreSQL owns the security boundary. Every root receives a typed `resource_owners` reference; existing and compatibility-created roots are platform-owned, and the default organization sees them only through explicit entitlements materialized from revision 1 of the active default bundle. A focused `internal/resourcetenancy` package reads these records and performs idempotent transactional bundle materialization, but no route or UI enables organization-private resource creation in this slice.

**Tech Stack:** Go 1.26, PostgreSQL 16+, pgx v5, Goose migrations, google/uuid, existing disposable-database harness.

**Spec:** `docs/superpowers/specs/2026-08-13-vondel-resource-tenancy-design.md`

## Global Constraints

- Preserve complete Silo-compatible `/api/v1` behavior and wire shapes.
- Existing media folders and plugin installations become platform-owned.
- Compatibility-era creates also become platform-owned; organization-private creation remains disabled.
- The default organization receives explicit, auditable entitlements to every migrated root.
- An organization entitlement may target only a platform-owned root.
- Root kinds are typed as `media_folder` or `plugin_installation`; no free-form resource table name is accepted.
- Bundle changes never silently rewrite existing organization entitlements.
- Missing owner, missing entitlement, suspended/revoked entitlement, and cross-owner relationships fail closed.
- No RLS is enabled in this slice; repository/RLS parity is a later delivery slice.
- No Live TV tables or behavior are added in this slice.
- Use `GOWORK=off` for every Go command.

---

## File map

- `migrations/sql/20260813090000_resource_tenancy_roots.sql` — owner, bundle, member, entitlement, ledger, root-column, backfill, constraint, and rollback boundary.
- `internal/database/resource_tenancy_migration_test.go` — clean-install, populated-upgrade, idempotence, default-create, constraint, and down/up verification.
- `internal/resourcetenancy/types.go` — typed owner/root/entitlement contracts and sentinel errors.
- `internal/resourcetenancy/store.go` — root-owner and active-entitlement lookups.
- `internal/resourcetenancy/store_test.go` — real PostgreSQL access and non-disclosure contracts.
- `internal/resourcetenancy/materializer.go` — transactional default-bundle materialization.
- `internal/resourcetenancy/materializer_test.go` — idempotence, concurrency, frozen-revision, and rollback contracts.
- `docs/architecture/resource-tenancy-foundation.md` — operator verification and rollback procedure.

### Task 1: Add ownership, bundle, and entitlement schema

**Files:**
- Create: `migrations/sql/20260813090000_resource_tenancy_roots.sql`
- Create: `internal/database/resource_tenancy_migration_test.go`

**Interfaces:**
- Produces `resource_owners`, `entitlement_bundles`, `entitlement_bundle_versions`, `entitlement_bundle_members`, `organization_entitlements`, and `resource_tenancy_migration_ledger`.
- Produces non-null `media_folders.owner_id` and `plugin_installations.owner_id`.
- Produces `vondel_platform_resource_owner_id()` plus typed compatibility-entitlement triggers for platform-only legacy root creation.
- Consumes the default organization created by migration `20260812190000`.

- [ ] **Step 1: Write the populated-upgrade RED test**

Create a disposable database at the migration immediately before `20260813090000`, insert two media folders and two plugin installations (including the reserved builtin installation shape), record their IDs and legacy columns, migrate up, and assert these hand-derived invariants:

```go
type resourceTenancySnapshot struct {
	PlatformOwnerID       uuid.UUID
	DefaultOrganizationID uuid.UUID
	FolderOwnerIDs        map[int]uuid.UUID
	PluginOwnerIDs        map[int64]uuid.UUID
	BundleID              uuid.UUID
	BundleRevision        int64
	BundleMemberCount     int
	EntitlementCount      int
}

if got.BundleRevision != 1 {
	t.Fatalf("default bundle revision = %d, want 1", got.BundleRevision)
}
if got.BundleMemberCount != 4 || got.EntitlementCount != 4 {
	t.Fatalf("coverage = members %d entitlements %d, want 4/4", got.BundleMemberCount, got.EntitlementCount)
}
for _, ownerID := range got.FolderOwnerIDs {
	if ownerID != got.PlatformOwnerID {
		t.Fatalf("folder owner = %s, want platform %s", ownerID, got.PlatformOwnerID)
	}
}
```

Also compare the recorded folder/plugin legacy columns after migration so the test catches behavior-changing rewrites.

- [ ] **Step 2: Write clean-install, compatibility-create, and constraint RED cases**

In the same test file, add cases that prove:

1. A clean install contains exactly one protected platform owner and one active default bundle at revision 1; its member and entitlement counts exactly equal the roots created by earlier migrations (currently the reserved builtin plugin installation and no media folders).
2. A legacy insert into `media_folders` or `plugin_installations` that omits `owner_id` receives the platform owner and one active default-organization entitlement through the compatibility boundary.
3. A second platform owner is rejected.
4. An organization owner without `organization_id`, or a platform owner with one, is rejected.
5. An entitlement to an organization-owned root is rejected by a database constraint.
6. A media-folder entitlement that names a plugin root, or whose recorded owner differs from the root owner, is rejected.
7. A duplicate active/suspended entitlement for one organization/root is rejected while a revoked historical row may coexist with one new live row.

- [ ] **Step 3: Run the migration tests and observe RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TestResourceTenancyMigration -count=1 -v
```

Expected: FAIL because migration `20260813090000` and its relations do not exist.

- [ ] **Step 4: Implement the owner and bundle core**

Create `resource_owners` with:

```sql
CREATE TABLE public.resource_owners (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (kind IN ('platform', 'organization')),
    organization_id uuid REFERENCES public.organizations(id) ON DELETE RESTRICT,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT resource_owners_kind_organization_check CHECK (
        (kind = 'platform' AND organization_id IS NULL) OR
        (kind = 'organization' AND organization_id IS NOT NULL)
    ),
    CONSTRAINT resource_owners_id_kind_key UNIQUE (id, kind),
    CONSTRAINT resource_owners_id_organization_key UNIQUE (id, organization_id),
    CONSTRAINT resource_owners_organization_key UNIQUE (organization_id)
);

CREATE UNIQUE INDEX resource_owners_one_platform_idx
    ON public.resource_owners(kind) WHERE kind = 'platform';
```

Insert the singleton platform owner and one organization owner for every existing organization. Add `vondel_platform_resource_owner_id()` as a stable SQL function that selects the singleton platform owner.

Create a versioned bundle core:

```sql
CREATE TABLE public.entitlement_bundles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'suspended', 'retired')),
    active_revision bigint NOT NULL DEFAULT 1 CHECK (active_revision > 0),
    is_organization_creation_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_bundles_id_active_revision_key UNIQUE (id, active_revision)
);

CREATE UNIQUE INDEX entitlement_bundles_slug_ci_idx
    ON public.entitlement_bundles(lower(slug));
CREATE UNIQUE INDEX entitlement_bundles_one_creation_default_idx
    ON public.entitlement_bundles(is_organization_creation_default)
    WHERE is_organization_creation_default;

CREATE TABLE public.entitlement_bundle_versions (
    bundle_id uuid NOT NULL REFERENCES public.entitlement_bundles(id) ON DELETE RESTRICT,
    revision bigint NOT NULL CHECK (revision > 0),
    created_by_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
    created_by_service text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bundle_id, revision),
    CONSTRAINT entitlement_bundle_versions_actor_check CHECK (
        (created_by_account_id IS NOT NULL)::integer +
        (created_by_service IS NOT NULL AND btrim(created_by_service) <> '')::integer = 1
    )
);
```

Insert `default-platform-catalog`, revision 1, created by service `resource-tenancy-migration`.

- [ ] **Step 5: Add typed roots and backfill them**

Add nullable `owner_id` columns to `media_folders` and `plugin_installations`, backfill every row with the platform owner in deterministic primary-key order, verify no nulls, then set both columns `NOT NULL DEFAULT public.vondel_platform_resource_owner_id()`. Add `UNIQUE (id, owner_id)` to both tables and foreign keys to `resource_owners(id)`.

The temporary default is deliberately platform-only. There is no organization-private create API in this slice, and callers cannot supply an organization identity through legacy v1 input.

- [ ] **Step 6: Add typed bundle members and entitlements**

Both tables use nullable typed resource columns plus composite foreign keys, never a free-form resource table name:

```sql
CREATE TABLE public.entitlement_bundle_members (
    bundle_id uuid NOT NULL,
    bundle_revision bigint NOT NULL,
    entitlement_kind text NOT NULL CHECK (entitlement_kind IN ('library_access', 'plugin_availability')),
    root_kind text NOT NULL CHECK (root_kind IN ('media_folder', 'plugin_installation')),
    root_owner_id uuid NOT NULL,
    root_owner_kind text NOT NULL DEFAULT 'platform' CHECK (root_owner_kind = 'platform'),
    media_folder_id integer,
    plugin_installation_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (bundle_id, bundle_revision)
        REFERENCES public.entitlement_bundle_versions(bundle_id, revision) ON DELETE RESTRICT,
    FOREIGN KEY (root_owner_id, root_owner_kind)
        REFERENCES public.resource_owners(id, kind) ON DELETE RESTRICT,
    FOREIGN KEY (media_folder_id, root_owner_id)
        REFERENCES public.media_folders(id, owner_id) ON DELETE RESTRICT,
    FOREIGN KEY (plugin_installation_id, root_owner_id)
        REFERENCES public.plugin_installations(id, owner_id) ON DELETE RESTRICT,
    CONSTRAINT entitlement_bundle_members_typed_root_check CHECK (
        (root_kind = 'media_folder' AND entitlement_kind = 'library_access' AND media_folder_id IS NOT NULL AND plugin_installation_id IS NULL) OR
        (root_kind = 'plugin_installation' AND entitlement_kind = 'plugin_availability' AND media_folder_id IS NULL AND plugin_installation_id IS NOT NULL)
    ),
    UNIQUE NULLS NOT DISTINCT (bundle_id, bundle_revision, media_folder_id, plugin_installation_id)
);
```

`organization_entitlements` repeats the same typed-root, owner-kind, and composite-root foreign keys and adds:

```sql
id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
status text NOT NULL CHECK (status IN ('active', 'suspended', 'revoked')),
source_bundle_id uuid,
source_bundle_revision bigint,
security_revision bigint NOT NULL DEFAULT 1 CHECK (security_revision > 0),
granted_by_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
granted_by_service text,
created_at timestamptz NOT NULL DEFAULT now(),
updated_at timestamptz NOT NULL DEFAULT now(),
revoked_at timestamptz
```

Require exactly one actor, require both source-bundle fields together, and reference `entitlement_bundle_versions(bundle_id, revision)`. Add partial unique indexes for live media-folder and plugin-installation entitlements where `status IN ('active','suspended')`.

- [ ] **Step 7: Materialize migration coverage and add a ledger**

Insert one bundle member for every existing root, then materialize one active entitlement per member for the default organization. Add `resource_tenancy_migration_ledger` keyed by `(phase, root_kind, root_id)` with status constrained to `pending`, `complete`, or `quarantined`, the root owner, attempt counters, protected diagnostic text, and timestamps. Record every backfilled root as complete.

Before ending the Up migration, raise an exception if any root lacks an owner, any existing root is absent from revision 1, or any revision-1 member lacks an active default-organization entitlement.

Add separate, typed `AFTER INSERT` trigger functions for `media_folders` and
`plugin_installations`. When—and only when—the inserted root resolves to the
platform owner, each function inserts one active entitlement for the default
organization with `granted_by_service='resource-root-compatibility'`. Use the
matching typed root column and the live-entitlement uniqueness contract. These
triggers preserve v1 create behavior while organization-private creation is
disabled. They do not alter bundle revision 1: adding a root to a future
organization-creation default is an explicit bundle-revision operation.

- [ ] **Step 8: Implement the pre-contract Down migration**

Down must drop the compatibility triggers/functions, entitlements, members, versions, bundles, ledger, root composite constraints, root `owner_id` columns, the owner-default function, and owners—in that dependency order. It must not change any pre-existing folder/plugin columns or rows. The documented rollback boundary is valid only before organization-private resource creation exists.

- [ ] **Step 9: Run migration, down/up, and diff verification**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TestResourceTenancyMigration -count=1 -v
GOWORK=off go test ./internal/database -run TestResourceTenancyMigration -count=1
git diff --check
```

Expected: every database case passes with exact coverage and unchanged legacy columns.

- [ ] **Step 10: Commit the schema boundary**

```bash
git add migrations/sql/20260813090000_resource_tenancy_roots.sql internal/database/resource_tenancy_migration_test.go
git commit -m "feat(tenancy): add resource ownership and entitlements"
```

### Task 2: Add typed resource-access reads

**Files:**
- Create: `internal/resourcetenancy/types.go`
- Create: `internal/resourcetenancy/store.go`
- Create: `internal/resourcetenancy/store_test.go`

**Interfaces:**
- Consumes: schema from Task 1 and `tenancy.Context` from the identity foundation.
- Produces: `RootRef`, `Owner`, `Entitlement`, `Store.RootOwner`, and `Store.RequireOrganizationAccess`.

- [ ] **Step 1: Write RED access-matrix tests**

Use real PostgreSQL and table-driven literal expectations for an entitled active organization, an unentitled organization, the wrong organization owner, suspended/revoked entitlement, missing root, and malformed `RootRef`. Every hidden case must return `ErrResourceHidden`; only infrastructure failure returns `ErrResourceUnavailable`.

```go
func TestStoreRequireOrganizationAccess(t *testing.T) {
	tests := []struct {
		name    string
		root    RootRef
		orgID   uuid.UUID
		wantErr error
	}{
		{name: "active platform grant", root: folderRoot, orgID: entitledID},
		{name: "unentitled", root: folderRoot, orgID: otherID, wantErr: ErrResourceHidden},
		{name: "missing root", root: RootRef{Kind: RootMediaFolder, ID: 999999}, orgID: entitledID, wantErr: ErrResourceHidden},
	}
}
```

Name the mutation each case catches: removing the status predicate, accepting a caller owner, selecting another organization's owner, or translating no rows to an internal error.

- [ ] **Step 2: Run RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy -run 'TestStore' -count=1 -v
```

Expected: FAIL because `internal/resourcetenancy` does not exist.

- [ ] **Step 3: Define the exact domain contracts**

```go
type OwnerKind string
const (
	OwnerPlatform     OwnerKind = "platform"
	OwnerOrganization OwnerKind = "organization"
)

type RootKind string
const (
	RootMediaFolder       RootKind = "media_folder"
	RootPluginInstallation RootKind = "plugin_installation"
)

type RootRef struct {
	Kind RootKind
	ID   int64
}

type Owner struct {
	ID             uuid.UUID
	Kind           OwnerKind
	OrganizationID *uuid.UUID
	Revision       int64
}

type Entitlement struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	Root                  RootRef
	RootOwnerID           uuid.UUID
	Status                EntitlementStatus
	SourceBundleID        *uuid.UUID
	SourceBundleRevision  *int64
	SecurityRevision      int64
}

var ErrResourceHidden = errors.New("resource not found")
var ErrResourceUnavailable = errors.New("resource scope unavailable")
var ErrInvalidRoot = errors.New("invalid resource root")
```

- [ ] **Step 4: Implement typed lookups**

Use a switch over the two known root kinds to select from the concrete root table; never interpolate caller text. `RequireOrganizationAccess` loads the root owner itself. For organization ownership it requires exact organization equality. For platform ownership it requires an `active` entitlement matching the concrete typed root and the loaded `root_owner_id`. It does not accept owner identity from the caller.

Use these signatures:

```go
func NewStore(pool *pgxpool.Pool) *Store
func (s *Store) RootOwner(ctx context.Context, root RootRef) (Owner, error)
func (s *Store) RequireOrganizationAccess(ctx context.Context, organizationID uuid.UUID, root RootRef) (Entitlement, error)
```

- [ ] **Step 5: Verify normal and race tests**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy -run 'TestStore' -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/resourcetenancy -run 'TestStore' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the typed read boundary**

```bash
git add internal/resourcetenancy/types.go internal/resourcetenancy/store.go internal/resourcetenancy/store_test.go
git commit -m "feat(tenancy): enforce typed resource access"
```

### Task 3: Materialize frozen default-bundle revisions

**Files:**
- Create: `internal/resourcetenancy/materializer.go`
- Create: `internal/resourcetenancy/materializer_test.go`

**Interfaces:**
- Consumes: Task 1 bundle/member schema.
- Produces: `Materializer.MaterializeDefaultBundle`.

- [ ] **Step 1: Write transactional RED tests**

Prove with real PostgreSQL that:

- first application inserts exactly the bundle member count;
- a repeat application returns zero created rows and preserves IDs/revisions;
- concurrent calls leave exactly one live entitlement per root;
- changing a bundle's active revision does not rewrite already materialized entitlements;
- failure on one invalid member rolls back every entitlement in that application; and
- a suspended organization cannot receive grants.

```go
type Actor struct {
	AccountID *int
	Service   string
}

type MaterializationResult struct {
	BundleID uuid.UUID
	Revision int64
	Created  int64
	Existing int64
}

func (m *Materializer) MaterializeDefaultBundle(
	ctx context.Context,
	organizationID uuid.UUID,
	actor Actor,
) (MaterializationResult, error)
```

- [ ] **Step 2: Run RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy -run 'TestMaterialize' -count=1 -v
```

Expected: FAIL on the missing materializer.

- [ ] **Step 3: Implement one locked transaction**

Validate exactly one actor before opening the transaction. Lock the organization, require `status='active'`, then lock the one active creation-default bundle and its `active_revision`. Read members only from that frozen revision. Insert entitlements with the typed root columns and `ON CONFLICT` against the corresponding live partial uniqueness contract. Re-read existing rows to distinguish created from existing without modifying status or revision. Commit only after the count equals the member count.

Return `ErrOrganizationUnavailable` for a missing/suspended organization, `ErrDefaultBundleUnavailable` for zero/multiple/inactive defaults, and preserve `ErrResourceUnavailable` for database failures.

- [ ] **Step 4: Verify concurrency and race behavior**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy -run 'TestMaterialize' -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/resourcetenancy -run 'TestMaterialize' -count=1
```

Expected: PASS with exactly one live row per root after concurrent execution.

- [ ] **Step 5: Commit materialization**

```bash
git add internal/resourcetenancy/materializer.go internal/resourcetenancy/materializer_test.go
git commit -m "feat(tenancy): materialize default resource bundles"
```

### Task 4: Lock compatibility creates and rollback behavior

**Files:**
- Modify: `internal/database/resource_tenancy_migration_test.go`
- Create: `internal/catalog/resource_owner_compat_test.go`
- Create: `internal/plugins/resource_owner_compat_test.go`

**Interfaces:**
- Consumes: unchanged `catalog.FolderRepository.Create` and `plugins.InstallationStore.Create` APIs.
- Proves: callers that know nothing about tenancy still create platform-owned roots and receive no new v1 response fields.

- [ ] **Step 1: Write repository-level RED tests**

Against a migrated disposable database, call the real existing repository `Create` methods without an owner argument. Query the inserted row and assert its owner is the singleton platform owner and that the default organization has exactly one active entitlement to it. Marshal the returned model through the existing handler response path and assert no `owner_id`, organization, bundle, or entitlement field appears.

The mutations caught are removal of the platform compatibility default, omission of the compatibility entitlement, accidental mutation of the frozen bundle revision, or exposure of ownership metadata on v1.

- [ ] **Step 2: Prove RED against the pre-slice migration**

Run the tests with migration `20260813090000` temporarily excluded by the test harness. Expected: FAIL because `owner_id`/`resource_owners` do not exist. Restore the normal migration set before implementation verification.

- [ ] **Step 3: Run GREEN against the new migration**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/catalog ./internal/plugins -run 'TestCompatibilityCreateUsesPlatformOwner' -count=1 -v
```

Expected: PASS without production repository changes. If production changes are required, they may only preserve the existing Create signatures and must not accept caller organization identity.

- [ ] **Step 4: Extend down/up verification**

Add an assertion that migration Down removes every new relation/function/column while preserving folder/plugin row IDs and legacy values, then Up deterministically recreates one platform owner, the same coverage counts, and valid entitlements.

- [ ] **Step 5: Commit compatibility gates**

```bash
git add internal/database/resource_tenancy_migration_test.go internal/catalog/resource_owner_compat_test.go internal/plugins/resource_owner_compat_test.go
git commit -m "test(tenancy): lock platform root compatibility"
```

### Task 5: Document and verify the slice

**Files:**
- Create: `docs/architecture/resource-tenancy-foundation.md`

**Interfaces:**
- Produces the operator verification and rollback contract for Slice 1.
- Does not advertise a public capability or route.

- [ ] **Step 1: Write the operator document**

Document:

- the platform-owner and organization-owner invariants;
- exact SQL queries for root coverage and default-organization entitlements;
- default bundle revision behavior;
- the temporary platform-only create default and typed default-organization entitlement triggers;
- why compatibility-created roots receive direct entitlements but do not silently rewrite the frozen bundle revision;
- why organization-private resource creation remains unavailable;
- safe Down rollback before private roots exist; and
- snapshot restore requirement after that future boundary.

- [ ] **Step 2: Run the complete focused verification**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TestResourceTenancyMigration -count=1 -v
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy ./internal/catalog ./internal/plugins -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/resourcetenancy -count=1
GOWORK=off go vet ./internal/resourcetenancy ./internal/database ./internal/catalog ./internal/plugins
GOWORK=off go build ./cmd/silo
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 3: Run populated Vondel preflight without mutation**

Against the Vondel development database, execute read-only queries that record current folder/plugin counts and prove the tenant identity foundation has exactly one default organization. Do not apply the migration to the persistent development database in this slice's implementation worktree.

- [ ] **Step 4: Commit documentation**

```bash
git add docs/architecture/resource-tenancy-foundation.md
git commit -m "docs(tenancy): describe resource ownership foundation"
```

## Completion gate

Slice 1 is complete only when:

- clean install and populated upgrade pass on disposable PostgreSQL;
- every existing and compatibility-created media folder/plugin installation is platform-owned and explicitly entitled to the default organization;
- revision 1 of the active default bundle contains every migrated root exactly once;
- the default organization has one explicit active entitlement per migrated root;
- wrong-owner, wrong-root-kind, organization-owned target, and duplicate-live relationships fail in PostgreSQL;
- typed access reads hide absent, unentitled, suspended, revoked, and cross-organization roots;
- concurrent default-bundle materialization is idempotent and atomic;
- Down/Up preserves all pre-existing folder/plugin data;
- `/api/v1` response shapes and creation inputs remain unchanged;
- no new v10 capability, RLS policy, Live TV behavior, or organization-private create path is enabled; and
- the slice receives independent schema/security review before repository scoping begins.

## Self-review

- **Spec coverage:** Ownership model and typed roots are Tasks 1–2; default bundle, explicit entitlements, frozen revisions, concurrency, and audit actors are Tasks 1 and 3; migration/backfill/verify/constrain/rollback are Tasks 1 and 4; compatibility defaults and unchanged v1 are Task 4; operator behavior and populated preflight are Task 5. Repository-wide scoping, workers, search/cache/events/object keys, playback/downloads, RLS, and populated mutation acceptance are explicitly later slices from the approved design.
- **Placeholder scan:** The plan contains no deferred implementation placeholders. Later slices are scope boundaries defined by the approved design, not missing steps in this deliverable.
- **Type consistency:** `RootRef`, `Owner`, `Entitlement`, `Actor`, and `MaterializationResult` are defined once and consumed with the same field names and signatures throughout Tasks 2–5.
