# Organization live updates

Status: contract for `feat/org-live`. A server implements it, and the admin apps build against it.

Organization pages in the admin apps (live streams, people, activity) refresh on timers because
Silo's events socket has no organization channels. This adds a socket that tells an organization
administrator's app **what changed**, so the app re-reads the item through the normal REST route.
REST stays the single source of truth: a late, duplicated or lost signal can never leave wrong data
on screen, only a stale list until the next signal or resync.

It is the same protocol as the staff-live socket the Proxy and HQ servers speak (`bloem-live.v1`:
`op` frames, `change`, `heartbeat`, `resync`, close codes 4401 and 4403, the same client rules for
foreground, backoff, watchdog and polling fallback). The differences are in
[Differences from the staff-live protocol](#differences-from-the-staff-live-protocol).

## Why a separate socket

Silo's events socket (`/api/v2/events/ws`, protocol `silo.events.v2`) is opened with a ticket for
an **account** login session, and its viewer scope comes from the account's own tenant. The
organization pages are authorized by the **administrative context pass** (`POST
/api/bloem/v2/admin/session`), which names one organization and can be a break-glass support
access of a platform operator who is not a member of it. An account ticket cannot express that, so
organization channels never ride the account socket: it refuses `org.*` as an unknown channel.

The pass is the credential here too. A ticket is minted with it, the socket is bound to the
organization the pass names, and the pass's authority is re-checked with the same code the REST
routes run.

## Mint a ticket

`POST /api/bloem/v2/admin/organization/live/ticket`

- Authorization: `Bearer <organization context pass>`, exactly as for
  `GET /api/bloem/v2/admin/organization/streams`. No body (a body is a 400).
- A **platform-scope** pass is refused with 403 `insufficient_organization_authority`, as the
  organization routes refuse it. A pass opened under support access is accepted.
- Response `200`, `Cache-Control: no-store`:

```json
{
  "ticket": "5mQ2…",
  "expires_at": "2026-11-16T12:00:30Z",
  "protocol": "bloem-live.v1",
  "channels": ["org.streams", "org.people", "org.activity"],
  "max_connection_seconds": 3600
}
```

- The ticket is opaque (43 URL-safe characters), single use and valid for **30 seconds**. It is
  never logged and never appears in a URL or a socket frame. Fetch a new one for every connect.
- `channels` lists what this server serves. `org.streams` is absent when the server cannot list
  live sessions. A client subscribes only to channels it shows and that are listed.
- `max_connection_seconds` is the longest the socket stays open: the pass's remaining lifetime,
  capped at one hour.
- Errors, in the usual `{ "error": code, "message": … }` shape:

| Status | `error` | Meaning |
| --- | --- | --- |
| 401 | `tenant_session_required`, `authorization_state_stale` | The pass is invalid, expired, or no longer matches the account, session, membership or support access. Renew the pass as for any organization route. |
| 403 | `insufficient_organization_authority` | Not an organization-scope pass. |
| 429 | `rate_limited` | More than 30 tickets a minute for the account on one node. `Retry-After` in seconds. |
| 503 | `live_unavailable` | The server's change feed is down. `Retry-After` in seconds. |
| 503 | `tenant_unavailable` | Tenant authorization is unavailable. |

The server advertises the feature in `GET /api/bloem/v2/capabilities` with the token
`organization_live_v1`. A server without it has no socket: poll as before.

## Connect

`GET /api/bloem/v2/organization/live/ws`, a WebSocket upgrade. The path is outside `/admin`
because a WebSocket client cannot send an `Authorization` header; the ticket is the credential.

- `Sec-WebSocket-Protocol: bloem-live.v1, ticket.<ticket>`. The server selects `bloem-live.v1`.
  A ticket in the URL (`?ticket=`, `?token=`) is a 400.
- `Origin` follows the events socket rule: absent, an app scheme or `null` is accepted, a browser
  origin must match the server's public origin.
- Before the upgrade the server answers plain HTTP: `400` malformed handshake, `403` origin,
  `503` with `Retry-After` while the change feed is down.
- After the upgrade the ticket is consumed. A bad, used, expired or foreign ticket closes with
  **4401**. Nothing is sent before the first `subscribe`.

### Close codes

| Code | Reason text | Client action |
| --- | --- | --- |
| 4401 | `invalid_ticket` | Fetch a new ticket (renew the pass if the mint answers 401). |
| 4403 | `access_lost` | The pass no longer holds: the session was revoked, the membership or role changed, the organization was suspended, or support access closed. Renew the pass. If renewing fails, the user lost access to the organization; handle it as a 401 or 403 on a REST route. Do not retry in a loop. |
| 4408 | `expired` | The pass or the connection lifetime ended. Not an error: renew the pass if needed and reconnect at once. |
| 4429 | `too_many_connections` | The account already has 5 sockets for this organization on this server. Back off. |
| 1008 | `bad_frame` | The client sent something this protocol does not accept. A client bug. |
| 1009 | `frame_too_large` | A client frame over 4 KiB. |
| 1012 | `restart` | The server is shutting down. Reconnect at once. |
| 1013 | `live_unavailable` | The change feed is down. Back off. After 3 failed attempts in a row fall back to polling, as the staff-live client rules say. |

Any other close, or a dropped connection, is a failed attempt: back off with full jitter.

## Frames

All frames are JSON text frames, one object each.

### Client to server

```json
{"op":"subscribe","channels":["org.streams","org.people"],"resume":null}
{"op":"unsubscribe","channels":["org.people"]}
```

- `subscribe` adds channels (at most 3 in total). Naming a channel already subscribed counts as a
  new subscribe. `resume` is accepted and ignored; see below.
- A channel the server does not know answers
  `{"op":"error","code":"unknown_channel","channel":"…"}`. A known channel this server does not
  serve answers `{"op":"error","code":"unavailable","channel":"…"}`. The connection stays open.

### Server to client

```json
{"op":"change","seq":17,"channel":"org.streams","id":"s_9f2…","rev":"4","kind":"updated"}
{"op":"heartbeat","seq":18}
{"op":"resync","seq":19,"channel":"org.people"}
```

- `change`: one item changed. `id` is the item's identifier in the matching REST list (below),
  `rev` is an opaque revision string (compare for inequality only), `kind` is `created`,
  `updated` or `deleted`. The client re-reads the item (or, for a list without an item route, the
  list) through REST. For `deleted` it can drop the row without reading.
- `heartbeat`: every **25 seconds**, whether or not anything changed. With no frame for **60
  seconds** the client treats the socket as dead.
- `resync`: re-read that whole list. The server sends it
  1. right after **every** accepted `subscribe` for each named channel, so there is no gap between
     a first REST read and the subscription, and a reconnect needs no replay;
  2. when it coalesced a burst it would not forward item by item;
  3. when it may have lost signals: after the change feed reconnected, or after an internal queue
     overflowed.
- `seq` increases by one for every `change`, `heartbeat` and `resync` on a connection, starting
  at 1. It is for the client's logging and ordering within a connection; it does not continue
  across connections.

A client that opens a screen subscribes to the channels it shows and loads on the `resync` that
answers (REST first is also correct, but costs one more read). It reloads on `resync` for the
channel it shows, applies `change` frames as it sees them, and ignores a `resync` for a channel
no screen shows.

## Channels

| Channel | Item `id` | Signals |
| --- | --- | --- |
| `org.streams` | the stream's `session_id` as `GET …/organization/streams` returns it | `created` when playback starts; `updated` when it pauses or resumes; `deleted` when it stops or the server drops it. Position ticks never signal. |
| `org.people` | the person's `account_id`, as a decimal string | `created` when a membership appears (a person joined or was added); `updated` when membership status, role or security revision changes, or when an audited administrative action targets the person; `deleted` when the membership is removed. |
| `org.activity` | `<source>:<id>` with `source` `lifecycle` or `entitlement`, matching the `source` and `id` of an entry in `GET …/organization/activity` | `created` for each new entry. Entries are immutable, so there is no `updated`. Re-read the first page. |

`rev` per channel: `org.streams` the session's lifecycle sequence number as a string;
`org.people` an opaque string that changes whenever the person's status, role or security
revision does; `org.activity` the same text as `id`.

Not signalled in this version: a member's own profile edits (adding, renaming, removing a profile
without an administrator). The people list shows them after the next re-read, a pull to refresh,
or any administrative action on that person. Add a channel or event if the pages need more.

## Scope

A connection receives changes for **one organization**: the one named by the pass it was minted
from. There is no wildcard and no way to name another organization in a frame. Isolation does not
depend on a filter that could be forgotten: the server keeps a registry of connections per
organization, and a change is delivered only to the connections registered under its organization.
A change whose organization cannot be determined is dropped, never broadcast.

The pass's authority is checked when the ticket is minted, when the socket opens, and again every
**15 seconds** for as long as it stays open, using the same code as the REST routes: the login
session, the operator's account, the membership and its role, or for support access the access
record and the organization's status. The first failure closes the socket with 4403.

## Bursts

The server coalesces per connection and channel. Signals collect for 250 ms, a later signal for
the same `id` replaces an earlier one (`created` then `updated` stays `created`; `created` then
`deleted` cancels out; `deleted` then `created` becomes `updated`), and the window is sent in
order. More than 100 distinct ids waiting on a channel in one window are replaced by a single
`resync` for that channel. A client never needs to rate-limit itself on top of this, but it should
coalesce its own re-reads (one in-flight read per list).

## Limits

- At most 5 sockets per account and organization, per server node. The sixth closes with 4429.
- At most 3 channels per socket. Client frames are at most 4 KiB.
- Tickets: 30 a minute per account, per node.
- A socket lives at most one hour, and never past its pass.

## Where signals come from

For people changing the server; clients can skip this.

- **People and activity** come from Postgres. Triggers on `organization_memberships`,
  `admin_audit_events` and `entitlement_audit_events` call `pg_notify('bloem_org_live', …)` with a
  small JSON payload (channel, organization id, item id, revision, kind). `pg_notify` inside a
  transaction delivers on commit, so a rolled-back change never signals, and every writer is
  covered, including the people bulk-job worker and other nodes. Each node holds one dedicated
  `LISTEN` connection and hands the payloads to its local registry. While that connection is
  down the endpoints answer 503, open sockets close with 1013, and on reconnect every connection
  gets a `resync` for each subscribed channel. This needs a direct Postgres connection (a
  transaction-pooling proxy breaks `LISTEN`).
- **Streams** come from the playback lifecycle events on Silo's hub (`session.started`,
  `session.paused`, `session.resumed`, `session.stopped`). Each node maps the session's account to
  its organization with the same rule as the REST filter (the account is a member and the
  profile, if any, belongs to the organization) and signals that organization only. The
  unattributed `sessions.replaced` event (a node died, stale sessions were swept) carries no
  session; on it, each node compares a fingerprint of the session set of every organization that
  has a subscriber on this node and sends `resync` to those whose set changed.
- Nothing is written to the shared hub for these channels, so plugins and the account socket never
  see them.

## Differences from the staff-live protocol

1. **Ticket lifetime** is 30 seconds (Silo's ticket store), not 60.
2. **No replay buffer.** The server runs on several nodes and a sequence number cannot be shared
   between them. `resume` is accepted and ignored; the `resync` after every subscribe closes any
   gap. `seq` is per connection.
3. **Scope** is one organization from the pass, not a tenant from an account.
4. **Path and credential**: the ticket is minted with the context pass at
   `POST /api/bloem/v2/admin/organization/live/ticket`, and the socket is
   `GET /api/bloem/v2/organization/live/ws`, not `/api/live/ticket` and `/api/live/ws`.
5. Extra close codes 4408 and 4429, and `error` frames carry the `channel` they refer to.

## Client notes

- Org mode needs this one socket for the organization pages. It does not need Silo's events
  channels (`sessions`, `scans`, `tasks`, `jobs`): those belong to server administrators.
- Open the socket only while an organization screen that uses a channel is on screen, per the
  staff-live foreground rules, and share one connection per organization pass.
- Renew the pass first when a mint answers 401, then mint again. Do not renew on a socket close
  other than 4403 and 4408.
- Keep the old timers as the fallback only: after 3 failed attempts in a row, poll the visible
  list at most once a minute, and stop when the socket returns.
