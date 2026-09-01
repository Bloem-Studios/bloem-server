# Admin Remote Control of Clients (S-5)

Server-side control of client apps by a server admin (and, in a reduced form, by household
members): see every device and playback session, and act on them — pause, stop, seek, switch
tracks, force a transcode replan with overrides, push a message, start playback on a device,
collect diagnostics. Status: DRAFT for owner approval, owner direction recorded 2026-09-01.

## Owner direction (binding)

- The goal is **server-admin control of clients**, not LAN pairing. LAN pairing / peer cast stay
  as they are and are out of scope here.
- New control features are **v3-only**: clients that don't advertise support simply never
  receive them. v2 clients keep working untouched; nothing here may break a v2 client.
- Every wire change is additive and optional; the v1 route-contract guard stays green.

## What already exists (evidence, current main `ff9401fc`)

- **Per-session control socket** `GET /api/v1/playback/sessions/{session_id}/control/ws`
  (`internal/api/handlers/session_ws.go`), ticket-authenticated by the playing client. The
  protocol is already an acked command channel: envelopes `hello | command | ack | result |
  event` with `command_id` (`internal/playback/realtime.go`).
- **Command vocabulary already defined** (`internal/playback/realtime.go:26-40`): `pause`,
  `unpause`, `play_pause`, `seek`, `set_volume`, `stop`, `terminate`, `display_message`,
  `server_restarting`, `server_shutting_down`, `play_media`, `set_audio_track`,
  `set_subtitle_track`, plus `plan_invalidated` (`NewPlanInvalidatedCommand`, :429).
- **Nothing sends them.** No admin or user endpoint produces a command; the only producer is the
  plan-invalidation path. The client side (v2 `PlaybackRealtimeClient`) decodes `command` frames
  and can `ack`/`result`, but does not execute them.
- **Device registry** `/api/v1/devices/{device_id}` (+ `/settings`), admin views of users' devices
  and auth sessions, **household sessions** `GET /api/v1/profiles/household/sessions`
  (`router.go:2607`), and the per-profile realtime `notifications` channel that every logged-in
  client holds while in the foreground.

So the missing pieces are: the **sender side** (admin + household endpoints), **device-scoped
delivery** for clients that are not currently playing, a **capability handshake** so only
supporting clients get commands, **audit**, and the **admin UI**.

## A. Capability handshake

Clients that support remote control say so, per device, in the existing device registration /
device-settings surface (additive field):

```json
{"remote_control": {"version": 1, "commands": ["pause","unpause","seek","stop","set_audio_track",
 "set_subtitle_track","set_volume","display_message","play_media","replan","collect_diagnostics",
 "refresh_settings"]}}
```

The server never sends a device a command it did not list. A device with no `remote_control`
block (every v2 client) is shown in the admin UI as "not controllable" and receives nothing.

## B. Delivery rails

1. **Session-scoped** (existing socket): commands aimed at a live playback session go over
   `/playback/sessions/{id}/control/ws` exactly as the protocol already defines. Reuse `command`
   / `ack` / `result` unchanged.
2. **Device-scoped** (new, additive): commands aimed at a device that is online but not playing
   go over the per-profile realtime `notifications` channel as a new event type
   `device.command` carrying the same envelope `{command_id, name, payload}`. The client acks via
   `POST /api/v1/devices/{device_id}/commands/{command_id}/ack` with `{status: accepted |
   rejected | done, error?}`. Unknown event types are already ignored by every client (forward-
   compat contract), so v2 clients are unaffected.
3. **Offline devices**: device-scoped commands are queued with a TTL (default 15 minutes,
   admin-settable per command up to 24 h) in a `device_commands` table and delivered on the
   device's next channel connect. Session-scoped commands are never queued (the session is gone).

## C. Command set (server-validated payloads)

| Name | Scope | Payload | Notes |
|---|---|---|---|
| `pause` / `unpause` / `play_pause` | session | — | existing |
| `seek` | session | `{position_ms}` | existing |
| `set_volume` | session | `{level: 0..100}` | existing |
| `stop` | session | `{reason?}` | existing; client returns to the detail screen |
| `terminate` | session | `{reason?}` | existing; server also tears the session down |
| `set_audio_track` / `set_subtitle_track` | session | `{track_id}` or `{off: true}` | existing |
| `display_message` | session or device | `{title, body, severity, timeout_ms?}` | existing name; device scope new |
| `play_media` | device | `{item_id, position_ms?, audio_track?, subtitle_track?}` | existing name; device scope new — this is "play on that device" |
| `replan` | session | `{overrides: {transcode: auto|force|direct, max_bitrate_kbps?, video_codec?, audio_codec?, container?}, reason}` | **new** — the admin "fix this playback" command: the server re-plans the session with the overrides pinned, then sends `plan_invalidated` as today; the client reloads the new plan without leaving the player |
| `collect_diagnostics` | device | `{include_logs: bool, note?}` | **new** — client uploads a diagnostics bundle to the self-hosted collector and returns the report id in `result` |
| `refresh_settings` | device | — | **new** — client re-fetches device/profile settings |
| `sign_out` | device | `{reason?}` | **new** — client drops its tokens and returns to login (pairs with the existing admin auth-session revoke) |

Every command carries `issued_by` (admin user id or household profile id) and `reason`
(free text, shown to the user for `stop`/`terminate`/`sign_out`/`display_message`).

## D. Endpoints (all additive)

Admin (same admin auth group as `/admin/notifications/*`):

- `GET /api/v1/admin/remote/devices` — every registered device with online state, current
  session (if any), capability block, app version, last seen.
- `GET /api/v1/admin/remote/sessions` — live playback sessions with plan summary (direct vs
  transcode, codecs, bitrate, buffer health from the client's last progress report).
- `POST /api/v1/admin/remote/sessions/{session_id}/commands` and
  `POST /api/v1/admin/remote/devices/{device_id}/commands` — body `{name, payload, reason?,
  ttl_seconds?}` → `201 {command_id, state: sent|queued|rejected_unsupported}`.
- `GET /api/v1/admin/remote/commands/{command_id}` — `{state: queued|sent|accepted|rejected|
  done|expired, result?, error?, timestamps}`.
- `GET /api/v1/admin/remote/audit` — who sent what to whom, outcome.

Household (authenticated user, own household only, reduced set: `pause`, `unpause`, `seek`,
`stop`, `set_volume`, `set_audio_track`, `set_subtitle_track`, `play_media`,
`display_message`):

- `POST /api/v1/profiles/household/sessions/{session_id}/commands` and
  `POST /api/v1/profiles/household/devices/{device_id}/commands`, same body and response.

Capability payload grows `{"remote_control": {"admin": true, "household": true}}` so v3 clients
know the sender side exists (they still only *receive* per their own advertised list).

## E. Admin UI (web, in the server's admin area)

"Devices & Sessions" page: table of devices (online/offline, controllable/not, playing what,
plan summary, buffer health), row actions per capability, a "Fix playback" dialog for `replan`
(transcode mode, bitrate cap, codec pins, reason), message dialog, and a command history drawer
that polls `GET /admin/remote/commands/{id}` until terminal. Product copy, no infra story.

## F. Audit, limits, safety

- Every command is persisted (`remote_commands` table: id, scope, target, name, payload,
  issued_by, reason, state, result, created/sent/acked/finished at, expires_at).
- Rate limit: 30 commands / minute / admin; `sign_out` and `terminate` require a reason.
- Tenancy: admins only see and control devices of users in their tenant scope (reuse the
  admin-user scoping the `/admin/users/{id}/devices` routes already apply).
- No PII in logs beyond ids; `reason` text is shown to the user, so it is product copy.

## G. Delivery order

S-5a: capability handshake + `remote_commands` table + session-scoped sender endpoints (admin +
household) + audit + `replan` command → the "fix this playback" path works end to end with a v3
client. S-5b: device-scoped rail + queue + `play_media`/`display_message`/`collect_diagnostics`/
`refresh_settings`/`sign_out` on device scope. S-5c: admin UI page. Each an independent
PR-sized package with route-contract tests.

## Client-side counterpart

`bloem-android-v3/docs/plan/tasks/WP-24-remote-control.md` (engine-level command executor for
both session and device rails; capability advertisement; user-facing surfaces for messages and
reasons). v2 clients are explicitly NOT touched.

## Implementation notes (S-5a) — session rail, on `feat/s5a-remote-control`

Everything below is additive; the v1 route-contract goldens are unchanged (every new route mounts
only when the playback session manager is wired, exactly like the control socket itself, which the
golden fixtures do not do). Device rail (§B.2/B.3), device-scoped commands and the admin UI are
S-5b/S-5c and not touched. The durable rules are in
[docs/architecture/admin-remote-control.md](../architecture/admin-remote-control.md); the notes
here are the how.

### Storage

Migration `20260901200034_remote_commands.sql`:

- `remote_commands`: `id`, `scope` (`session|device`), `target_session_id`, `target_device_id`,
  `target_user_id`, `target_profile_id`, `tenant_id`, `name`, `payload jsonb`, `issued_by`
  (`user:<id>` for admins, `profile:<id>` for household members — ids only), `issuer_kind`
  (`admin|household`), `reason`, `state`, `result jsonb`, `error`, `created_at/sent_at/acked_at/
  finished_at/expires_at`. Indexes on `(target_session_id, state)`, `(target_device_id, state)` and
  `created_at DESC`. Terminal rows never transition again (a late duplicate result is ignored).
- `remote_device_capabilities (user_id, profile_id, device_id) → version, commands jsonb`: the
  persisted §A handshake. Kept beside the audit, not on `user_devices`, so the Silo-identical
  device registry paths (Postgres and SQLite) stay untouched. No row = not controllable.

### Capability handshake (§A)

Two additive ways in, same row: `PUT /api/v1/devices/{device_id}/remote-control` with
`{"version":1,"commands":[...]}` (profile-scoped; the device must be registered to the calling
profile — 404 otherwise, as the device routes answer — and names must be command names the server
knows, including the S-5b device names so a v3 client can announce its full list today), and the
control socket's existing `hello.capabilities.commands`, which the server persists against the
session's device when non-empty. The upstream hello validator only knows the upstream socket
vocabulary, so the handler strips the remote-control-only names (`replan`, `collect_diagnostics`,
`refresh_settings`, `sign_out`) before running it and hands the full list to the remote service; a
hello listing the §A example connects, and a name unknown to both vocabularies still fails the hello
as upstream does. A v2 hello (empty list) leaves the device untouched. `DELETE /devices/{device_id}`
(forget) deletes the row. The
session's device is `X-Silo-Device-Id` at `/playback/start` (falling back to the session claims'
`device_id`), recorded as `Session.DeviceID`. The server refuses — `state: rejected_unsupported`,
row written, nothing sent — any command the device did not list.
`GET /api/v1/notifications/capabilities` grows `remote_control: {admin: true, household: true}`.

### Sender

`internal/remote` — `ValidateSessionPayload` (spec §C table, strict decode, unknown fields
rejected; `terminate` needs a non-empty reason; `set_audio_track` cannot be `off`), `Store`
(Postgres + memory), `Service.SendToSession`: validate → session lookup → scope check → rate limit
(30/min/issuer through the shared `ratelimit` limiter, key `remote:<kind>:<issuer>`) → device
capability check → `sent` row → deliver. Delivery goes through the playback handler's existing
`CommandDispatcher`/`RealtimeHub`/`CommandTracker` path via `NewPlaybackRemoteSender`; no second
socket registry. Missing or closed socket → **409 `session_not_connected`** and the row is left
`failed`. The socket's `ack` → `accepted` (only from the session the command was sent to),
`result` → `done|rejected` (with the client's error), and the 8 s command deadline → `expired`
(for `stop`/`terminate` the server also tears the session down, as the admin control routes
already do). An unanswered command that never hit its deadline reads as `expired` once
`expires_at` (60 s) has passed, and every command still open when its session ends is expired
then. `ttl_seconds` in the request body belongs to the device rail: a non-zero value on a session
command is `400 invalid_payload`, not ignored.

### `replan` (§C, new)

`{name:"replan", payload:{overrides:{transcode: auto|force|direct, max_bitrate_kbps?, video_codec?,
audio_codec?, container?}, reason}}` pins `playback.PlanOverridesV3` on the session and emits
`plan_invalidated` (`reason: admin_replan`, `plan_id` = the current plan) with the command's own
`command_id`, through the same negotiation the copy-safety notifier applies: the attempt must have
negotiated `plan_invalidated_v1` and the socket must be live, otherwise **409 `replan_unavailable`**.
The client's next `POST /playback/{session_id}/replan` sees the durable start request narrowed by
`ApplyPlanOverridesV3` (bandwidth cap lowered, never raised; `direct` clears cap/estimate/metered
and asks for original quality; codec/container pins filter the advertised decode capabilities, so
they decide what counts as directly playable — the transcode recipe stays the server's h264/aac)
plus `PlannerInputV3.ForceTranscode` for `force`. Household members cannot send `replan`.

The pin is not process memory: the replan handler reads the session's newest replan command row in
`sent | accepted | done` from the remote store (`Service.OpenReplanOverrides`) and derives the
overrides from its payload, so an instance that never delivered the command applies it when it
serves the client's `/replan`. What stays single-instance is the socket delivery itself: the
`RealtimeHub` is per process, so the admin's send must land on the instance holding the session's
socket (a miss is `409 session_not_connected`). The command is marked **`done`** when the client's
replan commits (`OnSessionReplanned`, result `{status, plan_id}`). That fires on *any* client replan
for the session — a seek re-anchor or failure recovery that happens to follow the command counts —
which is acceptable because every replan after the pin plans with the overrides applied, so the
observable outcome ("the session now runs the admin's plan") is the same whichever replan came
first. `done` keeps the pin (later client replans keep the admin's plan); `rejected`, `expired`
(deadline, TTL, or session end) and `failed` release it; a newer replan replaces it.

### Endpoints (§D)

Admin, inside the `requireActingAdmin` group: `GET /admin/remote/sessions` (the admin sessions
loader rows plus `remote_control {device_id, connected, controllable, commands}` and
`plan_summary`; buffer health from progress reports is not surfaced yet), `POST
/admin/remote/sessions/{session_id}/commands` → `201` command row, `GET
/admin/remote/commands/{command_id}`, `GET /admin/remote/audit?session_id&issued_by&issuer_kind&
limit&offset`. Tenancy reuses `adminResourceOrganization` (the scoping the tenant-member device
routes apply): when set, sessions whose `TenantID` differs are hidden and refused with 403.
`GET /admin/remote/devices` is deferred to S-5b with the device rail. Household: `POST
/profiles/household/sessions/{session_id}/commands`, same body, primary profile or admin only
(the household sessions listing's rule), sessions of the caller's own account only, reduced set
(`pause unpause play_pause seek stop set_volume set_audio_track set_subtitle_track
display_message`) → 403 `command_not_allowed` otherwise.

### Command contract

Request (`POST /api/v1/admin/remote/sessions/{session_id}/commands`, from
`TestRemoteControlAdminSendsAndTracksAckResultOverFakeSocket`):

```json
{"name": "seek", "payload": {"position_ms": 90000}, "reason": "skip recap"}
```

Response `201` (and the shape of `GET /admin/remote/commands/{command_id}` and each audit row):

```json
{
  "command_id": "5f1c…",
  "scope": "session",
  "session_id": "sess-…",
  "device_id": "device-1",
  "user_id": 7,
  "profile_id": "profile-1",
  "name": "seek",
  "payload": {"position_ms": 90000},
  "issued_by": "user:1",
  "issuer_kind": "admin",
  "reason": "skip recap",
  "state": "sent",
  "created_at": "2026-09-01T20:00:00Z",
  "sent_at": "2026-09-01T20:00:00Z",
  "expires_at": "2026-09-01T20:01:00Z"
}
```

`state` walks `sent → accepted → done | rejected`, or `expired`; `rejected_unsupported` (device did
not list the command) and `failed` (delivery error) are terminal at creation. After a result,
`result` is `{"status":"completed"}` / `{"status":"rejected"}` (`error` carries the client's
error), and for `replan` `{"status":"completed","plan_id":"…"}`. Frame on the wire, unchanged
protocol:

```json
{"type": "command", "command_id": "5f1c…", "session_id": "sess-…", "name": "seek",
 "reason": "skip recap", "issued_by": {"kind": "admin"}, "deadline_ms": 8000,
 "payload": {"position_ms": 90000}}
```

Replan request → same response shape with `name: "replan"`; the client receives
`{"type":"command","name":"plan_invalidated","payload":{"reason":"admin_replan","plan_id":"…"}}`.

Errors: `400 invalid_payload | unknown_command | reason_required | bad_request`,
`403 forbidden | command_not_allowed`, `404 not_found`, `409 session_not_connected |
replan_unavailable`, `429 rate_limited`.

Capability advertisement (`PUT /api/v1/devices/{device_id}/remote-control`):

```json
{"version": 1, "commands": ["pause", "seek", "replan", "collect_diagnostics"]}
```
→ `200 {"device_id": "device-9", "version": 1, "commands": ["collect_diagnostics", "pause", "replan", "seek"]}`
