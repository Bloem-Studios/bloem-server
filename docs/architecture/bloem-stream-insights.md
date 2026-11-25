# How stream insights work

Stream insights are a Bloem addition. They show the people who run a server where its
streams come from, how each one is connected, and who was turned away at a stream or
transcode limit. The Admin apps read them; a plain Silo server has none, and the apps
hide these screens there.

## Who sees what

- **Platform administrators** see everything: each live stream's route on a map, from
  the viewer through Cloudflare and a Bloem edge to the server and the transcode node,
  how the viewer is connected, and everyone's limits and refusals. Their viewing stats
  are Silo's own playback Insights (Monitor → Insights), not a Bloem addition.
- **Organization administrators** see their own members only: how much of their
  limits they are using, who was turned away, and viewing stats. They never see a
  location, an edge, a Cloudflare city or how a stream is connected.

## Where a stream comes from

Every playback session gets one record when it starts and is updated when it pauses,
resumes and stops. The record keeps the viewer's probable city and country, looked up
from their address in a local GeoLite2-City database. The address itself is never
stored. Without the database, nothing is placed on the map and everything else still
works.

A stream that passes through Cloudflare carries the Cloudflare city it went through. A
stream that passes through a Bloem edge carries the edge's name and place, which the
edge adds to every request it forwards. The server and node places come from the
server setting `bloem.map.locations`.

## How a stream is connected

Each media request tells how the viewer reached the first hop Bloem controls:

- **udp**: HTTP/3, or a direct UDP connection;
- **tcp**: HTTP/1.1 or HTTP/2;
- **cloudflare**: the viewer reached Cloudflare, which carried the rest;
- **unknown**: nothing said.

A live stream shows its latest request's class. A finished stream keeps the class that
carried most of its requests, and is marked as changed when more than one class
carried them.

## Who was turned away

Every refusal at a limit is recorded: a person's own stream or transcode limit, their
household's shared pool, or their organization's transcode pool. Repeats of one
person's refusal at one limit within a minute are one record. Platform administrators,
and the person's organization administrators, get an alert, at most one per person an
hour.

An administrator can set quiet hours on each phone. During them the server sends that
phone only the kinds of alert it lets through, and drops the rest; nothing is saved
for later.

## Words used in the code

| Word | Meaning |
|------|---------|
| stream record | `bloem_stream_records`, one row per playback session |
| request facts | what one media request says: protocol, edge, Cloudflare colo |
| refusal | `bloem_limit_refusals`, one row per coalesced refusal |
| transport class | udp, tcp, cloudflare or unknown, for the viewer's leg |
| trail | a stream's hops with coordinates, in route order |

## For developers

The code is in `internal/bloeminsights` (records, facts, refusals, queries),
`internal/bloemgeo` (the location lookup), `internal/api/bloem_stream_insights_*.go`
(the request hook and the startup wiring) and
`internal/api/handlers/bloem_stream_insights.go` with
`bloem_org_stream_limits.go` (the routes). No Silo file changes for it.

### Request facts and trust

- `captureNativeVideoRequest`, which Silo's router runs on every request, hands each
  media request to the hook. A request is media when its path is a delivery path
  (`middleware.DeliverySessionID`) or it carries a stream token.
- The session is the delivery path's session id; a stream token on that path must
  name the same session. Elsewhere a playback stream token's `sid` names it. This
  covers header-authenticated media, whose URLs carry no token.
- Facts are queued only once the response status is below 400, so an unauthorized
  request writes nothing. The hook queues at most 4096 requests and drops the rest;
  the request path never waits.
- `X-Bloem-Edge: name=<label>; lat=<deg>; lon=<deg>; viewer_proto=<h1|h2|h3|udp-direct|cloudflare>`
  counts only when the request's transport peer is inside `clientip.trusted_proxies`.
  Any viewer can send it, so from anyone else it is ignored.
- `CF-Ray` counts under the same trust, or when the peer is a Cloudflare egress
  address. Its colo is placed at its airport.
- Facts are kept six hours per session, in Redis when the server has one
  (`bloem:insights:facts:<session_id>`, a counter per class and the latest facts), and
  in process memory otherwise.
- When Silo's own `media_transport` lands, it replaces the request protocol for the
  server-facing leg.

### Records

- `Observer` follows the events hub's lifecycle events (`session.started`, `paused`,
  `resumed`, `stopped`) on every replica. An event whose `seq` is not newer than the
  record's is ignored, so a repeated or late event never overwrites a newer one.
- It decodes the event into its own `LifecycleEvent`, a copy of the admin wire shape
  pinned by `lifecycle_wire_test.go`: `apiv2` depends on the handlers that use this
  package.
- Records and refusals are kept 90 days; every replica prunes daily. A record with no
  stop is pruned 90 days after its last event, and counts as live for 24 hours.

### Refusals

Refusals reach `RefusalRecorder` from Bloem-owned code only:

- a fresh start: `noteStreamLimit`, deferred by `StartSessionWithFilesContext`;
- a switch the admission decider refuses (for example to a transcode mid-stream):
  Bloem's decider (`policy.NewBloemPlaybackAdmissionDecider`) reports through
  `playback.NoteAdmissionRefusal`. `startPreflight` marks a fresh start's context so
  the decider stays quiet about it, and each refusal is heard once. A switch refusal
  has no profile;
- a switch the household or organization pool refuses: `admitReplacementLocked`;
- a rebuilt session over a limit: the reconstruct reservation's `finish`.

`LimitRefused` only queues, at most 1024. A row is written unless the same person was
refused at the same limit in the last 60 seconds, which `INSERT … WHERE NOT EXISTS`
also enforces across replicas.

### Alerts and quiet hours

- `admin.stream_limit_reached` and `admin.transcode_limit_reached` are on the `limits`
  channel, with the dedupe key of the UTC hour, so a recipient gets at most one per
  person an hour.
- Their audience (`AudienceLimitWatchers`) is every platform administrator plus the
  active admins of the person's organization (`organization_memberships.legacy_role`
  is `admin`).
- An organization admin may register a device and switch on only these two kinds;
  any other gets `422 kind_not_available`. Their device is sent only these kinds, for
  their own organization.
- Quiet hours are stored per device (`quiet_start_minute`, `quiet_end_minute`,
  `quiet_time_zone`, `quiet_allow_kinds`) and applied when a delivery is queued. The
  two reachability kinds are the apps' own, and the apps apply quiet hours to them.
- Servers that keep quiet hours list `admin_push_quiet_hours_v1`.

### Routes

- Platform routes are under `/api/bloem/v2/admin/platform/`: `stream-insights/capabilities`,
  `streams/insights`, `streams/trails`, `streams/transport`, `limits/usage` and
  `limits/refusals`. They need a Platform administrative context; servers that serve them
  list `admin_stream_insights_v1`.
- Platform viewing stats are Silo's `GET /api/v2/admin/insights/*` (Silo #5269). Bloem's own
  `platform/stats/` routes were removed on 2026-11-25 before any client used them: Silo's
  count plays as viewings, cover every session and keep the whole history.
- Organization routes are under `/api/bloem/v2/admin/organization/`: `limits/usage`,
  `limits/refusals`, and `stats/top-people`, `heatmap`, `clients` and `transcode-causes`,
  because Silo's Insights is for platform administrators only. They need that
  organization's administrative context, count only its members, and carry no location or
  transport field; servers list `admin_org_stream_limits_v1`.
- Viewing stats read Silo's `admin_playback_history` read only; transcode causes read
  the stream records' adaptation reasons.
