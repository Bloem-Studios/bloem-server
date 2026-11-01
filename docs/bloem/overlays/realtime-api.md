# Bloem overlay: Realtime API

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to Silo's [`docs/realtime-api.md`](../../realtime-api.md), which stays exactly
> as Silo wrote it. Bloem replaces nothing there. The addition is in the
> [For developers](#for-developers) section.

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

### Where this overlay came from

Bloem runs Silo's socket tickets unchanged and attaches its tenant authority
through Silo's optional `AuthorityBinding` field and validate hook
(`internal/api/handlers/bloem_socket_authority.go`). Since `a7436b4e5`
(2026-10-25), Silo's v1 events, watch-together and playback-control sockets also
run Silo's own handshake. Bloem's earlier v1 audience tickets and their two
ticket routes are removed; see the [v1 scope overlay](architecture/v1-scope.md).
Bloem first documented its realtime difference in the Silo file in September
2026 and moved it here in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`, when Silo's `3e2d306b2` (#3141, 2026-10-26) had capped a
watch-together room at 200 suggestions and given frozen v1 suggestion creation
that cap and a 32 KiB body limit; that applies to Bloem unchanged.

From `3e2d306b2` through `63ef34b23` Silo changed the document seven times. The
change that touches this overlay is `eb8a09973` (2026-10-27), which lets an API
key with `admin:monitor:read` open the events socket and adds the subsection
“API-key handshake” inside “Session-bound socket handshake”. The paragraph this
overlay used to follow (“Native socket/ticket adoption and independent domain
review…”) now ends that new subsection, so this rewrite moved the anchor to the
end of the session handshake's own text, before the subsection. Bloem's
key-ticket check followed in `cc37f3900` (2026-10-29). The other changes, which
apply to Bloem as written, are `permessage-deflate` on every socket (#3275),
playback session lifecycle events (#3194) and the catalog item change events
(`f53b4b218`, `a469b18c9`, `97439e63b`, `86e5b948d`).

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
and [bounded browser evidence](../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits).

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
