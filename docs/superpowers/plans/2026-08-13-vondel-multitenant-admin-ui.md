# Vondel Multitenant Administration UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tenant-safe Vondel administration experience with explicit Platform and Organization contexts, organization lifecycle management, scalable people operations, and organization-scoped security controls while preserving Silo `/api/v1` compatibility.

**Architecture:** Keep the existing account session as the compatibility root and mint a separate, short-lived `/api/v2/admin` context token after server-side authority validation. A single React admin shell uses an in-memory context token, context-qualified query keys, server-driven lists, and existing page components adapted to v2 resources rather than duplicated.

**Tech Stack:** Go 1.26, chi, pgx/PostgreSQL, OPA/Rego, React 19, TypeScript, React Router, TanStack Query, Tailwind CSS, Vitest, Testing Library, pnpm.

**Spec:** `docs/superpowers/specs/2026-08-13-vondel-multitenant-admin-ui-design.md`

## Global Constraints

- Preserve every existing `/api/v1` request and response contract; Silo clients remain default-organization compatible.
- Never establish organization authority from headers, query parameters, path identifiers on organization-context routes, or mutation bodies.
- Keep administrative context tokens in browser memory only; only the selected context identifier may persist.
- Platform and organization pages share the existing React application and design system.
- The first release exposes one broad `organization_admin` role; delegated administrative roles remain disabled.
- Organization administrators cannot create, upload, activate or edit Rego.
- Hard organization deletion is absent; suspension is the reversible lifecycle control.
- Every tenant query key includes the active context identity.
- Large people and organization collections use server-side filtering, sorting and cursor pagination.
- All database-required tests fail rather than skip when `SILO_REQUIRE_TEST_DATABASE=1`.
- Use existing dependencies; do not introduce a new state manager, table library or component framework.

---

## File and responsibility map

- `internal/auth/admin_context.go`: signed administrative context claims and token service.
- `internal/api/handlers/v2_admin_session.go`: Platform/Organization context exchange endpoint.
- `internal/api/middleware/admin_context.go`: token validation, revision revalidation and authoritative context attachment.
- `internal/tenancy/admin_store.go`: organization directory, lifecycle and membership administration queries.
- `internal/adminpeople/service.go`: people search, immutable selection tokens and bulk membership/group jobs.
- `internal/api/handlers/v2_admin_platform.go`: Platform organization and lifecycle HTTP projection.
- `internal/api/handlers/v2_admin_organization.go`: organization overview, people, groups, libraries, entitlements and policy-decision projection.
- `web/src/api/adminV2Client.ts`: in-memory admin token and v2 request client.
- `web/src/contexts/AdminContextProvider.tsx`: available contexts, switching state, cache isolation and route-safe transitions.
- `web/src/components/admin/AdminContextSwitcher.tsx`: persistent authority selector.
- `web/src/pages/admin-platform/*`: Platform organization directory and detail.
- `web/src/pages/admin-organization/*`: organization overview, People and decision explanations.
- Existing `AdminAccessGroups`, library, invitation and policy components: context-aware adaptation without duplication.

---

### Task 1: Administrative context token and session exchange

**Files:**
- Create: `internal/auth/admin_context.go`
- Create: `internal/auth/admin_context_test.go`
- Create: `internal/api/handlers/v2_admin_session.go`
- Create: `internal/api/handlers/v2_admin_session_test.go`
- Create: `internal/api/middleware/admin_context.go`
- Create: `internal/api/middleware/admin_context_test.go`
- Modify: `internal/api/router_v2.go`
- Modify: `internal/api/router_v2_test.go`
- Modify: `cmd/silo/main.go`

**Interfaces:**
- Produces: `auth.AdminContextClaims`, `auth.AdminContextTokenService`, `handlers.AdminContextSessionHandler`, and `middleware.AdminContextMiddleware`.
- Consumes: existing account claims, `tenancy.Store`, `tenancy.Resolver`, platform-admin role checks, and JWT signing configuration.

- [ ] **Step 1: Write token and middleware failures first**

Add tests proving that a context token carries exactly one scope and that the middleware rejects stale, foreign, suspended and caller-forged authority:

```go
func TestAdminContextMiddlewareRejectsStaleOrganizationRevision(t *testing.T) {
    claims := auth.AdminContextClaims{
        AccountID: 41, Scope: auth.AdminScopeOrganization,
        OrganizationID: orgID, MembershipID: membershipID,
        PolicyRevision: 7, SecurityRevision: 11,
    }
    resolver := &adminContextResolverStub{tenant: tenancy.Context{
        AccountID: 41, OrganizationID: orgID, MembershipID: membershipID,
        PolicyRevision: 8, SecurityRevision: 11,
    }}
    rec := performAdminContextRequest(t, claims, resolver)
    if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorization_state_stale") {
        t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
    }
}
```

- [ ] **Step 2: Run the focused RED tests**

Run:

```bash
GOWORK=off go test ./internal/auth ./internal/api/middleware ./internal/api/handlers \
  -run 'TestAdminContext|TestV2AdminSession' -count=1
```

Expected: compilation failure because the admin context types and handlers do not exist.

- [ ] **Step 3: Implement the signed claims and revalidation boundary**

Use explicit scope values and a maximum 15-minute token lifetime:

```go
type AdminScope string

const (
    AdminScopePlatform     AdminScope = "platform"
    AdminScopeOrganization AdminScope = "organization"
)

type AdminContextClaims struct {
    AccountID       int
    Scope           AdminScope
    OrganizationID  uuid.UUID
    MembershipID    uuid.UUID
    PolicyRevision  int64
    SecurityRevision int64
    ExpiresAt       time.Time
}

type AdminContextTokenService interface {
    Mint(AdminContextClaims) (string, error)
    Parse(string) (AdminContextClaims, error)
}
```

`AdminContextMiddleware.Require` must parse the token, revalidate current platform authority or the exact membership and revisions, attach `tenancy.Context` for organization scope, and return typed errors without falling back to legacy resolution.

- [ ] **Step 4: Implement context exchange**

Accept this request and return shape:

```go
type adminContextSessionRequest struct {
    Scope          auth.AdminScope `json:"scope"`
    OrganizationID string          `json:"organization_id,omitempty"`
}

type adminContextSessionResponse struct {
    AccessToken string              `json:"access_token"`
    ExpiresAt   time.Time           `json:"expires_at"`
    Context     adminContextSummary `json:"context"`
}
```

Platform exchange requires current platform-admin authority. Organization exchange resolves an active membership and requires legacy role `admin` or platform authority. Never accept a membership ID from the caller.

- [ ] **Step 5: Register and compose the route**

Register `POST /api/v2/admin/session` behind existing account authentication, then create a `/api/v2/admin` group protected by `AdminContextMiddleware.Require`. Keep `/api/v2/organizations` account-authenticated and read-only.

- [ ] **Step 6: Verify and commit**

Run:

```bash
GOWORK=off go test ./internal/auth ./internal/api/middleware ./internal/api/handlers ./internal/api \
  -run 'TestAdminContext|TestV2AdminSession|TestV2Router' -count=1
GOWORK=off go test -race ./internal/auth ./internal/api/middleware -run 'TestAdminContext' -count=1
git diff --check
```

Commit:

```bash
git add internal/auth internal/api cmd/silo/main.go
git commit -m "feat(auth): add administrative context sessions"
```

---

### Task 2: Platform organization lifecycle API

**Files:**
- Create: `internal/tenancy/admin_store.go`
- Create: `internal/tenancy/admin_store_test.go`
- Create: `internal/api/handlers/v2_admin_platform.go`
- Create: `internal/api/handlers/v2_admin_platform_test.go`
- Modify: `internal/api/router_v2.go`
- Modify: `internal/activitylog/repository.go`

**Interfaces:**
- Consumes: Task 1 Platform context middleware.
- Produces: paginated organization directory, creation, update, suspend, reactivate, membership lifecycle and ownership transfer endpoints.

- [ ] **Step 1: Add PostgreSQL contract tests**

Cover stable cursor order, exact counts, unique slug conflicts, first-admin creation, reversible suspension, cross-organization membership isolation, ownership transfer and revision increments:

```go
func TestAdminStoreCreateOrganizationCreatesOwnerMembership(t *testing.T) {
    created, err := store.CreateOrganization(ctx, CreateOrganizationInput{
        Name: "North Sea Media", Slug: "north-sea-media", OwnerAccountID: ownerID,
    })
    if err != nil { t.Fatal(err) }
    membership, err := store.GetMembership(ctx, ownerID, created.ID)
    if err != nil || membership.LegacyRole != "admin" || membership.Status != MembershipActive {
        t.Fatalf("membership = %+v, err = %v", membership, err)
    }
}
```

- [ ] **Step 2: Run RED against a disposable database**

Run:

```bash
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" \
  GOWORK=off go test ./internal/tenancy -run 'TestAdminStore' -count=1
```

Expected: compilation failure on missing lifecycle methods.

- [ ] **Step 3: Implement store types and transactions**

Define:

```go
type OrganizationCursor struct { Name string; ID uuid.UUID }
type OrganizationPage struct { Items []OrganizationSummary; NextCursor string }
type OrganizationFilter struct { Query string; Status *OrganizationStatus; Limit int; Cursor string }

func (s *Store) ListOrganizations(context.Context, OrganizationFilter) (OrganizationPage, error)
func (s *Store) CreateOrganization(context.Context, CreateOrganizationInput) (Organization, error)
func (s *Store) UpdateOrganization(context.Context, uuid.UUID, int64, UpdateOrganizationInput) (Organization, error)
func (s *Store) SetOrganizationStatus(context.Context, uuid.UUID, int64, OrganizationStatus) (Organization, error)
func (s *Store) TransferOwnership(context.Context, uuid.UUID, int64, int) (Organization, error)
```

Use `(lower(name), id)` cursor ordering, transactions for owner membership changes, and revision preconditions in every mutation.

- [ ] **Step 4: Implement Platform HTTP endpoints**

Register:

```text
GET    /api/v2/admin/platform/organizations
POST   /api/v2/admin/platform/organizations
GET    /api/v2/admin/platform/organizations/{id}
PATCH  /api/v2/admin/platform/organizations/{id}
POST   /api/v2/admin/platform/organizations/{id}/suspend
POST   /api/v2/admin/platform/organizations/{id}/reactivate
POST   /api/v2/admin/platform/organizations/{id}/transfer-ownership
GET    /api/v2/admin/platform/organizations/{id}/memberships
POST   /api/v2/admin/platform/organizations/{id}/memberships
PATCH  /api/v2/admin/platform/organizations/{id}/memberships/{membership_id}
```

Map stale revisions to `409 authorization_state_changed`, foreign IDs to non-disclosing `404`, validation to field-addressable `422`, and database availability to `503`.

- [ ] **Step 5: Add audit events**

Record creation, rename, slug change, suspension, reactivation, membership status changes and ownership transfer with actor, Platform context, target organization, before/after revisions and request ID. Do not log email invite secrets or tokens.

- [ ] **Step 6: Verify and commit**

Run:

```bash
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" \
  GOWORK=off go test ./internal/tenancy ./internal/api/handlers \
  -run 'TestAdminStore|TestV2AdminPlatform' -count=1
GOWORK=off go vet ./internal/tenancy ./internal/api/handlers
git diff --check
```

Commit:

```bash
git add internal/tenancy internal/api/handlers internal/api/router_v2.go internal/activitylog
git commit -m "feat(admin): add organization lifecycle API"
```

---

### Task 3: Organization People API and immutable bulk jobs

**Files:**
- Create: `internal/adminpeople/service.go`
- Create: `internal/adminpeople/service_test.go`
- Create: `internal/adminpeople/postgres_test.go`
- Create: `internal/api/handlers/v2_admin_people.go`
- Create: `internal/api/handlers/v2_admin_people_test.go`
- Modify: `internal/api/router_v2.go`

**Interfaces:**
- Consumes: Task 1 organization context and existing access-group/profile stores.
- Produces: `adminpeople.Service`, cursor-paginated people search, immutable selection tokens and bulk job result contracts.

- [ ] **Step 1: Write search and isolation tests**

Seed overlapping accounts in two organizations and prove that every search field, profile and group result remains organization bounded:

```go
func TestServiceListPeopleNeverReturnsForeignMemberships(t *testing.T) {
    page, err := svc.List(ctx, orgA, Filter{Query: "shared@", Limit: 50})
    if err != nil { t.Fatal(err) }
    for _, person := range page.Items {
        if person.OrganizationID != orgA { t.Fatalf("foreign person: %+v", person) }
    }
}
```

- [ ] **Step 2: Write immutable selection and partial-result tests**

The selection token must bind organization, canonicalized filters, snapshot timestamp and expiry under an authenticated signature. A token issued for org A must fail in org B. Bulk results must separate `succeeded`, `skipped` and `failed` records.

- [ ] **Step 3: Run RED**

Run:

```bash
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" \
  GOWORK=off go test ./internal/adminpeople ./internal/api/handlers \
  -run 'TestService|TestV2AdminPeople' -count=1
```

Expected: package/type compilation failure.

- [ ] **Step 4: Implement service contracts**

```go
type Filter struct {
    Query string; Status []tenancy.MembershipStatus; GroupIDs []int;
    ActiveSince *time.Time; Sort string; Limit int; Cursor string
}
type Page struct { Items []PersonSummary; NextCursor string; ApproximateTotal int64 }
type Selection struct { Token string; Matched int64; Excluded int64; ExpiresAt time.Time }
type BulkAction struct { SelectionToken string; Kind string; GroupID *int }
type BulkResult struct { JobID string; Succeeded int; Skipped []RecordResult; Failed []RecordResult }

func (s *Service) List(context.Context, uuid.UUID, Filter) (Page, error)
func (s *Service) CreateSelection(context.Context, uuid.UUID, Filter) (Selection, error)
func (s *Service) ExecuteBulk(context.Context, uuid.UUID, int, BulkAction) (BulkResult, error)
```

Bulk group assignment updates profiles only inside the active organization and bumps affected security revisions. Membership suspension revokes active organization context sessions through revision changes.

- [ ] **Step 5: Add People routes**

```text
GET  /api/v2/admin/organization/people
GET  /api/v2/admin/organization/people/{account_id}
POST /api/v2/admin/organization/people/selections
POST /api/v2/admin/organization/people/bulk-jobs
GET  /api/v2/admin/organization/people/bulk-jobs/{job_id}
PATCH /api/v2/admin/organization/people/{account_id}/memberships/current
PATCH /api/v2/admin/organization/people/{account_id}/profiles/{profile_id}
```

The handler obtains organization identity only from middleware context. Its path account/profile identifiers are targets, never authority selectors.

- [ ] **Step 6: Verify and commit**

Run:

```bash
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" \
  GOWORK=off go test ./internal/adminpeople ./internal/api/handlers \
  -run 'TestService|TestV2AdminPeople' -count=1
GOWORK=off go test -race ./internal/adminpeople -count=1
git diff --check
```

Commit:

```bash
git add internal/adminpeople internal/api/handlers internal/api/router_v2.go
git commit -m "feat(admin): add organization people operations"
```

---

### Task 4: Organization overview and structured security projections

**Files:**
- Create: `internal/api/handlers/v2_admin_organization.go`
- Create: `internal/api/handlers/v2_admin_organization_test.go`
- Create: `internal/api/handlers/v2_policy_explain.go`
- Create: `internal/api/handlers/v2_policy_explain_test.go`
- Modify: `internal/api/router_v2.go`
- Modify: `internal/access/group_store.go`
- Modify: `internal/resourcetenancy/store.go`

**Interfaces:**
- Consumes: Task 1 organization context, existing access groups, folders, entitlements, invitation stores and policy decision repository.
- Produces: organization overview plus context-scoped groups, libraries, entitlements, invitations and redacted decision explanations.

- [ ] **Step 1: Write foreign-data and redaction tests**

For each projected resource, seed an in-organization and foreign row and assert only the in-organization row appears. Decision explanations must replace sensitive values with `"[redacted]"` and reject a foreign decision ID with `404`.

- [ ] **Step 2: Run RED**

Run:

```bash
GOWORK=off go test ./internal/api/handlers \
  -run 'TestV2OrganizationOverview|TestV2OrganizationGroups|TestV2PolicyExplain' -count=1
```

Expected: compilation failure because the v2 organization handlers do not exist.

- [ ] **Step 3: Implement overview and structured routes**

Register organization-context resources without organization IDs:

```text
GET /api/v2/admin/organization/overview
GET|POST /api/v2/admin/organization/groups
GET|PUT|DELETE /api/v2/admin/organization/groups/{id}
GET /api/v2/admin/organization/libraries
GET|PUT|DELETE /api/v2/admin/organization/entitlements/{folder_id}
GET|POST /api/v2/admin/organization/invitations
GET /api/v2/admin/organization/policy-decisions
GET /api/v2/admin/organization/policy-decisions/{id}
```

Reuse store behavior rather than forwarding internally to v1 HTTP handlers. Return group deletion impact (`profiles_reassigned`, `default_group_id`) and distinguish owned libraries from platform entitlements.

- [ ] **Step 4: Implement explainable policy response**

```go
type PolicyDecisionExplanation struct {
    Organization adminOrganizationRef `json:"organization"`
    Subject      adminSubjectRef      `json:"subject"`
    Group        adminGroupRef        `json:"group"`
    LibraryCeiling []int              `json:"library_ceiling"`
    Action       string               `json:"action"`
    Resource     map[string]any       `json:"resource"`
    Allowed      bool                 `json:"allowed"`
    ReasonCode   string               `json:"reason_code"`
    PolicyVersions []PolicyVersionRef `json:"policy_versions"`
}
```

Do not expose raw credential-bearing inputs or allow organization admins to create, validate or activate documents.

- [ ] **Step 5: Verify and commit**

Run:

```bash
GOWORK=off go test ./internal/access ./internal/resourcetenancy ./internal/api/handlers \
  -run 'TestV2Organization|TestV2PolicyExplain|TestGroup' -count=1
GOWORK=off go vet ./internal/api/handlers ./internal/access ./internal/resourcetenancy
git diff --check
```

Commit:

```bash
git add internal/api/handlers internal/api/router_v2.go internal/access internal/resourcetenancy
git commit -m "feat(admin): expose organization security controls"
```

---

### Task 5: Frontend admin context provider and shell

**Files:**
- Create: `web/src/api/adminV2Client.ts`
- Create: `web/src/api/adminV2Client.test.ts`
- Create: `web/src/contexts/AdminContextProvider.tsx`
- Create: `web/src/contexts/AdminContextProvider.test.tsx`
- Create: `web/src/components/admin/AdminContextSwitcher.tsx`
- Create: `web/src/components/admin/AdminContextSwitcher.test.tsx`
- Modify: `web/src/components/AdminSidebar.tsx`
- Modify: `web/src/components/AdminSidebar.test.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/adminNavigation.ts`
- Modify: `web/src/api/types.ts`

**Interfaces:**
- Consumes: Tasks 1–4 v2 contracts.
- Produces: `useAdminContext()`, `adminV2Api()`, Platform/Organization route guards and persistent shell switcher.

- [ ] **Step 1: Write in-memory token and cache-isolation tests**

Prove tokens are absent from local/session storage, switching cancels old requests, removes only old-context queries and never renders old organization data under the new label:

```tsx
it("clears the prior tenant before rendering the new context", async () => {
  renderAdminContextHarness();
  await selectContext("organization:org-b");
  expect(queryClient.cancelQueries).toHaveBeenCalledWith({ queryKey: ["admin-v2", "organization:org-a"] });
  expect(screen.queryByText("Org A member")).not.toBeInTheDocument();
  expect(screen.getByText("Org B")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run RED**

Run:

```bash
cd web
pnpm vitest run src/api/adminV2Client.test.ts src/contexts/AdminContextProvider.test.tsx \
  src/components/admin/AdminContextSwitcher.test.tsx
```

Expected: module resolution failures for the new client/provider/switcher.

- [ ] **Step 3: Implement the v2 client and provider**

```ts
export type AdminContextKey = "platform" | `organization:${string}`;

export interface AdminContextSummary {
  key: AdminContextKey;
  scope: "platform" | "organization";
  organizationId?: string;
  name: string;
  status: "active" | "suspended";
  authority: "platform_admin" | "organization_admin";
  policyRevision: number;
  securityRevision: number;
}

export interface AdminContextValue {
  available: AdminContextSummary[];
  active: AdminContextSummary | null;
  switching: boolean;
  switchContext(key: AdminContextKey): Promise<void>;
  clearContext(reason?: AdminContextFailure): void;
}

export function adminV2Api<T>(path: string, init?: RequestInit): Promise<T>;
```

Keep the token in a module-private variable. Persist only `AdminContextKey`. On `authorization_state_stale`, clear the token and all queries under the active context key before navigating to context selection.

- [ ] **Step 4: Add the switcher and context-aware navigation**

Place the switcher above navigation in both desktop sidebar and mobile drawer. Render the current scope, status and authority. Platform items and organization items are mutually exclusive. Restore focus to the new page heading after switching.

- [ ] **Step 5: Add route guards**

Wrap Platform and Organization route groups with guards that require the matching active context. A direct link to an unavailable route redirects to the nearest valid overview without briefly mounting the protected page.

- [ ] **Step 6: Verify and commit**

Run:

```bash
cd web
pnpm vitest run src/api/adminV2Client.test.ts src/contexts/AdminContextProvider.test.tsx \
  src/components/admin/AdminContextSwitcher.test.tsx src/components/AdminSidebar.test.tsx
pnpm run lint
pnpm run format:check
```

Commit:

```bash
git add web/src
git commit -m "feat(web): add administrative context shell"
```

---

### Task 6: Platform organization directory and lifecycle UI

**Files:**
- Create: `web/src/hooks/queries/admin/organizations.ts`
- Create: `web/src/pages/admin-platform/OrganizationsPage.tsx`
- Create: `web/src/pages/admin-platform/OrganizationsPage.test.tsx`
- Create: `web/src/pages/admin-platform/OrganizationDetailPage.tsx`
- Create: `web/src/pages/admin-platform/OrganizationDetailPage.test.tsx`
- Create: `web/src/components/admin/organizations/OrganizationLifecyclePanel.tsx`
- Create: `web/src/components/admin/organizations/OwnershipTransferDialog.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/lib/documentTitle.ts`

**Interfaces:**
- Consumes: Task 2 Platform API and Task 5 context provider.
- Produces: searchable directory, create/edit/suspend/reactivate workflows, membership management and re-authenticated ownership transfer.

- [ ] **Step 1: Write directory and lifecycle UI tests**

Cover cursor navigation, debounced search, create validation, stale revision recovery, suspension confirmation naming the organization, and ownership transfer requiring a password challenge.

- [ ] **Step 2: Run RED**

Run:

```bash
cd web
pnpm vitest run src/pages/admin-platform/OrganizationsPage.test.tsx \
  src/pages/admin-platform/OrganizationDetailPage.test.tsx
```

Expected: missing page and hook modules.

- [ ] **Step 3: Implement context-qualified query hooks**

All query keys begin with:

```ts
const platformKey = ["admin-v2", "platform"] as const;
export const organizationKeys = {
  list: (filter: OrganizationFilter) => [...platformKey, "organizations", filter] as const,
  detail: (id: string) => [...platformKey, "organizations", id] as const,
};
```

Mutations include the server revision and invalidate only Platform queries.

- [ ] **Step 4: Implement directory and detail pages**

Use the established page shell and surface components. Include name, slug, status, owner, people/profile/library counts, policy revision and activity. Do not add hard-delete affordances.

- [ ] **Step 5: Implement ownership and lifecycle safety**

Suspension/reactivation confirmations show organization name and affected active memberships. Ownership transfer requires typed organization name plus successful account re-authentication before the mutation is sent.

- [ ] **Step 6: Verify and commit**

Run:

```bash
cd web
pnpm vitest run src/pages/admin-platform src/components/admin/organizations
pnpm run lint
pnpm run format:check
```

Commit:

```bash
git add web/src
git commit -m "feat(web): add organization lifecycle management"
```

---

### Task 7: Organization overview and People UI

**Files:**
- Create: `web/src/hooks/queries/admin/organizationPeople.ts`
- Create: `web/src/pages/admin-organization/OrganizationOverviewPage.tsx`
- Create: `web/src/pages/admin-organization/OrganizationOverviewPage.test.tsx`
- Create: `web/src/pages/admin-organization/PeoplePage.tsx`
- Create: `web/src/pages/admin-organization/PeoplePage.test.tsx`
- Create: `web/src/components/admin/people/PeopleTable.tsx`
- Create: `web/src/components/admin/people/PersonDetailSheet.tsx`
- Create: `web/src/components/admin/people/BulkPeopleActionBar.tsx`
- Create: `web/src/components/admin/people/BulkJobResult.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: Task 3 People API, Task 4 overview API and Task 5 context provider.
- Produces: scalable People workflow with immutable selection and explicit partial results.

- [ ] **Step 1: Write server-driven interaction tests**

Test that filters serialize into the URL, changing filters clears selection, cursor pages do not merge records, and selecting all results requests a server selection token instead of collecting page IDs.

- [ ] **Step 2: Write bulk safety tests**

Verify that confirmation shows active organization, canonical filter summary, matched count and exclusions; a partial result renders each failed/skipped record and never displays a generic success toast.

- [ ] **Step 3: Run RED**

Run:

```bash
cd web
pnpm vitest run src/pages/admin-organization/PeoplePage.test.tsx \
  src/components/admin/people
```

Expected: missing page and component modules.

- [ ] **Step 4: Implement overview and People workflow**

Use a dense semantic table on desktop and a structured list on narrow screens. The detail drawer preserves filters and selection. Profile group changes are inline but require a revision-aware mutation.

- [ ] **Step 5: Implement bulk jobs and recovery**

Create the immutable selection, show confirmation, submit the bulk job, poll by job ID, render progress and present exact success/skipped/failure counts. A context switch cancels polling and removes the prior job from visible state.

- [ ] **Step 6: Verify and commit**

Run:

```bash
cd web
pnpm vitest run src/pages/admin-organization src/components/admin/people \
  src/contexts/AdminContextProvider.test.tsx
pnpm run lint
pnpm run format:check
```

Commit:

```bash
git add web/src
git commit -m "feat(web): add organization people management"
```

---

### Task 8: Adapt access groups, libraries, invitations and policy decisions

**Files:**
- Modify: `web/src/pages/AdminAccessGroups.tsx`
- Modify: `web/src/pages/AdminAccessGroups.test.tsx`
- Modify: `web/src/hooks/queries/admin/accessGroups.ts`
- Modify: `web/src/hooks/queries/admin/libraries.ts`
- Modify: `web/src/pages/admin-settings/InvitationsTab.tsx`
- Create: `web/src/pages/admin-organization/LibrariesEntitlementsPage.tsx`
- Create: `web/src/pages/admin-organization/LibrariesEntitlementsPage.test.tsx`
- Create: `web/src/pages/admin-organization/PolicyDecisionsPage.tsx`
- Create: `web/src/pages/admin-organization/PolicyDecisionsPage.test.tsx`
- Modify: `web/src/pages/admin-policy/AdminPolicyLayout.tsx`

**Interfaces:**
- Consumes: Task 4 structured organization API and Task 5 context provider.
- Produces: organization-scoped structured security administration and Platform-only Rego management.

- [ ] **Step 1: Write context adaptation tests**

Prove Access Groups and Invitations call v2 organization routes in Organization context, Platform policy remains available only in Platform context, and no page combines v1 data with v2 organization data.

- [ ] **Step 2: Run RED**

Run:

```bash
cd web
pnpm vitest run src/pages/AdminAccessGroups.test.tsx \
  src/pages/admin-organization/LibrariesEntitlementsPage.test.tsx \
  src/pages/admin-organization/PolicyDecisionsPage.test.tsx
```

Expected: tests fail because existing hooks still call `/api/v1/admin` and new pages do not exist.

- [ ] **Step 3: Adapt Access Groups and Invitations**

Add organization identity to headings and mutation confirmations. Deleting a group previews profile reassignment and the default destination group. Group member counts remain server supplied.

- [ ] **Step 4: Implement Libraries and Entitlements**

Visually distinguish organization-owned libraries from platform entitlements. Explain that effective access is the intersection of the tenant ceiling and profile/group restrictions. Do not describe an entitlement as unconditional access.

- [ ] **Step 5: Implement organization decision explanations**

Display subject, group, tenant ceiling, action, resource, outcome, reason code and policy versions. Redacted fields remain visibly labeled as redacted. No edit/activate controls appear in Organization context.

- [ ] **Step 6: Verify and commit**

Run:

```bash
cd web
pnpm vitest run src/pages/AdminAccessGroups.test.tsx src/pages/admin-organization \
  src/pages/admin-policy
pnpm run lint
pnpm run format:check
```

Commit:

```bash
git add web/src
git commit -m "feat(web): scope organization security administration"
```

---

### Task 9: End-to-end isolation, accessibility and release evidence

**Files:**
- Create: `internal/api/multitenant_admin_acceptance_test.go`
- Create: `web/src/pages/adminMultitenantAccessibility.test.tsx`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/architecture/opa-tenant-authorization.md`
- Modify: `docs/architecture/v2-security-foundation.md`
- Create: `docs/architecture/multitenant-administration.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: release-locking PostgreSQL, API, frontend, accessibility and documentation evidence.

- [ ] **Step 1: Write the two-organization PostgreSQL acceptance**

Create two organizations, an account belonging to both, organization-only accounts, distinct groups, libraries, entitlements and decisions. Mint both organization contexts and prove each returns only its people, profiles, groups, libraries and decisions. Suspend one membership and prove its already-minted context is rejected while the other organization remains usable.

- [ ] **Step 2: Add the CI-required database gate**

Add a PostgreSQL CI step with step-local:

```yaml
env:
  SILO_REQUIRE_TEST_DATABASE: "1"
  SILO_TEST_DATABASE_URL: postgres://silo:silo@localhost:5432/silo_test?sslmode=disable
```

Run only the named multitenant admin acceptance and required store/migration contracts in that step. Keep ordinary non-database Go tests unsignaled.

- [ ] **Step 3: Add accessibility and responsive tests**

Verify switcher keyboard operation, announced context change, focus restoration, semantic table headers, labeled bulk counts, drawer-to-sheet behavior and destructive confirmation copy. Render stable screenshots at desktop and narrow widths for Platform Organizations, Organization Overview, People and Access Groups.

- [ ] **Step 4: Update architecture documentation**

Document the context-token boundary, Platform/Organization navigation, v1 compatibility, structured organization controls, revocation behavior, operational audit and the explicit absence of hard deletion/delegated roles.

- [ ] **Step 5: Run final verification**

Run:

```bash
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" \
  GOWORK=off go test ./internal/api ./internal/tenancy ./internal/adminpeople \
  -run 'TestMultitenantAdmin|TestAdminStore|TestService' -count=1
GOWORK=off go test -race ./internal/auth ./internal/tenancy ./internal/adminpeople ./internal/api/middleware -count=1
GOWORK=off go vet ./internal/auth ./internal/tenancy ./internal/adminpeople ./internal/api/... 
GOWORK=off go build ./cmd/silo
cd web
pnpm vitest run
pnpm run lint
pnpm run format:check
cd ..
make verify-local-paths
git diff --check
```

Expected: every command exits zero; no database-required test skips; the worktree contains no generated build artifacts.

- [ ] **Step 6: Commit release evidence**

```bash
git add .github/workflows/ci.yml internal web docs
git commit -m "test(admin): lock multitenant UI isolation"
```

---

## Final review checklist

- [ ] Every `/api/v1` contract test remains unchanged and green.
- [ ] Organization-context routes contain no organization selector parameter.
- [ ] Platform routes require Platform context and label all organization data.
- [ ] Context tokens never enter local storage, session storage, URLs, logs or query persistence.
- [ ] Query keys and cancellation are context qualified.
- [ ] Foreign organization IDs return non-disclosing responses.
- [ ] Suspension and revision changes revoke active context sessions.
- [ ] Bulk selection tokens are signed, expiring and organization bound.
- [ ] Organization admins cannot reach Rego mutation routes or controls.
- [ ] Hard organization deletion is absent from API and UI.
- [ ] Responsive, keyboard, empty, loading, stale and partial-failure states are verified.
