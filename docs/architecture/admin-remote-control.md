# How remote control works

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`. Remote control is a Bloem addition: Silo's playback connection
> carries the commands, and Bloem adds who may send them, the app's list of accepted
> commands, and the record of each one. This page explains it in plain language; the
> invariants are in the [For developers](#for-developers) section.

**Remote control** lets an administrator, or a household's main profile, control an
app that is playing something: pause it, seek, stop it, change the volume or the audio
and subtitle tracks, or show a message on screen. This page walks through who can send
what, how a command reaches the app, the states a command goes through, and what the
server will never do.

## Who can send what

![Who may send which commands, the path of one command from sender to playing app with its two refusals, and the command's states](images/admin-remote-control/command-journey.svg)

- **A household's main profile** can control only its own account's playing apps, with
  a reduced set: pause, resume, play or pause, seek, stop, volume, audio track, subtitle
  track and show a message.
- **An administrator** can control the sessions in their own scope. On top of the
  household set, they can **end a session**, which needs a reason the viewer sees, and
  ask the app to **replan**: pick a playback plan again, for example a lower bit rate.

## How a command reaches the app

1. **The app says what it accepts.** When it starts playing, or when it registers, an
   app lists the commands it can carry out. An app that lists nothing is never sent
   anything.
2. **The sender sends one command** to one playing session.
3. **The server checks** that the sender may reach that session and send that command,
   and that the app listed it. A command the app did not list is recorded as refused
   and never sent.
4. **The server passes it on** over the app's live **playback connection**. If the app
   has no live connection, the command fails at once: it is never kept for later.
5. **The app answers**: accepted, then done, or rejected.

## What a command goes through

A command starts as **sent**, becomes **accepted**, and ends as **done** or
**rejected**. If the app does not answer within 8 seconds, the command passes its
60-second expiry, or the session ends first, it ends as **expired**. Once a command
has ended, nothing changes it again.

## What the server will never do

- **Send a command the app did not list.**
- **Queue a command** for an app that is not connected.
- **Make playback better than the app could ask for itself.** A replan can only narrow
  the plan, such as a lower bit rate, never raise it.
- **Write personal details to its logs.** The record of each command holds only IDs.

## Example: a stuck stream

This example is made up. Anna's son is watching on the living-room TV, and the picture
keeps stalling. From her phone, as the household's main profile, Anna sees the session
and sends **pause**. The TV listed pause, so the server passes it on; the TV answers
accepted, then done. Later, a platform administrator sees that the same TV keeps
buffering and sends **replan** with a lower bit rate. The TV picks a new plan within
that cap, and keeps it until the session ends.

## Who can change what

| Who | What they manage |
| --- | --- |
| Administrator | Listing sessions in their scope (every session for a platform administrator, one organization's when acting for it), sending any session command, ending a session with a reason, replanning, reading the record of commands. |
| An account's main profile (or an admin account) | The household set of commands, on the account's own sessions. |
| The playing app | Which commands it accepts. |

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Remote control | `internal/remote` (sender) and `internal/api/handlers/remote_control.go` (routes) |
| Playback connection | Silo's per-session playback control socket, `internal/api/handlers/session_ws.go` |
| The app's list of commands | a device's remote-control capability, table `remote_device_capabilities`, or `hello.capabilities.commands` on the socket |
| A command and its record | a `remote_commands` row; states in `remote.State` |
| Ending a session | the `terminate` command |
| Replan | the `replan` command, which reaches the app as Silo's `plan_invalidated` |

## For developers

- **API:** the routes are still on Silo's `/api/v1`:
  [Bloem's routes on Silo's `/api/v1`](../bloem/api/v1-routes.md#remote-control) lists
  `PUT /api/v1/devices/{device_id}/remote-control`,
  `GET /api/v1/admin/remote/sessions`,
  `POST /api/v1/admin/remote/sessions/{session_id}/commands`,
  `POST /api/v1/profiles/household/sessions/{session_id}/commands`,
  `GET /api/v1/admin/remote/commands/{command_id}` and `GET /api/v1/admin/remote/audit`,
  with bodies and error codes. Only Bloem's phone apps use them; the web app uses Silo's
  `/api/v2` session commands. Native routes, and remote-control discovery in
  `/api/bloem/v1/capabilities`, are added when the apps move
  ([leaving Silo's `/api/v1`](bloem-leaving-silo-v1.md)). Until then the phone apps
  discover it through the `remote_control` block (`admin`, `household`) of
  `GET /api/v1/notifications/capability`.

### History

Remote control is "S-5a" in Bloem's September 2026 plan series: first commit
`e1efec046` (2026-09-02), review round `9f299bf86` the same day, and the first version
of this page in `3136d0848` (2026-09-02). `internal/remote` and `remote_control.go` are
Bloem's; Silo's playback control socket (`session_ws.go`, a ledgered seam in
`contracts/seams.txt`) carries the commands. `5ee7e9455` (2026-09-23) moved Bloem's
in-body edits of Silo handlers into hooks, including the hello filter in
`internal/api/handlers/bloem_session_ws_ext.go`. The original spec, now a historical
snapshot, is [`docs/specs/admin-remote-control.md`](../specs/admin-remote-control.md);
this page records the rules that must survive it.

The device rail ("S-5b": queued device commands such as `play_media` and `sign_out`) is
not implemented; a device may already advertise its command names.

### Wiring

Server-issued commands to client apps: the sender side lives in `internal/remote`,
delivery rides the per-session playback control socket
(`internal/api/handlers/session_ws.go`), and the admin and household endpoints are in
`internal/api/handlers/remote_control.go`. The admin routes are mounted by
`mountBloemLegacyAdminV1` (`internal/api/router_bloem_wiring.go`); the household and
device routes sit in Silo's router (`internal/api/router.go`).

### Rules

- **No capability row, not controllable.** A device receives a command only if it
  advertised that command's name, either through
  `PUT /api/v1/devices/{device_id}/remote-control` (the device must be registered to the
  calling profile) or through the control socket's `hello.capabilities.commands`. The
  advertisement is stored in `remote_device_capabilities` keyed by
  `(user_id, profile_id, device_id)`; forgetting the device deletes it. A command the
  device did not list is recorded as `rejected_unsupported` and never sent.
- **v2 clients receive nothing.** A hello with an empty command list (every v2 client)
  leaves the device untouched, and the device registry itself is never consulted for
  capability. The upstream socket vocabulary and its validator are unchanged: the API
  layer strips the remote-control-only names (`replan`, the device-rail names;
  `upstreamHelloCommands`) before the upstream validator runs and hands the full list
  to the remote store, which validates it against its own vocabulary.
- **Household members control their own account only,** from the primary profile (or
  an admin account), with the reduced set `pause unpause play_pause seek stop
  set_volume set_audio_track set_subtitle_track display_message`. `terminate` and
  `replan` are admin-only; `terminate` (and, when it lands, `sign_out`) require a
  reason.
- **Admins see and control sessions in their tenant scope only**
  (`adminResourceOrganization`, the scoping the tenant-member device routes apply).
- **State machine.** `sent → accepted → done | rejected`, or `expired` (the 8 s socket
  deadline, the 60 s `expires_at`, or the session ending first); `rejected_unsupported`
  and `failed` are terminal at creation. Terminal rows never transition again; a late
  duplicate result is ignored. Only the session a command was sent to can ack or
  answer it. The 8 s deadline is `playback.CopySafetyInvalidationDeadline` unless
  `PlaybackHandler.RemoteCommandDeadline` overrides it; the 60 s expiry is
  `remote.DefaultConfig().CommandTTL`.
- **Session commands are never queued.** No live control socket on the target session
  means `409 session_not_connected`; the row is written as `failed`. Queueing belongs
  to the device rail only.
- **The replan pin is durable; delivery is not.** An admin `replan` pins its overrides
  on the session by way of its own command row: the client's next `/replan` reads the
  newest replan command in `sent | accepted | done` from the store, so any API instance
  serving that replan applies it, and the pin outlives the client's first replan (seek
  re-anchor and recovery keep the admin's plan) until the session ends or a newer
  replan replaces it. The socket delivery itself is per instance — the `RealtimeHub`
  only knows the sockets connected to the process that sends — so the sending instance
  must be the one holding the session's socket.
- **Overrides only narrow.** A pinned bandwidth cap is never raised above the client's
  own; codec and container pins remove decode capabilities, never add them; every plan
  an override produces is one the client could have negotiated itself.
- **Audit carries IDs only.** `issued_by` is `user:<id>` or `profile:<id>`; `reason` is
  shown to the user and is product copy, never logged with personal data.
- **Rate limit.** Each issuer may send 30 commands a minute
  (`remote.DefaultConfig().PerMinute`); beyond that, `429 rate_limited`.

### Related pages

- [How apps connect](bloem-apps.md#remote-control): the plain overview.
- [Admin remote control spec](../specs/admin-remote-control.md) (a historical record).

The picture is an SVG file under `images/admin-remote-control/`, drawn by
`scripts/bloem/pictures/admin_remote_control.py` through
`scripts/bloem/draw-pictures.py`. Redraw it there, not by hand.
