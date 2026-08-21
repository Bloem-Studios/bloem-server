# Bulk Policy Cohorts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add authoritative policy inspection and safe, durable retroactive cohort-policy operations for organization and direct accounts.

**Architecture:** Represent every non-default effective policy as an immutable organization cohort revision backed by a protected access group. Extend the existing immutable people-selection and durable bulk-job framework for preview-bound policy assignments, while exposing generic platform read and mutation APIs. Organization and platform UIs consume the same stores and job model.

**Tech Stack:** Go, PostgreSQL 18/pgx, chi, React, TypeScript, TanStack Query, Vitest, Playwright

**Spec:** `docs/superpowers/specs/2026-08-21-bulk-policy-cohorts-design.md`

## Global Constraints

- A bulk job contains at most 10,000 immutable account targets.
- Global template revisions remain immutable and are never revised by a selection-only operation.
- Every account remains attached to an enforceable access group; restore means the managed default.
- Inherited profiles move with the account; custom profiles move only with explicit `include_custom_profiles=true` confirmation.
- Authoritative reads come from current Server access groups and effective-policy resolution, never caller expectations.
- Cohort groups are protected from generic group update/delete/default mutation.
- Strict bounded JSON, scoped not-found behavior, expiring preview confirmation, idempotency, safe errors, and immutable audits apply to every new route.
- Public source, API names, documentation, tests, and UI copy remain standalone and contain no private integration or private product names.
- Preserve the unrelated untracked `web/package-lock.json`.

---

### Task 1: Persist immutable cohort revisions and adopt existing managed groups

**Files:**
- Create: `migrations/sql/20260822090000_entitlement_policy_cohorts.sql`
- Create: `internal/entitlements/cohort_store.go`
- Create: `internal/entitlements/cohort_store_test.go`
- Modify: `internal/access/group_store.go`
- Modify: `internal/access/group_store_test.go`
- Modify: `internal/database/entitlement_templates_migration_test.go`

**Interfaces:**
- Consumes: existing `entitlements.Policy`, immutable template revisions, and protected template-managed access groups.
- Produces:

```go
type CohortRevision struct {
    ID uuid.UUID
    OrganizationID uuid.UUID
    Name string
    Revision int64
    AccessGroupID int64
    SourceTemplateKey string
    SourceTemplateRevision int64
    ParentID uuid.UUID
    DerivationKind string
    Policy Policy
    PolicyDigest string
    Archived bool
    CreatedByAccountID int
    CreatedAt time.Time
}

func (s *Store) EnsureExactCohortInTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64, actorID int) (CohortRevision, bool, error)
func (s *Store) DeriveCohortInTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, parentID uuid.UUID, name string, patch PolicyPatch, actorID int) (CohortRevision, bool, error)
func (s *Store) ListCohorts(ctx context.Context, organizationID uuid.UUID, includeArchived bool) ([]CohortRevision, error)
func (s *Store) GetCohort(ctx context.Context, organizationID, cohortID uuid.UUID) (CohortRevision, error)
```

- [ ] **Step 1: Write migration and store RED tests**

Add real-PostgreSQL tests proving exact-template adoption does not change the group policy or assignments, identical exact cohorts converge, a derived cohort creates a separate protected group, revision rows reject update/delete, and Down refuses while derived cohorts exist.

```go
func TestEnsureExactCohortAdoptsManagedGroupWithoutPolicyDrift(t *testing.T) {
    fixture := newCohortFixture(t)
    before := fixture.snapshotManagedGroup()
    cohort, created, err := fixture.store.EnsureExactCohortInTx(fixture.ctx, fixture.tx, fixture.organizationID, "standard", 1, fixture.actorID)
    require.NoError(t, err)
    require.True(t, created)
    require.Equal(t, before, fixture.snapshotGroup(cohort.AccessGroupID))
}

func TestDeriveCohortCreatesImmutableProtectedGroup(t *testing.T) {
    fixture := newCohortFixture(t)
    disabled := false
    derived, created, err := fixture.store.DeriveCohortInTx(fixture.ctx, fixture.tx, fixture.organizationID, fixture.standardCohortID, "Standard without downloads", PolicyPatch{DownloadAllowed: &disabled}, fixture.actorID)
    require.NoError(t, err)
    require.True(t, created)
    mutated := "mutated"
    _, err = fixture.groupStore.Update(fixture.ctx, fixture.organizationID, derived.AccessGroupID, access.UpdateGroupInput{Name: &mutated})
    require.ErrorIs(t, err, access.ErrManagedGroup)
}
```

- [ ] **Step 2: Run RED tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/entitlements ./internal/access ./internal/database -run 'Cohort|EntitlementTemplatesMigration' -count=1`

Expected: FAIL because cohort tables/types/functions do not exist.

- [ ] **Step 3: Add additive schema and adoption transaction**

Create stable lineage and immutable revision tables with organization ownership, one access group per revision, full resolved policy, source/parent provenance, digest, creator, and archive metadata. Add a managed-cohort marker to `access_groups`, constraints preventing template/cohort mismatch, immutability triggers, indexes, and rollback refusal.

Implement adoption and derivation in one transaction. Reuse the exact policy validation and dynamic-library materialization functions from `template_store.go`; do not copy validation logic.

- [ ] **Step 4: Protect cohort groups in generic access APIs**

Update group mutation guards so a group is managed when either template or cohort metadata is present.

```go
if group.ManagedTemplateKey != "" || group.ManagedCohortID != uuid.Nil {
    return Group{}, ErrManagedGroup
}
```

- [ ] **Step 5: Run GREEN and migration rollback tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL TEST_DATABASE_DESTRUCTIVE_URL=$TEST_DATABASE_DESTRUCTIVE_URL go test ./internal/entitlements ./internal/access ./internal/database -run 'Cohort|EntitlementTemplatesMigration' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/sql/20260822090000_entitlement_policy_cohorts.sql internal/entitlements/cohort_store.go internal/entitlements/cohort_store_test.go internal/access/group_store.go internal/access/group_store_test.go internal/database/entitlement_templates_migration_test.go
git commit -m "feat: add immutable entitlement cohorts"
```

### Task 2: Expose authoritative current account and profile policies

**Files:**
- Create: `internal/entitlements/account_policy.go`
- Create: `internal/entitlements/account_policy_test.go`
- Create: `internal/api/handlers/entitlement_account_policy.go`
- Create: `internal/api/handlers/entitlement_account_policy_test.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/handlers/admin.go`

**Interfaces:**
- Consumes: cohort store, `access.EffectivePolicyForSubject`, organization/account ownership, platform authority.
- Produces:

```go
type ProfilePolicySnapshot struct {
    ProfileID string `json:"profile_id"`
    ProfileName string `json:"profile_name"`
    GroupID int64 `json:"group_id"`
    InheritsAccount bool `json:"inherits_account"`
    State string `json:"state"`
    Policy Policy `json:"policy"`
}

type AccountPolicySnapshot struct {
    ObservedAt time.Time `json:"observed_at"`
    OrganizationID uuid.UUID `json:"organization_id"`
    AccountID int `json:"account_id"`
    GroupID int64 `json:"group_id"`
    CohortID uuid.UUID `json:"cohort_id,omitempty"`
    CohortRevision int64 `json:"cohort_revision,omitempty"`
    SourceTemplateKey string `json:"source_template_key,omitempty"`
    SourceTemplateRevision int64 `json:"source_template_revision,omitempty"`
    State string `json:"state"`
    PolicyRevision int64 `json:"policy_revision"`
    Policy Policy `json:"policy"`
    Profiles []ProfilePolicySnapshot `json:"profiles"`
}

func (s *Store) GetAccountPolicy(ctx context.Context, organizationID uuid.UUID, accountID int) (AccountPolicySnapshot, error)
func (s *Store) GetAccountPolicies(ctx context.Context, organizationID uuid.UUID, accountIDs []int) ([]AccountPolicySnapshotResult, time.Time, error)
```

- [ ] **Step 1: Write authoritative-read RED tests**

Test exact managed, derived cohort, custom group, legacy unmanaged, dynamic all-library resolution, inherited profile, custom profile, cross-organization 404, and bounded 10,001-ID rejection. Assert caller-supplied expected template data is absent from the read interface.

- [ ] **Step 2: Run RED tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/entitlements ./internal/api/handlers -run 'AccountPolicy|EntitlementSnapshot' -count=1`

Expected: FAIL on missing snapshot store and handlers.

- [ ] **Step 3: Implement one-snapshot policy resolution**

Read account group, cohort/template provenance, account policy revision, and profiles under one repeatable-read transaction. Resolve effective policies through existing access policy code. Return `managed`, `custom`, or `legacy_unmanaged` without synthesizing missing provenance.

- [ ] **Step 4: Mount strict single and bounded routes**

Mount:

```text
GET  /api/v2/admin/platform/accounts/{account_id}/entitlement
GET  /api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement
POST /api/v2/admin/platform/accounts/entitlement-snapshots
POST /api/v2/admin/platform/organizations/{organization_id}/entitlement-snapshots
```

The bulk request is `{ "account_ids": [1, 2] }`; response is `{ "observed_at": "...", "items": [...] }` with safe per-ID not-found results.

- [ ] **Step 5: Run focused GREEN, auth, and race tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test -race ./internal/entitlements ./internal/api/handlers -run 'AccountPolicy|EntitlementSnapshot' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/entitlements/account_policy.go internal/entitlements/account_policy_test.go internal/api/handlers/entitlement_account_policy.go internal/api/handlers/entitlement_account_policy_test.go internal/api/router.go internal/api/handlers/admin.go
git commit -m "feat: expose authoritative account policies"
```

### Task 3: Add policy patching, immutable previews, and confirmation binding

**Files:**
- Create: `internal/entitlements/policy_patch.go`
- Create: `internal/entitlements/policy_patch_test.go`
- Create: `internal/adminpeople/policy_preview.go`
- Create: `internal/adminpeople/policy_preview_test.go`
- Modify: `internal/adminpeople/service.go`
- Modify: `internal/api/handlers/v2_admin_people.go`
- Modify: `internal/api/handlers/v2_admin_people_test.go`
- Modify: `internal/api/router_v2.go`

**Interfaces:**
- Consumes: immutable people selections, account policy snapshots, cohort store.
- Produces:

```go
type SetOperation[T any] struct { Mode string `json:"mode"`; Values []T `json:"values,omitempty"` }
type PolicyPatch struct {
    Libraries *SetOperation[int] `json:"libraries,omitempty"`
    Permissions *SetOperation[string] `json:"permissions,omitempty"`
    PlaybackAllowed *bool `json:"playback_allowed,omitempty"`
    TranscodeAllowed *bool `json:"transcode_allowed,omitempty"`
    DownloadAllowed *bool `json:"download_allowed,omitempty"`
    DownloadTranscodeAllowed *bool `json:"download_transcode_allowed,omitempty"`
    RequestsAllowed *bool `json:"requests_allowed,omitempty"`
    MaxStreams *int `json:"max_streams,omitempty"`
    MaxProfiles *int `json:"max_profiles,omitempty"`
    MaxTranscodes *int `json:"max_transcodes,omitempty"`
    MaxPlaybackQuality *string `json:"max_playback_quality,omitempty"`
}

type PolicyCommand struct {
    Kind string `json:"kind"`
    CohortID uuid.UUID `json:"cohort_id,omitempty"`
    TemplateKey string `json:"template_key,omitempty"`
    TemplateRevision int64 `json:"template_revision,omitempty"`
    Name string `json:"name,omitempty"`
    Patch PolicyPatch `json:"patch,omitempty"`
    IncludeCustomProfiles bool `json:"include_custom_profiles"`
}
```

- [ ] **Step 1: Write patch table tests and preview RED tests**

Cover add/remove/replace/all/none libraries, permission operations, boolean/limit dependencies, deterministic digest, current-cohort distribution, profile impact, already-compliant counts, and token invalidation after command/selection/actor/policy-revision change.

- [ ] **Step 2: Run RED tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/entitlements ./internal/adminpeople ./internal/api/handlers -run 'PolicyPatch|PolicyPreview' -count=1`

Expected: FAIL on missing patch and preview functions.

- [ ] **Step 3: Implement canonical patch and validation**

Implement `ApplyPolicyPatch(base Policy, patch PolicyPatch) (Policy, error)` using sorted, deduplicated sets and existing template policy validation. Reject toggles, unknown modes, negative limits, invalid dependencies, blank derived names, and no-op derived commands.

- [ ] **Step 4: Extend immutable selections and signed previews**

Extend snapshot records with current account group/cohort/policy revision and inherited/custom profile classification. Add `POST /api/v2/admin/organization/people/policy-previews`, returning counts, policy diff, and an HMAC confirmation token bound to every material field and expiry.

- [ ] **Step 5: Run GREEN tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/entitlements ./internal/adminpeople ./internal/api/handlers -run 'PolicyPatch|PolicyPreview' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/entitlements/policy_patch.go internal/entitlements/policy_patch_test.go internal/adminpeople/policy_preview.go internal/adminpeople/policy_preview_test.go internal/adminpeople/service.go internal/api/handlers/v2_admin_people.go internal/api/handlers/v2_admin_people_test.go internal/api/router_v2.go
git commit -m "feat: preview bulk policy changes"
```

### Task 4: Execute policy jobs through the durable people worker

**Files:**
- Create: `migrations/sql/20260822100000_admin_people_policy_jobs.sql`
- Modify: `internal/adminpeople/service.go`
- Modify: `internal/adminpeople/postgres_test.go`
- Modify: `internal/adminpeople/worker.go`
- Modify: `internal/adminpeople/worker_test.go`
- Modify: `internal/api/handlers/v2_admin_people.go`
- Modify: `internal/api/handlers/v2_admin_people_test.go`

**Interfaces:**
- Consumes: `PolicyCommand`, confirmation token, immutable selection, cohort store.
- Produces: existing `BulkResult` plus safe policy-specific result reasons and target cohort identity.

- [ ] **Step 1: Write durable execution RED tests**

Add PostgreSQL tests for exact template assignment, derived cohort convergence, restore default, inherited profile movement, custom preservation and opt-in movement, 10,000 targets, restart, exact replay, payload mismatch, concurrent jobs, stale account/profile snapshot, revoked actor, and partial failure.

```go
func TestPolicyBulkJobMovesInheritedProfilesAndPreservesCustomProfiles(t *testing.T) {
    fixture := newPolicyBulkFixture(t)
    selection := fixture.createSelection(fixture.accountID)
    job := fixture.enqueuePolicyJob(selection, PolicyCommand{Kind: "apply_entitlement_template", TemplateKey: "premium", TemplateRevision: 1})
    result, err := fixture.service.ProcessBulkJob(fixture.ctx, fixture.organizationID, job.JobID)
    require.NoError(t, err)
    require.Equal(t, 1, result.Succeeded)
    require.Equal(t, fixture.premiumGroupID, fixture.accountGroup(fixture.accountID))
    require.Equal(t, fixture.premiumGroupID, fixture.profileGroup(fixture.inheritedProfileID))
    require.Equal(t, fixture.customGroupID, fixture.profileGroup(fixture.customProfileID))
}

func TestPolicyBulkJobRestartResumesWithoutRepeatingCompletedTargets(t *testing.T) {
    fixture := newPolicyBulkFixture(t)
    job := fixture.enqueueThreeAccountPolicyJob()
    first, err := fixture.service.ProcessBulkBatch(fixture.ctx, fixture.organizationID, job.JobID, 1)
    require.NoError(t, err)
    require.Equal(t, 1, first.ProgressCurrent)
    completed, err := fixture.service.ProcessBulkJob(fixture.ctx, fixture.organizationID, job.JobID)
    require.NoError(t, err)
    require.Equal(t, 3, completed.Succeeded)
    require.Equal(t, 3, fixture.assignmentAuditCount(job.JobID))
}
```

- [ ] **Step 2: Run RED tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/adminpeople -run 'PolicyBulkJob' -count=1`

Expected: FAIL because policy action persistence and execution are absent.

- [ ] **Step 3: Extend durable schema and action decoding**

Add immutable `action_payload jsonb`, preview digest, and target cohort fields while preserving old job rows. Expand action constraints and immutability triggers. Store only allowlisted policy data and safe codes.

- [ ] **Step 4: Implement per-target transactional assignment**

Create/load the target cohort before batches. For each account, lock and revalidate membership/account/profile snapshots, move the account group, move inherited/custom profiles according to confirmation, bump revisions, and record immutable entitlement and people audits inside the savepoint.

- [ ] **Step 5: Run focused GREEN and race tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test -race ./internal/adminpeople -run 'PolicyBulkJob' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/sql/20260822100000_admin_people_policy_jobs.sql internal/adminpeople/service.go internal/adminpeople/postgres_test.go internal/adminpeople/worker.go internal/adminpeople/worker_test.go internal/api/handlers/v2_admin_people.go internal/api/handlers/v2_admin_people_test.go
git commit -m "feat: execute durable bulk policy jobs"
```

### Task 5: Add generic platform cohort and bulk-policy APIs

**Files:**
- Create: `internal/api/handlers/entitlement_bulk.go`
- Create: `internal/api/handlers/entitlement_bulk_test.go`
- Modify: `internal/api/handlers/admin.go`
- Modify: `internal/api/router.go`
- Modify: `docs/vondel-v2-api-reference.md`

**Interfaces:**
- Consumes: cohort store, policy preview service, durable people worker wake.
- Produces platform organization and direct-account cohort list/detail, preview, enqueue, and status contracts.

- [ ] **Step 1: Write exact wire-contract RED tests**

Pin strict methods, paths, JSON, status codes, API-key/platform-admin authority, cross-organization 404, redirect rejection, 10,001 limit, token expiry, idempotent replay, payload conflict, safe 429/5xx behavior, and worker wake.

- [ ] **Step 2: Run RED tests**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/api/handlers -run 'PlatformEntitlementBulk' -count=1`

Expected: FAIL on missing routes.

- [ ] **Step 3: Implement organization routes**

Mount cohort list/detail and policy preview/job/status beneath `/api/v2/admin/platform/organizations/{organization_id}`. Require exact organization binding for every account.

- [ ] **Step 4: Implement direct-account routes**

Mount preview/job/status beneath `/api/v2/admin/platform/accounts/entitlement-bulk`. Resolve each explicit account inside the default organization and reject non-direct or cross-scope targets.

- [ ] **Step 5: Document the generic contracts and run GREEN**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/api/handlers -run 'PlatformEntitlementBulk' -count=1 && go test ./internal/api/... -run '^$'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/entitlement_bulk.go internal/api/handlers/entitlement_bulk_test.go internal/api/handlers/admin.go internal/api/router.go docs/vondel-v2-api-reference.md
git commit -m "feat: expose platform bulk policy APIs"
```

### Task 6: Build organization cohort and bulk-policy UI

**Files:**
- Create: `web/src/hooks/queries/admin/entitlementCohorts.ts`
- Create: `web/src/components/admin/people/BulkPolicyDrawer.tsx`
- Create: `web/src/components/admin/people/BulkPolicyDrawer.test.tsx`
- Create: `web/src/pages/admin-organization/EntitlementCohortsPage.tsx`
- Create: `web/src/pages/admin-organization/EntitlementCohortsPage.test.tsx`
- Modify: `web/src/components/admin/people/BulkPeopleActionBar.tsx`
- Modify: `web/src/pages/admin-organization/PeoplePage.tsx`
- Modify: `web/src/pages/admin-organization/PeoplePage.test.tsx`
- Modify: `web/src/hooks/queries/admin/organizationPeople.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/adminNavigation.ts`

**Interfaces:**
- Consumes: organization cohort, preview, job, and people selection APIs.
- Produces four-step accessible drawer, cohort history view, progress/result display.

- [ ] **Step 1: Write component RED tests**

Test selection summary; each operation; explicit add/remove/replace controls; policy dependency disabling; live diff; inherited/custom profile count; confirmation; stale preview reset; duplicate-submit guard; progress; safe failure; retry selection; keyboard and screen-reader labels.

- [ ] **Step 2: Run RED tests**

Run: `cd web && pnpm exec vitest run src/components/admin/people/BulkPolicyDrawer.test.tsx src/pages/admin-organization/EntitlementCohortsPage.test.tsx src/pages/admin-organization/PeoplePage.test.tsx`

Expected: FAIL on missing components/hooks.

- [ ] **Step 3: Implement typed hooks and four-step drawer**

Keep query keys organization-scoped. Clear preview whenever selection, operation, policy, or custom-profile option changes. Use synchronous refs to guard submission before React rerenders.

- [ ] **Step 4: Implement cohort page and navigation**

Show source template/revision, full effective policy, member count, immutable lineage, archive state, and “Apply to people” link. Never offer in-place policy editing.

- [ ] **Step 5: Run focused and full web gates**

Run: `cd web && pnpm exec vitest run src/components/admin/people/BulkPolicyDrawer.test.tsx src/pages/admin-organization/EntitlementCohortsPage.test.tsx src/pages/admin-organization/PeoplePage.test.tsx && pnpm run lint && pnpm run build`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/hooks/queries/admin/entitlementCohorts.ts web/src/components/admin/people/BulkPolicyDrawer.tsx web/src/components/admin/people/BulkPolicyDrawer.test.tsx web/src/pages/admin-organization/EntitlementCohortsPage.tsx web/src/pages/admin-organization/EntitlementCohortsPage.test.tsx web/src/components/admin/people/BulkPeopleActionBar.tsx web/src/pages/admin-organization/PeoplePage.tsx web/src/pages/admin-organization/PeoplePage.test.tsx web/src/hooks/queries/admin/organizationPeople.ts web/src/App.tsx web/src/lib/adminNavigation.ts
git commit -m "feat: add organization bulk policy UI"
```

### Task 7: Build platform direct-account bulk UI and end-to-end coverage

**Files:**
- Create: `web/src/pages/admin-platform/DirectAccountPolicyBulkPage.tsx`
- Create: `web/src/pages/admin-platform/DirectAccountPolicyBulkPage.test.tsx`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/bulk-policy-cohorts.spec.ts`
- Modify: `web/package.json`
- Modify: `web/pnpm-lock.yaml`
- Modify: `web/src/pages/admin-platform/DirectAccountsPage.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/adminNavigation.ts`

**Interfaces:**
- Consumes: authoritative account snapshots and direct-account bulk APIs.
- Produces direct-account policy inspection, multi-select preview/job UI, production-shaped browser journey.

- [ ] **Step 1: Write platform UI and browser RED tests**

Test authoritative current policy/profiles, custom/legacy state, explicit account selection, exact template and derived patch, restore default, stale confirmation, progress, cross-account denial, and no policy-less state.

- [ ] **Step 2: Run RED tests**

Run: `cd web && pnpm exec vitest run src/pages/admin-platform/DirectAccountPolicyBulkPage.test.tsx`

Expected: FAIL on missing page.

- [ ] **Step 3: Implement page and mount it behind PlatformContextGuard**

Require an explicit selection; never default to the first template/cohort. Render complete observed policy and profile exceptions before enabling preview.

- [ ] **Step 4: Add a minimal Playwright UI contract and journey**

Add Playwright as a pinned development dependency and a Vite `webServer` configuration. The browser journey uses strict route fixtures for the already independently tested HTTP contracts, creates two cohorts, assigns different selected accounts, derives a downloads change, preserves a custom profile, opts in on a second job, restores default, and verifies authoritative-read rendering. Cross-organization denial remains a live PostgreSQL handler assertion from Task 5 rather than a mocked browser assertion.

- [ ] **Step 5: Run full verification**

Run:

```bash
TEST_DATABASE_URL=$TEST_DATABASE_URL go test -p 1 ./internal/entitlements ./internal/adminpeople ./internal/api/handlers ./internal/access ./internal/playback ./internal/jellycompat -count=1
go test ./... -run '^$' -count=1
go vet ./...
cd web && pnpm test -- --run && pnpm run lint && pnpm run build
pnpm exec playwright test e2e/bulk-policy-cohorts.spec.ts
git diff --check
```

Expected: all PASS; only documented pre-existing warnings may remain.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/admin-platform/DirectAccountPolicyBulkPage.tsx web/src/pages/admin-platform/DirectAccountPolicyBulkPage.test.tsx web/playwright.config.ts web/e2e/bulk-policy-cohorts.spec.ts web/package.json web/pnpm-lock.yaml web/src/pages/admin-platform/DirectAccountsPage.tsx web/src/App.tsx web/src/lib/adminNavigation.ts
git commit -m "feat: add platform bulk policy workflow"
```

### Task 8: Review, publish, deploy, and canary Server capabilities

**Files:**
- Modify: `README.md`
- Modify: `docs/operations/entitlement-templates.md`
- Create: `docs/operations/bulk-policy-cohorts.md`
- Create: `docs/operations/bulk-policy-cohorts-runbook.md`

**Interfaces:**
- Consumes: all prior Server tasks.
- Produces reviewed, documented, deployed Server API/UI and canary evidence.

- [ ] **Step 1: Update standalone administration and runbook docs**

Document cohorts, authoritative reads, preview/confirmation, job monitoring, restore-default semantics, custom-profile behavior, safe retry, and rollback refusal. Do not mention any private integration.

- [ ] **Step 2: Request independent code review**

Review migration rollback, cohort immutability, authoritative read correctness, worker idempotency/restart, profile semantics, authorization, API contracts, UI confirmation, and secret/error redaction. Fix every Critical/Important issue with RED/GREEN tests and a separate commit.

- [ ] **Step 3: Run fresh release gates**

Run the full commands from Task 7 on a clean disposable PostgreSQL 18 database, plus migration down/up refusal tests and changed-code lint.

- [ ] **Step 4: Commit documentation/review corrections**

```bash
git add README.md docs/operations/entitlement-templates.md docs/operations/bulk-policy-cohorts.md docs/operations/bulk-policy-cohorts-runbook.md
git commit -m "docs: document bulk policy administration"
```

- [ ] **Step 5: Push exact reviewed commit and deploy Server**

Fetch `origin/main`, require a fast-forward base, push non-force `HEAD:main`, build/deploy the exact SHA, and verify the live health/version endpoint reports that SHA before canary mutations.

- [ ] **Step 6: Run bounded production canary**

Read one account's authoritative policy, preview/apply a harmless exact-template assignment to one manually selected test account, poll the durable job, read policy again, verify playback/profile behavior and audit evidence, then restore the prior cohort through a second reviewed job.

- [ ] **Step 7: Record canary evidence**

Record deployed SHA, account/organization test identity, preview/job IDs, before/after/restored policy digests, safe result counts, health checks, and rollback readiness without credentials or personal data.
