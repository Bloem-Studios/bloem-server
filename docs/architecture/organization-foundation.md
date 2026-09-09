# Organization ownership and viewer library boundaries

This foundation extends Silo's existing account and library model. It does not yet
qualify the server for multi-organization hosting: tenant administration, delivery
revocation, background work, alternate listeners, and production migration still
require their own isolation checks.

## Ownership

An account has one required organization. Profiles inherit ownership through their
account. Organization membership is not selectable on a request and no transfer
operation is exposed. Global username and email uniqueness are retained.

The default organization preserves ordinary single-organization provisioning.
Initial platform administrators use the separate platform organization. The existing
server-wide `users.role` retains its meaning; `organization_role` does not grant
platform authority. Organization administration is not exposed by this foundation.

A media folder with no organization owner is a platform library. An owned folder
is private to that organization. `organization_library_grants` grants access to
platform libraries only. Several organizations can use the same platform library;
this does not duplicate its files or catalog entries. A granted library cannot be
changed to private ownership until its grants are removed.

Access groups belong to an organization. Group names and the default-group slot are
unique within that organization. Composite foreign keys prevent accounts and emailed
invitations from referencing another organization's group. Deleting a group preserves
the account's organization while clearing the group reference.

## Viewer enforcement

The main router wraps Silo's existing viewer resolver with an organization boundary:

    effective libraries = Silo's resolved libraries intersect organization libraries
    organization libraries = owned libraries union granted platform libraries

An unrestricted Silo result means all libraries within the organization boundary.
An empty organization library set means no access. The wrapper preserves Silo's
profile, rating, quality, and PIN decisions; it does not modify the policy engine.
The final intersection prevents a custom policy from widening the organization set.
Disabled-library references are also bounded to that organization.

Organization identity comes from the account record. Missing or suspended organizations
cannot resolve a viewer scope. Library authority is read from PostgreSQL rather than
cached as an organization token claim. Grant mutations advance the organization's
access revision. The resolved scope includes organization identity and revision.

Progress snapshot transactions apply the same intersection using their existing
transaction. The underlying Silo resolver remains available for the transaction's
profile and policy evaluation. This is not proof that every delivery or background
path enforces the boundary; that audit precedes tenant hosting.

## Native media delivery

Direct streams, subtitle files, and subtitle font requests check the selected
source file against the current viewer library scope before reading bytes or
subtitle inventory. HLS checks the source before serving or proxying a manifest
or segment. Signed restart recipes are checked before recreating a session.
Owning a session does not preserve a revoked library grant. Managed-download
subtitles also check their source file, including downloaded subtitle rows whose
file identity is validated by the existing delivery service.

These guards reuse the existing library filter and leave playback quality
selection at admission: a lower-resolution transcode must not be rejected merely
because its source has a higher resolution. Native API routes and client payloads
remain unchanged; neither Apple nor Android needs a wire-format change.

V2 delivery regression tests exercise distinct files for the same catalog title,
private and shared library scopes, grant removal between requests, and live and
reconstructed sessions. They use a controlled resolved scope; the organization
repository and resolver have separate database-backed tests. These checks do not
interrupt a response already in flight or complete the background-job and
alternate-listener audit.

The standalone proxy checks the signed source file against the enabled account's
active organization and current library ownership or shared-library grant on each
media request. This covers signed recipes, media grants, and download URLs alongside
their existing credential checks. Inaccessible sources return 404; an unavailable
authority lookup returns 503. Existing route-specific error encodings are retained.
Raw transcode-worker media routes remain protected by the shared node secret;
regression tests verify that viewer access tokens and signed media tokens cannot
authenticate directly to those routes.

## Shared catalog projections

Catalog details and versions retain Silo's existing library-based file filtering.
Regression tests alternate organizations' library scopes against the same detail
service and verify that movie, episode, and extra file metadata stays within the
selected libraries. Audiobook-list cache tests also cover distinct library ceilings
and an empty ceiling after revocation. These tests preserve the existing production
catalog implementation; they do not establish isolation for custom shared metadata
or artwork editing.

## Realtime and background scope resolution

V2 event and playback-control sockets use Silo's existing authority validator.
Its fingerprint includes the complete resolved scope, including organization
identity and access revision. Both sockets revalidate every 15 seconds with a
2-second lookup timeout; this is periodic revocation, not immediate delivery
cancellation. A websocket regression test exercises the organization wrapper
with grant removal and suspension after connection, without changing the protocol.

Notification interest indexing and request-reconciliation entitlement evaluation
use the same organization wrapper as HTTP requests. Notification fanout also
checks current organization activity and library ownership/grants in its recipient
query: a stale interest row cannot authorize another organization's library or a
revoked shared library. Existing preferences, fanout transactions, and delivery
identities retain their upstream behavior.

Queued notification delivery lookups also require an active organization and,
for library-bound notices, current library ownership or a shared-library grant.
Webhook, web push, and mobile push senders already reload deliveries through this
lookup; their existing missing-row handling ends an inaccessible attempt. Email
and Discord digest reads and pending-work checks apply the same predicate.
Account-level notices without a library remain eligible for active organizations.
Database errors retain the existing retry behavior. This check precedes sending;
it cannot recall a message already sent or cancel a send already in progress.

Download artifacts are shared preparation jobs, not account-owned delivery
permissions. The existing byte-delivery checks remain necessary after preparation;
revoking one account's access must not indiscriminately cancel an artifact needed
by another authorized account. Request routing/fulfillment, shared metadata isolation,
and remaining alternate listeners still require review before hosted deployment.

## Invitations

Invite codes and emailed invitations store their destination organization. Code
redemption derives the account destination from the locked code and permits ordinary
accounts only. Emailed invitation acceptance carries the stored organization into
existing transactional account provisioning. A composite reference prevents an
invitation from recording an accepted account in another organization.

Resending preserves the locked invitation's organization. Superseding a pending
invitation for an email is scoped to the destination organization. Named invite-code
creation includes organization ownership in its retry conflict check.

## Compatibility and migration limits

Existing API routes, operation IDs, playback contracts, and web UI are unchanged.
No Jellyfin, Audiobookshelf, or IPTV additions are imported from Bloem. Existing
upstream compatibility surfaces have not been certified for hosted isolation.

The schema migrations backfill an upstream Silo database. They are not an automatic
upgrade path from the previous Bloem fork. A restored-backup migration rehearsal is
required before any production cutover. Down migrations refuse to discard additional
tenant ownership where that would erase its meaning; backup restoration is the
production rollback mechanism.
