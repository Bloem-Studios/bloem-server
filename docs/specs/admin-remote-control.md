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
