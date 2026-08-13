# Vondel Resource Tenancy Design

**Date:** 2026-08-13

**Status:** Approved

**Depends on:** `2026-08-12-vondel-tenant-identity-foundation.md`

**Source architecture:** `2026-08-12-vondel-multitenant-security-and-authorization-design.md`

## Objective

Make ownership and organization visibility explicit for every server resource
before Vondel enables direct-profile login, delegated administration, or
multi-organization resource management. The populated Vondel catalog becomes
platform-owned and is granted to the default organization through stored,
auditable entitlements. Existing Silo-compatible `/api/v1` behavior remains
unchanged.

This phase builds and proves the isolation substrate. It does not enable
organization-private library creation.

## Decisions

- Use typed ownership columns and root-resource inheritance rather than a
  polymorphic ownership table or per-organization database.
- A resource owner is either the platform or exactly one organization.
- Organizations never share resources directly with one another.
- Existing libraries, media, plugins, and future tuner resources migrate to
  platform ownership.
- The default organization receives explicit entitlements materialized from a
  configurable default bundle.
- Entitlements attach to root resources. Derived resources inherit ownership
  and visibility from their authoritative root.
- PostgreSQL constraints and repository filtering enforce isolation before OPA.
  OPA may narrow access but cannot grant missing ownership or entitlement.
- RLS activates only after repository-to-policy parity is executable and green.
- Legacy v1 requests resolve through the default organization and the same
  tenant-aware repositories used by v10.

## Non-Goals

- Organization-private library creation or management.
- Direct-profile credentials, shared-device pairing, or new session modes.
- Delegated administrative roles or new authorization capabilities.
- Cross-organization resource sharing.
- A separate database or schema per organization.
- Enabling Live TV. This phase provides ownership types that the later Live TV
  subsystem uses for tuner groups, lineups, DVR rules, and recordings.
- Exposing a new multi-organization UI.

## Ownership Model

`resource_owners` is the canonical ownership record:

- stable UUID primary key;
- `kind`, constrained to `platform` or `organization`;
- nullable `organization_id`;
- timestamps and revision metadata; and
- constraints requiring `organization_id` exactly when `kind` is
  `organization`.

The platform owner is a protected singleton. An organization owner has a
one-to-one relationship with its organization. Owner records are referenced by
typed root resources, not selected through a free-form resource-type string.

Initial root resources are:

- media libraries/folders;
- plugin installations and organization-visible plugin availability; and
- entitlement bundles.

The ownership types must also support the later addition of:

- tuner groups and channel lineups;
- DVR rules and recordings; and
- other platform roots approved by a later design.

Root tables carry a non-null `owner_id`. Organization-owned roots additionally
carry or derive the exact organization identity needed by composite foreign
keys and RLS. Platform-owned roots are visible only through an active
entitlement.

## Root Inheritance

Derived records do not receive independent sharing semantics. They inherit
from an authoritative root:

- media items, files, seasons, episodes, provider identifiers, literary works,
  editions, tracks, and library membership inherit from a media library;
- scanner and matcher work inherits from the queued library/root;
- search documents inherit from the indexed library/root;
- artwork, thumbnails, image-cache entries, and generated derivatives inherit
  from the media root that produced them;
- playback plans, downloads, events, notifications, and audit facts reference
  the authoritative media root; and
- plugin configuration and runtime bindings inherit from the installation or
  availability root.

Where a derived row can legitimately relate to multiple libraries, the
library-membership relation is the visibility boundary. The item itself does
not become visible merely because another organization can see a different
library containing an equivalent title.

Materialized owner or organization columns are allowed on hot or asynchronous
paths when they are protected by composite foreign keys, triggers, or verified
write helpers that prevent divergence from the root. They are never accepted
as unverified caller authority.

## Entitlements and Default Bundles

`organization_entitlements` links one organization to one platform-owned root
resource. Each row contains:

- organization and owner/root identity;
- entitlement kind and root identifier;
- active, suspended, or revoked status;
- source bundle and bundle revision when materialized from a bundle;
- security revision and audit timestamps; and
- the actor or service responsible for the change.

Uniqueness prevents duplicate live entitlements for the same organization and
root. An entitlement cannot target an organization-owned root.

`entitlement_bundles` and versioned bundle members define reusable platform
defaults. One active bundle may be marked as the organization-creation default.
Creating an organization materializes that bundle into explicit entitlement
rows in the same transaction as initial membership provisioning. Later bundle
edits do not silently rewrite existing organizations. Applying or reconciling
a newer bundle is an explicit, audited operation.

During migration, the default organization receives entitlements to every
existing platform root. This preserves the populated server's current visible
catalog without relying on an implicit global-access exception.

## Request Enforcement

The enforcement order is fixed:

1. Authenticate the account or service identity.
2. Resolve and validate the organization membership.
3. Require exact owner equality for an organization-owned resource, or an
   active organization entitlement for a platform-owned resource.
4. Apply the active profile and its access-group restrictions for consumption.
5. Apply scoped administrative authority when administration is requested.
6. Evaluate OPA, which may narrow the decision but cannot create ownership,
   entitlement, or administrative authority.

Tenant context is passed explicitly into stores and services. There is no
process-global or package-global current tenant. A repository API that reads or
writes tenant-visible data accepts a typed tenant/service scope or is private to
an already-scoped unit.

Background workers use explicit service identities plus root scope. An
unscoped worker cannot claim or mutate tenant-visible work. Queue rows carry
their authoritative owner/root identity and are revalidated when claimed.

Unauthorized, unentitled, missing, and cross-organization resources use the
same external not-found shape and comparable execution path. Logs and audit
records may retain a protected internal reason, but client responses cannot be
used for tenant or resource enumeration.

## Data-Plane Isolation

Every data plane includes owner/root identity:

- repository predicates and write constraints;
- queue uniqueness and claim predicates;
- search index document keys and filters;
- cache namespaces and invalidation keys;
- artwork and generated-object keys;
- event routing, notification payload construction, and WebSocket fan-out;
- playback, download, and signed URL issuance; and
- plugin configuration, runtime dispatch, and secrets.

Object-storage keys begin with a stable owner/root namespace. Existing objects
are mapped or copied through a resumable migration ledger before the old key is
retired. A database ownership update never points at an unverified object.

No list, count, search, recommendation, history, activity, image, event, or
timing response may reveal a root that the organization cannot access.

## Compatibility

Legacy `/api/v1` account login, profile selection, PIN handling, refresh,
catalog, playback, and administration keep their existing wire shapes. A v1
session resolves to the default organization through the same tenant resolver
and repositories as v10. The default organization's explicit entitlements make
current platform resources visible.

There is no legacy bypass in repositories and no second enforcer. Compatibility
tests compare behavior before and after ownership backfill, including response
fields, ordering, counts, error shapes, and profile restrictions.

Capability discovery does not advertise organization resource management.
Resource-tenancy capability flags remain dark until the full isolation matrix
is green and reviewed.

## Migration Strategy

The schema rollout follows expand, backfill, verify, constrain, then contract:

1. Add owner, bundle, entitlement, and migration-ledger tables.
2. Add nullable ownership/root columns and supporting indexes.
3. Create the protected platform owner and the active default bundle.
4. Backfill existing root resources as platform-owned in deterministic batches.
5. Materialize default-organization entitlements for every existing root.
6. Backfill derived ownership/root identity and object-key mappings.
7. Verify complete coverage, referential consistency, uniqueness, and unchanged
   default-organization visibility.
8. Add non-null, check, unique, and composite foreign-key constraints.
9. Switch all repositories and workers to required typed scopes.
10. Compare repository filtering with candidate RLS policies.
11. Enable RLS only after parity is exact.
12. Remove compatibility defaults and obsolete unscoped write paths only after
    all callers have migrated.

Backfills are resumable, idempotent, observable, and safe while the server is
running unless a specific constraint transition requires a bounded maintenance
window.

Before tenant-specific resource writes exist, rollback returns to the immediate
pre-resource-tenancy migration. After such writes are enabled, rollback is an
application/image rollback plus restoration from the named database snapshot;
it never discards ownership data to force an older schema.

## Error and Failure Behavior

- Missing owner or root identity fails closed.
- Unknown, suspended, or revoked entitlements fail closed.
- A platform root without any entitlement remains invisible to organizations.
- A default-bundle materialization failure rolls back organization creation.
- Cross-owner foreign-key attempts fail atomically.
- A stale queue claim is rejected when ownership or entitlement revision has
  changed.
- Search, cache, event, or object records with inconsistent scope are excluded,
  quarantined, and surfaced through protected diagnostics.
- RLS or repository scope failure returns a stable unavailable/denied contract;
  it never retries through an unscoped query.
- Control-plane unavailability does not widen access. Existing consumption may
  use only a previously verified snapshot under the separately defined stale
  policy; new grants and administrative writes fail closed.

## Verification

### Migration and database invariants

- empty installation and populated upgrade;
- complete platform-owner backfill;
- exact default-organization entitlement coverage;
- bundle creation and concurrent organization materialization;
- idempotent resume after interrupted batches;
- cross-owner composite foreign-key rejection;
- immediate predecessor down/up migration before tenant-specific writes; and
- snapshot restore procedure after the contract boundary.

### Repository and service matrix

Every tenant-visible read, write, list, count, search, and delete path runs with:

- entitled active organization;
- unentitled organization;
- wrong organization;
- suspended membership or organization;
- platform service identity with explicit root scope;
- unscoped service identity; and
- legacy v1/default-organization context.

The matrix covers libraries, catalog, scanner, matcher, queues, search,
recommendations, history, artwork/cache, playback, downloads, events,
notifications, plugins, and object keys.

### RLS parity

Before activation, executable tests run representative repository operations
with application predicates and candidate RLS policies and require identical
allow/deny sets. RLS tests prove transaction-local context is reset between
pooled connections and that missing context exposes no tenant rows.

### Populated Vondel acceptance

1. Snapshot the database and application LXCs.
2. Record counts and visibility for users, profiles, libraries, media, plugin
   installations, queues, search, and stored objects.
3. Upgrade and verify every current root is platform-owned and entitled to the
   default organization.
4. Exercise catalog, search, playback, download, scan, rescan, artwork, events,
   and plugin execution through existing v1 and tenant-aware paths.
5. Create an unentitled organization and prove it receives no resource names,
   counts, identifiers, artwork, events, timing oracle, or object access.
6. Re-run scans and workers and prove stable IDs, counts, and ownership.
7. Verify replica health and rollback artifacts.

### Scale and security gates

- thousands of memberships and organizations;
- concurrent entitlement checks and bundle materialization;
- bounded query counts and reviewed query plans on catalog/search hot paths;
- cache and event isolation under concurrent organizations;
- no organization identifier or protected resource in unauthorized output; and
- independent security-focused review before capability activation.

## Delivery Slices

1. Ownership and entitlement schema plus populated backfill.
2. Tenant-aware library/catalog repositories and v1 parity.
3. Scanner, matcher, queues, and plugin scope.
4. Search, recommendations, history, caches, artwork, events, and object keys.
5. Playback, downloads, signed URLs, and asynchronous revocation behavior.
6. RLS parity, activation, and removal of unscoped paths.
7. Populated-system acceptance, load testing, and independent review.

Each slice is independently reviewed, uses real PostgreSQL, preserves upstream
mergeability, and leaves `main` deployable.

## Completion Criteria

Resource tenancy is complete when:

- every existing root is platform-owned;
- the default organization has explicit entitlement to the current catalog;
- every derived row and data-plane artifact has a verified root scope;
- no unscoped repository or worker path can access tenant-visible data;
- cross-owner relationships are rejected by PostgreSQL;
- repository and RLS decisions match exactly;
- v1 compatibility and populated-system behavior are preserved;
- an unentitled organization learns nothing about platform resources;
- scale gates pass for thousands of organizations and memberships; and
- organization-private creation remains disabled pending its later reviewed
  enablement.
