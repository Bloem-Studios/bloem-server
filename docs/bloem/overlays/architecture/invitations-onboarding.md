# Bloem overlay: Invitations and server-driven onboarding

**What this overlays.** Silo's
[`docs/architecture/invitations-onboarding.md`](../../../architecture/invitations-onboarding.md).
It adds to the opening section and the sections “Token model” and “Lifecycle
invariants”, and it replaces two bullets of “Lifecycle invariants” and one
paragraph of “Accounts vs household profiles”. Everything else in the Silo
document applies to Bloem as written. That includes link invitations without an
address, Owner-only `admin` invitations, recorded delivery and the onboarding
tour. An *overlay* is a Bloem-owned page that records Bloem's additions to, or
replacements of, sections of a Silo-owned document. The Silo file stays exactly
as Silo wrote it. See the [documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem is multi-tenant: every account belongs to an
*organization* (a tenant), and each invitation carries an `organization_id`.
Bloem added organization-scoped invitations and the organization console's
invitation routes in August 2026 (for example `463afb680`, 2026-08-13, which made
the one-pending-invitation index per organization). Bloem also has optional
direct-profile credentials. Bloem's text moved out of the Silo file into this
overlay in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/invitations`, `internal/auth/bloem_account_provisioner.go`,
`internal/api/handlers/bloem_admin_organization.go`). Since this overlay was
written (against Silo `8e5ae2f46`), Silo changed the document twice:
`be15e8b0e` (#1523, 2026-09-26) lets only the server Owner create or resend an
`admin` invitation, and `4a0cff41f` (#1788, 2026-10-02) adds link invitations,
which have no address until accept, and records delivery. Both apply to Bloem.
The earlier edition of this overlay replaced whole Silo sections with
September text that predated those changes; it now records only Bloem's real
differences.

Index: [Bloem documentation](../../README.md).

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
