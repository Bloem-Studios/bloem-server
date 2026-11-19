# Bloem overlay: Realtime API

This overlay covers Silo's [`docs/realtime-api.md`](../../realtime-api.md), which stays exactly as Silo wrote it.

The **realtime API** is the live connection (a socket) an app keeps open so the
server can push events to it: playback changes, new titles, messages. Before an app
opens one, it gets a short-lived **ticket** that proves who it is. Bloem uses
Silo's sockets and tickets unchanged, with two additions.

## What Bloem changes

- **A socket stays in its organization.** Bloem records the organization a ticket
  was issued for, inside the ticket, where the app cannot see or change it. Each
  time the server checks the connection again, it checks that same person,
  profile and organization. A socket can never drift into another organization.
- **A ticket without that record is refused**, except an API key's ticket, such
  as a monitoring tool's or a profile key's. It belongs to a key rather than a
  sign-in and is checked the way an ordinary request with that key is, in its
  owner's organization.
- **A key that acts as a profile stays in its organization.** When an API key's
  ticket names a profile, that profile must belong to the organization the key's
  owner is in, checked again each time the server checks the connection.
- **Bloem's own checks end a socket the way Silo's do.** When the session,
  account, organization, membership or profile is gone, suspended, changed or
  moved, the socket closes with `4002` `credential_invalid`. When the server
  cannot run the check because a lookup failed or timed out, it closes with
  `4004` `authority_unavailable`. A ticket request or a connection attempt then
  gets `503`, and the app tries again later.
- **The web player asks for a remote-control ticket less often when refused.**
  After a server restart, a browser tab that keeps a session from before the
  restart is refused a ticket. Silo's player asks again every 5 seconds for as
  long as the tab stays open; Bloem's waits longer each time, up to one attempt
  every 30 seconds, and still reconnects as soon as the server answers.

## Words used in the code

| On this page | In the code |
| --- | --- |
| The organization recorded in a ticket | the ticket's server-owned `AuthorityBinding` (`internal/events/socket_ticket.go`) |
| Bloem's check | `bloemSocketAuthority` in `internal/api/handlers/bloem_socket_authority.go` |
| A monitoring tool's ticket | a ticket minted by an API key with Silo's `admin:monitor:read` scope |
| A key that acts as a profile | a key `SocketIdentity` with a `ProfileID` (Silo's "User keys") |
| The remote-control ticket | the owner-bound playback control ticket, minted by `web/src/player/hooks/usePlaybackRealtime.ts` |

## For developers

### Section by section

---

**Location:** section “Session-bound socket handshake”, after the paragraph
beginning “The selected message protocol retains the existing event frames…”
(before the subsection “API-key handshake”). **Bloem adds:**

On Bloem, the events adapter additionally captures the original principal and resolved
tenant in the ticket store's optional server-owned authority binding. Contextless
upgrade and periodic checks revalidate that same account/session/profile, organization,
membership and revisions; they cannot silently select another tenant. Unbound
login-session tickets fail closed. The binding is neither client input nor part of the
public ticket response. One-use consumption, expiry, Origin, PIN, policy fingerprint and
revocation checks above remain in force. See
[security invariants](../../architecture/bloem-security-foundation.md#native-seasonal-delivery-and-events-authority)
and [bounded browser evidence](../../architecture/bloem-web-feature-coverage.md#validation-status).

A ticket minted by an API key (the subsection “API-key handshake”) has no login
session to bind, so it carries no Bloem binding. Bloem admits it as an HTTP
request with that key is admitted: Silo's key checks decide, in the tenant its
owner's requests resolve to (`validateKey`). A key ticket that carries a binding or a
session was not minted for a key and is refused.

Bloem's checks follow the rule in the subsection “Close codes”: a read that
answers gives `evt.ErrSocketTicket` (`4002`) when the credential, tenant or
profile is missing, suspended, revoked, expired, mismatched or moved, and a read
that fails gives `socketAuthorityUnavailable` (`4004`, and `503` at mint and
upgrade). Tenant and profile-organization errors are sorted as the tenant
middleware sorts them for an HTTP request: `ErrTenantNotFoundOrHidden`,
`ErrTenantSuspended` and `ErrOwnershipResolutionRequired` end the credential,
and any other error means the check could not run.

Silo's user keys (the subsection “User keys”) open sockets on Bloem too. A key
ticket that names a profile (every profile key, and an account key sent with
`X-Profile-Id`) is admitted only when `ProfileOrganization` puts that profile in the
organization of the owner's resolved tenant, the check a sign-in ticket's profile
gets; Silo's key checks then decide. A profile in another organization, or one that
moved there, is refused on the next check.

### Related pages

- [Security foundation](../../architecture/bloem-security-foundation.md): events
  authority and organization-scoped session revocation.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).
