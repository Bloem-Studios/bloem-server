# Realtime API

`GET /api/v2/events/capabilities` describes the shared event subscription
protocol. It requires an authenticated account; no active profile is required.
An unavailable event service returns `503`.

The response contains `schema_version`, `subscribe_frame`, `declared_channels`,
`subscribe_grace_period_seconds`, `max_requested_channels`, and `channels`.
These values come from the same implementation as the bridge capability route
and the event socket's enforced subscription limits. `channels` lists client
channels independently of the caller's role. The socket hello frame determines
which channels that connection may actually subscribe to. Internal plugin
channels are excluded.

This read creates no ticket or connection and does not grant access to a
channel. It describes the subscription protocol, not a socket authentication
credential or a guarantee that a particular API-version socket is available.
Socket authorization and ticket issuance are separate contracts. The bridge
capability response remains unchanged. Current first-party web, Apple, and
Android callers do not consume this capability read; Jellyfin has no matching
native realtime discovery operation.

## Session-bound socket handshake

`POST /api/v2/events/ws-ticket` delegates a current access-token login session
for one connection. API keys and credentials without a bounded access-token
expiry cannot mint this proof. A profile is optional; when present, its ownership
and PIN proof must validate. The ticket is opaque, expires within 30 seconds,
and binds the account, login session, account role, profile proof and resolved
access policy. The response includes `ticket`, `expires_in`,
`max_connection_seconds` (300), and `protocol` (`silo.events.v2`). It is not cached.
Minting is naturally idempotent in effect: extra credentials are harmless
orphans that expire, so shared session refresh may retry the mint. A consumed
credential is never reused for reconnects.

Connect to `GET /api/v2/events/ws` with exactly these offered subprotocols,
in order: `silo.events.v2`, `silo.ticket.<ticket>`. The server selects only
`silo.events.v2`, never the credential-bearing entry. Neither bearer tokens nor
tickets belong in the URL. Only the `channels` query selection is needed for
clients using declared subscriptions. Request bodies are refused. An Origin,
when present, must equal the configured public origin; without that setting it
must match the request scheme and host. Forwarded host headers grant no Origin
exception. Native clients may omit Origin but need the same session proof.
Malformed upgrades and rejected origins do not consume valid tickets.

Consumption is atomic through Redis `GETDEL`. A Redis failure fails closed.
Without Redis, tickets are process-local and require affinity to the minting
node; restarting that node invalidates them. This store is distinct from the
bridge's user/profile-only ticket, which cannot authenticate a v2 connection.

Admission rechecks the current session, enabled account, account role and viewer
policy/PIN proof. Secondary profiles do not receive administrator channels.
Connections end at access-token expiry or after five minutes, whichever comes
first. Current session/account/profile policy is rechecked every 15 seconds;
a failed check closes the connection, with a two-second bound on authority
lookups. Clients must reconnect with a newly minted credential. This is bounded
revocation detection, not an instantaneous revocation guarantee.

The selected message protocol retains the existing event frames (`hello`,
`subscribe`, `subscribed`, `snapshot`, `event`, `error`) and per-channel payloads.
The shared event implementation still applies channel eligibility, the subscribe
grace period, inbound frame limits, snapshots, ping/pong and delivery filtering.
The handshake version does not rewrite another domain's event payload.
Web consumers capture account/profile authority before minting and discard
connection results and frames after that authority changes.

Native socket/ticket adoption and independent domain review are required before
these two migration rows can be ratified. No bridge socket or ticket was removed.
