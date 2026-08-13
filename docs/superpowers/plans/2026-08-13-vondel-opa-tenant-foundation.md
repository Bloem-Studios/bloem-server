# Vondel OPA Tenant Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reconcile the existing tenant foundation with Silo's OPA and access-group architecture, replace the unpublished `/api/v10` surface with `/api/v2`, make profile access groups and resource visibility organization-safe, and preserve exact Silo v1 behavior.

**Architecture:** PostgreSQL remains the hard tenant boundary, while the existing `internal/policy` package receives typed tenant and resource facts and produces the effective media scope. This increment deliberately stops before delegated administrative-role storage or mutation APIs; it creates the secure, testable foundation those later plans consume.

**Tech Stack:** Go 1.26, PostgreSQL 18, pgx, Goose SQL migrations, embedded OPA/Rego, chi, GitHub Actions

**Spec:** `docs/superpowers/specs/2026-08-13-vondel-opa-centered-multitenant-authorization-design.md`

## Global Constraints

- Preserve `/api/v1` request and response contracts and current profile switching.
- The additive tenant-aware API is `/api/v2`; do not retain a `/api/v10` alias.
- Keep the Go module and shared commit content Silo-compatible and product-neutral.
- PostgreSQL constraints and organization-scoped queries enforce tenant isolation independently of OPA.
- Only `internal/policy` may import OPA.
- Existing policy JSON fields remain stable; new tenant facts are additive.
- Unknown tenant state, stale revisions, foreign profiles, and foreign resources fail closed.
- Each profile has one organization-owned access group.
- `users.access_group_id` remains a temporary v1 compatibility ceiling; do not drop it in this increment.
- Organization-owned resources require matching organization ownership; platform resources require an active entitlement.
- Required PostgreSQL tests must fail rather than skip when CI has not provided its database.
- Do not merge or cherry-pick `dbcd3a6dad64c68d8d36e5dc783d5a033ff0439e`; reuse only separately reviewed pieces in later plans.

## Program Sequence

This is implementation plan 1 of the approved authorization program. Later
plans, written only after this increment is green, cover:

1. structured platform and organization administrative roles plus typed OPA
   administration decisions;
2. `/api/v2` direct-profile login, organization selection, shared devices,
   administrative mode, and step-up authentication;
3. resource administration, entitlements, organization resource packages,
   durable bulk operations, and audit;
4. the context-aware platform and organization web workspace;
5. Live TV, plugins, folder roots, adult-scene policy, and distributed stream
   enforcement; and
6. upstream PR preparation as independently reviewable slices.

## File Map

### API version correction

- Rename `internal/api/router_v10.go` to `internal/api/router_v2.go` and expose only `/api/v2`.
- Rename `internal/api/router_v10_test.go` to `internal/api/router_v2_test.go`.
- Rename `internal/api/handlers/v10_system.go` to `internal/api/handlers/v2_system.go`.
- Rename `internal/api/handlers/v10_system_test.go` to `internal/api/handlers/v2_system_test.go`.
- Modify `internal/api/router.go` to mount v2.
- Modify `internal/api/middleware/tenant.go` and its test to rename `RequireV10` to `RequireV2`.
- Rename `docs/architecture/v10-security-foundation.md` to `docs/architecture/v2-security-foundation.md`.
- Modify `FORK.md`, CI, and active architecture docs to describe v2.

### Organization-owned access groups

- Create `migrations/sql/20260813110000_organization_access_group_invariants.sql` for one default group per organization and profile-canonical assignment invariants.
- Create `internal/database/organization_access_group_migration_test.go` for real PostgreSQL up/down/up coverage.
- Modify `internal/access/group_store.go` so every administrative operation accepts an organization ID and member counts come from profiles.
- Modify `internal/access/groups.go` to resolve a profile group plus the temporary account ceiling.
- Modify the corresponding tests and API handler interfaces.

### Tenant-aware policy facts

- Modify `internal/policy/input.go` with an additive `TenantFacts` document.
- Modify `internal/policy/viewer_resolver.go` and middleware/action adapters to populate it.
- Modify policy simulation manifests/tests to accept the additive facts without widening a decision.

### Tenant-visible resource scope

- Modify `internal/resourcetenancy/store.go` with a bounded available-media-folder query.
- Modify `internal/policy/vendor/scope.rego` to intersect the existing scope with tenant-visible library IDs.
- Modify parity, resolver, and store tests.

### End-to-end and CI

- Rename and strengthen `internal/api/v1_tenancy_compat_test.go` v2 assertions.
- Create `internal/api/opa_tenant_foundation_test.go` for real PostgreSQL compatibility/isolation coverage.
- Modify `.github/workflows/ci.yml` so exact focused tests run with PostgreSQL and cannot silently skip.
- Update active operator documentation; historical plans remain marked superseded.

---

### Task 1: Replace the unpublished v10 surface with v2

**Files:**

- Rename: `internal/api/router_v10.go` → `internal/api/router_v2.go`
- Rename: `internal/api/router_v10_test.go` → `internal/api/router_v2_test.go`
- Rename: `internal/api/handlers/v10_system.go` → `internal/api/handlers/v2_system.go`
- Rename: `internal/api/handlers/v10_system_test.go` → `internal/api/handlers/v2_system_test.go`
- Rename: `docs/architecture/v10-security-foundation.md` → `docs/architecture/v2-security-foundation.md`
- Modify: `internal/api/router.go`
- Modify: `internal/api/middleware/tenant.go`
- Modify: `internal/api/middleware/tenant_test.go`
- Modify: `internal/tenancy/resolver_test.go`
- Modify: `internal/resourcetenancy/store_test.go`
- Modify: `internal/api/v1_tenancy_compat_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `FORK.md`
- Modify: `docs/superpowers/specs/2026-08-13-vondel-resource-tenancy-design.md`

**Interfaces:**

- Consumes: existing `tenancy.Resolver.Resolve(ctx, accountID, organizationID, legacy)`.
- Produces: `TenantMiddleware.RequireV2(http.Handler) http.Handler`, `mountV2`, `V2SystemHandler`, public `GET /api/v2/capabilities`, and authenticated `GET /api/v2/organizations`.

- [ ] **Step 1: Change tests to require v2 and reject v10**

Rename the test files, test types, and test names. Add an explicit regression:

```go
func TestV2MountedAndV10Absent(t *testing.T) {
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, nil)

	v2 := httptest.NewRecorder()
	router.ServeHTTP(v2, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if v2.Code != http.StatusOK || !strings.Contains(v2.Body.String(), `"api":"v2"`) {
		t.Fatalf("v2 capabilities = %d %s", v2.Code, v2.Body.String())
	}

	v10 := httptest.NewRecorder()
	router.ServeHTTP(v10, httptest.NewRequest(http.MethodGet, "/api/v10/capabilities", nil))
	if v10.Code != http.StatusNotFound {
		t.Fatalf("v10 status = %d, want 404", v10.Code)
	}
}
```

- [ ] **Step 2: Run the renamed focused tests and observe RED**

Run:

```bash
GOWORK=off go test ./internal/api ./internal/api/handlers ./internal/api/middleware -run 'TestV2|TestTenantRequireV2' -count=1
```

Expected: compilation failures for the missing v2 names and/or a 404 from `/api/v2`.

- [ ] **Step 3: Rename the implementation without retaining an alias**

Use `git mv` for the four Go files and the operator document. Rename all exported and private v10 identifiers:

```go
func (m *TenantMiddleware) RequireV2(next http.Handler) http.Handler
func mountV2(r chi.Router, deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware)
func mountV2Routes(r chi.Router, system *handlers.V2SystemHandler, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware)

type V2OrganizationStore interface {
	ListMemberships(context.Context, int) ([]tenancy.Membership, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type V2SystemHandler struct {
	organizations V2OrganizationStore
}
```

Mount only `r.Route("/api/v2", ...)`, return `"api":"v2"`, and rename the capability test/CI selectors. Do not add redirects or compatibility routes for `/api/v10`, because that surface has never been released.

- [ ] **Step 4: Update active documentation and code comments**

Change active references in `FORK.md`, the resource-tenancy spec, the renamed operator document, and source comments from v10 to v2. Leave superseded historical plans intact behind their existing non-executable warning.

- [ ] **Step 5: Verify GREEN and scan active code**

Run:

```bash
GOWORK=off go test ./internal/tenancy ./internal/resourcetenancy ./internal/api ./internal/api/handlers ./internal/api/middleware -run 'TestV1|TestV2|TestTenant' -count=1
rg -n 'V10|v10|/api/v10' internal cmd .github FORK.md docs/architecture docs/superpowers/specs/2026-08-13-vondel-resource-tenancy-design.md
git diff --check
```

Expected: tests pass; the scan has no API-v10 results unrelated to third-party versions such as Jellyfin or Discord.

- [ ] **Step 6: Commit**

```bash
git add internal/api internal/tenancy internal/resourcetenancy .github/workflows/ci.yml FORK.md docs/architecture docs/superpowers/specs/2026-08-13-vondel-resource-tenancy-design.md
git commit -m "refactor(api): align tenant foundation on v2"
```

### Task 2: Enforce one organization-owned default access group

**Files:**

- Create: `migrations/sql/20260813110000_organization_access_group_invariants.sql`
- Create: `internal/database/organization_access_group_migration_test.go`
- Modify: `internal/database/tenant_identity_migration_test.go`

**Interfaces:**

- Consumes: `organizations`, `access_groups.organization_id`, and `user_profiles(organization_id, access_group_id)` from migration `20260812190000`.
- Produces: one default access group per organization and a database-enforced organization/profile/group relationship.

- [ ] **Step 1: Write a real PostgreSQL migration test**

Create a disposable database test that migrates through the new migration, inserts two active organizations, and proves each may have one default group while a second default in the same organization fails:

```go
func TestOrganizationAccessGroupMigrationUpDownUp(t *testing.T) {
	db := newDisposableMigrationDatabase(t)
	runAllMigrations(t, db)

	orgA := insertOrganization(t, db, "group-org-a")
	orgB := insertOrganization(t, db, "group-org-b")
	insertDefaultGroup(t, db, orgA, "Default A")
	insertDefaultGroup(t, db, orgB, "Default B")

	_, err := db.Exec(context.Background(), `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Second A', true)`, orgA)
	assertSQLState(t, err, "23505")

	assertCrossOrganizationProfileGroupRejected(t, db, orgA, orgB)
	runMigrationDownUp(t, db, "20260813110000_organization_access_group_invariants.sql")
}
```

Use the disposable-database helpers already defined in
`internal/database/tenant_identity_migration_test.go`. The test must create and
drop its own database and fail when `SILO_TEST_DATABASE_URL` is absent in CI
mode.

- [ ] **Step 2: Run the migration test and observe RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TestOrganizationAccessGroupMigrationUpDownUp -count=1 -v
```

Expected: FAIL because the existing global `access_groups_one_default_idx` prevents a default in the second organization.

- [ ] **Step 3: Add the invariant migration**

The up migration must replace the global partial index with an organization-scoped partial index and verify no invalid rows exist:

```sql
-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS public.access_groups_one_default_idx;

CREATE UNIQUE INDEX access_groups_one_default_per_organization_idx
    ON public.access_groups (organization_id)
    WHERE is_default;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.user_profiles AS profiles
        LEFT JOIN public.access_groups AS groups
          ON groups.organization_id = profiles.organization_id
         AND groups.id = profiles.access_group_id
        WHERE profiles.access_group_id IS NOT NULL
          AND groups.id IS NULL
    ) THEN
        RAISE EXCEPTION 'profile references an access group outside its organization';
    END IF;
END;
$$;
-- +goose StatementEnd
```

The down migration restores the original single-default index only after retaining the default organization's default and clearing `is_default` on other organizations. This is rollback compatibility, not data deletion.

- [ ] **Step 4: Verify migration GREEN**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run 'TestTenantIdentityMigration|TestOrganizationAccessGroupMigration' -count=1 -v
git diff --check
```

Expected: PASS, including up/down/up and cross-organization FK rejection.

- [ ] **Step 5: Commit**

```bash
git add migrations/sql/20260813110000_organization_access_group_invariants.sql internal/database/organization_access_group_migration_test.go internal/database/tenant_identity_migration_test.go
git commit -m "fix(access): scope default groups to organizations"
```

### Task 3: Make access-group administration and resolution tenant-scoped

**Files:**

- Modify: `internal/access/group_store.go`
- Modify: `internal/access/group_store_test.go`
- Modify: `internal/access/groups.go`
- Modify: `internal/access/groups_test.go`
- Modify: `internal/access/resolver.go`
- Modify: `internal/access/resolver_test.go`
- Modify: `internal/policy/viewer_resolver.go`
- Modify: `internal/policy/viewer_resolver_test.go`
- Modify: `internal/api/handlers/access_groups.go`
- Modify: `internal/api/handlers/access_groups_test.go`
- Modify: `internal/api/router.go`

**Interfaces:**

- Consumes: a server-resolved `tenancy.Context` and the composite profile/group foreign key.
- Produces:

```go
type GroupSubject struct {
	OrganizationID uuid.UUID
	AccountID      int
	ProfileID      string
	Legacy         bool
}

type GroupPolicyProvider interface {
	ResolvePolicy(context.Context, GroupSubject) (*GroupPolicy, error)
}

func (s *GroupStore) List(context.Context, uuid.UUID) ([]Group, error)
func (s *GroupStore) Get(context.Context, uuid.UUID, int64) (*Group, error)
func (s *GroupStore) Create(context.Context, uuid.UUID, CreateGroupInput) (*Group, error)
func (s *GroupStore) Update(context.Context, uuid.UUID, int64, UpdateGroupInput) (*Group, error)
func (s *GroupStore) Delete(context.Context, uuid.UUID, int64) error
```

- [ ] **Step 1: Add failing cross-organization store tests**

Extend the database fixture to create two organizations, groups with the same name, and profiles. Required assertions:

```go
func TestGroupStoreNeverReadsOrMutatesAnotherOrganization(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	foreign := fixture.createGroup(fixture.orgB, "Shared Name")

	if _, err := fixture.store.Get(ctx, fixture.orgA, foreign.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Get error = %v, want ErrGroupNotFound", err)
	}
	if _, err := fixture.store.Update(ctx, fixture.orgA, foreign.ID, UpdateGroupInput{Name: ptr("Changed")}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Update error = %v, want ErrGroupNotFound", err)
	}
	if err := fixture.store.Delete(ctx, fixture.orgA, foreign.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("foreign Delete error = %v, want ErrGroupNotFound", err)
	}
}
```

Also prove member counts use `user_profiles.access_group_id`, not
`users.access_group_id`, and that `ResolvePolicy` rejects a profile belonging
to another account or organization with `ErrGroupNotFound`.

- [ ] **Step 2: Run focused tests and observe RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/access -run 'TestGroupStore|TestEffectivePolicyForSubject' -count=1 -v
```

Expected: compilation failures for the new organization-aware signatures and failing member-count assertions.

- [ ] **Step 3: Scope every group query and mutation**

Add `OrganizationID uuid.UUID` to `Group` and include it in the select/scan columns. Every query includes `g.organization_id = $N`. Creating a group explicitly inserts the organization ID. Clearing or setting a default is restricted to the same organization. Revision bumps update only accounts owning profiles in that organization:

```sql
UPDATE users
SET access_policy_revision = access_policy_revision + 1
WHERE id IN (
    SELECT DISTINCT user_id
    FROM user_profiles
    WHERE organization_id = $1
      AND access_group_id = $2
)
```

Do not use the legacy `users.access_group_id` column to select revision-bump
targets for profile-group changes.

- [ ] **Step 4: Resolve the canonical profile group and transitional ceiling**

Implement `ResolvePolicy` with these exact rules:

- a non-empty `ProfileID` joins `user_profiles` to `access_groups` on both
  `organization_id` and `access_group_id`, and verifies `user_id`;
- a v2 subject with an empty profile ID is rejected;
- a legacy subject with an empty profile ID may load `users.access_group_id`
  only in the default organization as the temporary compatibility ceiling;
- missing, foreign, or mismatched rows return the same non-disclosing
  `ErrGroupNotFound`; and
- nil group assignment returns nil policy, never another organization's
  default.

Replace `EffectivePolicyForUser` with:

```go
func EffectivePolicyForSubject(
	ctx context.Context,
	user *models.User,
	subject GroupSubject,
	provider GroupPolicyProvider,
) (EffectiveUserPolicy, error)
```

The merge remains strictest-wins and keeps account fields as the temporary
membership ceiling.

- [ ] **Step 5: Adapt resolvers and handlers**

Both legacy and OPA viewer resolvers load the profile before resolving its
group and obtain the organization only from `tenancy.FromContext(ctx)`. V1
middleware supplies the default organization. V2 consumption without a
profile fails closed.

Change the access-group handler interface to organization-aware methods. Its
organization ID comes from validated tenant context; no request header or JSON
field may select an organization. Existing v1 admin routes receive the default
organization from `ResolveLegacy`.

- [ ] **Step 6: Verify the complete access package and handlers**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/access ./internal/policy ./internal/api/handlers -run 'AccessGroup|ViewerResolver|EffectivePolicy' -count=1 -v
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/access ./internal/policy -run 'AccessGroup|ViewerResolver|EffectivePolicy' -count=1 -v
git diff --check
```

Expected: PASS, including same-name groups in different organizations and all foreign-object non-disclosure cases.

- [ ] **Step 7: Commit**

```bash
git add internal/access internal/policy/viewer_resolver.go internal/policy/viewer_resolver_test.go internal/api/handlers/access_groups.go internal/api/handlers/access_groups_test.go internal/api/router.go
git commit -m "feat(access): resolve profile groups by organization"
```

### Task 4: Add typed tenant facts to existing OPA inputs

**Files:**

- Modify: `internal/policy/input.go`
- Modify: `internal/policy/viewer_resolver.go`
- Modify: `internal/policy/playback_adapter.go`
- Modify: `internal/downloads/policy.go`
- Modify: `internal/api/middleware/policy_gates.go`
- Modify: `internal/policy/simulate.go`
- Modify: `internal/policy/scope_parity_test.go`
- Modify: `internal/policy/permission_parity_test.go`
- Modify: `internal/policy/action_parity_test.go`
- Modify: `internal/policy/viewer_resolver_test.go`
- Modify: `internal/api/middleware/policy_gates_test.go`

**Interfaces:**

- Consumes: `tenancy.Context` previously attached only by server middleware.
- Produces an additive shared input:

```go
type TenantFacts struct {
	Present                    bool   `json:"present"`
	Legacy                     bool   `json:"legacy"`
	OrganizationID             string `json:"organization_id"`
	MembershipID               string `json:"membership_id"`
	OrganizationStatus         string `json:"organization_status"`
	MembershipStatus           string `json:"membership_status"`
	OrganizationPolicyRevision int64  `json:"organization_policy_revision"`
	MembershipSecurityRevision int64  `json:"membership_security_revision"`
}

func TenantFactsFromContext(context.Context) (TenantFacts, error)
```

`ScopeInput`, `PermissionInput`, and `ActionInput` each gain this field without
renaming existing JSON fields:

```go
Tenant TenantFacts `json:"tenant"`
```

- [ ] **Step 1: Write failing input and adapter tests**

Add tests that marshal exact tenant facts, reject absent tenant context in a
server adapter, and prove existing custom Rego that references old fields
still compiles and produces the same decision:

```go
func TestTenantFactsFromContextRequiresCompleteResolvedContext(t *testing.T) {
	_, err := TenantFactsFromContext(context.Background())
	if !errors.Is(err, ErrTenantFactsUnavailable) {
		t.Fatalf("error = %v, want ErrTenantFactsUnavailable", err)
	}
}
```

Parity generators must populate valid default-organization legacy facts rather
than leaving the new document zero-valued.

- [ ] **Step 2: Run policy and middleware tests and observe RED**

Run:

```bash
GOWORK=off go test ./internal/policy ./internal/api/middleware ./internal/downloads -run 'TenantFacts|Parity|Policy' -count=1
```

Expected: compilation failures for `TenantFacts` and failure of the absent-context guard.

- [ ] **Step 3: Implement and populate additive facts**

Add `ErrTenantFactsUnavailable` in `internal/policy/errors.go`. The conversion
requires non-nil organization and membership UUIDs, positive revisions, active
membership, and active organization except for the existing legacy-
initializing compatibility state.

All server adapters call `TenantFactsFromContext`; they do not accept tenant
facts from request bodies. Policy simulation accepts caller-provided tenant
facts only as inert simulation input and marks the simulation response as
non-authoritative.

- [ ] **Step 4: Add vendor-policy schema validation without changing grants**

In each vendor package, add a helper that validates the tenant document. For
this transition task, existing decisions receive the facts but preserve their
base grant logic; absence at the server adapter fails before evaluation. Add
Rego tests proving malformed status, missing IDs, and zero revisions do not
produce an allow when the corresponding Go adapter is exercised.

- [ ] **Step 5: Verify compatibility and the 25 ms evaluation budget**

Run:

```bash
GOWORK=off go test ./internal/policy ./internal/api/middleware ./internal/downloads -count=1
GOWORK=off go test -race ./internal/policy -count=1
GOWORK=off go test ./internal/policy -run '^$' -bench 'Benchmark.*Policy' -benchtime=2s
git diff --check
```

Expected: all parity matrices pass and no existing JSON field is removed or renamed.

- [ ] **Step 6: Commit**

```bash
git add internal/policy internal/api/middleware internal/downloads
git commit -m "feat(policy): carry resolved tenant facts"
```

### Task 5: Intersect media scope with tenant-visible resources in OPA

**Files:**

- Modify: `internal/resourcetenancy/types.go`
- Modify: `internal/resourcetenancy/store.go`
- Modify: `internal/resourcetenancy/store_test.go`
- Modify: `internal/policy/input.go`
- Modify: `internal/policy/viewer_resolver.go`
- Modify: `internal/policy/viewer_resolver_test.go`
- Modify: `internal/policy/vendor/scope.rego`
- Modify: `internal/policy/vendor/scope_test.rego`
- Modify: `internal/policy/scope_parity_test.go`

**Interfaces:**

- Consumes: validated `tenancy.Context`, organization resource ownership, and active platform entitlements.
- Produces:

```go
func (s *Store) AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)

// Add to ScopeInput without removing or renaming its current fields.
TenantLibraryIDs []int `json:"tenant_library_ids"`
```

- [ ] **Step 1: Add failing availability and OPA intersection tests**

The database fixture must include an organization-owned folder, an entitled
platform folder, a non-entitled platform folder, and a foreign organization
folder. Assert the available list contains exactly the first two.

Add Rego cases:

```rego
test_tenant_scope_intersects_unrestricted_profile if {
    result := decision with input as object.union(valid_input, {
        "account_restricted": false,
        "profile_library_restricted": false,
        "tenant_library_ids": [10, 20],
    })
    result.unrestricted == false
    result.allowed_library_ids == [10, 20]
}

test_tenant_scope_cannot_be_widened_by_custom_policy if {
    # Base tenant scope [10]; override attempts [10, 99]; result remains [10].
}
```

- [ ] **Step 2: Run the focused tests and observe RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy ./internal/policy -run 'AvailableMediaFolder|TenantScope' -count=1 -v
```

Expected: compilation failures for the missing query/input and failing Rego tests.

- [ ] **Step 3: Implement the bounded SQL availability query**

Use one query that returns distinct media-folder IDs where either the owner is
the tenant organization or a matching live platform entitlement exists:

```sql
SELECT folders.id
FROM media_folders AS folders
JOIN resource_owners AS owners ON owners.id = folders.owner_id
LEFT JOIN organization_entitlements AS entitlements
  ON entitlements.organization_id = $1
 AND entitlements.root_owner_id = owners.id
 AND entitlements.media_folder_id = folders.id
 AND entitlements.status = 'active'
WHERE (owners.kind = 'organization' AND owners.organization_id = $1)
   OR (owners.kind = 'platform' AND entitlements.id IS NOT NULL)
ORDER BY folders.id
```

Validate active tenant context before querying. Database errors return
`ErrResourceUnavailable`; absent and foreign roots never appear.

- [ ] **Step 4: Intersect inside vendor Rego**

The vendor scope policy treats `tenant_library_ids` as a mandatory upper bound.
It intersects that bound with account, group, profile, and disabled-library
rules. The output is never `unrestricted` once a tenant is resolved because a
tenant must not see future foreign resources merely because a profile has no
explicit library restriction.

The custom override tightening logic must intersect against the already tenant-
bounded base decision. An override cannot add an ID absent from the base.

- [ ] **Step 5: Wire the viewer resolver**

Add a narrow interface:

```go
type TenantLibraryResolver interface {
	AvailableMediaFolderIDs(context.Context, tenancy.Context) ([]int, error)
}
```

Inject it into `NewViewerResolver`. Load tenant-visible IDs after validating
the profile and before calling `ResolveViewerScope`. Absence of the resolver or
an availability error fails closed; do not interpret it as unrestricted.

- [ ] **Step 6: Prove v1 parity and cross-tenant isolation**

The default-organization fixture must materialize entitlements for every
pre-existing platform folder. Assert catalog/search-visible IDs and playback
authorization match the pre-change v1 result. Then create a foreign folder and
assert it is absent even when the legacy account and profile are otherwise
unrestricted.

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/resourcetenancy ./internal/policy ./internal/access -count=1 -v
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/resourcetenancy ./internal/policy -count=1 -v
git diff --check
```

- [ ] **Step 7: Commit**

```bash
git add internal/resourcetenancy internal/policy internal/access
git commit -m "feat(policy): bound media scope by tenant resources"
```

### Task 6: Lock compatibility, database execution, and release evidence

**Files:**

- Create: `internal/api/opa_tenant_foundation_test.go`
- Modify: `internal/api/v1_tenancy_compat_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/architecture/v2-security-foundation.md`
- Create: `docs/architecture/opa-tenant-authorization.md`
- Modify: `FORK.md`

**Interfaces:**

- Consumes: Tasks 1–5.
- Produces: a CI-enforced foundation with v1 parity, v2 capability discovery, tenant-scoped groups, and OPA-bounded resource visibility.

- [ ] **Step 1: Write the disposable-database acceptance test**

Create one test that starts from a fully migrated disposable PostgreSQL
database and proves:

1. pre-existing users and profiles resolve through the default organization;
2. v1 login/profile switching response contracts are unchanged;
3. `/api/v2/capabilities` advertises only implemented features;
4. `/api/v10/*` is 404;
5. two organizations may have same-named default groups;
6. profile group selection is organization-scoped;
7. organization-owned and entitled folders are visible;
8. foreign and non-entitled folders are absent from catalog scope;
9. stale membership or organization revisions reject v2 requests; and
10. the same OPA decision revision is observable across v1 and v2 adapters.

Name the test:

```go
func TestOPATenantFoundationWithDisposablePostgres(t *testing.T)
```

The test must call `t.Fatal` when the CI contract says PostgreSQL is required
but `SILO_TEST_DATABASE_URL` is absent. It must create and drop a uniquely
named database and verify absence after cleanup.

- [ ] **Step 2: Run the acceptance test and observe RED before final wiring**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/api -run TestOPATenantFoundationWithDisposablePostgres -count=1 -v -timeout=30m
```

Expected before final wiring: FAIL because `tenant_bounded_media_scope` is not
advertised and the foreign folder remains visible to the unrestricted fixture.

- [ ] **Step 3: Complete router composition and capability advertisement**

Wire the tenancy resolver, organization-scoped group store, resource-tenancy
store, and policy viewer resolver in `internal/api/router.go` and `cmd/silo/main.go`.
Advertise only these v2 contracts in this increment:

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
  }
}
```

- [ ] **Step 4: Make CI execute the exact PostgreSQL gates**

Replace obsolete v10 selectors and add explicit commands:

```yaml
- name: Tenant and access-group migrations
  run: go test ./internal/database -run 'TestTenantIdentityMigration|TestOrganizationAccessGroupMigration' -count=1 -v -timeout=30m

- name: Tenant resource and policy stores
  run: go test -race ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m

- name: OPA tenant compatibility acceptance
  run: go test ./internal/api -run 'TestV1TenancyCompatibility|TestOPATenantFoundationWithDisposablePostgres' -count=1 -v -timeout=30m
```

All steps inherit the job's PostgreSQL service and `SILO_TEST_DATABASE_URL`.

- [ ] **Step 5: Update operator and fork documentation**

Document the v2 capability boundary, default-organization compatibility,
profile-canonical group assignment, platform entitlement requirement, policy
failure behavior, rollback boundary, and exact verification commands. State
that administrative roles and mutation routes are not yet implemented.

- [ ] **Step 6: Run the complete verification gate**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api ./internal/api/handlers ./internal/api/middleware -count=1 -v -timeout=45m
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=45m
GOWORK=off go vet ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api/...
GOWORK=off go build ./cmd/silo
git diff --check
git status --short
```

Expected: every command passes; no required PostgreSQL test reports SKIP; only
the intended plan-execution changes are present.

- [ ] **Step 7: Commit**

```bash
git add internal/api .github/workflows/ci.yml docs/architecture/opa-tenant-authorization.md docs/architecture/v2-security-foundation.md FORK.md cmd/silo/main.go
git commit -m "test(auth): lock OPA tenant foundation"
```

## Final Review Gate

Before starting the administrative-role plan, obtain two independent reviews:

1. spec compliance against
   `docs/superpowers/specs/2026-08-13-vondel-opa-centered-multitenant-authorization-design.md`,
   limited to this increment's declared scope; and
2. security review of tenant non-disclosure, access-group scoping, entitlement
   intersection, OPA tightening, v1 parity, migration rollback, and CI skip
   prevention.

Do not merge if either review finds an unbounded query, caller-selected tenant,
cross-organization identifier leak, policy widening path, skipped PostgreSQL
gate, `/api/v10` alias, or v1 behavior regression.
