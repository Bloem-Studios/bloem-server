# Admin remote control of clients — invariants

> **Provenance.** Bloem addition ("S-5a" in Bloem's September 2026 plan series): first
> commit `e1efec046` (2026-09-02), review round `9f299bf86` the same day, and this page
> in `3136d0848` (2026-09-02). `internal/remote` and `remote_control.go` are Bloem's;
> Silo's playback control socket (`session_ws.go`, a ledgered seam in
> `contracts/seams.txt`) carries the commands. Later: `5ee7e9455` (2026-09-23) moved
> Bloem's in-body edits of Silo handlers into hooks, including the hello filter in
> `internal/api/handlers/bloem_session_ws_ext.go`. **Status:** current at `e852f92bf`.
> The device rail ("S-5b": queued device commands such as `play_media` and `sign_out`)
> is not implemented; a device may already advertise its command names.

Server-issued commands to client apps: the sender side lives in `internal/remote`, delivery
rides the per-session playback control socket (`internal/api/handlers/session_ws.go`), and the
admin/household endpoints are in `internal/api/handlers/remote_control.go`. The original spec,
now a historical snapshot, is [`docs/specs/admin-remote-control.md`](../specs/admin-remote-control.md);
this page records the rules that must survive it.

## Rules

- **No capability row, not controllable.** A device receives a command only if it advertised
  that command's name, either through `PUT /api/v1/devices/{device_id}/remote-control` (the
  device must be registered to the calling profile) or through the control socket's
  `hello.capabilities.commands`. The advertisement is stored in `remote_device_capabilities`
  keyed by `(user_id, profile_id, device_id)`; forgetting the device deletes it. A command the
  device did not list is recorded as `rejected_unsupported` and never sent.
- **v2 clients receive nothing.** A hello with an empty command list (every v2 client) leaves the
  device untouched, and the device registry itself is never consulted for capability. The
  upstream socket vocabulary and its validator are unchanged: the API layer strips the
  remote-control-only names (`replan`, the device-rail names; `upstreamHelloCommands`) before the upstream validator runs
  and hands the full list to the remote store, which validates it against its own vocabulary.
- **Household members control their own account only,** from the primary profile (or an admin
  account), with the reduced set `pause unpause play_pause seek stop set_volume set_audio_track
  set_subtitle_track display_message`. `terminate` and `replan` are admin-only; `terminate` (and,
  when it lands, `sign_out`) require a reason.
- **Admins see and control sessions in their tenant scope only** (`adminResourceOrganization`,
  the scoping the tenant-member device routes apply).
- **State machine.** `sent → accepted → done | rejected`, or `expired` (the 8 s socket deadline,
  the 60 s `expires_at`, or the session ending first); `rejected_unsupported` and `failed` are
  terminal at creation. Terminal rows never transition again; a late duplicate result is ignored.
  Only the session a command was sent to can ack or answer it.
- **Session commands are never queued.** No live control socket on the target session means
  `409 session_not_connected`; the row is written as `failed`. Queueing belongs to the device rail
  only.
- **The replan pin is durable; delivery is not.** An admin `replan` pins its overrides on the
  session by way of its own command row: the client's next `/replan` reads the newest replan
  command in `sent | accepted | done` from the store, so any API instance serving that replan
  applies it, and the pin outlives the client's first replan (seek re-anchor and recovery keep the
  admin's plan) until the session ends or a newer replan replaces it. The socket delivery itself
  is per instance — the `RealtimeHub` only knows the sockets connected to the process that sends —
  so the sending instance must be the one holding the session's socket.
- **Overrides only narrow.** A pinned bandwidth cap is never raised above the client's own;
  codec/container pins remove decode capabilities, never add them; every plan an override
  produces is one the client could have negotiated itself.
- **Audit carries ids only.** `issued_by` is `user:<id>` or `profile:<id>`; `reason` is shown to
  the user and is product copy, never logged with PII.
