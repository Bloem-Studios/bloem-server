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

### Select room content as the host

`PUT /api/v2/watch-together/rooms/{room_id}/selection` (`selectWatchTogetherRoomItem`) requires authenticated profile authority, the demo guard and both the host account and profile. The body requires `content_id`; optional `file_id` and `library_id` are positive string IDs. Host selection does not require guest room proof. The existing resolver retains playable-content and access checks. Vote rooms reject direct selection with `409`; non-host authority returns `403`, missing room `404`, invalid selection `422`, closed room `409`, and unavailable service/token configuration `503`.

A changed selection retains the existing service behavior: reset playback anchor to zero and paused, enter waiting with resume-on-ready, advance selection revision and generation, clear prior member playback-session/readiness/buffering/ignore-wait state, and broadcast the local snapshot. The v2 writer locks the authoritative database row and compares resolved content, file and library identity before any reset. An identical current selection preserves anchor, revision, generation, member readiness, waiting timer and broadcasts. This is a current-state no-op, not a historical replay receipt: after a different intervening selection, the old request can select its content again. The operation remains non-retryable. A competing generation writer can cause the service to return its refreshed winning snapshot without replaying the failed selection. Clients must use the returned snapshot.

The actual web action captures input and authority, sends once without authentication replay, and fences replaced authority, room and request run before publication. A newer held generation is retained only for the same room. Stale receipts do not clear the candidate or display completion feedback. There is no automatic read, rebase or retry after an uncertain result. Success returns the existing room snapshot and renewed room/account/profile proof; that proof is not a session-bound socket credential. The v1 selection writer retains its frozen reset behavior; all nodes serving this v2 operation must use the guarded writer. A no-op refresh from another node adopts a newer selection revision locally and discards readiness belonging to the prior selection. This port does not migrate the room socket, establish cross-node broadcast or activate dormant native UI.

### Suggestion creation receipt storage

The v2 suggestion creation prerequisite stores a caller-selected suggestion identity with the original account, profile, room and a SHA-256 digest of the canonical content metadata. The receipt and suggestion insert commit in one transaction. The room row is locked before admission, so committed closure refuses creation. An identical retry returns the existing suggestion; a different owner, room or payload conflicts. The created flag distinguishes insertion from replay so the later caller can avoid repeat broadcasts.

Receipts survive suggestion and room deletion. Repeating a deleted identity conflicts instead of recreating it. Receipts retain no title, note or poster URL payload and are removed on login-account deletion. There is no age-based receipt expiry; expiring them would permit identity reuse. Legacy suggestion insertion/deletion and their wire behavior are unchanged. The receipt store does not dispatch broadcasts or provide durable broadcast delivery; the caller behavior is described below.

### Create a room suggestion

`POST /api/v2/watch-together/rooms/{room_id}/suggestions` (`createWatchTogetherSuggestion`) requires authenticated profile authority, the demo guard and original room proof in `X-Room-Token`. Supply a client-generated UUID `suggestion_id`, required `content_id`, `content_type` (`movie` or `episode`) and `title`, plus optional `subtitle`, `poster_url` and `note`. The receipt migration must be installed before enabling this writer on any v2-serving node.

A successful insertion or identical live replay returns `201` with `suggestion_id`. The original account/profile/room and metadata are bound by the receipt store. Changed or deleted identities return `409` without resurrection; closed room returns `409`, missing room `404`, invalid input `422`, invalid proof `403`, and unavailable service `503`. Current suggestion state is read separately. The service supplies creation time and attempts the existing local suggestion snapshot broadcast only for a new insertion. A failure after commit can leave the broadcast incomplete; retry returns the identity without attempting it again. This is not durable delivery or an outbox.

The actual web candidate holds an immutable ID, metadata, room proof and authority. Explicit retry of that unchanged draft reuses them; dismissing or replacing it creates a new identity. Authority replacement refuses the old draft instead of rebinding it. Each submission sends one POST without authentication replay and refreshes the existing suggestion list under the original authority after a matching receipt. Superseded room/run/authority cannot publish the list; superseded candidate cannot be cleared or receive success/error feedback. Drafts are not persisted across page reloads. Room proof expiry is surfaced without automatic renewal or rebasing. V1 creation remains unchanged; native caller inventory and adoption are separate gates.
