# Multitenant administration

> **Provenance.** Bloem addition: Silo is single-tenant, and organizations
> (`internal/tenancy`), memberships and the administrative context are Bloem's.
> The UI isolation tests and this page date from `0e6f7f03d` (2026-08-13); the Silo
> client compatibility matrix and the access-group deletion ruling from 2026-08-30
> (`e566633db`, `c3b815996`, `4f5a8af62`). Later changes that matter: the v1 sockets
> moved onto Silo's tickets (`a7436b4e5`, 2026-10-25); account policy moved back onto
> Silo's `users` table and each account got at most one membership (`c60b7008e`,
> 2026-10-26); Silo's token binding (#3140, merged in `68070f04b`, 2026-10-27) now also
> checks the administrative context's login session against its account.
> **Status:** current at `e852f92bf`.

Bloem has one administration shell with two explicit authority contexts. A
Platform context manages organizations and platform-wide operations. An
Organization context manages only the selected tenant's people, profiles,
groups, libraries, entitlements, invitations, and policy-decision evidence.
Navigation never mixes those scopes.

## Session and authority boundary

The Silo-compatible account session remains the authentication root. An
authenticated caller exchanges it at `POST /api/bloem/v1/admin/session` for a signed
administrative context token lasting no more than 15 minutes. The token contains
either Platform authority or one exact organization membership plus current
policy and security revisions. Organization authority never comes from a path,
query, request body, or caller-selected membership ID.

The token is bound to the login session it was exchanged from, so a
credential without a login session (an API key) cannot open a context. Since
`68070f04b` the context middleware also requires that login session to belong to the
account the token names (Silo's `ActiveSession` account binding).

Every request revalidates durable authority. A revoked or expired login
session, suspended organization or membership, disabled or replaced account
(a different account incarnation), ownership change, or revision mismatch
therefore rejects an already-minted context. Foreign IDs receive non-disclosing responses.
The web client keeps the token in memory, qualifies cache keys with `platform`
or `organization:<uuid>`, removes the old context's queries before switching,
and persists only the non-secret context key.

## Websocket credentials

The v1 bridge sockets use Silo's handshake unchanged: the socket authenticates
with the ordinary login credential, and `POST /api/v1/events/ws-ticket`
(profile required) binds only the profile. Bloem adds no v1 ticket routes.

The v2 events and playback-control sockets use Silo's socket tickets with a
Bloem adapter (`internal/api/handlers/bloem_socket_authority.go`) on Silo's
`Validate` hook. Minting binds the caller's organization, membership and
revisions to the ticket server-side, and the handshake and periodic checks
revalidate that same tenant instead of resolving one from a bare
account/profile. An unbound ticket fails closed.

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

The embedded organization console provides three further bounded workflows:

- **Library grants:** suspend, restore or withdraw an existing grant after confirmation.
  `expected_revision` is the grant's security revision, not the organization's policy
  revision. Withdrawal cannot be undone from this screen; it does not create new grants.
- **Invitations:** create user invitations, regenerate a pending/expired link or revoke
  it with the selected organization's policy revision. Creation/regeneration returns a
  one-time handoff for manual delivery and sends no email. Foreign or admin invitations
  are outside this surface. Handoffs never enter query/mutation caches or persistent storage.
- **Activity:** explicitly load 50-event cursor pages from the selected organization's
  lifecycle and entitlement ledgers. Responses contain redacted summaries, without raw
  before/after documents or platform-wide events. Only recorded events appear; the page
  does not imply complete auditing of every operation.

Drafts and confirmations capture administrative-context generation and the applicable
revision. An expired or replaced context cannot lend new credentials to an old intent.
The browser reloads after attempted writes and does not replay uncertain mutations.
Platform campaign/seasonal authoring and account-session household credential management
have separate authorities. See the [native workflow contract](../bloem-api-reference.md#embedded-web-workflow-adapters)
and [web coverage](bloem-web-feature-coverage.md).

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
| Member of an organization (a tenant or the default organization) | its organization | the same organization |
| No membership at all | the pre-tenancy default-organization answer | n/a |

Before `c60b7008e` (2026-10-26) an account could hold memberships in several
organizations, and the matrix carried rows for that case. The constraint
`organization_memberships_one_per_account` now allows one.

Two invariants hold this together:

- **Picking a profile must not move an account between organizations.** A client
  that browses before selection and then selects must see one tenant, not two.
- **One notion of "the account's organization".** `Store.AccountOrganization`
  answers with the account's one active membership, and the account's policy lives
  on Silo's `users` row, so entitlements and content come from the same
  organization. (Until `c60b7008e` a separate account-policy projection had to apply
  the same precedence rule; two independent rules would eventually have disagreed.)

`catalog.AccessFilter` carries a resolved library list rather than an organization,
so a client sees one organization's libraries and never a union across them.

### Keeping it that way

Two guards hold the compatibility promise, and they are deliberately different
mechanisms.

`TestV1RouteSurfaceWithADatabaseIsUnchanged` compares the whole surface against a
golden file and is regenerated whenever the surface changes. It is good at
showing a diff. It is not a guarantee: an addition and a removal look alike in
it, and regenerating absorbs both.

`TestV1SiloRouteContractIsNeverNarrowed` is the guarantee. It asserts every route
in `testdata/v1_routes_silo_contract.txt` is still served, and that file only
shrinks when someone deletes a line. Adding routes needs no change to it, because
an addition cannot break a client that never calls it; removing one is a visible,
reviewable act with a removals entry to write in
[v1-scope.md](v1-scope.md).

Behaviour needs its own guard, because the route surface cannot see it. A client
broke once during this work with no route change at all: it browsed before the
viewer picked a profile, and the profile-less subject resolved through the
deployment default organization rather than the account's own tenant, so a
tenant's end user was told its tenant did not exist. That class of regression is
what `TestSiloClientSubjectResolutionMatrix` above exists to catch.

## Access group deletion

Deleting an access group reassigns its profiles (`user_profiles`) and its
organization's accounts (`users.access_group_id`) to the organization's default group
in one transaction, reports the number of profiles moved, and then deletes the group
(`TenantGroupStore.DeleteWithImpact`, `internal/access/bloem_group_mutation.go`). The
default group itself cannot be deleted. Profiles reference their group through
`user_profiles_organization_access_group_fkey` on `(organization_id, access_group_id)`,
which has no `ON DELETE` action, so a group that still holds a profile cannot be
dropped: the constraint is the proof that the reassignment covered every profile, not
a workflow the operator is expected to hit. Until `c60b7008e` (2026-10-26) a
membership column, `organization_memberships.access_group_id`, had an
`ON DELETE RESTRICT` key that played the same role for accounts; that column was
dropped when account policy returned to `users`.

This is a deliberate divergence from upstream Silo, which declares
`users.access_group_id REFERENCES access_groups(id) ON DELETE SET NULL` (that key
is still present in Bloem; the explicit reassignment runs first). In Silo, deleting
a group succeeds and silently detaches its members to no group at all, which `GetPolicyForUser` reads as "no access-group restrictions" — so
removing a group quietly widens what its former members can reach. An
administrative delete must never be a privilege grant, which is why the
reassign-to-default behaviour is kept even though it costs a divergence from
upstream. Do not resolve a future upstream conflict here by taking Silo's
`SET NULL`.

## Silo switching

An account's policy, including its access group, lives on `users`, as in Silo;
its one membership links it to its organization. A database shared with
upstream Silo has `public.bloem_silo_switching` enabled and is limited to a
single organization. See [Silo backend switching](silo-backend-switching.md).

## Compatibility and audit

The Silo bridge keeps its recorded contracts and
[reviewed Bloem exceptions](v1-scope.md), using the account/profile projection
described above. Legacy clients retain account login and profile switching; they
do not receive administrative context claims.

Lifecycle, membership, ownership, group, entitlement, invitation, and people
mutations record actor, authority context, target organization, revision change,
outcome, and request correlation. Tokens, credentials, invitation secrets, and
sensitive policy inputs are excluded or redacted.

## Release evidence

`TestMultitenantAdminTwoOrganizationIsolation` creates a disposable PostgreSQL
database, two organizations, each with its own administrator (an account belongs to
one organization), organization-only people,
profiles, groups, owned and entitled libraries, and decisions. It mints both
contexts, proves every projection is tenant-bounded, suspends one membership,
proves its existing context is revoked, and proves the other remains usable.
Cleanup closes connections, drops the child database, and verifies its absence.

The web release test locks keyboard context switching, live announcements,
semantic people headers and controls, bulk counts, the full-height narrow detail
sheet, destructive confirmation copy, and stable desktop/narrow DOM snapshots
for Organizations, Organization Overview, People, and Access Groups.
