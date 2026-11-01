# Bloem overlay: Invitation API

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to Silo's [`docs/invitations-api.md`](../../invitations-api.md) (titled
> “Email invitation API” until Silo's `4a0cff41f`), which stays exactly as Silo
> wrote it. Bloem replaces nothing there. The additions are in the
> [For developers](#for-developers) section.

An **invitation** lets someone create an account by following a link. Silo's page
explains the administrator routes that create, resend and revoke invitations.
Bloem serves them unchanged, but Bloem has **organizations**, and every invitation
belongs to one. So Bloem adds two things: which organization Silo's routes work in,
and the separate routes an organization administrator uses.

![Two ways to invite someone. Silo's administrator routes work only in the default organization and can send the link by email. The organization console works in its own organization, invites ordinary users only, sends no email and shows a one-time link the administrator passes on. Both keep one live invitation per address in each organization](../../architecture/images/overlay-invitations/two-paths.svg)

## What Bloem changes

- **Silo's invitation routes work in the default organization.** They list, send
  and revoke only the **default organization**'s invitations, and the account an
  invitation creates is placed there.
- **An organization administrator has routes of their own.** The organization
  console invites people into its own organization, as ordinary users only.
- **The console sends no email.** It shows a link once, and the administrator
  passes it on. The console never stores the link where it could leak.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Silo's invitation routes | `/api/v2/admin/invitations` and their `/api/v1` counterparts |
| The organization console's routes | `/api/bloem/v1/admin/organization/invitations` |
| The one-time link | `claim_token` in the create and resend responses |
| Which organization an invitation belongs to | `invitations.organization_id` |

## For developers

### Where this overlay came from

Bloem first documented this route family in the Silo file in September 2026 and
moved it here in `47ea7c889` (2026-09-23). The design is in the
[invitations overlay](architecture/invitations-onboarding.md). It was reviewed on
2026-10-27 against Silo `3e2d306b2` and Bloem `main` at `e852f92bf`
(`internal/api/router_bloem.go`, `internal/api/handlers/bloem_admin_organization.go`,
`internal/invitations`). Since this overlay was written (against Silo
`8e5ae2f46`), Silo changed the document three times:

- `be15e8b0e` (#1523, 2026-09-26): only the server Owner may create or resend an
  `admin` invitation;
- `4a0cff41f` (#1788, 2026-10-02): link invitations with `delivery: "link"`,
  `email_required` and `note`;
- `d9c79f3b4` (#2399, 2026-10-13): unknown library IDs are refused, and deleting
  a library removes its ID from stored invitations.

All three apply to Bloem's `/api/v2` routes as written. Silo has not changed the
document again through `63ef34b23`.

### Section by section

---

**Location:** section “Invitation API”, after the paragraph beginning “All three
POST operations are **non-retryable**, including authentication-refresh…”.
**Bloem adds:**

#### Scope of the v2 administrator routes on Bloem

Silo's `/api/v2/admin/invitations` routes, and their `/api/v1` counterparts, list,
read, create, resend, revoke and delete only invitations of Bloem's default
organization (`public.bloem_default_organization_id()` in
`internal/invitations/repository.go`). An invitation they create belongs to that
organization, and its accepted account is created there. Invitations created
through the organization console below are not visible to them.

An emailed invitation link uses the invitation service's `server.public_url`,
which `orgdomains.SettingsOverlay` (with `PublicURL: true`) answers for the
sending request's organization (`orgdomains.OrganizationFor`): another
organization's primary domain, or the server public URL for the default
organization. See
[organization domains](../../architecture/bloem-organization-domains.md).

#### Bloem organization invitation links

The embedded organization console uses
`/api/bloem/v1/admin/organization/invitations`: GET lists invitation metadata
without claim tokens, POST creates, POST `/{id}/resend` regenerates the link
after confirmation, and DELETE `/{id}` revokes it after confirmation. This
native flow:

- is limited to role `user` invitations in the selected organization. Platform
  and `admin` invitations cannot be created, regenerated or revoked here;
- binds every invitation to an email address, like Silo's emailed invitations,
  but sends **no email**, even when SMTP is configured. Creation and
  regeneration return a one-time `claim_token` that the administrator delivers.
  Stored rows have `delivery` NULL;
- regenerates only an unaccepted, unrevoked invitation. Regeneration revokes
  the old link in the same transaction.

The browser captures the administrative-context generation before a write,
reloads after attempted mutations, and never replays an uncertain create or
regeneration. Handoffs stay out of query and mutation caches and out of
persistent storage. Closing the result, switching context or logging out clears
them. See the
[native workflow contract](../../bloem-api-reference.md#embedded-web-workflow-adapters)
for revision, status and audit details. Silo's v2 delivery semantics above
(`link`, `email`, recorded delivery) are separate and unchanged.

### Related pages

- [Invitations overlay](architecture/invitations-onboarding.md): the one live
  invitation per address in each organization, and the profile-store rule.
- [How organizations work](../../architecture/bloem-organizations.md).
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_invitations.py` into
`docs/architecture/images/overlay-invitations/`. Redraw it there, not by hand.
