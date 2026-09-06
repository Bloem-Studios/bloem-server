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

### V2 suggestion reads and vote membership

`GET /api/v2/watch-together/rooms/{room_id}/suggestions` requires login/profile
credentials and the matching signed room access token in `X-Room-Token`. The room
proof binds the room, account and profile; it does not replace login authority.
The response is `{items, page}` with string identifiers and UTC timestamps.
`limit` and opaque `cursor` bound a live traversal ordered by creation time then
suggestion ID. Cursor scope includes account, profile, access policy, room and
page size. Vote changes do not move suggestions across the cursor. Concurrent
creation/deletion is not a snapshot; clients refresh to reconcile live changes.
Room lifecycle and the room websocket still use the bridge contract.

`POST` and `DELETE` on
`/api/v2/watch-together/rooms/{room_id}/suggestions/{suggestion_id}/vote` use the same
authority and return bodyless `204` for the requested vote membership, including
an already satisfied state. Repository membership and tally changes remain one
transaction. Existing no-op handling precedes list reads and broadcasts; actual
changes retain the existing room broadcast path. Opposing votes have no generation
ordering. A closed room returns `409`; a missing room or suggestion returns `404`.
An error after a database commit does not prove that the vote was unchanged.

The web adapter drains bounded pages under one captured authority and sorts the
completed list by votes for display. After a vote receipt it reloads under that
same authority; it does not replay or retarget a mutation after authentication or
profile changes. Suggestion creation, deletion, promotion, room policy and room
socket migration are separate operations. Native consumer closure remains
required before ratifying these mappings.

`DELETE /api/v2/watch-together/rooms/{room_id}/suggestions/{suggestion_id}`
requires the same login/profile and `X-Room-Token` proof. Only the room host or
original suggester, matched by both account and profile, may delete the entry.
Success returns bodyless `204`. Missing suggestions, including repeated deletion,
return `404` before list reads or broadcasts. Current creation never reuses IDs,
so repeating deletion cannot address a replacement entry. The existing domain
service retains its deletion and broadcast path. A failure after deletion does
not establish that the entry still exists.

The existing web delete action sends once, surfaces errors including `404`, and
reloads the bounded list only after success under the original captured authority.
It does not replay after a lost response or authentication error. Suggestion
creation and promotion remain separate bridge operations.

### End a Watch Together room

`DELETE /api/v2/watch-together/rooms/{room_id}` (`closeWatchTogetherRoom`) requires authenticated profile authority and the demo guard. The existing service checks both the host account and host profile. A guest room token does not authorize closing; this operation does not require room proof in addition to host identity.

Success returns bodyless `204` after the existing room-close service completes. Non-host authority returns `403`, a missing room `404`, an already-ended room `409`, and unavailable service `503`. Natural-idempotent classification describes convergence on ended state, not a promise that every repeated request returns `204`. The actual web action sends once without authentication replay and fences original authority before dispatch, after receipt and before completion feedback. It does not optimistically mark the room ended or automatically retry an uncertain close.

The owning service retains persistence, host/wait timer cleanup, local connected-member `room_closed` dispatch with the existing `host_left` reason, and live-room removal. This port changes no domain persistence or callback behavior. It does not cancel already-dispatched playback, establish cross-node socket broadcast, or complete the separate v2 room-socket contract. Existing room creation, joining and room credentials remain separate migration scopes; v1 wire behavior is unchanged.

### Read a Watch Together room

`GET /api/v2/watch-together/rooms/{room_id}` (`getWatchTogetherRoom`) requires authenticated profile authority, the demo guard and existing room proof in `X-Room-Token`. It fails closed when the room/token service is unavailable. Proof must match the exact room, account and profile. Invalid proof returns `403`, missing room `404`, closed room `409`, and unavailable service `503`.

A successful no-store `200` returns `room` and renewed `room_access_token`. The snapshot preserves selection, playback anchor, host/member roles and permission fields. IDs are strings on v2; the anchor time is a typed UTC instant. Renewal retains the existing room/account/profile token semantics. This token is not a session-bound socket credential, does not grant account authentication, and does not complete the separate v2 room-socket contract.

The existing web initial-room read sends proof only in the header, captures authority before dispatch, rejects stale success and failure, validates identities before converting to the existing UI model, and fences publication against room-effect cancellation and authority replacement. Closed-room conflict is terminal. Existing room-proof storage and socket transport remain separate; the read does not activate a new native surface or migrate the room socket. Domain snapshot and v1 wire behavior are unchanged.

### Set guest transport policy

`PATCH /api/v2/watch-together/rooms/{room_id}/policy` (`updateWatchTogetherRoomPolicy`) requires authenticated profile authority, the demo guard and both the host account and profile. Its body contains `guest_control_policy`: `host_only` or `guest_play_pause`. This host action does not require guest room proof. Invalid policy returns `422`, non-host authority `403`, missing room `404`, closed room `409`, and unavailable service/token configuration `503`.

Success returns a no-store `200` room snapshot and renewed existing room proof. The owning service retains generation-based persistence and local snapshot broadcasts. If a competing writer wins the generation check, it can return the refreshed winning snapshot without retrying or broadcasting the failed write. Clients must use that returned policy rather than assuming the requested value was stored. Natural-idempotent classification describes the policy value; it does not promise an unchanged generation or suppress successful-write broadcasts.

The existing web toggle captures policy and authority, sends once without authentication replay, and fences authority, replaced room and superseded run before publication or feedback. It retains a newer currently held room generation when a policy response is older. Stale room/run failures do not report into the replacement context. It does not automatically read, rebase or retry a conflict/uncertain result. Existing proof renewal is not a session-bound socket credential. V1 service behavior, guest transport enforcement and socket protocol remain unchanged; no cross-node broadcast or native activation is implied.

### Resolve a room invitation

`POST /api/v2/watch-together/join` (`joinWatchTogetherRoom`) requires authenticated account/profile authority and the demo guard. Send `code` or `join_token` in the JSON body. Values are trimmed; a nonempty invite token takes precedence when both are supplied. Missing input returns `422`, missing room `404`, closed room `409`, and unavailable room/token service `503` before proof issuance.

Success returns a no-store `200` with the existing room snapshot and room/account/profile-bound `room_access_token`. HTTP joining resolves the invitation and issues proof; it does not connect a member, attach playback, or cancel the host-disconnect timer. Those effects belong to the separate socket lifecycle. Repeated resolution can issue a different proof and observe a newer room snapshot; natural-idempotent classification does not promise a stable credential or durable admission receipt. Existing v1 behavior is unchanged.

The actual web code-entry and invite auto-join/retry actions copy input and capture authority synchronously, send once without authentication replay, and suppress navigation and errors after authority replacement, unmount, superseding request or invite replacement. The existing room route still receives room proof for the legacy socket flow. This operation does not make that proof session-bound, migrate the socket, or activate dormant native UI. Room creation remains a separate operation.
