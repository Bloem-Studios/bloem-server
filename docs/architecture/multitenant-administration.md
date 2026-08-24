# Multitenant administration

Bloem has one administration shell with two explicit authority contexts. A
Platform context manages organizations and platform-wide operations. An
Organization context manages only the selected tenant's people, profiles,
groups, libraries, entitlements, invitations, and policy-decision evidence.
Navigation never mixes those scopes.

## Session and authority boundary

The Silo-compatible account session remains the authentication root. An
authenticated caller exchanges it at `POST /api/v2/admin/session` for a signed
administrative context token lasting no more than 15 minutes. The token contains
either Platform authority or one exact organization membership plus current
policy and security revisions. Organization authority never comes from a path,
query, request body, or caller-selected membership ID.

Every request revalidates durable authority. A suspended organization or
membership, disabled account, ownership change, or revision mismatch therefore
rejects an already-minted context. Foreign IDs receive non-disclosing responses.
The web client keeps the token in memory, qualifies cache keys with `platform`
or `organization:<uuid>`, removes the old context's queries before switching,
and persists only the non-secret context key.

## Administrative controls

Platform administrators can create, rename, suspend, and reactivate
organizations; manage memberships; and transfer ownership after password
reauthentication. Suspension is the reversible lifecycle control. There is no
hard organization deletion.

The first release has one broad `organization_admin` role. Delegated roles are
absent. Organization administrators use structured controls and cannot upload,
edit, or activate Rego. Effective media access is always the intersection of
the tenant's owned or entitled library ceiling and narrower account, group, and
profile policy.

People lists use server filtering, cursor pagination, and immutable, expiring,
organization-bound selections. Bulk work is durable, bounded, audited, and
reports exact successes, skips, and failures. Destructive confirmations name
the organization and affected count.

## Compatibility and audit

All `/api/v1` Silo contracts remain unchanged and resolve the default
organization. Legacy clients retain account login and profile switching; they
do not receive administrative context claims.

Lifecycle, membership, ownership, group, entitlement, invitation, and people
mutations record actor, authority context, target organization, revision change,
outcome, and request correlation. Tokens, credentials, invitation secrets, and
sensitive policy inputs are excluded or redacted.

## Release evidence

`TestMultitenantAdminTwoOrganizationIsolation` creates a disposable PostgreSQL
database, two organizations, a shared administrator, organization-only people,
profiles, groups, owned and entitled libraries, and decisions. It mints both
contexts, proves every projection is tenant-bounded, suspends one membership,
proves its existing context is revoked, and proves the other remains usable.
Cleanup closes connections, drops the child database, and verifies its absence.

The web release test locks keyboard context switching, live announcements,
semantic people headers and controls, bulk counts, the full-height narrow detail
sheet, destructive confirmation copy, and stable desktop/narrow DOM snapshots
for Organizations, Organization Overview, People, and Access Groups.
