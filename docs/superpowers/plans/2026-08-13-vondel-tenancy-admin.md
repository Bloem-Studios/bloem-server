# Vondel Tenancy Administration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a complete v10 tenancy administration API and unified web workspace for organizations, immutable resource ownership, direct entitlements, versioned default bundles, durable bulk application, and audit history.

**Architecture:** Extend the existing `tenancy` and `resourcetenancy` boundaries with a focused `tenancyadmin` application package. PostgreSQL remains authoritative for lifecycle, concurrency, idempotency, audit, and restart-safe jobs; v10 handlers expose platform-admin mutations and scoped organization-admin reads; React consumes those APIs through typed TanStack Query hooks.

**Tech Stack:** Go 1.26, chi, pgx/PostgreSQL 18, Goose SQL migrations, React 19, TypeScript, TanStack Query, Vitest, Testing Library, Playwright-compatible browser tests.

**Spec:** `docs/superpowers/specs/2026-08-13-vondel-tenancy-admin-design.md`

## Global Constraints

- Existing `/api/v1` payloads, routes, Silo login, and profile switching remain unchanged.
- All tenancy management endpoints live under `/api/v10`.
- Platform-owner authority is the initial platform-admin implementation; expose it through `AuthorityResolver` so a subsequent delegated-role delivery can replace the lookup without changing handlers.
- Organization administrators have read-only access to their own organization and cannot enumerate hidden organizations or resources.
- Resource ownership is selected at creation and immutable afterward.
- Published bundle revisions and audit events are immutable.
- Direct entitlement changes are immediate, revision-checked, idempotent, and audited.
- Bulk application is asynchronous, restart-safe, bounded, dry-run-first, and atomic per organization.
- Large lists use bounded cursor pagination and indexed filters.
- Existing compatibility creation paths continue to create platform-owned resources and grant the default organization directly.
- Commands assume the repository root is the current working directory.

---

## File Map

### Database and domain

- Create `migrations/sql/20260813100000_tenancy_administration.sql`: bundle lifecycle, idempotency, audit, bulk jobs/results, indexes, and reversible down migration.
- Create `internal/tenancyadmin/types.go`: public application types, cursors, filters, errors, actor, and mutation inputs.
- Create `internal/tenancyadmin/repository.go`: paginated organizations/resources/audit reads and platform authority lookup.
- Create `internal/tenancyadmin/entitlements.go`: transactional direct entitlement state machine and audit writes.
- Create `internal/tenancyadmin/bundles.go`: draft, diff, and publish operations.
- Create `internal/tenancyadmin/bulk.go`: preview, launch, claim, apply, retry, and progress reads.
- Create matching focused tests under `internal/tenancyadmin/*_test.go`.

### API and wiring

- Create `internal/api/handlers/v10_tenancy_admin.go`: request decoding, response mapping, cursor handling, and error mapping.
- Create `internal/api/handlers/v10_tenancy_admin_test.go`: platform and organization authorization plus handler contracts.
- Modify `internal/api/router_v10.go`: mount platform and organization read/mutation groups.
- Modify `internal/api/router_v10_test.go`: lock route/middleware boundaries.
- Modify `internal/api/handlers/v10_system.go`: advertise `tenancy_administration` capability.
- Modify `internal/api/handlers/v10_system_test.go`: lock capability behavior.
- Modify `internal/api/router.go` and `cmd/silo/main.go`: inject the tenancy service and run the durable bulk worker.

### Owner-aware creation

- Modify `internal/catalog/folder_repo.go`: add `CreateOwned` while preserving `Create`.
- Modify `internal/plugins/installation.go` and `internal/plugins/service.go`: carry an immutable owner into installation persistence without changing legacy defaults.
- Add owner-aware creation tests beside the existing catalog/plugin tests.
- Add the v10 owner-aware library and plugin endpoints to `v10_tenancy_admin.go`.

### Web

- Modify `web/src/api/types.ts`: v10 tenancy DTOs and mutation bodies.
- Modify `web/src/hooks/queries/keys.ts`: tenancy query keys.
- Create `web/src/hooks/queries/admin/tenancy.ts`: typed queries, mutations, invalidation, and errors.
- Create `web/src/pages/AdminTenancy.tsx`: unified workspace shell and overview.
- Create `web/src/components/admin/tenancy/TenancyAdminGate.tsx`: capability/authority gate that admits platform owners or current organization admins without broadening legacy `/admin/*` access.
- Create `web/src/components/admin/tenancy/OrganizationsPanel.tsx`.
- Create `web/src/components/admin/tenancy/ResourcesPanel.tsx`.
- Create `web/src/components/admin/tenancy/BundlePanel.tsx`.
- Create `web/src/components/admin/tenancy/AuditPanel.tsx`.
- Create `web/src/components/admin/tenancy/TenancyDialogs.tsx`.
- Add focused tests beside each component.
- Modify `web/src/App.tsx`, `web/src/lib/adminNavigation.ts`, and their tests to mount and discover the workspace.
- Modify the existing library/plugin creation form and hook files to send owner selection through v10 only.

### Verification and documentation

- Create `internal/api/v10_tenancy_admin_integration_test.go`: disposable-PostgreSQL end-to-end API coverage.
- Create `web/src/pages/AdminTenancy.integration.test.tsx`: platform-admin and organization-admin flows.
- Modify `docs/architecture/resource-tenancy-foundation.md`: operational management contract.
- Modify `.github/workflows/ci.yml`: run the new disposable-PostgreSQL tenancy administration suite.

---

### Task 1: Persist bundle lifecycle, audit, idempotency, and bulk jobs

**Files:**
- Create: `migrations/sql/20260813100000_tenancy_administration.sql`
- Test: `internal/database/tenancy_administration_migration_test.go`

**Interfaces:**
- Consumes: tables introduced by migrations `20260812190000` and `20260813090000`.
- Produces: `tenancy_audit_events`, `tenancy_idempotency_keys`, `tenancy_bulk_jobs`, `tenancy_bulk_job_targets`, and published/draft metadata on `entitlement_bundle_versions`.

- [ ] **Step 1: Write the failing migration tests**

Add disposable-PostgreSQL cases named:

```go
func TestTenancyAdministrationMigrationCleanInstallAndRollback(t *testing.T)
func TestTenancyAdministrationMigrationRejectsMutablePublishedRevision(t *testing.T)
func TestTenancyAdministrationMigrationEnforcesSingleDraftAndIdempotency(t *testing.T)
func TestTenancyAdministrationMigrationPreservesResourceRootsOnDown(t *testing.T)
```

The tests must migrate through `20260813090000`, prove the new relations are absent, migrate up, verify constraints with real inserts/updates, migrate down to `20260813090000`, and prove organizations, folders, plugins, owners, and entitlements remain.

- [ ] **Step 2: Run the focused tests and capture RED**

Run:

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -run TenancyAdministration -count=1 -v
```

Expected: FAIL because migration `20260813100000` and its tables/columns do not exist.

- [ ] **Step 3: Create the migration with explicit lifecycle constraints**

Use the repository generator, retaining the resulting version `20260813100000`, then implement the schema:

```sql
ALTER TABLE entitlement_bundle_versions
  ADD COLUMN lifecycle text NOT NULL DEFAULT 'published'
    CHECK (lifecycle IN ('draft','published')),
  ADD COLUMN reason text NOT NULL DEFAULT '',
  ADD COLUMN published_by_account_id integer REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN published_at timestamptz,
  ADD CONSTRAINT entitlement_bundle_versions_publication_check CHECK (
    (lifecycle='draft' AND published_by_account_id IS NULL AND published_at IS NULL) OR
    (lifecycle='published' AND published_at IS NOT NULL)
  );

CREATE UNIQUE INDEX entitlement_bundle_versions_one_draft_idx
  ON entitlement_bundle_versions(bundle_id) WHERE lifecycle='draft';
```

Backfill revision 1 as published with its `created_at`. Add deferrable constraint triggers that reject update/delete of published versions and their members while allowing the one draft to change. Add append-only audit enforcement, unique `(actor_account_id, idempotency_key)` keys, bulk job state checks (`queued`, `running`, `completed`, `completed_with_errors`, `failed`), frozen target rows, cursor-supporting indexes, and a down migration that removes only administration additions.

- [ ] **Step 4: Run migration tests through up/down/up**

Run the Step 2 command. Expected: PASS with no skipped test.

- [ ] **Step 5: Run database regression tests**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/sql/20260813100000_tenancy_administration.sql internal/database/tenancy_administration_migration_test.go
git commit -m "feat(tenancy): persist administration lifecycle"
```

---

### Task 2: Add authority and paginated administration reads

**Files:**
- Create: `internal/tenancyadmin/types.go`
- Create: `internal/tenancyadmin/repository.go`
- Test: `internal/tenancyadmin/repository_test.go`
- Modify: `internal/resourcetenancy/materializer.go`
- Test: `internal/resourcetenancy/materializer_test.go`

**Interfaces:**
- Consumes: `tenancy.Context`, `resourcetenancy.RootRef`, and Task 1 schema.
- Produces:

```go
type Actor struct { AccountID int; Authority string }
type Page[T any] struct { Items []T; NextCursor string }
type Repository struct { /* pgx pool */ }
func NewRepository(*pgxpool.Pool) *Repository
type Service struct { /* repository plus transactional pool */ }
func NewService(*pgxpool.Pool) *Service
func (r *Repository) RequirePlatformAdmin(context.Context, int) (Actor, error)
func (r *Repository) RequireOrganizationAdmin(context.Context, tenancy.Context) (Actor, error)
func (r *Repository) Overview(context.Context) (Overview, error)
func (r *Repository) ListOrganizations(context.Context, OrganizationFilter) (Page[OrganizationSummary], error)
func (r *Repository) ListResources(context.Context, ResourceFilter) (Page[ResourceSummary], error)
func (r *Repository) ListOrganizationAccess(context.Context, uuid.UUID, AccessFilter) (Page[AccessSummary], error)
func (r *Repository) ListAudit(context.Context, AuditFilter) (Page[AuditEvent], error)
func (s *Service) CreateOrganization(context.Context, Actor, CreateOrganizationInput) (OrganizationSummary, error)
func (s *Service) SetOrganizationStatus(context.Context, Actor, OrganizationStatusInput) (OrganizationSummary, error)
func (m *Materializer) MaterializeDefaultBundleTx(context.Context, pgx.Tx, uuid.UUID, resourcetenancy.Actor) (MaterializationResult, error)
```

- [ ] **Step 1: Write failing repository contract tests**

Cover platform owner success; enabled legacy admin who is not platform owner denial; disabled owner denial; current organization membership `legacy_role='admin'` success; regular or suspended organization membership denial; organization-scoped access filtering; hidden cross-organization roots; stable cursor pagination with duplicate timestamps; resource kind/owner/status filters; and audit organization scoping. Add organization lifecycle tests proving create atomically inserts an initializing organization, owner resource row, owner membership, default-bundle materialization, activation, and audit; status mutation must be revision-checked, idempotent, and invalidate the organization policy revision.

- [ ] **Step 2: Run tests and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancyadmin -run 'Authority|List|Overview' -count=1 -v
```

Expected: FAIL because `tenancyadmin` is undefined.

- [ ] **Step 3: Implement bounded types and opaque cursors**

Define filters with `Limit` clamped to `1..100`. Encode cursors as base64url JSON containing only the last stable sort tuple. Validate kind/status values before SQL. Define sentinel errors:

```go
var (
    ErrHidden = errors.New("tenancy object not found")
    ErrForbidden = errors.New("tenancy administration forbidden")
    ErrConflict = errors.New("tenancy administration conflict")
    ErrInvalid = errors.New("invalid tenancy administration request")
    ErrUnavailable = errors.New("tenancy administration unavailable")
)
```

- [ ] **Step 4: Implement indexed reads and current platform authority**

`RequirePlatformAdmin` must join `platform_security` to `users`, require the exact current owner, `users.enabled`, and `users.role='admin'`, and return authority `platform_owner`. `RequireOrganizationAdmin` must re-read the exact membership from `tenancy.Context`, require active organization/membership and `legacy_role='admin'`, and return authority `organization_admin`. Organization-admin read methods accept a server-resolved `tenancy.Context`; they never accept organization scope from headers. Extract the existing materializer body behind `MaterializeDefaultBundleTx`; its public pool-owned method begins/commits and delegates, preserving current behavior. Organization creation accepts `slug`, `name`, and an existing enabled owner account ID, creates that account's active admin membership, materializes the default bundle through the transaction-aware method, activates the organization, and audits the result in one transaction.

- [ ] **Step 5: Run normal and race tests**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancyadmin -count=1
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/tenancyadmin -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tenancyadmin internal/resourcetenancy/materializer.go internal/resourcetenancy/materializer_test.go
git commit -m "feat(tenancy): add administration read model"
```

---

### Task 3: Implement audited direct entitlement mutations

**Files:**
- Create: `internal/tenancyadmin/entitlements.go`
- Test: `internal/tenancyadmin/entitlements_test.go`
- Modify: `internal/tenancyadmin/types.go`

**Interfaces:**
- Consumes: `Actor`, Task 1 idempotency/audit tables, and typed roots.
- Produces:

```go
type EntitlementAction string // grant, suspend, restore, revoke
type EntitlementMutation struct {
    OrganizationID uuid.UUID
    Root resourcetenancy.RootRef
    Action EntitlementAction
    ExpectedRevision int64
    IdempotencyKey string
    Reason string
}
func (s *Service) MutateEntitlement(context.Context, Actor, EntitlementMutation) (EntitlementResult, error)
```

- [ ] **Step 1: Write the state-machine tests first**

Use real PostgreSQL to prove: grant creates active revision 1; replay returns the identical result; concurrent duplicate grants create one live row; suspend/restore preserve ID and increment revision; stale revision conflicts; revoke is terminal; grant after revoke creates a new ID; invalid transitions do not audit; successful changes write exactly one redacted audit event in the same transaction.

- [ ] **Step 2: Run tests and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancyadmin -run EntitlementMutation -count=1 -v
```

Expected: FAIL because `Service.MutateEntitlement` does not exist.

- [ ] **Step 3: Implement one serializable transaction per mutation**

Lock the organization, root owner, live entitlement, and idempotency row in a consistent order. Require active organization and platform-owned root. Apply the explicit transition table:

```text
none + grant -> active
active + suspend -> suspended
suspended + restore -> active
active|suspended + revoke -> revoked
revoked + grant -> new active row
```

All other combinations return `ErrConflict`. Trim reasons and require `1..500` Unicode code points. Require UUID idempotency keys.

- [ ] **Step 4: Insert audit and idempotency results atomically**

Audit payloads contain only typed IDs, action, state, and revisions. Store the serialized successful response against the actor/idempotency key so exact replay never performs a second transition.

- [ ] **Step 5: Run normal and race tests**

Run both Task 2 commands. Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tenancyadmin/entitlements.go internal/tenancyadmin/entitlements_test.go internal/tenancyadmin/types.go
git commit -m "feat(tenancy): add audited entitlement mutations"
```

---

### Task 4: Implement immutable bundle draft, diff, and publication

**Files:**
- Create: `internal/tenancyadmin/bundles.go`
- Test: `internal/tenancyadmin/bundles_test.go`
- Modify: `internal/tenancyadmin/types.go`

**Interfaces:**
- Produces:

```go
func (s *Service) GetDefaultBundle(context.Context) (BundleView, error)
func (s *Service) CreateDraft(context.Context, Actor, CreateDraftInput) (BundleView, error)
func (s *Service) ReplaceDraftMembers(context.Context, Actor, ReplaceDraftMembersInput) (BundleDiff, error)
func (s *Service) PublishDraft(context.Context, Actor, PublishDraftInput) (BundleView, error)
```

- [ ] **Step 1: Write failing bundle lifecycle tests**

Prove one draft only; draft revision equals published revision plus one; draft begins as an exact member copy; only platform-owned typed resources can be members; replacement is revision-checked and idempotent; diff is deterministic; publish atomically marks the draft published and advances `active_revision`; published rows/members reject modification; publication alone does not change existing entitlements.

- [ ] **Step 2: Run tests and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancyadmin -run Bundle -count=1 -v
```

Expected: FAIL because bundle administration methods are absent.

- [ ] **Step 3: Implement draft and diff operations**

Canonicalize members by `(root_kind, root_id)`, reject duplicates, lock the default bundle row, and write audit/idempotency records in the same transaction. Return added, removed, and unchanged arrays sorted by kind and ID.

- [ ] **Step 4: Implement atomic publication**

Require expected active/draft revisions, non-empty reason, at least one valid member, and current platform authority. Set publisher/time/lifecycle and advance `active_revision` in one transaction. Do not materialize organizations from this method.

- [ ] **Step 5: Run package normal/race tests**

Run both Task 2 commands. Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tenancyadmin/bundles.go internal/tenancyadmin/bundles_test.go internal/tenancyadmin/types.go
git commit -m "feat(tenancy): manage immutable bundle revisions"
```

---

### Task 5: Add restart-safe dry-run and bulk application

**Files:**
- Create: `internal/tenancyadmin/bulk.go`
- Test: `internal/tenancyadmin/bulk_test.go`
- Modify: `internal/tenancyadmin/types.go`

**Interfaces:**
- Produces:

```go
func (s *Service) PreviewBulkApply(context.Context, Actor, BulkPreviewInput) (BulkPreview, error)
func (s *Service) LaunchBulkApply(context.Context, Actor, BulkLaunchInput) (BulkJob, error)
func (s *Service) RetryBulkApply(context.Context, Actor, uuid.UUID, string, string) (BulkJob, error)
func (s *Service) GetBulkJob(context.Context, Actor, uuid.UUID) (BulkJob, error)
type Worker struct { /* service, poll interval, concurrency */ }
func NewWorker(*Service, WorkerOptions) *Worker
func (w *Worker) Run(context.Context) error
```

- [ ] **Step 1: Write failing preview/job/worker tests**

Cover frozen target selection, preview hash mismatch, published-revision requirement, no-op targets, additions only (never revoke unrelated direct grants), one transaction per organization, bounded concurrency, heartbeat/lease recovery after cancellation, partial completion, exact progress, retry of failed/unprocessed targets only, and audit chain retention.

- [ ] **Step 2: Run tests and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/tenancyadmin -run Bulk -count=1 -v
```

Expected: FAIL because bulk methods and worker are absent.

- [ ] **Step 3: Implement deterministic preview and launch**

The preview returns target count, add/no-op/conflict counts, and a SHA-256 hash over bundle ID/revision plus sorted organization IDs and outcomes. Launch requires that hash, freezes target rows, records the actor/reason/idempotency key, and enqueues exactly one job.

- [ ] **Step 4: Implement leased worker processing**

Use `FOR UPDATE SKIP LOCKED`, a bounded default concurrency of 4, 30-second leases, and periodic heartbeat. Each target transaction inserts missing active entitlements with bundle provenance, records its result and audit event, and advances counters. Recovery reclaims expired running targets.

- [ ] **Step 5: Implement retry**

Retry creates a linked job containing only failed and unprocessed target IDs. Successful/no-op targets are not repeated. Require a fresh idempotency key and reason.

- [ ] **Step 6: Run normal and race tests**

Run both Task 2 commands. Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tenancyadmin/bulk.go internal/tenancyadmin/bulk_test.go internal/tenancyadmin/types.go
git commit -m "feat(tenancy): apply bundles with durable jobs"
```

---

### Task 6: Expose v10 capability, routes, and authorization boundaries

**Files:**
- Create: `internal/api/handlers/v10_tenancy_admin.go`
- Test: `internal/api/handlers/v10_tenancy_admin_test.go`
- Modify: `internal/api/router_v10.go`
- Test: `internal/api/router_v10_test.go`
- Modify: `internal/api/handlers/v10_system.go`
- Test: `internal/api/handlers/v10_system_test.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/silo/main.go`

**Interfaces:**
- Consumes: Tasks 2–5 service methods.
- Produces platform routes under `/api/v10/admin/tenancy/*` and organization reads under `/api/v10/tenancy/*`.

- [ ] **Step 1: Write handler and router authorization tests**

Lock these boundaries:

```text
GET  /api/v10/capabilities
GET  /api/v10/admin/tenancy/overview
GET  /api/v10/admin/tenancy/context
GET  /api/v10/admin/tenancy/organizations
POST /api/v10/admin/tenancy/organizations
POST /api/v10/admin/tenancy/organizations/{id}/status
GET  /api/v10/admin/tenancy/resources
POST /api/v10/admin/tenancy/organizations/{id}/entitlements/{kind}/{rootID}/{action}
GET  /api/v10/admin/tenancy/default-bundle
POST /api/v10/admin/tenancy/default-bundle/draft
PUT  /api/v10/admin/tenancy/default-bundle/draft/members
POST /api/v10/admin/tenancy/default-bundle/publish
POST /api/v10/admin/tenancy/bulk/preview
POST /api/v10/admin/tenancy/bulk
GET  /api/v10/admin/tenancy/bulk/{id}
POST /api/v10/admin/tenancy/bulk/{id}/retry
GET  /api/v10/admin/tenancy/audit
GET  /api/v10/tenancy/organization
GET  /api/v10/tenancy/context
GET  /api/v10/tenancy/resources
GET  /api/v10/tenancy/audit
```

Tests must prove platform routes require normal authentication plus current platform-owner lookup; organization routes require `TenantMiddleware.RequireV10` plus membership role `admin`; regular users receive forbidden; cross-scope reads return non-disclosing not-found. The two context endpoints return the server-derived authority and are the only inputs the web gate uses.

- [ ] **Step 2: Run tests and capture RED**

```bash
GOWORK=off go test ./internal/api/handlers ./internal/api -run 'V10Tenancy|V10Capabilities' -count=1 -v
```

Expected: FAIL because routes/handlers/capability are absent.

- [ ] **Step 3: Implement request/response contracts and error mapping**

Add `tenancy_administration: true` to v10 capability features. Decode bounded JSON with unknown-field rejection. Obtain actor IDs only from authenticated claims. Map `ErrHidden` to 404, `ErrForbidden` to 403, `ErrConflict` to 409, `ErrInvalid` to field-addressable 400, and `ErrUnavailable` to 503. Set `X-Request-ID` and include `request_id` in mutation responses.

- [ ] **Step 4: Wire service and worker lifecycle**

Add `TenancyAdmin *tenancyadmin.Service` to router dependencies. Construct it once from the production pool in `cmd/silo/main.go`, start one worker with the application context, and pass the same service to routes. Shutdown must cancel and wait for the worker before closing the pool.

- [ ] **Step 5: Run focused and broad API tests**

```bash
GOWORK=off go test ./internal/api/handlers ./internal/api -count=1
GOWORK=off go test -race ./internal/api/handlers ./internal/api -run 'V10Tenancy|V10Capabilities' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/v10_tenancy_admin.go internal/api/handlers/v10_tenancy_admin_test.go internal/api/router_v10.go internal/api/router_v10_test.go internal/api/handlers/v10_system.go internal/api/handlers/v10_system_test.go internal/api/router.go cmd/silo/main.go
git commit -m "feat(api): expose v10 tenancy administration"
```

---

### Task 7: Add immutable owner selection to resource creation

**Files:**
- Modify: `internal/catalog/folder_repo.go`
- Test: `internal/catalog/folder_repo_test.go`
- Modify: `internal/plugins/installation.go`
- Modify: `internal/plugins/service.go`
- Test: `internal/plugins/service_install_local_test.go`
- Modify/Test: `internal/api/handlers/v10_tenancy_admin.go`, `internal/api/handlers/v10_tenancy_admin_test.go`

**Interfaces:**
- Produces:

```go
type OwnerSelection struct { Kind resourcetenancy.OwnerKind; OrganizationID *uuid.UUID }
func (r *FolderRepository) CreateOwned(context.Context, CreateFolderInput, OwnerSelection) (*models.MediaFolder, error)
type InstallOptions struct { Owner OwnerSelection /* existing install fields remain */ }
```

- [ ] **Step 1: Write failing ownership creation tests**

Prove legacy `Create` and existing v1 plugin installation still choose platform ownership and default-org entitlement; v10 platform-owner creation may choose platform or one active organization; organization-owned roots receive that organization's owner and no platform entitlement; suspended/hidden organizations fail; owner cannot be changed after creation; failed plugin introspection leaves no owned installation.

- [ ] **Step 2: Run tests and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/catalog ./internal/plugins ./internal/api/handlers -run 'Owned|OwnerSelection' -count=1 -v
```

Expected: FAIL because owner-aware paths are absent.

- [ ] **Step 3: Implement owner resolution and owned creation**

Resolve selections server-side to `resource_owners.id`, require active organization, and persist `owner_id` in the initial insert. Keep existing `Create`/install methods as wrappers passing platform selection. Do not add owner mutation SQL or methods.

- [ ] **Step 4: Add v10 creation endpoints**

Add `POST /api/v10/admin/tenancy/libraries` and `POST /api/v10/admin/tenancy/plugins/installations`. Reuse existing validation/installation services, accept `owner_kind` plus `owner_organization_id`, and return the typed resource summary. Never expose these fields through existing v1 responses.

- [ ] **Step 5: Run focused and compatibility suites**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/catalog ./internal/plugins ./internal/api -count=1
GOWORK=off go test ./internal/api -run V1TenancyCompatibility -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog internal/plugins internal/api/handlers/v10_tenancy_admin.go internal/api/handlers/v10_tenancy_admin_test.go
git commit -m "feat(tenancy): select immutable resource owners"
```

---

### Task 8: Add typed web client, navigation, and workspace shell

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/hooks/queries/keys.ts`
- Create: `web/src/hooks/queries/admin/tenancy.ts`
- Test: `web/src/hooks/queries/admin/tenancy.test.ts`
- Create: `web/src/pages/AdminTenancy.tsx`
- Test: `web/src/pages/AdminTenancy.test.tsx`
- Create: `web/src/components/admin/tenancy/TenancyAdminGate.tsx`
- Test: `web/src/components/admin/tenancy/TenancyAdminGate.test.tsx`
- Modify: `web/src/components/AdminLayout.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/adminNavigation.ts`
- Test: `web/src/components/AdminSidebar.test.tsx`

**Interfaces:**
- Consumes: Task 6 JSON contracts.
- Produces `useTenancyCapability`, `useTenancyOverview`, `useOrganizations`, `useResources`, `useOrganizationAccess`, `useDefaultBundle`, `useTenancyAudit`, and mutation hooks mirroring v10 actions.

- [ ] **Step 1: Write failing client and route tests**

Test exact v10 URLs/methods/bodies, cursor retention, UUID idempotency header/body, invalidation after mutations, capability-hidden navigation, `/admin/tenancy` routing, tab query parameters, loading/empty/error states, and read-only rendering from authority returned by the API. Prove an organization admin can enter only `/admin/tenancy`, cannot enter any legacy admin route, and sees a tenancy-only sidebar; prove this change does not broaden `RequireAdmin`.

- [ ] **Step 2: Run tests and capture RED**

```bash
cd web && pnpm vitest run src/hooks/queries/admin/tenancy.test.ts src/pages/AdminTenancy.test.tsx src/components/AdminSidebar.test.tsx
```

Expected: FAIL because hooks/page/navigation are absent.

- [ ] **Step 3: Add exact DTOs and query keys**

Use discriminated unions for `root_kind`, `owner_kind`, entitlement status, bundle lifecycle, job state, and audit action. Keep cursors opaque strings. Do not use `any` or duplicate server-derived authority logic.

- [ ] **Step 4: Implement the workspace shell**

Add the Tenancy navigation entry under Security. Mount exact `/admin/tenancy` before the existing `/admin/*` route through `TenancyAdminGate`, leaving `RequireAdmin` unchanged. Allow `AdminLayout` to render a tenancy-only navigation mode for organization admins. Build stable tabs for overview, organizations, resources, default bundle, and audit; organization-admin mode exposes only its organization access/history views. Fetch only the selected tab's data. Show mutation controls only when the server response says `authority: "platform_owner"`.

- [ ] **Step 5: Run focused web tests and checks**

```bash
cd web && pnpm vitest run src/hooks/queries/admin/tenancy.test.ts src/components/admin/tenancy/TenancyAdminGate.test.tsx src/pages/AdminTenancy.test.tsx src/components/AdminSidebar.test.tsx
cd web && pnpm run lint && pnpm run format:check && pnpm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/api/types.ts web/src/hooks/queries/keys.ts web/src/hooks/queries/admin/tenancy.ts web/src/hooks/queries/admin/tenancy.test.ts web/src/components/admin/tenancy/TenancyAdminGate.tsx web/src/components/admin/tenancy/TenancyAdminGate.test.tsx web/src/pages/AdminTenancy.tsx web/src/pages/AdminTenancy.test.tsx web/src/App.tsx web/src/components/AdminLayout.tsx web/src/lib/adminNavigation.ts web/src/components/AdminSidebar.test.tsx
git commit -m "feat(web): add tenancy administration workspace"
```

---

### Task 9: Build organizations, resources, and entitlement controls

**Files:**
- Create/Test: `web/src/components/admin/tenancy/OrganizationsPanel.tsx`, `OrganizationsPanel.test.tsx`
- Create/Test: `web/src/components/admin/tenancy/ResourcesPanel.tsx`, `ResourcesPanel.test.tsx`
- Create/Test: `web/src/components/admin/tenancy/TenancyDialogs.tsx`, `TenancyDialogs.test.tsx`
- Modify/Test: existing library form/hook files under `web/src/components/admin/libraries/` and `web/src/hooks/queries/admin/libraries.ts`
- Modify/Test: `web/src/pages/AdminPlugins.tsx` and `web/src/hooks/queries/admin/plugins.ts`

**Interfaces:**
- Consumes: Task 8 hooks and Task 7 creation endpoints.
- Produces complete organization lifecycle, resource inventory, immutable owner display/selection, and direct entitlement flows.

- [ ] **Step 1: Write failing component tests**

Cover debounced server search, cursor next/back behavior, status filters, organization detail tabs, ownership labels, owner selector defaults, hidden ownership editing after create, grant/suspend/restore/revoke confirmation, required reason, affected-resource preview, focus restoration, Escape behavior, stale 409 refresh messaging, and organization-admin absence of buttons.

- [ ] **Step 2: Run tests and capture RED**

```bash
cd web && pnpm vitest run src/components/admin/tenancy/OrganizationsPanel.test.tsx src/components/admin/tenancy/ResourcesPanel.test.tsx src/components/admin/tenancy/TenancyDialogs.test.tsx
```

Expected: FAIL because components are absent.

- [ ] **Step 3: Implement organizations and resource inventory**

Use semantic tables on wide screens and labelled stacked rows on narrow screens. Keep filters in URL search parameters. Organization status changes and entitlement actions require a reason and confirmation. Render request IDs in success details and errors.

- [ ] **Step 4: Integrate immutable owner selection into creation forms**

Load active organizations only for platform owners. Default to platform, require explicit confirmation for organization ownership, submit through v10, and lock the displayed owner after successful creation. Legacy callers and screens without capability continue their existing v1 path.

- [ ] **Step 5: Run focused and broad web tests**

```bash
cd web && pnpm vitest run src/components/admin/tenancy src/pages/AdminLibraries.test.tsx src/pages/AdminPlugins.test.tsx
cd web && pnpm run lint && pnpm run format:check && pnpm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/admin/tenancy web/src/components/admin/libraries web/src/hooks/queries/admin/libraries.ts web/src/pages/AdminPlugins.tsx web/src/pages/AdminPlugins.test.tsx web/src/hooks/queries/admin/plugins.ts
git commit -m "feat(web): manage organizations and resource access"
```

---

### Task 10: Build bundle, bulk-job, and audit interfaces

**Files:**
- Create/Test: `web/src/components/admin/tenancy/BundlePanel.tsx`, `BundlePanel.test.tsx`
- Create/Test: `web/src/components/admin/tenancy/AuditPanel.tsx`, `AuditPanel.test.tsx`
- Modify/Test: `web/src/pages/AdminTenancy.tsx`, `AdminTenancy.test.tsx`

**Interfaces:**
- Consumes: Task 8 bundle/bulk/audit hooks.
- Produces draft editor, deterministic diff review, publication, bulk dry-run/launch/progress/retry, and paginated audit UI.

- [ ] **Step 1: Write failing UI tests**

Cover published-only initial state, create-draft copy, resource add/remove, deterministic diff, publish reason/confirmation, no automatic application to existing organizations, target filters, dry-run summary/hash, launch confirmation, progress polling, partial failure details, retry selection, audit filters/cursors, redacted payload rendering, and accessible live-region announcements.

- [ ] **Step 2: Run tests and capture RED**

```bash
cd web && pnpm vitest run src/components/admin/tenancy/BundlePanel.test.tsx src/components/admin/tenancy/AuditPanel.test.tsx src/pages/AdminTenancy.test.tsx
```

Expected: FAIL because bundle/audit panels are absent.

- [ ] **Step 3: Implement bundle revision workflow**

Render published and draft revisions separately. Save draft membership with expected revision and idempotency key. Publication displays exact additions/removals/unchanged counts and requires reason confirmation.

- [ ] **Step 4: Implement bulk flow and recovery UI**

Never enable launch before a current preview hash exists. Poll only queued/running jobs, stop on terminal state, show per-target failures without secrets, and make retry include only failed/unprocessed targets as returned by the server.

- [ ] **Step 5: Implement audit table**

Use server-side actor/organization/resource/action/time filters and cursor pagination. Render compact typed state changes and copyable request IDs. Do not stringify arbitrary unknown JSON into the DOM.

- [ ] **Step 6: Run focused and broad web verification**

```bash
cd web && pnpm vitest run src/components/admin/tenancy src/pages/AdminTenancy.test.tsx
cd web && pnpm run lint && pnpm run format:check && pnpm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/admin/tenancy/BundlePanel.tsx web/src/components/admin/tenancy/BundlePanel.test.tsx web/src/components/admin/tenancy/AuditPanel.tsx web/src/components/admin/tenancy/AuditPanel.test.tsx web/src/pages/AdminTenancy.tsx web/src/pages/AdminTenancy.test.tsx
git commit -m "feat(web): manage bundles and tenancy audit"
```

---

### Task 11: Lock end-to-end compatibility, operations, and CI

**Files:**
- Create: `internal/api/v10_tenancy_admin_integration_test.go`
- Create: `web/src/pages/AdminTenancy.integration.test.tsx`
- Modify: `docs/architecture/resource-tenancy-foundation.md`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: complete Tasks 1–10.
- Produces: clean-install acceptance evidence and enforced CI gates.

- [ ] **Step 1: Write the failing disposable-database integration test**

Exercise one real sequence: create platform owner and organization admin; create an organization; create platform and organization-owned resources; grant/suspend/restore/revoke; draft/publish revision 2; prove existing organization unchanged; preview/apply revision 2 to selected organizations; interrupt/restart worker; verify progress/audit; verify cross-org 404; run existing v1 login/profile/library/plugin reads unchanged.

- [ ] **Step 2: Run the integration test and capture RED**

```bash
SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/api -run TestV10TenancyAdministrationEndToEnd -count=1 -v
```

Expected: FAIL until all routes and worker behavior are connected.

- [ ] **Step 3: Add the browser-level integration test**

Mock only network transport, not component hooks. Exercise platform overview → organization detail → grant → draft diff → publish → dry run → bulk completion → audit. Rerender with organization-admin authority and assert the same scoped resource/history information remains visible while every mutation control and platform-wide tab is absent.

- [ ] **Step 4: Update architecture and CI documentation**

Document route groups, authority mapping, lifecycle tables, worker recovery, operational queries, and rollback boundary. Add CI steps under the existing disposable-PostgreSQL job:

```yaml
- name: Verify tenancy administration database and API
  run: GOWORK=off go test -race ./internal/tenancyadmin ./internal/api -run 'TenancyAdministration|V10Tenancy' -count=1
  env:
    SILO_TEST_DATABASE_URL: postgres://silo:silo@localhost:5432/silo_test?sslmode=disable
```

- [ ] **Step 5: Run full verification**

```bash
make embed-stub
GOWORK=off go test ./internal/tenancyadmin ./internal/database ./internal/api ./internal/catalog ./internal/plugins -count=1
GOWORK=off go test -race ./internal/tenancyadmin ./internal/api -run 'TenancyAdministration|V10Tenancy' -count=1
GOWORK=off go vet ./...
GOWORK=off go build ./...
cd web && pnpm vitest run src/components/admin/tenancy src/pages/AdminTenancy.test.tsx src/pages/AdminTenancy.integration.test.tsx
cd web && pnpm run lint && pnpm run format:check && pnpm run build
make verify-local-paths
git diff --check
```

Expected: every command PASS. If the repository-wide suite exposes a pre-existing unrelated failure, record it separately and rerun every affected package sequentially; do not weaken or skip a new gate.

- [ ] **Step 6: Verify against an empty disposable deployment**

Build the server image, start it with fresh PostgreSQL and Redis, complete initial setup, open `/admin/tenancy`, and execute the platform-admin workflow. Verify health, migration `20260813100000`, worker restart recovery, zero browser console errors, and unchanged Silo-compatible login/profile switching.

- [ ] **Step 7: Commit**

```bash
git add internal/api/v10_tenancy_admin_integration_test.go web/src/pages/AdminTenancy.integration.test.tsx docs/architecture/resource-tenancy-foundation.md .github/workflows/ci.yml
git commit -m "test(tenancy): verify administration end to end"
```

---

## Final Review Gate

- [ ] Confirm every commit is on top of current `origin/main`, the worktree is clean, and no migration version collision was introduced.
- [ ] Confirm the production Vondel database is not migrated until exact-head CI and Docker builds pass.
- [ ] Request an independent spec-compliance review focused on authorization, non-disclosure, idempotency, ownership immutability, bundle immutability, and restart recovery.
- [ ] Deploy the exact reviewed image to the empty Vondel server, allow normal startup migrations, verify `/api/v1/health`, v10 capability discovery, platform-admin workspace, organization-admin read-only workspace, and migration counts.
- [ ] Record the deployed commit and image in the host's `DEPLOYMENT` file without storing credentials in the repository.
