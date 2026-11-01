# Bloem overlay: Invitations and server-driven onboarding

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to or replaces in Silo's
> [`docs/architecture/invitations-onboarding.md`](../../../architecture/invitations-onboarding.md),
> which stays exactly as Silo wrote it. The section-by-section changes are in the
> [For developers](#for-developers) section.

Silo's page explains how an invitation becomes an account and how a new person is
shown around the app. Everything there applies to Bloem: link invitations without
an address, Owner-only administrator invitations, recorded delivery and the
onboarding tour. Bloem changes only what organizations and profile sign-ins touch.

![Two ways to invite someone: Silo's administrator routes in the default organization, which can email the link, and the organization console in its own organization, which sends no email and shows a one-time link. Both keep one live invitation per address in each organization](../../../architecture/images/overlay-invitations/two-paths.svg)

## What Bloem changes

- **Every invitation belongs to one organization.** Silo's administrator routes
  work in the **default organization**. An **organization administrator** invites
  people into their own organization from the organization console, without email.
- **One live invitation per address, in each organization.** Sending a new
  invitation to an address replaces the old link for that organization, so a
  forwarded copy of the old link stops working.
- **The organization must be active.** Accepting an invitation into an
  organization that is not active is refused.
- **A profile's own sign-in is not a second account.** Someone who needs their own
  password still gets a second invitation and a second account.

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Which organization an invitation belongs to | `invitations.organization_id` |
| One live invitation per address in each organization | partial unique index `invitations_one_pending_idx` on `(organization_id, email)` |
| The organization console's routes | `/api/bloem/v1/admin/organization/invitations` |
| The one-time link | `claim_token` |
| A profile store that cannot join the transaction | `ErrTransactionalProfileUnavailable` |

## For developers

### Where this overlay came from

Bloem added organization-scoped invitations and the organization console's
invitation routes in August 2026 (for example `463afb680`, 2026-08-13, which made
the one-pending-invitation index per organization). Bloem's text moved out of the
Silo file into this overlay in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/invitations`, `internal/auth/bloem_account_provisioner.go`,
`internal/api/handlers/bloem_admin_organization.go`). Since this overlay was
written (against Silo `8e5ae2f46`), Silo changed the document twice:
`be15e8b0e` (#1523, 2026-09-26) lets only the server Owner create or resend an
`admin` invitation, and `4a0cff41f` (#1788, 2026-10-02) adds link invitations,
which have no address until accept, and records delivery. Both apply to Bloem.
The earlier edition of this overlay replaced whole Silo sections with
September text that predated those changes; it now records only Bloem's real
differences. Silo has not changed the document again through `63ef34b23`.

### Section by section

---

**Location:** section “Invitations and server-driven onboarding”, after the
paragraph beginning “Two complementary entry paths coexist…”. **Bloem adds:**

Every invitation belongs to one organization. Silo's administrator invitation
routes, on `/api/v1` and `/api/v2`, list, create, resend, revoke and delete only
the default organization's invitations. Bloem's organization console has its
own routes under `/api/bloem/v1/admin/organization/invitations`. They create
email-bound invitations with role `user` only, in the console's organization.
That flow never sends mail: the create and resend responses return the claim
token once and the administrator delivers the link. Those rows are stored with
`delivery` NULL, the value Silo otherwise reserves for rows that predate the
column. See [organization invitation links](../invitations-api.md#bloem-organization-invitation-links).

---

**Location:** section “Token model”, after the paragraph beginning “Token
handling mirrors the email-verification flow…”. **Bloem adds:**

The raw token also exists in the one-time `claim_token` that the organization
console's create and resend routes return, and nowhere else.

---

**Location:** section “Lifecycle invariants”, the bullet “**One live invitation
per address.**”. **Bloem replaces** it with:

- **One live invitation per address in each organization.** Bloem's partial
  unique index `invitations_one_pending_idx` is on `(organization_id, email)`
  where neither accepted nor revoked (migration `20260813180000`).
  `Repository.Create` revokes any live invitation for the address in the same
  organization, in the same transaction, so re-invite and resend *supersede*: a
  forwarded copy of the old link stops working. An address that already has an
  account anywhere on the server is still refused at send.

**Location:** section “Lifecycle invariants”, the bullet “**Profile storage must
support the transaction.**”. **Bloem replaces** it with:

- **Profile storage must support the transaction.** A required default profile
  uses the PostgreSQL provider's transaction capability, preserved through the
  notification wrapper. A SQLite profile store cannot join that transaction.
  Acceptance with `create_profile=true` fails with
  `ErrTransactionalProfileUnavailable` before the account and its organization
  membership are inserted, so it also creates no SQLite files.
  `create_profile=false` remains supported. An invitation that names an
  organization also requires that organization to be active at accept. This
  restriction applies to emailed and link invitations; ordinary signup
  invite-code behavior is unchanged.

---

**Location:** section “Accounts vs household profiles”, the sentence beginning
“A spouse who needs their own password…”. **Bloem replaces** it with:

Someone needing a separate login account receives a second invitation, which
creates a second account. Bloem's optional direct-profile credentials stay
owned by the account and only work on their supported routes. They do not
create another account or enable browser direct-profile login; see the
[authentication overlay](../auth-api.md).

### Related pages

- [Invitation API overlay](../invitations-api.md): the scope of Silo's routes on
  Bloem, the organization console's routes, and email links on organization
  domains.
- [How organizations work](../../../architecture/bloem-organizations.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_invitations.py` into
`docs/architecture/images/overlay-invitations/`. Redraw it there, not by hand.
