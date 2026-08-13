# Vondel OPA-Centered Multi-Tenant Authorization Design

**Date:** 2026-08-13

**Status:** Approved in conversation; awaiting written-spec review

**API boundary:** Silo-compatible `/api/v1`; additive tenant-aware `/api/v2`
**Upstream baseline:** Silo's merged OPA and access-group implementation in
[PR #282](https://github.com/Silo-Server/silo-server/pull/282)

## Purpose

Vondel must support both a single self-hosted installation and a service with
many independent organizations and thousands of accounts. It must preserve
Silo client behavior while adding direct profile authentication, scoped
administration, platform-shared resources, and strong tenant isolation.

This design extends Silo's existing access groups and embedded Open Policy
Agent (OPA) implementation. It does not introduce a parallel authorization
engine. PostgreSQL stores and constrains authorization facts; OPA makes typed
policy decisions; every protocol adapter uses the same services.

This document supersedes:

- `2026-08-12-vondel-multitenant-security-and-authorization-design.md`; and
- `2026-08-13-vondel-tenancy-admin-design.md`.

The earlier tenant-identity and resource-ownership implementation remains
subject to review against this design. The unmerged tenancy-administration
migration is not approved for merge merely because portions may be reusable.

## Goals

- Preserve current Silo `/api/v1` account login and profile switching.
- Add tenant-aware identity and administration through `/api/v2`.
- Separate platform authority, organization administration, and media access.
- Make OPA the single application policy decision point.
- Enforce tenant isolation independently through database constraints and
  organization-scoped queries.
- Support organization-owned and platform-shared media, Live TV, plugins, DVR
  storage, and other consumable-media resources.
- Apply identical policy to native, Jellyfin, Audiobookshelf, Live TV, plugin,
  download, playback, and event surfaces.
- Keep foundational commits reviewable and suitable for upstream Silo PRs.

## Non-Goals

- Organization-authored raw Rego in the initial release.
- Multiple simultaneous access groups per profile.
- Cross-organization resource sharing.
- Implicit media access for platform or organization administrators.
- Encoding Vondel branding into shared server contracts.
- Using an artificially high API version to distinguish Vondel clients.

## Architectural Responsibilities

### PostgreSQL

PostgreSQL is the source of durable authorization facts:

- accounts, organizations, memberships, and profiles;
- platform and organization owners;
- administrative role definitions, assignments, and scopes;
- resource ownership and platform-resource entitlements;
- organization-owned access groups and profile assignment;
- profile-specific restrictions;
- security revisions, policy revisions, and audit history; and
- durable bulk-operation state and idempotency records.

Composite foreign keys and constraints reject cross-organization references.
Stores require explicit organization scope and never depend on a caller-
supplied organization identifier that has not been resolved from authenticated
authority.

### OPA

The existing `internal/policy` package remains the only package that imports
OPA. Its typed policy-decision interface is extended for:

- platform administration;
- organization administration;
- media visibility and metadata disclosure;
- playback, download, transcode, stream, recording, and Live TV actions;
- plugin administration and invocation; and
- sensitive actions requiring step-up authentication.

OPA input contains bounded, typed facts. It does not query the database. A
decision includes allow or deny, a stable reason code, applicable bounded
scope, policy and security revisions, and audit metadata.

### Application services and stores

Application services authenticate a subject, resolve its tenant context, load
tenant-scoped facts, call the typed OPA decision, perform a tenant-scoped store
operation, and record required audit evidence.

For large inventories, OPA resolves a bounded scope rather than evaluating
each row. SQL applies organization, resource-type, and allowed-resource filters
while querying. Contextual action checks still run through OPA before a
sensitive operation occurs.

### Protocol adapters

Silo v1, Vondel/Silo v2, web administration, Jellyfin compatibility,
Audiobookshelf compatibility, Live TV, plugins, downloads, playback, and event
delivery are adapters over the same subject-resolution and policy services.
Changing protocol surface cannot change effective authority.

## Authorization Facts

### Accounts, memberships, and profiles

An account may hold memberships in multiple organizations. A membership is the
account's administrative and lifecycle relationship with one organization. A
profile is an organization-bound media identity with its own history,
preferences, restrictions, and optional direct credentials.

Administrative authority attaches to the account membership. Media authority
attaches to the active profile. Neither implies the other.

### Administrative roles

Platform roles and organization roles occupy separate namespaces.

- The platform owner holds protected recovery and ownership authority.
- Platform administrators receive explicit platform capabilities.
- An organization owner holds protected authority for one organization.
- Organization administrators receive one or more structured role assignments.

Organization owners may clone built-in role templates and create custom roles.
An assignment may carry resource scopes. A delegator may grant only
capabilities and scopes the delegator already holds and may not grant platform
authority, ownership, protected recovery, or unrestricted policy authority.
Role names have no security meaning; only validated capabilities and scopes do.

Built-in templates include Full Administrator, Identity Manager, Delegated
Admin Manager, Library Manager, Live TV Manager, Plugin Manager, Policy
Manager, Auditor, and Support Operator. Templates are editable only by cloning.

### Access groups

Platform access groups are templates. An organization clones a template into
an organization-owned access group and controls the clone thereafter. Access
groups cannot span organizations.

Each profile has exactly one access group. Profile-specific restrictions may
only narrow that group. Platform and organization structured restrictions may
also narrow it through the vendor policy. Multiple reusable narrowing overlays
are explicitly deferred, but the versioned OPA input may add them later without
changing v1 behavior.

### Resource ownership and entitlements

A resource is platform-owned or owned by exactly one organization.
Organizations do not share resources directly with one another.

An organization automatically has administrative availability for its own
resources. A platform-owned resource requires an explicit organization
entitlement before organization administrators may assign it or profiles may
consume it. Neither ownership nor entitlement grants profile access; the
profile's access group and OPA decision still apply.

Three template concepts remain distinct:

- administrative role templates define management capabilities;
- access-group templates define media-consumption defaults; and
- organization resource packages define default platform-resource
  entitlements for newly created organizations.

The UI and API use these names and do not call all three concepts “bundles.”

## Policy Evaluation

The policy evaluation order is:

1. immutable tenant and Vondel safety rules;
2. resource ownership or active platform entitlement;
3. applicable platform or organization administrative grant, for an
   administrative action;
4. profile access-group grant, for a consumption action;
5. assignment scope and profile-specific restrictions;
6. platform-authored narrowing policy; and
7. device, network, time, authentication strength, and request context.

A later layer can only narrow an earlier grant. Unknown capabilities, media
classifications, organizations, resources, or subjects deny. Missing or stale
security state, policy timeouts, malformed decisions, and failed snapshot
validation deny.

Only platform security administrators may edit raw Rego in the initial
release. Organization administrators use structured roles, groups, schedules,
device rules, ratings, adult-content controls, and resource scopes. Raw custom
Rego remains narrowing-only and cannot create authority, cross a tenant
boundary, weaken owner protections, or reveal hidden resources.

## Media and Compatibility Enforcement

Policy input and enforcement cover:

- libraries and individual library roots;
- media categories and ratings;
- adult titles and separately classified adult scenes;
- chapters, thumbnails, previews, search, recommendations, and timelines;
- downloads, direct play, remux, transcode, concurrency, and stream
  termination;
- tuner groups, channels, guide data, recording rules, recordings, and DVR
  storage;
- plugin classes, configuration, secret access, and invocation; and
- WebSocket and other event visibility.

Adult-scene access requires both title access and explicit scene-level
authorization. Denied titles and scenes are omitted from discovery surfaces;
errors, counts, artwork, chapter metadata, and logs do not reveal their
existence.

Compatibility adapters must not reconstruct access policy locally. Jellyfin
and Audiobookshelf resolve the same bounded media scope and action decisions as
native clients. Folder restrictions, downloads, playback limits, and revocation
therefore have identical results across protocols.

## API Compatibility

### Silo-compatible `/api/v1`

V1 preserves current behavior:

- account login establishes a session in the default organization;
- existing profile discovery and switching remain unchanged;
- unlocked secondary profiles require no password;
- protected profiles use the existing PIN flow;
- only the default-organization owner and Full Administrators project as
  legacy `admin`;
- legacy admin routes retain their primary-profile restrictions; and
- scoped administrators project as ordinary users because v1 cannot represent
  partial authority.

Platform authority alone does not create a legacy admin session or grant media
access.

### Tenant-aware `/api/v2`

V2 adds:

- organization membership selection;
- direct-profile login;
- shared-device enrollment and bounded profile switching;
- separate administrative mode and step-up authentication;
- structured platform and organization capabilities;
- scoped administrative roles;
- organization and resource administration; and
- stable denial reasons and audit correlation.

The primary profile may have credentials separate from the account login.
Secondary profiles may optionally have direct credentials. Account and profile
email addresses are unique and cannot collide. An enrolled shared device can
switch among its explicitly allowed unprotected profiles without repeatedly
entering passwords; protected profiles require their configured PIN or
password. Administrative mode never follows implicitly from profile switching.

`/api/v2/capabilities` advertises optional feature contracts and their schema
versions. Feature contracts evolve independently of the top-level API version.

## Administration Workspace

The web interface provides one context-aware security workspace rather than a
parallel tenancy console beside Silo's access-group and policy pages.

Platform context contains organizations, platform administrators and roles,
platform-owned resources, organization resource packages, the platform policy
editor, cross-organization audit, and bulk operations.

Organization context contains members and profiles, organization roles and
assignments, access groups, owned and entitled resources, library/Live TV/
plugin/DVR scopes, and organization audit.

Changing administrative context is explicit. Administrative mode is separate
from media-profile switching. Existing Silo pages link into the default-
organization context but do not maintain separate state or enforcement logic.

All large lists use bounded cursor pagination and server-side filtering. Bulk
mutations require a dry run, explicit reason, idempotency key, frozen target
set, conflict detection, durable execution, and auditable per-target outcomes.
Hidden organizations and resources do not appear in counts, selectors, or
distinguishable errors.

Publishing a new organization resource package affects future organizations.
Applying it to existing organizations is a separate reviewed bulk operation.

## Audit, Revision, and Revocation

Security facts carry monotonic revisions. Sensitive administration requires
current facts and fails when authorization state is stale. Membership,
organization, profile, role, entitlement, access-group, and policy changes
publish invalidation events to every application node.

Account and organization suspension use a small strongly consistent revocation
path. Stream admission and termination use authoritative shared state rather
than process-local counters.

Every sensitive administrative decision, mutation, denial, support action,
ownership event, and bulk result is audited with actor, effective subject,
organization, action, target, request ID, revisions, and stable reason code.
Ordinary successful media decisions may be sampled. Secrets and raw credential
values are never accepted into policy input, audit payloads, or error details.

## Migration

An existing installation migrates into one default organization:

- the setup administrator becomes platform owner and organization owner;
- other administrators become default-organization Full Administrators;
- users become accounts with default-organization memberships;
- profiles preserve identifiers, PINs, history, preferences, and restrictions;
- access groups become organization-owned groups;
- account-level access-group assignment is projected onto the account's
  profiles;
- current account restrictions remain a temporary membership ceiling until
  profile parity is proven; and
- libraries, tuners, plugins, recordings, settings, sessions, and tokens attach
  to the default organization.

Existing v1 sessions continue until normal expiry but never gain stronger
authority. Migration remains reversible until an explicit finalization step.
No destructive cleanup occurs in the initial release.

## Error Contract

Externally visible errors use stable codes and do not distinguish hidden from
missing tenant resources. Core codes include:

- `authorization_denied`;
- `authorization_state_stale`;
- `authentication_step_up_required`;
- `admin_mfa_required`;
- `organization_suspended`;
- `account_suspended`;
- `resource_not_found_or_hidden`;
- `profile_verification_required`; and
- `policy_evaluation_failed`.

## Verification Gates

Required evidence includes:

- PostgreSQL migration up/down/up tests that cannot silently skip;
- cross-organization store and constraint tests for every tenant-owned table;
- OPA matrices for roles, scopes, entitlements, groups, restrictions, and stale
  revisions;
- differential default-organization tests against current Silo v1 behavior;
- native v2, Jellyfin, Audiobookshelf, Live TV, plugin, playback, download, and
  event authorization tests;
- adult-title and adult-scene non-disclosure tests across every discovery and
  playback surface;
- folder/root access tests;
- shared-device switching and administrative step-up tests;
- multi-node revocation and stream-termination tests;
- restart, idempotency, conflict, partial-failure, and retry tests for bulk
  operations;
- property and fuzz tests proving added restrictions cannot widen authority;
- load tests for thousands of accounts and policy snapshot reloads; and
- audit redaction and secret-exclusion tests.

CI provisions PostgreSQL and fails if required integration tests skip. Release
ordering is migrate and populate facts, prove compatibility parity, advertise
v2 capabilities, then expose administration.

## Upstream Delivery Strategy

Implementation is divided into independently reviewable changes:

1. organization-aware schema and default-organization compatibility;
2. tenant-scoped stores and hard isolation constraints;
3. structured OPA inputs and typed decisions;
4. compatibility parity across native, Jellyfin, and Audiobookshelf paths;
5. additive `/api/v2` identity and capability contracts;
6. organization roles, resources, and entitlements;
7. platform and organization administration UI; and
8. Live TV and plugin policy extensions.

Shared commits remain product-neutral. Vondel branding, optional capabilities,
and deployment defaults stay in separate commits. Upstream Silo may accept the
foundation incrementally without adopting every Vondel feature.

## Deferred

- Organization-authored raw Rego.
- Multiple narrowing policy overlays per profile.
- Ownership transfer.
- Cross-organization resource sharing.
- Widening custom policy.
- Destructive removal of compatibility columns and legacy code.
