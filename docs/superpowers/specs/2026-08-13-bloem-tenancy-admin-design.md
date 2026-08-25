# Bloem Tenancy Administration Design

Date: 2026-08-13
Status: Superseded by `2026-08-13-bloem-opa-centered-multitenant-authorization-design.md`

## Purpose

Bloem already has the first server-side resource-tenancy boundary: typed
platform and organization owners, platform-resource entitlements, immutable
bundle membership revisions, and fail-closed access checks. Those primitives
are not currently manageable through the API or web interface.

This design adds a complete administration surface for:

1. organizations;
2. platform and organization resources; and
3. organization entitlements and default bundles.

The surface must remain safe for a deployment with thousands of accounts and
must not change existing Silo client behavior.

## Decisions

- The web application gets one unified **Admin / Tenancy** workspace.
- Platform administrators can perform every tenancy mutation.
- Organization administrators receive read-only access to their own
  organization, effective resources, entitlement state, and history.
- Regular users receive no tenancy administration access.
- Legacy Silo administrators continue through the existing default-
  organization compatibility path. The new management contract is v10-only.
- A library or plugin installation receives its owner at creation. Ownership is
  immutable in this release.
- Direct entitlement changes apply immediately.
- Default-bundle changes create a staged revision that is reviewed and
  published. Publishing affects future organizations by default.
- Applying a published revision to existing organizations is a separate,
  explicit bulk action with a dry run.

## Alternatives Considered

### Unified tenancy workspace — selected

One platform-wide workspace contains overview, organizations, resources,
default bundle, and audit sections. It gives operators a coherent view of the
security boundary and makes changes easier to audit.

### Controls distributed across existing pages

Ownership and entitlement controls could be embedded in library, plugin, user,
and security pages. This is familiar locally but fragments cross-organization
state and makes platform-wide review difficult.

### Organization-first console

Every operation could begin from an organization detail page. This is strong
for tenant support but weak for platform resource inventory and default-bundle
administration.

## Authorization Model

The server authorizes every request; client-side visibility is only a usability
aid.

### Platform administrator

A current platform administrator may:

- create, inspect, activate, and suspend organizations;
- list all platform and organization resources;
- choose an immutable owner while creating a library or plugin installation;
- grant, suspend, restore, and revoke an organization's platform-resource
  entitlement;
- create, edit, review, and publish a default-bundle draft;
- preview and launch bulk application of a published bundle revision; and
- inspect complete tenancy audit history and bulk-job outcomes.

The API revalidates current platform authority and security revisions for every
mutation. A stale or disabled administrator cannot rely on an older session.

### Organization administrator

A current organization administrator may read only:

- their own organization summary;
- resources owned by their organization;
- platform resources to which their organization has an active, suspended, or
  historical entitlement; and
- tenancy audit events visible to their organization.

They cannot enumerate other organizations, discover hidden resources, modify
entitlements, change ownership, manage bundles, or launch jobs.

### Regular and legacy callers

Regular users receive no tenancy administration routes. V1 callers continue to
resolve the default organization exactly as they do today. Organization IDs,
owner IDs, entitlement details, and bundle metadata are not added to existing
v1 responses.

Out-of-scope or hidden objects return the same non-disclosing not-found
response as missing objects.

## Unified Web Workspace

The platform administrator sees **Admin / Tenancy** with five stable sections.

### Overview

The overview shows organization counts by state, platform resource counts,
the active default-bundle revision, organizations behind that revision,
suspended entitlements, active bulk jobs, and recent audit events. Counts and
lists come from summary endpoints rather than client-side aggregation.

### Organizations

The organization list supports server-side search, status filtering, and cursor
pagination. The detail view contains summary, members, access, and history
tabs. Access rows show resource kind, name, owner, entitlement state, source,
and bundle revision. Platform administrators receive grant/suspend/restore/
revoke actions; organization administrators see the same scoped information
without mutation controls.

Organization create, suspend, and reactivate actions require confirmation.
Suspension previews active members and resources. Mutations require a reason.

### Resources

The resource inventory combines media folders and plugin installations under a
typed resource representation. It supports kind, owner kind, owning
organization, and status filters. Ownership is displayed prominently and is
not editable.

Existing library and plugin creation flows gain an owner selector visible only
to platform administrators. It defaults deliberately according to the caller's
chosen context; it must never infer a non-platform owner from a browser header.
After creation, changing the owner requires a future dedicated transfer design.

### Default bundle

The page shows the published revision and, when present, exactly one editable
draft based on it. Operators can add or remove typed resources, review a
before/after diff, provide a reason, and publish. A published revision is
immutable and becomes the organization-creation default.

Publication does not rewrite existing entitlements. An **Apply to existing**
action opens a target filter, performs a dry run, displays additions and
conflicts, then launches a durable bulk job after confirmation.

### Audit

The append-only audit view supports cursor pagination and filters for actor,
organization, resource, action, request ID, and time. Entries show the reason
and compact before/after state. Sensitive credentials and secret configuration
values are never accepted into or rendered from tenancy audit payloads.

## API Shape

The exact route naming may follow established router conventions, but the
contract is separated into these capabilities:

- platform overview and paginated organization inventory;
- organization create/read/status mutation;
- typed, paginated resource inventory;
- organization-effective resource and entitlement history reads;
- immediate entitlement grant/suspend/restore/revoke mutations;
- active and draft default-bundle reads;
- draft create/update/diff/publish operations;
- bulk dry run, launch, status, failure detail, and retry operations; and
- paginated tenancy audit reads.

All management routes live under `/api/v10`. Platform mutation routes use the
platform-admin middleware. Organization-admin reads use organization-bound v10
session authority and derive organization scope exclusively from validated
claims.

Mutation requests carry:

- an idempotency key;
- the expected organization, entitlement, bundle, or policy security revision;
- a human-readable reason;
- no caller-selectable actor identity; and
- no organization selector outside routes explicitly restricted to platform
  administrators.

Responses use stable typed identifiers and include the resulting revision and
request ID. Lists use opaque cursors and bounded page sizes.

## Persistence

The existing tables remain authoritative:

- `resource_owners`;
- `entitlement_bundles`;
- `entitlement_bundle_versions`;
- `entitlement_bundle_members`; and
- `organization_entitlements`.

The management workflow adds only the persistence required for explicit state:

### Bundle lifecycle

Bundle versions record draft or published state, creator, creation time,
publisher, publication time, and reason. At most one draft may exist for the
organization-creation default bundle. Draft membership may change through
revision-checked transactions. Published membership and metadata are
immutable. The active revision can reference only a published version.

### Audit events

An append-only tenancy audit table records:

- event ID and timestamp;
- actor account and effective platform role;
- action and target type/ID;
- organization and resource IDs when applicable;
- request and idempotency IDs;
- reason; and
- bounded, typed before/after state.

Audit insertion occurs in the same transaction as a single-resource mutation.
Database constraints reject update or deletion of audit events through the
application role.

### Bulk jobs

A durable job stores the published bundle revision, initiating actor, reason,
frozen target criteria and target organization IDs, dry-run summary hash,
state, progress counters, and timestamps. Per-organization result rows record
success, no-op, conflict, or failure without containing secrets.

Each organization is processed atomically. A failed organization does not roll
back successful organizations. Retry operates only on failed or unprocessed
targets and retains the original job identity and audit chain.

## Mutation Semantics

- Grant creates one active historical entitlement identity when no live row
  exists.
- Suspend and restore preserve the same entitlement identity and increment its
  security revision.
- Revoke is terminal, records revocation metadata, and preserves history.
- Granting the same resource after revocation creates a new entitlement row.
- Uniqueness constraints continue to allow at most one active or suspended
  entitlement for an organization and typed resource.
- Organization suspension invalidates organization-bound sessions through the
  existing organization and membership revision checks.
- Ownership remains immutable after root creation.
- Concurrent or stale writes return conflict without partially mutating state.

## Errors and Recovery

- Hidden, cross-organization, and nonexistent objects return indistinguishable
  not-found responses.
- Stale expected revisions and incompatible live state return conflict.
- Invalid input returns field-addressable validation errors.
- Replayed idempotent requests return the original result.
- Bulk-job progress survives server restart.
- Bundle publication is an atomic transaction and cannot be left partially
  active.
- A partial bulk failure does not alter the published default revision and does
  not prevent reads.
- The UI shows stable request IDs and retryable versus terminal failures.

## Scale and Performance

The design assumes thousands of accounts and many organizations:

- all large lists use indexed server-side filtering and cursor pagination;
- overview counts use bounded aggregate queries with supporting indexes;
- organization detail does not load all members or audit events eagerly;
- bulk work is asynchronous, restart-safe, and bounded per transaction;
- workers use explicit concurrency and rate limits rather than one goroutine per
  organization; and
- entitlement mutations advance only the revisions needed to invalidate
  affected authority.

## Compatibility

- Existing v1 payloads and routes are unchanged.
- Existing Silo login and profile switching continue against the default
  organization.
- Existing compatibility-created platform resources continue to receive the
  default organization's direct entitlement.
- Existing roots are shown with their migrated platform owner.
- No client must inspect a server version. New Bloem clients discover the v10
  tenancy capability and hide the workspace when absent.
- The first implementation is server plus web administration. Native client
  administration can consume the same v10 API later without changing its
  semantics.

## Testing

### Database and stores

Disposable-PostgreSQL tests cover migrations, rollback before private-resource
writes, ownership immutability, bundle lifecycle constraints, audit
append-only behavior, entitlement state transitions, idempotency, optimistic
concurrency, and restart-safe bulk processing.

### API and authorization

Handler tests cover platform-admin success, organization-admin scoped reads,
regular-user denial, stale authority, cross-organization non-disclosure,
pagination, validation, conflicts, and audit creation. Tests prove caller-
supplied headers cannot select organization authority.

### Web

Typed-client tests lock request and response contracts. React tests cover every
workspace state, paginated filtering, confirmations, required reasons,
revision conflicts, bulk dry-run review, retry, read-only organization-admin
rendering, keyboard operation, focus management, and accessible status/error
announcements.

### Compatibility and end-to-end

Existing v1 compatibility tests remain green. Browser-level tests exercise one
platform-admin workflow and one organization-admin read-only workflow against
a disposable database. Load-oriented tests cover pagination and bounded bulk
worker concurrency without relying on household-sized fixtures.

## Deployment

The database migration is additive. Deploy the API before exposing the web
workspace. Existing servers continue operating with no new required settings.
The workspace is shown only when the capability endpoint confirms support and
the current caller has platform or organization administration authority.

Bulk processing is disabled until its tables, worker, and recovery checks are
available in the same release. No production operation depends on an
in-memory-only queue.

## Out of Scope

- Transferring ownership of an existing resource.
- Organization-admin mutations or acceptance workflows.
- Per-user or per-profile resource entitlements.
- Changing legacy Silo client contracts or exposing tenancy metadata through
  v1.
- Native Apple or Android administration screens in this delivery.
- Billing, quotas, or usage metering.
