# Bloem overlay: Admin API

Bloem additions and overrides for the upstream Silo document [`docs/admin-api.md`](../../admin-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “`GET /api/v2/admin/logs/app` — `level`”, after the paragraph beginning “`level` accepts a comma-separated list, so one request can ask…”. **Bloem adds:**

## `/api/v1/admin/remote` — remote control of playing clients

Server-side control of client apps (`docs/specs/admin-remote-control.md`). These
routes mount only when the playback session manager is wired, beside the
per-session control socket they ride on. A command reaches a client only when
the device advertised it — through `PUT /api/v1/devices/{device_id}/remote-control`
(a device registered to the calling profile; `404` otherwise) or the control
socket's `hello.capabilities.commands` — so clients that never advertise (every
native client) receive nothing. Forgetting a device drops its advertisement. Rules:
`docs/architecture/admin-remote-control.md`.

| Route                                                        | Purpose                                                                              |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| `GET /api/v1/admin/remote/sessions`                          | Live sessions with a `remote_control` block (device, connected, advertised commands) and a `plan_summary`. |
| `POST /api/v1/admin/remote/sessions/{session_id}/commands`   | Send one command: `{name, payload, reason?}` → `201` with the command row (`ttl_seconds` is device-rail only: `400` here). |
| `GET /api/v1/admin/remote/commands/{command_id}`             | Command state: `sent → accepted → done \| rejected`, or `expired`; `rejected_unsupported` / `failed` are terminal at creation. |
| `GET /api/v1/admin/remote/audit`                             | Every command sent, newest first; `session_id`, `issued_by`, `issuer_kind`, `limit`, `offset`. |

Session-scoped names: `pause`, `unpause`, `play_pause`, `seek {position_ms}`,
`set_volume {level}`, `stop {reason?}`, `terminate {reason?}` (reason required),
`set_audio_track` / `set_subtitle_track {track_id} | {off: true}`,
`display_message {title, body, severity, timeout_ms?}`, and `replan {overrides:
{transcode: auto|force|direct, max_bitrate_kbps?, video_codec?, audio_codec?,
container?}, reason}`, which pins the overrides on the session and withdraws the
current plan with `plan_invalidated` so the client reloads without leaving the
player.

Errors: `400 invalid_payload | unknown_command | reason_required | bad_request`,
`403 forbidden` (session outside the admin's tenant scope), `404 not_found`,
`409 session_not_connected | replan_unavailable`, `429 rate_limited` (30 commands
per minute per admin). Household members get the same contract on
`POST /api/v1/profiles/household/sessions/{session_id}/commands` for sessions of
their own account, restricted to the playback commands (no `terminate`, no
`replan`; `403 command_not_allowed`).
