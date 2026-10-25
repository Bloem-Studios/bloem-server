# Bloem overlay: Realtime API

Bloem additions and overrides for the upstream Silo document [`docs/realtime-api.md`](../../realtime-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Session-bound socket handshake”, after the paragraph beginning “Native socket/ticket adoption and independent domain review are required before…”. **Bloem adds:**

On Bloem, the events adapter additionally captures the original principal and resolved
tenant in the ticket store's optional server-owned authority binding. Contextless
upgrade and periodic checks revalidate that same account/session/profile, organization,
membership and revisions; they cannot silently select another tenant. Unbound tickets
fail closed. The binding is neither client input nor part of the public ticket response.
One-use consumption, expiry, Origin, PIN, policy fingerprint and revocation checks above
remain in force. See [security invariants](../../architecture/bloem-security-foundation.md#native-seasonal-delivery-and-events-authority)
and [bounded browser evidence](../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits).

**Location:** section “Personal event recipients”, after the paragraph beginning “On the bridge socket, only a ticket from `POST /api/v1/events/ws-ticket` binds a profile…”. **Bloem replaces the ticket mechanics; the recipient rules are Silo's:**

On Bloem, the bridge ticket is a route-bound audience ticket and is itself the
handshake credential. Connect with `?ticket=` and no `Authorization` header or
`token` query parameter; either of those authenticates the handshake instead,
the `ticket` parameter is not read, and the socket is unbound. The ticket's
profile replaces the handshake's `X-Profile-Id` and `X-Profile-Token`, so it both
binds the socket and decides administrator access. Bloem's mint route does not
require a profile; a ticket minted without one opens an unbound socket. See
[derived websocket credentials](../../architecture/multitenant-administration.md#derived-websocket-credentials).
