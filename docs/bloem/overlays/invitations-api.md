# Bloem overlay: Invitation API

**What this overlays.** Silo's [`docs/invitations-api.md`](../../invitations-api.md)
(titled “Email invitation API” until Silo's `4a0cff41f`). Bloem adds one section
after the paragraph beginning “All three POST operations are **non-retryable**…”
and replaces nothing. An *overlay* is a Bloem-owned page that records Bloem's
additions to, or replacements of, sections of a Silo-owned document. The Silo
file stays exactly as Silo wrote it. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem is multi-tenant. Each invitation belongs to one
*organization* (tenant), and Bloem's embedded organization console manages its
organization's user invitations through Bloem's own native routes. Bloem first
documented this route family in the Silo file in September 2026 and moved it
here in `47ea7c889` (2026-09-23). The design is in the
[invitations overlay](architecture/invitations-onboarding.md).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/router_bloem.go`,
`internal/api/handlers/bloem_admin_organization.go`, `internal/invitations`).
Since this overlay was written (against Silo `8e5ae2f46`), Silo changed the
document three times:

- `be15e8b0e` (#1523, 2026-09-26): only the server Owner may create or resend an
  `admin` invitation;
- `4a0cff41f` (#1788, 2026-10-02): link invitations with `delivery: "link"`,
  `email_required` and `note`;
- `d9c79f3b4` (#2399, 2026-10-13): unknown library IDs are refused, and deleting
  a library removes its ID from stored invitations.

All three apply to Bloem's `/api/v2` routes as written.

Index: [Bloem documentation](../README.md).

---

**Location:** section “Invitation API”, after the paragraph beginning “All three
POST operations are **non-retryable**, including authentication-refresh…”.
**Bloem adds:**

## Scope of the v2 administrator routes on Bloem

Silo's `/api/v2/admin/invitations` routes, and their `/api/v1` counterparts, list,
read, create, resend, revoke and delete only invitations of Bloem's default
organization. An invitation they create belongs to that organization, and its
accepted account is created there. Invitations created through the organization
console below are not visible to them.

## Bloem organization invitation links

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
