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

## Silo client compatibility

Silo clients are a supported caller against a multi-tenant deployment. They have
no concept of organizations: they authenticate as an account and ask
account-shaped questions. Two facts about them drive the design.

**They send `X-Profile-Id` conditionally.** silo-android guards it with
`activeProfileId?.let`, silo-apple with `if let profileId`, and the web client
with `if (profileId && ...)`. So a client browses profile-less until the viewer
picks a profile, and callers with no profile concept at all -- API keys, the
audiobookshelf surface -- never send it.

**They need an answer every time.** An empty account-level answer reads to a
client as "no entitlements", which is indistinguishable from a locked-out user.

The resulting contract, pinned by
`TestSiloClientSubjectResolutionMatrix` in `internal/tenancy`:

| Account | Without a profile | With a profile |
| --- | --- | --- |
| Tenant member (one organization) | its own tenant | the same tenant |
| Default-organization account | the default organization | the profile's organization |
| Member of several organizations | the default organization, else its earliest membership | the profile's organization |
| No membership at all | the pre-tenancy default-organization answer | n/a |

Two invariants hold this together:

- **Picking a profile must not move an account between organizations.** A client
  that browses before selection and then selects must see one tenant, not two.
- **One notion of "the account's organization".** `Store.AccountOrganization` and
  the account policy projection in `auth.userSource` use the same precedence --
  the default organization when the account belongs to it, otherwise its earliest
  membership. Two independent rules would eventually disagree, and the disagreement
  would surface as a client seeing entitlements from one organization and content
  from another.

The ambiguity that remains is confined to an account holding memberships in
several organizations, which is an administrator. A Silo client cannot express
which organization it means, so it gets the deterministic choice above;
administrators who need a specific organization use the admin surfaces, which are
organization-scoped throughout. This under-serves rather than over-serves --
`catalog.AccessFilter` carries a resolved library list rather than an
organization, so a client sees one organization's libraries and never a union
across them.

## Access group deletion

Deleting an access group reassigns its members to the organization's default
group and reports the count, rather than detaching them. The
`(organization_id, access_group_id)` foreign key from
`organization_memberships` to `access_groups` is `ON DELETE RESTRICT`, so a
group that still holds members cannot be dropped: the restriction is the proof
that the reassignment covered everything, not a workflow the operator is
expected to hit.

This is a deliberate divergence from upstream Silo, which declares
`users.access_group_id REFERENCES access_groups(id) ON DELETE SET NULL`.
There, deleting a group succeeds and silently detaches its members to no group
at all, which `GetPolicyForUser` reads as "no access-group restrictions" — so
removing a group quietly widens what its former members can reach. An
administrative delete must never be a privilege grant, which is why the
reassign-to-default behaviour is kept even though it costs a divergence from
upstream. Do not resolve a future upstream conflict here by taking Silo's
`SET NULL`.

Known gap: `GroupStore.DeleteWithImpact` reassigns `user_profiles`, but
`20260829085838_membership_policy_isolation` backfilled
`organization_memberships.access_group_id` as a second holder of the same
reference, and that column is frozen until the membership policy authority
reaches its `finalized` phase. The membership half of the reassignment
therefore has to land together with the authority handoff, not before it.

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
