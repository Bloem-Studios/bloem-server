# Bloem overlay: Realtime API

This overlay covers Silo's [`docs/realtime-api.md`](../../realtime-api.md), which stays exactly as Silo wrote it.

The **realtime API** is the live connection (a socket) an app keeps open so the
server can push events to it: playback changes, new titles, messages. Before an app
opens one, it gets a short-lived **ticket** that proves who it is. Bloem uses
Silo's sockets and tickets unchanged, with one addition.

## What Bloem changes

- **A socket stays in its organization.** Bloem records the organization a ticket
  was issued for, inside the ticket, where the app cannot see or change it. Each
  time the server checks the connection again, it checks that same person,
  profile and organization. A socket can never drift into another organization.
- **A ticket without that record is refused**, except a monitoring tool's ticket,
  which belongs to an API key rather than a sign-in and is checked the way an
  ordinary request with that key is.

## Words used in the code

| On this page | In the code |
| --- | --- |
| The organization recorded in a ticket | the ticket's server-owned `AuthorityBinding` (`internal/events/socket_ticket.go`) |
| Bloem's check | `bloemSocketAuthority` in `internal/api/handlers/bloem_socket_authority.go` |
| A monitoring tool's ticket | a ticket minted by an API key with Silo's `admin:monitor:read` scope |

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
owner's requests resolve to (`validateKey`). A key ticket that carries a binding, a
session or a profile was not minted for a key and is refused.

### Related pages

- [Security foundation](../../architecture/bloem-security-foundation.md): events
  authority and organization-scoped session revocation.
- [v1 scope overlay](architecture/v1-scope.md#retired-bloem-v1-socket-ticket-routes):
  the retired Bloem ticket routes.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).
