# Vondel Tenant and Identity Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the tenant and identity roots required by Vondel's security model, migrate every existing installation into one default organization, and expose a read-only `/api/v10` foundation without changing Silo `/api/v1` behavior.

**Architecture:** PostgreSQL becomes authoritative for organizations, protected ownership, memberships, and profile organization/access-group identity. A focused `internal/tenancy` package owns these concepts; auth and API code consume its typed interfaces. The migration is expand-only, legacy columns and token behavior remain intact, and ambiguous existing ownership prevents v10 activation without blocking v1 compatibility.

**Tech Stack:** Go 1.26, PostgreSQL 16+, pgx v5, Goose migrations, chi, google/uuid, embedded migration FS, existing OPA and auth infrastructure.

## Global Constraints

- Preserve complete current Silo `/api/v1` account login, profile switching, profile PIN, playback, and legacy admin behavior.
- Native Vondel routes use `/api/v10`; `/api/v1` remains the compatibility surface.
- Exactly one platform owner exists after ownership activation; exactly one owner exists for each active organization.
- Account email and future direct-profile aliases share one case-insensitive global namespace and cannot collide.
- Administrative authority belongs to memberships; media identity belongs to profiles.
- Existing `users.role`, `users.access_group_id`, JWT role claims, and access-group behavior remain authoritative during this expand phase.
- Unknown organization, missing tenant context, malformed identity, and ambiguous ownership fail closed on `/api/v10`.
- No production route enables multi-organization hosting in this plan.
- Do not add RLS to media tables in this plan; resource tenantization is Plan 2 in the program roadmap.
- Use `GOWORK=off` for all Go verification commands.

---

## File map

- `migrations/sql/20260812190000_tenant_identity_foundation.sql` — expand-only schema and deterministic default-organization backfill.
- `internal/database/tenant_identity_migration_test.go` — isolated clean-install, upgrade, ambiguity, and down-migration checks.
- `internal/tenancy/types.go` — organization, membership, ownership, and status contracts.
- `internal/tenancy/store.go` — PostgreSQL reads and transactional ownership activation.
- `internal/tenancy/store_test.go` — store invariants against migrated PostgreSQL.
- `internal/tenancy/context.go` — typed request tenant context.
- `internal/tenancy/resolver.go` — account-to-membership/default-organization resolution.
- `internal/tenancy/resolver_test.go` — missing, suspended, ambiguous, and active resolution cases.
- `internal/tenancy/profile_identity.go` — default-organization identity for legacy profile creation.
- `internal/tenancy/profile_identity_test.go` — zero/one/multiple membership behavior.
- `internal/auth/service.go` — initial setup ownership activation seam.
- `internal/auth/service_test.go` — setup success and rollback/error behavior.
- `internal/auth/jwt.go` — optional v10 organization/membership claims; old tokens remain valid.
- `internal/auth/jwt_test.go` — optional-claim round trip and legacy-token compatibility.
- `internal/api/middleware/tenant.go` — authenticated membership resolution and context injection.
- `internal/api/middleware/tenant_test.go` — fail-closed v10 and legacy-default behavior.
- `internal/api/handlers/v10_system.go` — v10 capability and membership responses.
- `internal/api/handlers/v10_system_test.go` — response and non-disclosure contracts.
- `internal/api/router_v10.go` — isolated v10 route mounting.
- `internal/api/router_v10_test.go` — route/auth boundary tests.
- `internal/api/router.go` — call the isolated v10 mount and construct the tenancy store when PostgreSQL is present.
- `cmd/vondel/main.go` — wire ownership activation into the production auth service.
- `docs/architecture/v10-security-foundation.md` — operator-facing activation and ambiguity behavior.

### Task 1: Expand schema and prove migration safety

**Files:**
- Create: `migrations/sql/20260812190000_tenant_identity_foundation.sql`
- Create: `internal/database/tenant_identity_migration_test.go`

**Interfaces:**
- Produces tables `platform_security`, `organizations`, and `organization_memberships`.
- Produces `user_profiles.organization_id`, `user_profiles.access_group_id`, and `access_groups.organization_id`.
- Keeps `users.role` and `users.access_group_id` unchanged.

- [ ] **Step 1: Write the isolated upgrade tests**

Create table-driven tests that migrate disposable databases seeded in these
states: no users; one admin with profiles/groups; multiple admins; ordinary
users plus one admin. Assert the exact ownership and backfill rules:

```go
func TestTenantIdentityMigrationBackfill(t *testing.T) {
	tests := []struct {
		name                  string
		adminCount            int
		wantOwner             bool
		wantResolutionRequired bool
	}{
		{name: "fresh install", adminCount: 0},
		{name: "single setup admin", adminCount: 1, wantOwner: true},
		{name: "ambiguous admins", adminCount: 2, wantResolutionRequired: true},
	}
	// For each case: migrate to the version immediately before the new file,
	// seed users/profiles/groups, migrate up, and query every invariant.
}
```

The assertions must prove one default organization, one membership per user,
profile organization backfill, profile access-group copy, no invented owner for
zero/multiple admins, and byte-equivalent legacy columns.

- [ ] **Step 2: Run the migration test and observe RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TestTenantIdentityMigration -count=1 -v
```

Expected: FAIL because migration `20260812190000` and its tables do not exist.

- [ ] **Step 3: Add the expand-only migration**

Use this schema contract:

```sql
CREATE TABLE public.platform_security (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    owner_account_id integer UNIQUE REFERENCES public.users(id) ON DELETE RESTRICT,
    policy_revision bigint NOT NULL DEFAULT 1 CHECK (policy_revision > 0),
    ownership_resolution_required boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('initializing','active','suspended')),
    owner_account_id integer REFERENCES public.users(id) ON DELETE RESTRICT,
    policy_revision bigint NOT NULL DEFAULT 1 CHECK (policy_revision > 0),
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX organizations_slug_ci_idx ON public.organizations(lower(slug));
CREATE UNIQUE INDEX organizations_one_default_idx ON public.organizations(is_default) WHERE is_default;

CREATE TABLE public.organization_memberships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES public.organizations(id) ON DELETE CASCADE,
    account_id integer NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('invited','active','suspended')),
    legacy_role text NOT NULL CHECK (legacy_role IN ('admin','user')),
    security_revision bigint NOT NULL DEFAULT 1 CHECK (security_revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, account_id)
);
```

Insert exactly one default organization. Backfill memberships from `users`.
Set both owners only when exactly one enabled admin exists; set
`ownership_resolution_required=true` when more than one enabled admin exists.
Use organization status `active` only when one owner is selected; use
`initializing` for a fresh install or ambiguous upgrade. Legacy v1 may continue
against the initializing default organization, but v10 organization-bound
operations reject it.
Add nullable columns, backfill them, verify no nulls, and then set
`organization_id` non-null on profiles and access groups. Copy each user's
legacy `access_group_id` into all of that user's profiles. Add organization-aware
indexes. Replace global access-group name uniqueness with
`UNIQUE (organization_id, name)`, add `UNIQUE (organization_id, id)` on groups,
and add a composite profile foreign key
`(organization_id, access_group_id) REFERENCES access_groups(organization_id, id)`.
Do not remove or reinterpret legacy columns.

The Down migration removes only the new columns/tables and must leave legacy
identity and access-group state intact.

- [ ] **Step 4: Verify clean install, upgrade, ambiguity, and down migration**

Run the command from Step 2. Expected: PASS for all cases, including a down/up
round trip that leaves `users`, `user_profiles`, and `access_groups` legacy
columns unchanged.

- [ ] **Step 5: Commit the schema boundary**

```bash
git add migrations/sql/20260812190000_tenant_identity_foundation.sql internal/database/tenant_identity_migration_test.go
git commit -m "feat(tenancy): add identity foundation schema"
```

### Task 2: Add typed tenancy storage

**Files:**
- Create: `internal/tenancy/types.go`
- Create: `internal/tenancy/store.go`
- Create: `internal/tenancy/store_test.go`

**Interfaces:**
- Produces `type Store struct` and `NewStore(pool *pgxpool.Pool) *Store`.
- Produces `DefaultOrganization`, `ListMemberships`, `GetMembership`, and `ActivateInitialOwnership`.
- Consumes the schema from Task 1.

Use these exact method signatures:

```go
func (s *Store) DefaultOrganization(ctx context.Context) (Organization, error)
func (s *Store) ListMemberships(ctx context.Context, accountID int) ([]Membership, error)
func (s *Store) GetMembership(ctx context.Context, accountID int, organizationID uuid.UUID) (Membership, error)
func (s *Store) ActivateInitialOwnership(ctx context.Context, accountID int) (OwnershipState, error)
```

- [ ] **Step 1: Write store contract tests**

Test duplicate membership rejection, suspended membership reads, default
organization uniqueness, ownership activation, repeat activation idempotence,
and rejection of a different second owner.

```go
func TestStoreActivateInitialOwnership(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	got, err := store.ActivateInitialOwnership(fixture.ctx, fixture.adminID)
	if err != nil {
		t.Fatalf("ActivateInitialOwnership: %v", err)
	}
	if got.PlatformOwnerAccountID != fixture.adminID || got.Organization.OwnerAccountID == nil || *got.Organization.OwnerAccountID != fixture.adminID {
		t.Fatalf("owners = %#v, want account %d", got, fixture.adminID)
	}

	_, err = store.ActivateInitialOwnership(fixture.ctx, fixture.otherID)
	if !errors.Is(err, tenancy.ErrOwnerAlreadyAssigned) {
		t.Fatalf("second owner error = %v, want ErrOwnerAlreadyAssigned", err)
	}
}
```

- [ ] **Step 2: Run tests and observe RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancy -count=1 -v
```

Expected: FAIL because `internal/tenancy` does not exist.

- [ ] **Step 3: Implement domain types and sentinels**

Define:

```go
type OrganizationStatus string
const (
	OrganizationInitializing OrganizationStatus = "initializing"
	OrganizationActive       OrganizationStatus = "active"
	OrganizationSuspended    OrganizationStatus = "suspended"
)

type Organization struct {
	ID uuid.UUID
	Slug string
	Name string
	Status OrganizationStatus
	OwnerAccountID *int
	PolicyRevision int64
	Default bool
}

type Membership struct {
	ID uuid.UUID
	OrganizationID uuid.UUID
	AccountID int
	Status MembershipStatus
	LegacyRole string
	SecurityRevision int64
}

type OwnershipState struct {
	PlatformOwnerAccountID int
	Organization Organization
}

var ErrOwnerAlreadyAssigned = errors.New("owner already assigned")
var ErrOwnershipResolutionRequired = errors.New("ownership resolution required")
var ErrMembershipNotFound = errors.New("membership not found")
```

- [ ] **Step 4: Implement transactional store operations**

`ActivateInitialOwnership` must lock `platform_security` and the default
organization, require both owner fields to be null or already equal to the
requested account, require an active membership, assign both owners in one
transaction, clear ambiguity, activate the organization, and increment both
policy/security revisions. Map missing rows and unique/check violations to the
typed sentinels.

- [ ] **Step 5: Run focused and race tests**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancy -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/tenancy -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the storage package**

```bash
git add internal/tenancy
git commit -m "feat(tenancy): add organization membership store"
```

### Task 3: Establish typed tenant context and resolution

**Files:**
- Create: `internal/tenancy/context.go`
- Create: `internal/tenancy/resolver.go`
- Create: `internal/tenancy/resolver_test.go`

**Interfaces:**
- Produces `Context`, `WithContext`, `FromContext`, and `Resolver.Resolve`.
- Consumes `Store.GetMembership` and `Store.DefaultOrganization` from Task 2.

- [ ] **Step 1: Write resolver tests**

Cover explicit organization, legacy default organization, absent membership,
suspended membership, suspended organization, ambiguous ownership, and missing
store. The legacy default path may resolve for v1 compatibility; v10 must reject
ambiguous ownership.

```go
type Context struct {
	OrganizationID uuid.UUID
	MembershipID uuid.UUID
	AccountID int
	OrganizationStatus OrganizationStatus
	MembershipStatus MembershipStatus
	PolicyRevision int64
	SecurityRevision int64
	Legacy bool
}
```

- [ ] **Step 2: Run tests and observe RED**

```bash
GOWORK=off go test ./internal/tenancy -run 'TestResolver|TestContext' -count=1
```

Expected: FAIL with undefined resolver/context contracts.

- [ ] **Step 3: Implement resolution without ambient defaults**

`Resolve(ctx, accountID, requestedOrganizationID, legacy)` must use the default
organization only when `legacy=true` and the requested ID is nil. It must never
select the first membership for a v10 request. Return typed errors for hidden,
suspended, ambiguous, and unavailable state.

- [ ] **Step 4: Verify resolution and commit**

```bash
GOWORK=off go test ./internal/tenancy -count=1
git add internal/tenancy/context.go internal/tenancy/resolver.go internal/tenancy/resolver_test.go
git commit -m "feat(tenancy): resolve authenticated tenant context"
```

### Task 4: Bind initial setup to protected ownership

**Files:**
- Modify: `internal/auth/service.go`
- Modify: `internal/auth/service_test.go`
- Modify: `cmd/vondel/main.go`

**Interfaces:**
- Adds `OwnershipBootstrapper.ActivateInitialOwnership(context.Context, int) error` to auth.
- Adds `(*Service).SetOwnershipBootstrapper(OwnershipBootstrapper)` for production wiring.
- Consumes `tenancy.Store.ActivateInitialOwnership` from Task 2 through an adapter.

- [ ] **Step 1: Write failing setup tests**

Add a recording bootstrapper. Prove initial setup calls it with the newly
created account before login, and that activation failure deletes the created
account and returns an error without issuing tokens.

```go
type OwnershipBootstrapper interface {
	ActivateInitialOwnership(ctx context.Context, accountID int) error
}
```

- [ ] **Step 2: Run the focused RED test**

```bash
GOWORK=off go test ./internal/auth -run TestSetupInitialUserOwnership -count=1
```

Expected: FAIL because setup does not activate ownership.

- [ ] **Step 3: Implement the setup seam**

Add an optional bootstrapper to `Service`. In `SetupInitialUser`, retain the
created user, call ownership activation before `Login`, and use the existing
account deletion path on failure. Nil remains valid for isolated compatibility
fixtures, but production `cmd/vondel/main.go` must always wire the tenancy store
when `deps.DB` is present.

- [ ] **Step 4: Verify auth behavior and commit**

```bash
GOWORK=off go test ./internal/auth -count=1
GOWORK=off go test -race ./internal/auth -count=1
git add internal/auth/service.go internal/auth/service_test.go cmd/vondel/main.go
git commit -m "feat(auth): claim protected ownership during setup"
```

### Task 5: Add optional tenant identity to tokens and middleware

**Files:**
- Modify: `internal/auth/jwt.go`
- Modify: `internal/auth/jwt_test.go`
- Create: `internal/api/middleware/tenant.go`
- Create: `internal/api/middleware/tenant_test.go`

**Interfaces:**
- Adds optional `OrganizationID`, `MembershipID`, `PolicyRevision`, and `SecurityRevision` claims.
- Produces `TenantMiddleware.RequireV10` and `TenantMiddleware.ResolveLegacy`.
- Consumes `tenancy.Resolver` from Task 3.

- [ ] **Step 1: Write token and middleware RED tests**

Prove old JWT fixtures still validate with empty tenant claims. Prove new claims
round-trip. Prove v10 rejects absent/foreign/suspended membership and injects
the exact resolved context. Prove legacy resolution uses only the default
organization and does not alter the role/profile claims.

- [ ] **Step 2: Run focused tests and observe RED**

```bash
GOWORK=off go test ./internal/auth ./internal/api/middleware -run 'Tenant|OrganizationClaim|LegacyToken' -count=1
```

Expected: FAIL on missing claims and middleware.

- [ ] **Step 3: Add optional claims and middleware**

Use `omitempty` for all new JWT fields:

```go
OrganizationID   string `json:"organization_id,omitempty"`
MembershipID     string `json:"membership_id,omitempty"`
PolicyRevision   int64  `json:"policy_revision,omitempty"`
SecurityRevision int64  `json:"security_revision,omitempty"`
```

Middleware must ignore caller-supplied organization headers on v1. V10 takes
organization identity only from a validated token/session selection and checks
current membership state through the resolver before storing tenant context.

- [ ] **Step 4: Verify compatibility and commit**

```bash
GOWORK=off go test ./internal/auth ./internal/api/middleware -count=1
git add internal/auth/jwt.go internal/auth/jwt_test.go internal/api/middleware/tenant.go internal/api/middleware/tenant_test.go
git commit -m "feat(auth): carry optional tenant session identity"
```

### Task 6: Expose the read-only API v10 foundation

**Files:**
- Create: `internal/api/handlers/v10_system.go`
- Create: `internal/api/handlers/v10_system_test.go`
- Create: `internal/api/router_v10.go`
- Create: `internal/api/router_v10_test.go`
- Modify: `internal/api/router.go`

**Interfaces:**
- Produces `GET /api/v10/capabilities` and authenticated `GET /api/v10/organizations`.
- Advertises only implemented foundation capabilities; direct login, admin RBAC, and multi-organization creation remain absent.

- [ ] **Step 1: Write route contract tests**

The public capability response is exact and versioned:

```json
{
  "api":"v10",
  "identity_schema":1,
  "features":{
    "legacy_silo_v1":true,
    "organization_memberships":true,
    "direct_profile_login":false,
    "shared_device_pairing":false,
    "delegated_admin_roles":false
  }
}
```

The organization list returns only active memberships for the authenticated
account, never owner emails, other member counts, or hidden organizations.
Unauthenticated list requests return 401 with the existing constant-shape error
contract.

- [ ] **Step 2: Run route tests and observe RED**

```bash
GOWORK=off go test ./internal/api -run TestV10 -count=1
GOWORK=off go test ./internal/api/handlers -run TestV10 -count=1
```

Expected: FAIL because `/api/v10` is not mounted.

- [ ] **Step 3: Implement isolated v10 mounting**

Create `mountV10(r chi.Router, deps Dependencies, authMW *AuthMiddleware,
tenantMW *TenantMiddleware)`. Keep it outside the `/api/v1` route block. Mount
capabilities publicly and organization listing behind account authentication.
Membership listing precedes organization selection, so it must not require
tenant context. Every future organization-bound v10 route uses
`tenantMW.RequireV10`. Construct `tenancy.Store` and `tenancy.Resolver` only
when `deps.DB != nil`; without them, the membership route returns service
unavailable and never invents a tenant.

- [ ] **Step 4: Verify routes and commit**

```bash
GOWORK=off go test ./internal/api ./internal/api/handlers -run TestV10 -count=1
git add internal/api/router.go internal/api/router_v10.go internal/api/router_v10_test.go internal/api/handlers/v10_system.go internal/api/handlers/v10_system_test.go
git commit -m "feat(api): expose v10 identity foundation"
```

### Task 7: Move access-group identity to profiles without cutting over

**Files:**
- Modify: `internal/userstore/types.go`
- Modify: `internal/userstore/pgstore/profiles.go`
- Modify: `internal/userstore/pgstore/setting_values_test.go`
- Create: `internal/tenancy/profile_identity.go`
- Create: `internal/tenancy/profile_identity_test.go`

**Interfaces:**
- Adds `OrganizationID string` and `AccessGroupID *int64` to `userstore.Profile`.
- Keeps `users.access_group_id` as the effective v1 compatibility value.
- Produces `ResolveLegacyProfileIdentity(ctx, accountID) (organizationID uuid.UUID, accessGroupID *int64, error)`.

- [ ] **Step 1: Write RED persistence and isolation tests**

Prove profile reads/writes preserve organization/access-group identity, a group
from another organization cannot be assigned, zero/multiple default
memberships fail rather than selecting an arbitrary tenant, and the legacy user
assignment and v1 group member counts remain unchanged.

- [ ] **Step 2: Run focused tests and observe RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/userstore/pgstore ./internal/tenancy -run 'Organization|ProfileAccessGroup|LegacyProfileIdentity' -count=1
```

Expected: FAIL because profile tenancy fields are not mapped.

- [ ] **Step 3: Implement additive profile mapping**

Add the two fields to every Postgres profile select/insert/update scanner in one
mechanical change. Default legacy profile creation to the account's sole active
membership organization and copy `users.access_group_id` only when no explicit
profile group is supplied. `ResolveLegacyProfileIdentity` may use the one
default membership for v1 profile creation but rejects zero or multiple
matches. Validate the composite organization/group foreign key before insert.
Do not change `access.GroupStore`, member-count semantics, or effective v1 group
enforcement in this expand phase.

- [ ] **Step 4: Run store conformance and commit**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/userstore/... ./internal/tenancy -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/tenancy -count=1
git add internal/userstore internal/tenancy/profile_identity.go internal/tenancy/profile_identity_test.go
git commit -m "feat(access): assign access groups to tenant profiles"
```

### Task 8: Prove compatibility, rollback, and operator behavior

**Files:**
- Create: `internal/api/v1_tenancy_compat_test.go`
- Create: `docs/architecture/v10-security-foundation.md`
- Modify: `docs/superpowers/specs/2026-08-12-vondel-profile-login-and-shared-devices-design.md`

**Interfaces:**
- Produces the phase acceptance gate and operator runbook.
- Reconciles the older profile-login document with the approved rule that the primary profile's optional direct credential is distinct from the account credential.

- [ ] **Step 1: Add end-to-end compatibility tests**

Against a disposable migrated database, assert byte-semantic equality for v1
setup/login, profile list, profile select, PIN unlock, admin gate, and token
refresh before and after tenant backfill. Assert v10 remains read-only and that
ambiguous ownership blocks v10 without blocking v1 login/profile switching.

- [ ] **Step 2: Run the acceptance tests and fix only foundation regressions**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/api -run 'TestV1TenancyCompatibility|TestV10Foundation' -count=1 -v
```

Expected: PASS. Any v1 response-shape or behavior difference is a release
blocker; do not update golden expectations to accept drift.

- [ ] **Step 3: Write the operator runbook and reconcile the earlier spec**

Document clean setup, automatic single-admin ownership, multiple-admin
ambiguity resolution, v10 activation status, rollback command, and verification
queries. Update the earlier profile-login spec so primary direct login uses a
separate optional globally unique profile credential; preserve Silo account
login and profile switching exactly.

- [ ] **Step 4: Run full verification**

```bash
GOWORK=off gofmt -w internal/tenancy/*.go internal/auth/*.go internal/api/middleware/*.go internal/api/handlers/v10_system*.go internal/api/router_v10*.go internal/userstore/types.go internal/userstore/pgstore/profiles.go
GOWORK=off go test ./internal/tenancy ./internal/auth ./internal/api/middleware ./internal/api/handlers ./internal/api ./internal/access ./internal/userstore/...
GOWORK=off go test -race ./internal/tenancy ./internal/auth ./internal/api/middleware ./internal/access
GOWORK=off go vet ./internal/tenancy ./internal/auth ./internal/api/middleware ./internal/api/handlers ./internal/api ./internal/access ./internal/userstore/...
GOWORK=off go build ./cmd/vondel
git diff --check
```

With `SILO_TEST_DATABASE_URL` set, also rerun Tasks 1, 2, 7, and 8 DB-backed
commands from a fresh disposable database. Expected: every command exits 0.

- [ ] **Step 5: Commit the acceptance boundary**

```bash
git add internal/api/v1_tenancy_compat_test.go docs/architecture/v10-security-foundation.md docs/superpowers/specs/2026-08-12-vondel-profile-login-and-shared-devices-design.md
git commit -m "test(tenancy): lock v1 compatibility and migration gates"
```

## Completion gate

This plan is complete only when:

- clean install, single-admin upgrade, multiple-admin ambiguity, and rollback
  tests pass on disposable PostgreSQL;
- all v1 compatibility cases are unchanged;
- v10 exposes only the exact implemented read-only capabilities;
- profile organization and access-group identity are persisted but legacy
  enforcement remains unchanged;
- no route guesses an organization for a v10 request;
- no second owner can be assigned through races or retries;
- normal and race tests, vet, build, and diff checks pass; and
- the phase receives an independent security/spec review before Plan 2 starts.
