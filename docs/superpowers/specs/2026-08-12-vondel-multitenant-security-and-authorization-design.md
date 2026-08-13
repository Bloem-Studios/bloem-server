# Vondel Multi-Tenant Security and Authorization Design

**Date:** 2026-08-12
**Status:** Approved
**API boundary:** Silo-compatible `/api/v1`; native Vondel `/api/v10`
**Related prior work:** [Silo Server PR #251 — Add ACL access management](https://github.com/Silo-Server/silo-server/pull/251)

## Goal

Replace the binary `admin`/`user` security model with a scalable authorization
system that supports both a single self-hosted organization and a deployment
hosting many independent organizations with thousands of users.

The design must:

- preserve existing Silo client login, profile switching, profile PIN, playback,
  and legacy administration behavior;
- provide Vondel clients with organizations, direct-profile login, shared-device
  enrollment, delegated administration, and precise capabilities;
- separate administrative authority from media consumption;
- enforce organization isolation below the policy layer;
- use embedded OPA/Rego as the policy decision engine;
- support organization-owned and platform-shared media libraries and Live TV;
- enforce adult-title and adult-scene restrictions at the server;
- remain usable as one process while scaling to clustered deployments; and
- fail closed without making transient control-plane failure stop established,
  authorized playback unnecessarily.

## Core decisions

1. A deployment may host one or many organizations. A fresh or migrated
   single-organization installation gets one default organization and does not
   require multi-tenant ceremony in its ordinary UI.
2. There is exactly one protected platform owner per deployment and exactly one
   protected organization owner per organization.
3. Accounts are global security identities. Organization membership carries
   administrative authority. Profiles are organization-scoped media identities.
4. Administrative roles and profile access groups are separate systems.
5. Structured role assignments supply grants. Embedded OPA evaluates the final
   decision. Custom Rego may only narrow structured grants and vendor decisions.
6. Tenant ownership and isolation are enforced in Go and PostgreSQL before OPA
   evaluates a resource decision.
7. `/api/v1` remains the Silo-compatible projection. `/api/v10` is the native
   Vondel platform contract.
8. MFA is not globally mandatory. Platform and organization policy may require
   it specifically for administrative mode.

## Terminology and hierarchy

```text
Vondel deployment
├── platform owner
├── delegated platform operators
├── platform-owned libraries, tuners, plugins, and policy ceilings
└── organizations
    ├── organization owner
    ├── memberships and scoped administrative role assignments
    ├── profiles and profile access groups
    ├── organization-owned libraries, tuners, plugins, and policies
    └── organization audit log
```

### Account

A global login identity containing email, password or passkeys, optional MFA,
recovery methods, enabled/suspended state, and security revision. An account may
belong to and own multiple organizations.

Account login emails are globally unique. Direct-profile login aliases are also
globally unique and cannot equal an account login email.

### Membership

The relationship between an account and an organization. It carries membership
state and administrative role assignments. Administrative authority never
belongs directly to a viewing profile.

### Profile

An organization-scoped media identity containing watch history, preferences,
ratings, parental controls, and access-group membership. A secondary profile
does not require its own password. A profile may optionally receive a distinct
direct-login credential.

### Device enrollment

An organization-scoped authorization for a shared client. It determines which
profiles the client may expose and supports current shared-device profile
switching without storing every profile password.

### Service identity

A non-human identity for automation, scanners, plugins, integrations, and API
clients. It uses scoped, expiring credentials rather than a human password and
never receives implicit platform or organization ownership.

## Ownership and protected authority

The platform owner controls deployment recovery, organization lifecycle,
platform operators, and platform-owned resources. An organization owner
controls one organization's security root and delegated administrators.

An owner cannot be disabled, deleted, demoted, impersonated, or restricted by
another administrator. Owner-only capabilities cannot be placed in a custom
role or granted through custom Rego.

Ownership transfer requires:

- fresh strong authentication by the current owner;
- explicit acceptance by the recipient;
- a short-lived, single-use transfer transaction; and
- immutable audit entries for initiation, acceptance, cancellation, and expiry.

Full Administrators remain delegated administrators and are not equivalent to
owners. Platform recovery can restore control only through a documented
break-glass process. It does not automatically grant access to encrypted tenant
media.

## Three authorization planes

### Platform plane

Representative capabilities include:

- `organizations.create` and `organizations.suspend`;
- `platform_libraries.manage`;
- `platform_tuners.manage`;
- `platform_plugins.manage`;
- `platform_audit.read`; and
- `platform_operators.assign`.

Platform authority does not imply the right to browse or consume organization
media.

### Organization administration plane

Representative capabilities include:

- `members.manage`;
- `profiles.manage`;
- `admin_roles.assign`;
- `libraries.manage`;
- `metadata.manage`;
- `livetv.manage`;
- `recordings.manage`;
- `plugins.manage`;
- `security_policy.manage`; and
- `audit.read`.

Assignments may be scoped to libraries, access groups, tuner groups, plugins,
or other supported resource sets.

### Media-consumption plane

Profile access groups and profile-specific restrictions control visible
libraries and channels, media categories, content ratings, adult content,
playback, transcoding, downloads, requests, recording, quality, schedules,
devices, networks, and concurrency.

An administrator's profile receives no special media access. A media-entitled
profile receives no administrative authority.

## Administrative roles

Vondel provides editable-by-cloning built-in templates:

- **Full Administrator:** all organization-delegable capabilities, excluding
  ownership and protected recovery actions.
- **Identity Manager:** memberships, invitations, profiles, access groups, and
  device enrollment; cannot appoint administrators.
- **Delegated Admin Manager:** assigns only grants and scopes the delegator
  already possesses; cannot assign ownership or Full Administrator.
- **Library Manager:** selected libraries, scanners, metadata, collections, and
  maintenance.
- **Live TV Manager:** selected tuner groups, channels, guide data, recording
  rules, and DVR storage.
- **Plugin Manager:** selected plugin classes and configurations; secret access
  is separately gated.
- **Policy Manager:** access groups and narrowing custom policy; cannot modify
  protected vendor policy or owner rules.
- **Auditor:** read-only security, activity, and decision logs with scope-based
  redaction.
- **Support Operator:** temporary authority delivered through a support session.

An account may hold multiple assignments. Applicable grants combine by union,
but each grant retains its resource scope. Vondel must never flatten scoped
assignments into an unscoped capability set.

There are no per-user deny overrides in the role editor. Additional restrictions
belong in assignment scope or narrowing policy so decisions remain explainable.

### Delegation

Only an owner may appoint Full Administrators or create and edit administrative
role definitions by default. An owner may grant limited delegation. A limited
delegator can assign only capabilities and scopes already held and can never
grant ownership, Full Administrator, policy-root access, protected secret
access, or ownership transfer.

## Profile access groups

Access groups attach primarily to profiles and govern media consumption. A
group is an upper bound; profile-specific settings may only narrow it.

Restriction composition follows strictest-wins semantics:

- libraries and channel sets: intersection;
- playback quality and ratings: strictest ceiling;
- boolean rights such as downloads: logical AND;
- stream and transcode limits: strictest positive limit, with zero meaning no
  limit at that layer; and
- media-category and adult-content rights: intersection and explicit denial.

The account or membership may impose an additional ceiling during migration or
for organization policy, but it cannot turn a profile into an administrator.

## Platform-shared media and Live TV

A resource is either organization-owned or platform-owned. Organizations never
share resources directly with one another.

Platform libraries, tuner groups, and channel lineups are made available through
explicit organization entitlements. Entitlement grants visibility to the
organization; it does not grant access to every profile. Profile access groups
and OPA still decide consumption.

The evaluation chain is:

```text
platform entitlement to organization
→ scoped administrative authority, if administration is requested
→ profile access-group permission, if consumption is requested
→ contextual OPA decision
```

## OPA architecture

Vondel embeds the OPA Go library in each application node. It does not require an
OPA server or a network policy-decision call for normal requests.

Each node holds prepared, typed Rego queries built from a cryptographically
verified, immutable policy snapshot. In a cluster, an authorization control
plane stores structured assignments and policy revisions, compiles snapshots,
and distributes invalidation events. Nodes atomically replace a snapshot only
after validation.

OPA receives typed facts including actor, account, membership, organization,
admin assignments and scopes, active profile, access group, resource, device,
authentication strength, support-session state, request time, and policy
revision. It returns a typed decision containing allow/deny, stable reason code,
policy revision, and decision metadata.

### Policy layering

The final decision applies these layers:

1. immutable Vondel safety rules;
2. platform policy ceiling;
3. organization administrative grants;
4. scoped role assignments;
5. profile access group;
6. profile-specific restrictions;
7. optional platform custom policy;
8. optional organization custom policy; and
9. request context.

Custom policy is narrowing-only. It cannot create a missing grant, cross an
organization boundary, weaken owner protections, reveal hidden content, or
override immutable safety rules.

Custom Rego is an advanced, off-by-default surface. It is compiled before
activation, restricted to allowlisted pure operations, denied network,
filesystem, environment, random, and arbitrary runtime access, evaluated under
strict resource limits, versioned, simulated, reviewed, and atomically
activated. Malformed or invalid output fails closed.

## Availability, staleness, and revocation

- Established media consumption may continue using the last verified snapshot
  during a short control-plane interruption.
- Ordinary decisions may use a cached snapshot only within its configured
  maximum age.
- Sensitive administration requires the current authorization revision;
  otherwise it fails with `authorization_state_stale`.
- Security mutations commit centrally and publish invalidation events.
- Account and organization suspension use a small, strongly consistent
  revocation path independent of the larger policy bundle.
- Missing tenant context, unknown organization, missing snapshot, policy
  timeout, malformed decision, or unknown capability always denies.
- Failed policy activation retains the last valid snapshot, rejects the new
  version, and creates an audit event.

## Authentication and sessions

Passkeys are preferred; passwords remain supported. MFA is supported but is not
globally mandatory.

Platform and organization owners may enable an `admin_mfa_required` policy for
their respective administrative domain. When enabled, entering admin mode
requires MFA-strength authentication. Accounts without an enrolled factor must
enroll before performing administrative work. Viewing and profile switching do
not require MFA merely because the account has administrative roles.

Entering admin mode always requires fresh account-level authentication. Direct
profile credentials cannot enter admin mode. High-risk operations require a
newer step-up than ordinary administrative work; when MFA is not required, a
fresh password or passkey satisfies the step-up policy.

Sessions bind to account, membership, organization, device, active profile,
authentication method and strength, administrative elevation state, and
security/policy revisions. Profile switching changes media identity without
upgrading administrative authority.

## Support access

Organization owners may grant a platform operator a time-limited support
session specifying capabilities, scopes, reason, reference, and expiry.

Support access is visible in the organization UI and does not include media
consumption, credential viewing, adult content, or ownership transfer by
default. Every support action appears in both platform and organization audit
logs.

Emergency access without owner approval requires break-glass authentication, a
declared incident reason, a short expiry, prominent notification, and immutable
audit events. It cannot hide or delete its own records.

## Adult titles and scenes

Items and timed scenes may carry sensitivity classifications including
`explicit_adult`. Policy independently controls discoverability, metadata and
artwork visibility, playback, download, direct play, scene filtering, and
step-up requirements.

For a profile without explicit-content access:

- explicit titles are absent from search, recommendations, collections,
  notifications, activity feeds, autocomplete, and profile-visible logs;
- counts and identifiers do not disclose hidden items;
- artwork, preview thumbnails, subtitles, chapter titles, and trick-play images
  are filtered;
- direct downloads are denied; and
- a mixed-content title cannot direct-play its original file.

A mixed-content title must use a server-generated filtered rendition that
removes prohibited time ranges or be denied. A client-only skip marker is not a
security boundary. Silo clients may receive a safe rendition when supported;
otherwise the server denies the mixed-content title. Administrative visibility
into adult libraries also requires explicit resource scope.

## Tenant isolation and encryption

Every organization-owned row carries a non-null `organization_id`.
Organization-aware composite foreign keys prevent cross-tenant relationships
where practical. PostgreSQL row-level security provides defense in depth, and
each transaction sets an authenticated organization context. Missing context
denies tenant access.

Platform resources use distinct ownership records and explicit entitlement
tables rather than nullable organization ownership.

Organization identity is mandatory in cache keys, queues, search indexes,
object-storage paths, metrics, WebSocket topics, events, and background jobs.
Bulk jobs process one organization partition at a time. Normal request paths do
not use database-superuser credentials.

Encryption uses a deployment master-key hierarchy with a distinct derived data
encryption key per organization. This supports crypto-erasure and future
customer-managed keys without requiring a separate database for every tenant.

## Audit model

Vondel always records denials, administrative actions, policy changes, role
assignments, support access, ownership events, authentication failures,
adult-content access, exports, and secret access. Routine successful media
decisions may be sampled at a configurable rate. Ordinary viewing activity is
stored separately from the security audit log.

Decision records include a correlation ID, policy revision, actor, organization,
profile, action, scoped resource reference, reason code, authentication strength,
support state, and application node.

Adult and sensitive titles and paths are redacted unless the auditor is
separately entitled to that content. Audit storage is append-only and
partitioned, with retention tiers, signed export batches, and external
SIEM/webhook export. Application users, including owners, cannot edit audit
records.

## Client and API compatibility

### Silo-compatible `/api/v1`

The v1 surface preserves current Silo behavior:

- account login establishes a session in one organization;
- clients show the profiles currently available to that account;
- current profile switching remains unchanged;
- unlocked secondary profiles require no password;
- protected profiles use the existing PIN flow;
- switching changes viewing identity, history, preferences, and restrictions;
- only organization owners and Full Administrators project as legacy `admin`;
- delegated administrators project as ordinary users because Silo clients
  cannot represent partial authority; and
- legacy administrative endpoints accept only compatible owner/full-admin
  sessions under the existing primary-profile conditions.

Hidden organizations, profiles, libraries, adult resources, and capabilities
are omitted rather than returned as discoverable denied objects.

### Native Vondel `/api/v10`

The v10 surface exposes organizations, memberships, direct-profile login,
device enrollment, shared-device profile switching, admin mode, scoped roles,
capability documents, support sessions, policy simulation, audit correlation,
and stable denial reasons.

`/api/v10/capabilities` advertises supported feature and contract versions.
Individual contracts retain their own schema versions, allowing additive
evolution without changing the top-level API version.

Both API versions call the same underlying services and OPA decision system.
V1 is a compatibility projection, not a second authorization implementation.
Clients cannot gain authority by supplying a different role, organization,
membership, or profile identifier.

## Migration

An existing installation migrates into one default organization without
changing effective access:

- the original setup administrator becomes platform owner and organization
  owner;
- other administrators become Full Administrators;
- users become accounts plus memberships;
- profiles retain identifiers, history, restrictions, and PIN behavior;
- each user's current access-group assignment is copied to that user's profiles;
- current user-level restrictions remain a temporary membership ceiling until
  profile-level parity is proven;
- libraries, tuners, plugins, settings, sessions, and tokens attach to the
  default organization; and
- existing sessions remain compatible until normal expiry but are never
  upgraded to stronger authority.

Ambiguous ownership blocks migration and requires an explicit operator choice.
Rollback remains available until an explicit finalization step. Destructive
cleanup occurs only in a later release after parity and rollback gates pass.

## Error contract

Authorization failures return stable machine codes without disclosing hidden
resources. Core codes include:

- `authorization_denied`;
- `authorization_state_stale`;
- `authentication_step_up_required`;
- `admin_mfa_required`;
- `organization_suspended`;
- `account_suspended`;
- `resource_not_found_or_hidden`;
- `profile_verification_required`; and
- `policy_evaluation_failed`.

Externally visible status and message selection must not reveal whether a hidden
tenant, profile, library, adult item, or scene exists.

## Verification and release gates

The implementation is not complete until it passes:

- generated authorization-matrix tests for every capability, built-in role,
  scope, owner protection, profile state, and authentication strength;
- Rego unit and typed contract tests;
- legacy-versus-OPA behavioral parity tests;
- cross-tenant negative tests across APIs, repositories, RLS, caches, search,
  queues, WebSockets, artwork, subtitles, and object storage;
- adult-content non-disclosure and mixed-scene enforcement tests;
- property tests proving custom policy cannot widen vendor decisions;
- multi-node revocation and stale-snapshot tests;
- confused-deputy, IDOR, delegation escalation, session fixation, and cache
  poisoning tests;
- full Silo client contracts for login, profile switching, PINs, playback, and
  compatible administration;
- Vondel client contracts for organization switching, direct-profile login,
  capabilities, admin mode, and step-up;
- load tests for thousands of concurrent sessions and high-cardinality audit
  ingestion; and
- independent security review before production multi-organization hosting is
  enabled.

## Deferred

- Customer-managed organization encryption keys.
- Separate physical databases for selected organizations.
- A network policy-decision service; the embedded typed PDP seam must allow it
  later without changing callers.
- Cross-deployment federation.
- Organization-to-organization sharing. Shared resources remain platform-owned
  and explicitly entitled.
