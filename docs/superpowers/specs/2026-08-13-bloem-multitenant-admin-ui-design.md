# Bloem multitenant administration UI design

**Status:** Approved design
**Date:** 2026-08-13
**Related design:** `docs/superpowers/specs/2026-08-13-bloem-opa-centered-multitenant-authorization-design.md`

## Purpose

Bloem needs an administrative interface for the OPA-centered multitenant
foundation. The current web administration supports server-wide users, access
groups and policies, but it does not expose organizations, memberships,
organization-scoped groups, tenant entitlements or an explicit authority
context.

The new interface must remain usable for installations with thousands of
accounts while preserving Silo client compatibility. It must make the active
authority boundary visible, prevent data from one organization appearing in
another, and keep policy-engine administration separate from routine
organization administration.

## Product principles

1. Authority is visible. Every administrative page shows whether the operator
   is acting at Platform scope or in a named organization.
2. Tenant authority comes only from a server-validated session. Headers, query
   parameters and mutation bodies cannot select an organization.
3. Legacy `/api/v1` behavior remains unchanged and continues to project Silo
   clients into the default organization.
4. Platform and organization administration share one web application and one
   design system, but expose different navigation and data.
5. Everyday controls remain structured. Organization administrators configure
   memberships, groups, limits and entitlements; they cannot deploy arbitrary
   Rego.
6. Large collections use server-driven search, filtering, sorting and cursor
   pagination.
7. Destructive or authority-changing operations are explicit, audited and
   revision guarded.

## Authority contexts

### Platform context

Platform context is available only to platform administrators. It owns:

- the organization directory and organization lifecycle;
- global Rego documents, versions, activation and simulation;
- policy-engine health and cross-organization decision diagnostics;
- plugins, nodes, storage, tasks, global settings and diagnostics; and
- cross-organization operational audit.

All cross-organization records display an organization label. Platform context
does not imply that an unlabeled row is globally owned.

### Organization context

Organization context is available to platform administrators and active
organization administrators authorized for that organization. It owns:

- organization overview and health;
- people, memberships and profiles;
- access groups and profile-to-group assignment;
- organization libraries and platform entitlements;
- invitations; and
- organization-filtered policy decisions and explanations.

The first release has one broad `organization_admin` administrative role.
Delegated user, library, policy and support roles remain capability-gated and
unavailable until their backend enforcement model is implemented.

Ordinary users never receive the admin shell or authority switcher.

## Administrative context session

The existing account session remains the compatibility root. A separate,
short-lived administrative context session authorizes `/api/v2/admin`.

1. The administrator signs in through the existing account flow.
2. `GET /api/v2/organizations` lists active memberships visible to the account.
3. `POST /api/v2/admin/session` accepts a requested Platform context or an
   organization identifier.
4. The server revalidates platform authority or active organization membership.
5. The server returns a short-lived token bound to the selected scope,
   membership identifier and current policy/security revisions.
6. `/api/v2/admin/*` accepts only that token and attaches authoritative tenant
   context before authorization or storage access.

The browser keeps the administrative context token in memory. It may persist
the last selected context identifier, but it never persists the context token.
After reload, it re-mints the context from the current account session.

Changing context performs one ordered transition:

1. cancel active requests from the previous context;
2. clear tenant-scoped query caches;
3. revalidate and mint the new context session;
4. rebuild context-aware navigation; and
5. redirect to the nearest route authorized in the new context.

Every tenant query key includes the context identity. A stale revision,
suspended organization, revoked membership or lost platform authority
invalidates the session immediately and returns the operator to context
selection with a specific explanation.

## Admin shell

The authority switcher sits above the administrative navigation. It always
shows:

- `Platform` or the organization name;
- organization status when applicable;
- the operator's authority level; and
- a control to change to another authorized context.

The desktop sidebar and mobile drawer use the same context model. Switching
context never leaves a stale page visible behind a loading overlay; the old
route unmounts before the new context begins fetching.

### Platform navigation

- Organizations
- Global policy
- Policy health
- Plugins
- Nodes and infrastructure
- Activity and cross-organization audit
- Existing global administration surfaces

### Organization navigation

- Overview
- People
- Access groups
- Libraries and entitlements
- Invitations
- Policy decisions
- Activity and audit

Existing pages are adapted behind the administrative context provider rather
than copied into parallel Platform and Organization implementations.

## Organization lifecycle

Platform administrators can:

- list, search, filter and inspect organizations;
- create an organization with its first organization administrator;
- edit its name and slug;
- suspend and reactivate it;
- add, remove, suspend and reactivate memberships; and
- transfer ownership after explicit confirmation and account
  re-authentication.

The organization detail includes status, owner, membership and profile counts,
library and entitlement counts, policy revision, recent authorization activity
and security warnings.

Hard deletion is not part of the first release. Suspension is the reversible
administrative lock until retention, media ownership, audit preservation and
legal deletion semantics have a separate approved design.

## People management

The People page is server-driven. It supports:

- text search across account email, display name and profile name;
- filters for membership status, access group and recent activity;
- server-side sorting and cursor pagination;
- account, membership status, profiles, assigned groups, last activity and
  security revision columns;
- inline profile group reassignment;
- bulk group assignment; and
- bulk membership suspension and reactivation.

A contextual drawer handles routine inspection without losing the current
filter and selection. A dedicated detail page exposes deeper account, profile,
membership, session and authorization history.

Organization administrators receive records only from the active organization.
Platform administrators can inspect cross-organization membership only from
explicit Platform routes whose records include organization identity.

Bulk actions do not submit a browser-held list of every matched identifier.
The server creates an immutable, organization-bound selection token from the
current filters. Confirmation shows the organization, filters, matched count
and excluded records. The job result separates succeeded, skipped and failed
records and never treats partial failure as complete success.

## Access groups and media scope

The existing Access Groups page becomes organization scoped. It retains its
structured controls for libraries, downloads, playback limits, requests and
assignable permissions. It adds:

- the current organization identity;
- server-driven member counts;
- profile assignment and reassignment;
- effective media-ceiling explanation; and
- policy/security revision feedback after mutations.

Every profile has exactly one canonical access group. Deleting a non-default
group reassigns its profiles to the same organization's default group in the
server transaction. The UI previews that effect before confirmation.

Libraries and Entitlements distinguish organization-owned libraries from
platform libraries granted to the organization. Effective media access is the
intersection of the tenant ceiling and the profile/group restrictions; the UI
must not describe entitlements as unconditional grants.

## Policy administration and explanations

Only platform administrators can manage Rego documents and versions, validate
or activate policies, or change policy-engine settings.

Organization administrators can inspect decisions for their organization. An
explanation displays:

- organization and membership context;
- account and profile subject;
- resolved access group;
- tenant library ceiling;
- requested action and resource;
- allow or deny outcome and reason code; and
- contributing vendor and custom policy versions.

Inputs marked sensitive by the policy contract are redacted. Organization
administrators cannot use simulation to query another organization or deploy
policy code.

## API shape

### Session

- `POST /api/v2/admin/session`

### Platform resources

- `/api/v2/admin/platform/organizations`
- `/api/v2/admin/platform/organizations/{id}`
- organization lifecycle actions below the organization resource;
- global policy documents, versions, simulation and health; and
- explicitly cross-organization activity and audit resources.

### Active-organization resources

- `/api/v2/admin/organization/overview`
- `/api/v2/admin/organization/people`
- `/api/v2/admin/organization/groups`
- `/api/v2/admin/organization/libraries`
- `/api/v2/admin/organization/entitlements`
- `/api/v2/admin/organization/invitations`
- `/api/v2/admin/organization/policy-decisions`
- organization activity and audit resources.

Active-organization routes do not accept an organization identifier. They
derive it from the validated context session. Platform inspection uses
explicit `/platform/organizations/{id}/...` routes and requires Platform
authority.

List endpoints use stable cursor pagination and return filter metadata needed
for deterministic selection tokens. Mutations carry revision preconditions.
A stale mutation returns `409 authorization_state_changed` with the current
revision; the web UI reloads the record while preserving the unsaved draft for
comparison.

## Frontend architecture

The React application adds:

- `AdminContextProvider` for available contexts, selected context, transition
  state and authority-aware navigation;
- a dedicated v2 admin API client that attaches only the in-memory context
  token;
- context identity in all v2 admin React Query keys;
- request cancellation and scoped cache removal during context changes;
- feature-gated Platform and Organization navigation; and
- reusable server-table, immutable-selection and partial-job-result primitives.

Existing hooks and pages migrate incrementally to context-aware v2 endpoints.
No page may mix v1 default-organization data with v2 organization-context data
in one view.

## Error and revocation behavior

- `401 tenant_session_required`: return to context selection and re-mint when
  the account session is still valid.
- `401 authorization_state_stale`: discard the context token, clear scoped
  caches and require a fresh context selection.
- `403 organization_suspended`: show the suspension state and prevent further
  organization actions.
- `403 insufficient_platform_authority`: remove Platform navigation and return
  to an authorized organization.
- `404`: do not disclose foreign organizations, memberships, profiles or
  groups.
- `409 authorization_state_changed`: preserve the draft, reload current state
  and show a comparison.
- `422`: render field-level validation errors without losing the draft.
- `503 tenant_unavailable`: keep the authority label visible, block mutations
  and provide a retry action.

Audit entries record actor, authority context, target organization, subject,
before/after revision, outcome and request correlation identifier. Tokens,
credentials and sensitive policy inputs are never logged.

## Accessibility and responsive behavior

- The context switcher is keyboard navigable and announces scope changes.
- Focus moves to the new page heading after a successful switch and returns to
  the switcher after a failed switch.
- Tables retain semantic headers, selection labels and bulk-action counts.
- The People detail drawer becomes a full-height sheet on small screens.
- Loading uses layout-matched skeletons; empty and error states remain scoped
  to the visible organization.
- Destructive confirmation includes the organization name and affected count.

## Verification

Backend verification covers:

- context-session minting, expiry, revision mismatch and revocation;
- platform versus organization route authorization;
- cross-organization non-disclosure;
- organization lifecycle and ownership transfer;
- pagination and immutable bulk selection;
- partial bulk failures and retries;
- profile group reassignment and policy revision invalidation; and
- unchanged `/api/v1` Silo compatibility contracts.

Frontend verification covers:

- context-aware navigation and route guards;
- cache and request isolation during rapid context switching;
- organization directory and lifecycle workflows;
- People filtering, pagination, selection and partial job results;
- access-group and entitlement explanations;
- loading, empty, stale, forbidden and unavailable states;
- keyboard and focus behavior; and
- responsive screenshots for Platform, Organization, People and Access Groups.

A real PostgreSQL acceptance suite creates two organizations with an account
that belongs to both, organization-only accounts, distinct groups and distinct
media entitlements. It proves that switching context returns only the selected
organization's people, profiles, groups, libraries and policy decisions, and
that revocation invalidates an already-minted context session.

## Delivery sequence

1. Administrative context session and authority middleware.
2. Context provider, switcher, cache isolation and route guards.
3. Platform organization directory and lifecycle.
4. Organization overview and People management.
5. Organization-scoped Access Groups, Invitations, Libraries and Entitlements.
6. Platform policy management adaptation and organization decision
   explanations.
7. PostgreSQL acceptance, responsive/accessibility verification and release
   evidence.

## Explicit non-goals

- No hard organization deletion.
- No arbitrary Rego editing by organization administrators.
- No delegated administrative roles in the first release.
- No v1 organization selector or organization header compatibility behavior.
- No direct-profile login or shared-device pairing implementation in this UI
  phase; those remain separately capability-gated.
