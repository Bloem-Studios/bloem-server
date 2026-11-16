# How organizations work

Organizations are a Bloem addition: Silo has one set of accounts per server. This page
explains the model in plain language. The technical pages linked at the end hold the
invariants.

Bloem is built in four tiers. Each tier lives inside the one above it, and each one
can only narrow what the tier above it allows. This page walks through who is who,
what someone can watch, how an organization starts and ends, and what happens when
someone presses play.

## Who is who

![The platform contains organizations, organizations contain accounts, and accounts contain profiles](images/organizations/tiers.svg)

1. **The platform** is the Bloem service itself: its servers, its libraries (movies,
   series, audiobooks, ebooks, Live TV) and the people who run it, the **platform
   administrators**.
2. **An organization** is one customer. It sees only its own people, libraries and
   settings, never another organization's. Its **organization administrator** runs it.
3. **An account** is one sign-in, an email address and a password. Every account
   belongs to exactly one organization. Its **plan** decides how many screens it can
   watch on at once, the picture quality and whether it can download.
4. **A profile** is one person using the account, such as each member of a household.
   A profile can have a PIN, an age limit and its own set of libraries. The account's
   main profile manages the household.

### Two kinds of organization

- **The default organization** holds Bloem's own direct customers. The storefront
  (Bloem HQ) or an administrator manages each of these accounts one by one. Every
  server has exactly one default organization, and accounts from before organizations
  existed live there too. Each new platform library is granted to it automatically.
- **A reseller organization** is sold through the storefront as a block of **seats**
  (see the [reseller example](#example-a-reseller)).
  It suits a club, a business or a reseller that looks after its own members. Each
  person takes one seat. The organization also gets a number of **shared video
  conversions** for all of its members together. It can own libraries of its own.

A platform administrator can also create an organization by hand in the admin
console. A server with no reseller organizations has only the default one, and every
account is in it.

### Its own address and look

An organization can have web addresses of its own, its **domains**, and its own
name, logo and colours. On its domains the sign-in page and the web app show its
branding, and its emails link to its **primary domain**. An account that signs in on
another organization's domain is told where to sign in instead. A domain changes
only what people see, never what they can reach. The default organization uses the
server's own branding, and the server's public address is registered as its
primary domain. See
[how organization domains and branding work](bloem-organization-domains.md).

## What can someone watch?

![A funnel from everything on the server, to what the organization may use, to what the plan allows, to what the profile may see](images/organizations/what-can-i-watch.svg)

Each step narrows the one before it:

1. **Everything on the server.** Platform administrators add the libraries.
2. **What the organization may use.** This is the libraries the organization owns,
   plus the platform libraries granted to it. It is a hard ceiling: nothing further
   down can reach past it.
3. **What the account's plan allows.** The plan picks libraries and sets screens,
   quality and downloads. An administrator can adjust a single account, but still only
   within step 2.
4. **What this profile may see.** Kids profiles, age limits and a chosen set of
   libraries narrow it further.

A **browse-only** plan shows the catalog but never plays anything. The storefront's
preview visitors use a protected Preview plan like it. When the storefront switches
on free play, a visitor who confirms an email address can watch for a metered time.
See [browse-only content policy](bloem-browse-only-content-policy.md).

## How an organization starts, runs and ends

![Five steps from a storefront sale to people watching, and the organization's four states](images/organizations/lifecycle.svg)

1. A reseller buys a block of seats on the storefront.
2. The storefront asks Bloem to create the organization, with its seats and shared
   video conversions. The organization is **getting ready**: it has no administrator yet.
3. Bloem grants it the platform's **starting set** of libraries and plugins.
4. The first administrator account is added, and the organization becomes **active**.
5. The organization's administrator adds people. Each one takes a seat and gets the
   plan's limits.

An organization can be **suspended**. That happens on its own when it has more people
than seats, for example after its seats are reduced, and lifts on its own once it is
back under. A platform administrator or
the storefront can also suspend it, for example when a bill goes unpaid. That kind
lifts only when someone lifts it. While an organization is suspended, its members are
refused.

When the storefront ends a reseller organization, Bloem **retires** it. Its members
are removed, and accounts with nothing else to hold them are deleted. Accounts that
own another organization, or that are platform administrators, are kept. The
organization itself is never deleted outright: it stays on record as the owner of
anything it had ([lifecycle safety](bloem-lifecycle-safety.md)).

## How an organization administrator invites people

An organization administrator invites someone from the admin console's
**Invitations** page. Bloem never emails these invitations: the page shows a
one-time **invitation link**, and the administrator passes it on, for example in a
chat. The administrator first picks who can use the link:

- **Anyone you share it with.** The link has no email address. Whoever opens it
  enters their own address and chooses a password. This is the first choice,
  as on Silo's own invitations page when a server cannot send email.
- **One email address.** Only that address can use the link, and it becomes the
  person's username.

Either way, the new account joins the administrator's own organization, never
another one, with the access chosen on the invitation. A link works once and
expires after seven days. Regenerating it stops the old link, and revoking it
stops it for good. In the history, a link with no address shows as a **link
invitation**, with its note so the administrator can tell links apart.

## Example: a reseller

This example is made up.

Riverside Club, a football club, resells Bloem to its members. It buys **10 seats**
and **2 shared video conversions** on the storefront. Early on, eight seats are taken.

![Ten seats with eight members and two free, two shared conversions in use, and the club's libraries and plan](images/organizations/reseller-bought.svg)

- **Seats.** Each member account takes one seat. When all ten are taken, an 11th
  member is refused until a seat is free.
- **Shared video conversions.** All members share them. A video that plays without
  converting doesn't use one. When both are busy, a third member who needs a
  converted video is refused until one frees up.
- **What the members can watch.** Members get the platform's starting set of
  libraries plus the club's own library, all on the club's plan. Riverside Club
  never sees another organization's people or libraries, and no other organization
  sees Riverside Club's.

Over a year, the club, the storefront and Bloem each play their part:

![A year in eight steps, each with the organization's state after it](images/organizations/reseller-year.svg)

- **A missed payment** and **too many people for the seats** both suspend the
  organization, and its members are refused while it is suspended.
- **They lift differently.** A suspension for non-payment lifts only when the
  storefront or a platform administrator lifts it. A suspension for too many people
  lifts by itself once the club is back within its seats. If someone lifts a
  payment suspension while the club still has too many people, it stays suspended
  for that reason.
- **Cancelling retires the organization.** Its members are removed, and accounts
  with no other organization are deleted.

## What happens when someone presses play

![Five checks before a video plays, each with the refusal shown beside it](images/organizations/press-play.svg)

1. **Who is watching?** The app has signed in and picked a profile. Bloem looks up the
   account's organization. A sign-in from before the organization revoked the account
   is ended, and the app asks the person to sign in again.
2. **Is the organization active?** A suspended organization's members are refused.
3. **May this profile see this title?** This is the funnel above. A title the profile
   may not see is not shown at all.
4. **Is a screen free?** The plan sets how many videos the account can play at once.
   When none is free, playback is refused. If the storefront has switched it on, the
   viewer also gets an upgrade offer in their inbox.
5. **If the video needs converting, is there room?** The account has its own limit,
   and a reseller organization also has its shared pool. When either is full, playback
   is refused until a conversion frees up.

While the video plays, Bloem checks library access again each time it sends video,
not only when the title is listed.

## Who can change what

| Who | What they manage |
| --- | --- |
| Platform administrator | Every organization: create, suspend, reactivate, transfer ownership, its domains and branding. Platform libraries and which organization gets them. Plans (entitlement templates). |
| Organization administrator | Their own organization only: people, profiles, groups, invitations, its own libraries, and grants already made to it. |
| The storefront (Bloem HQ) | Creating and retiring reseller organizations, their seats, conversions, domains and branding. Putting an account on a plan. It uses a platform administrator's API key. |
| An account's main profile | The household's profiles: who they are, PINs, age limits and libraries. |

An account with the `admin` role is a platform administrator. An organization
administrator's authority comes from their membership in that organization. The admin
console asks which of the two you are acting as, and never mixes them.

Apps built for Silo, and Jellyfin or Audiobookshelf apps, know nothing about
organizations. They sign in to an account, and Bloem works out the organization from
it.

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Platform | the one `platform` row in `resource_owners` |
| Organization | `organizations` (a "tenant" in `internal/tenancy`) |
| Default organization | the organization with `is_default` |
| Reseller organization | an organization with an `external_service_id`, created through the tenants API (`/api/bloem/v2/admin/platform/tenants`; the legacy v1 tenants API went with Silo's v1 API on 2026-11-11) |
| Seats | `organizations.slots`; each seat is one `organization_memberships` row |
| Shared video conversions | `organizations.transcodes`, a pool shared by every member |
| Getting ready, active, suspended | `organizations.status`: `initializing`, `active`, `suspended`, with `suspension_reason` `quota` or `admin` |
| Retired | suspended, with the external service identity cleared and the memberships removed |
| Membership | `organization_memberships`, at most one per account |
| Library grant | an `organization_entitlements` row for a platform library or plugin |
| Starting set | the organization-creation default bundle (`entitlement_bundles`) |
| Plan | an entitlement template, applied to an access group (`access_groups.managed_template_key`) |
| Account settings | the policy columns on Silo's `users` row, which override the plan's values |
| Profile limits | `user_profiles`: PIN, child flag, age limit and library restrictions |
| Domains, primary domain | `bloem_organization_domains`, with one `is_primary` row per organization (`internal/orgdomains`) |
| Its own branding | `bloem_organization_branding`, the organization's own `branding.*` values |
| Invitation link | an `invitations` row with the organization's `organization_id`; `delivery` is `link` |
| Anyone you share it with | `delivery: "link"` on create; the row's `email` stays empty until someone accepts |

## For developers

- **API:** [organizations and administration](../bloem/api/organizations.md):
  administrative contexts, organization administration, the platform's
  organization lifecycle, and the storefront's reseller organizations
  (`/api/bloem/v2/admin/platform/tenants`), their domains and branding
  (`/api/bloem/v2/admin/platform/tenants/{id}/domains` and `/branding`, capability
  `organization_domains_v1`), member accounts and account create and update. Plans
  are on the [plans and preview](../bloem/api/plans.md) page. Bloem's callers no longer
  use Silo's v1 API, which Silo retired on 2026-11-11
  ([leaving Silo's v1 API](bloem-leaving-silo-v1.md)).

- **Organization invitations:** `POST /api/bloem/v2/admin/organization/invitations`
  takes Silo's optional `delivery` field (Silo #1788), advertised by
  the `organization_invitation_links_v1` capability token. `link` creates an
  invitation with no `email` (sending one is a 422 on `email`); the invitee enters
  their address at accept, through Silo's own lookup (`email_required`) and accept.
  Omitted keeps the address-bound invitation, which needs `email`. Silo's other
  value, `email`, and any unknown value are a 422 on `delivery`: this route never
  sends mail, so it matches Silo's behaviour on a server without email. Both kinds
  are stored with `delivery` `link` and return the one-time `claim_token`, never a
  claim URL; the web app puts it only in the address fragment (`/invite#<token>`)
  so it never reaches a server log. The organization comes only from the
  administrative context: a body `organization_id` is refused (unknown field) and
  a query one is ignored. Regenerating a link invitation
  (`/invitations/{id}/resend`) revokes the old row itself, because nothing
  supersedes an address-free invitation by address, and the replacement stays a
  link. Code: `bloemOrganizationInvitationAddress` in
  `internal/api/handlers/bloem_admin_organization.go`,
  `ResendForOrganization` in `internal/invitations/bloem_lifecycle.go`, the web
  form in `web/src/pages/admin-settings/OrganizationInvitationsTab.tsx`. Tests:
  `TestBloemOrganizationInvitationDelivery`,
  `TestBloemOrganizationLinkInvitationStaysInTheResolvedOrganization`,
  `TestOrganizationLinkInvitationRotationRevokesTheOldLink` and the
  "organization link invitations" tests in `OrganizationInvitationsTab.test.tsx`.
  See the [invitation API overlay](../bloem/overlays/invitations-api.md#bloem-organization-invitation-links).

Technical pages:

- [How multitenant administration works](multitenant-administration.md): the
  Platform and Organization admin contexts, and how Silo clients resolve an
  organization.
- [How library ownership and grants work](resource-tenancy-foundation.md): resource
  owners, grants and the starting set.
- [How tenant authorization works](opa-tenant-authorization.md): how tenant facts,
  the policy engine and SQL scope reads.
- [How retiring an organization and deleting a library stay safe](bloem-lifecycle-safety.md).
- [How organization domains and branding work](bloem-organization-domains.md): each
  organization's own addresses, branding and email links, and the sign-in check.
- [Delivery boundary enforcement](delivery-boundary-enforcement.md): access rechecked
  when video and rows leave the server.
- [How plans work](bloem-plans.md) and the [entitlement templates guide](../wiki/admin/entitlement-templates.md).
- [Silo backend switching](silo-backend-switching.md): a database shared with Silo is
  limited to one organization.

The pictures are SVG files under `images/organizations/`, drawn by
`scripts/bloem/draw-pictures.py`. Redraw them there when the model changes, not
by hand.
