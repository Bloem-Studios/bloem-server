# Admin app push notifications

Bloem's admin apps (Android and iOS) receive push notifications from a Bloem server. A plain Silo
server cannot send them: its relay cannot reach these apps, and its registration accepts one Apple
topic. The apps keep their own local server-down checks for those servers, and show no other
notification setting.

Everything here is Bloem-only. Silo's `push_devices`, its fan-out and its sender are unchanged, so
a server with no admin device sends exactly what it sent before.

## What goes out

A notification goes out only when something is broken or waiting on the person. The kind ids are
stable keys, used in payloads, switches and analytics.

| Kind | When | Who | Tap opens | Buttons |
|---|---|---|---|---|
| `admin.request_waiting` | a movie or series request is pending (not auto-approved) | admins of the requester's organization | the request | Approve, Decline |
| `admin.scan_failed` | a library scan failed, or a scan found the library's storage unreachable (`dead_root`) | admins | the library | none |
| `admin.backup_failed` | the latest backup run failed or was interrupted (read within six hours of finishing) | admins | backups | none |
| `admin.node_offline` | an enabled node has been unhealthy for two minutes | admins | nodes | none |
| `admin.plugin_failed` | an enabled plugin's resident process reached its failure budget | admins | the plugin | none |
| `admin.server_down`, `admin.server_up` | produced by the apps' own checks, never by the server | the device owner | that server | none |

The two reachability kinds are listed so a device's switch for them is stored on the server, but
`Notify` refuses them: a server that is down cannot push, and the server cannot see another
server's state.

"Admin" means an enabled account with the `admin` role, judged when the event happens, again when a
queued send runs, and again on every content read. A request notice reaches only admins who share an
organization with the requester, which is the same bound Bloem puts on administrator authority over
requests. The other kinds describe the deployment, so every admin hears them.

## Path of one notification

1. An event reaches an emitter in `internal/notifications/bloem_admin_push_emitters.go`.
   - Requests: a decorator the router installs around the request lifecycle notifier.
   - Scan failures: the events hub (`scan.failed`). Live Activities follow every `scan.*` event (see below).
   - Backup, node, plugin and mount state: pollers, once a minute, reading state every replica can see.
2. `Notify` resolves the recipients, drops devices that switched the kind off, and inserts one
   delivery per admin plus one attempt per device in a single transaction. The delivery id is a
   UUIDv5 of kind, subject, occurrence and recipient, so replicas that see one event store it once.
3. The sender claims due attempts with `SKIP LOCKED` and posts to the Bloem relay in `private_alert`
   mode, using the deployment's existing relay credential. Retries follow Silo's push schedule and keep
   one idempotency key per attempt. A token the push service reports as gone disables the device until
   the app registers again.
4. The phone wakes with only a delivery id and fetches the text from this server.

The relay never sees any text. The push carries `{device_id}.{delivery}` as its delivery id: the
device id lets an app with several saved servers pick the right one, because the relay cannot carry a
server id. The relay collapse id is a hash of kind and subject, so a repeating event replaces the
notification already showing instead of stacking.

## Registration and content

Routes are under `/api/bloem/v2/admin/push/` and declared in the native OpenAPI document.

- `POST devices` registers or refreshes an installation. It needs an administrator's access token with a
  login session. The Apple topic must be `com.bloemstudios.admin`. The same `installation_id` keeps its
  device id and switches. A token held by another account or installation moves to this one. It returns a
  display token.
- `PATCH devices/{device_id}/kinds` switches kinds for one of the account's own devices.
- `DELETE devices/{device_id}` removes one of the account's own devices.
- `POST devices/{device_id}/retire` removes a device on its display token alone, for an app that has
  signed out.
- `GET content/{delivery_id}` returns title, body, lock-screen text, the page and item a tap opens, the
  buttons and the badge. It takes the display token, not a login, because the iOS notification extension
  runs while the phone is locked.

The display token opens only those two routes. It is stored as a hash, and a new registration replaces
it. A delivery is readable only by a device it was sent to, for the account it was written for, for 24
hours, and only while the account is still an enabled admin.

## Widget wakes

A wake is a silent push (the relay's `background_wake`; on APNs a background push at priority 5, on FCM a data-only
message) that tells an app with a widget placed to re-read what the widget shows. It has no title, no sound and no
content, and it never opens anything.

- **Who gets one.** Only a device that reported `widgets_placed`, and only for the channels its widgets show:
  `sessions` (now playing), `requests`, `libraries`, `nodes`, `health` (the Needs a look widget). The app reports
  them with `POST devices` (`widgets_placed`, `widget_channels`) or `PUT devices/{device_id}/widgets`
  (`widgets_placed`, `channels`), and again whenever a widget is added or removed. Reporting `widgets_placed` false
  clears the channels and any wake waiting. A server that lists `admin_widget_wakes_v1` accepts the report; only a
  server that also lists `admin_widget_health_v1` accepts `health`. An older server refuses the whole report when it
  names a channel it does not know, so an app leaves `health` out for a server without that token.
- **What triggers one.** A session starting, pausing, resuming or stopping (`sessions`); a request arriving, whether
  it waits for an admin or was auto-approved (`requests`, only to admins of the requester's organization); a scan
  starting, completing, being cancelled or failing (`libraries`); a node going offline or coming back (`nodes`).
  `health` wakes on state changes only: a new failed or interrupted backup run (within six hours of finishing, so a
  restart does not wake for old news), a successful run after a failed one, a plugin failing, and a plugin whose
  failure was reported recovering. The backup and plugin watchers wake before they notify, so the
  `admin.backup_failed` or `admin.plugin_failed` alert covers the wake on the devices it reaches; a device with that
  alert switched off still gets the wake. Progress ticks, traffic, error-log counts and similar figures never
  wake.
- **Rate limit,** applied per device in the database so every replica agrees: changes gather for 30 seconds into one
  wake; a device gets at most one wake every 10 minutes, and the changes held back go out together when the gap has
  passed; and none goes out within 2 minutes after a visible alert push to that device, because the push already
  refreshes the widgets. A wake still waiting when a visible push goes out is dropped as covered by it. A wake is
  claimed once across replicas. If the relay cannot take it, it is tried again after the gap; a token the push
  service reports as gone switches the device off, as for an alert.
- **On the wire.** The delivery id is `{device_id}.widgets-{channels}-{unix seconds}`, with the channel names sorted
  and joined by hyphens, for example `01K….widgets-nodes-requests-1763254800`. The mode is `background_wake`. The
  relay carries no other field, so the reason and the channels travel in this id. The app reads them from it and
  fetches nothing: a wake has no content row, so `GET content` answers 404 for it.
- **Plain Silo servers** have none of these routes, so they send no wakes.

## Live Activities

The server keeps the apps' scan and backup Live Activities current while the app is closed: on iOS through the
relay's `live_activity` mode, which updates an ActivityKit activity by push, and on Android through an ordinary
push that tells the app to redraw its ongoing notification. Code: `internal/notifications/bloem_admin_live_*.go`.

- **Registration.** Under the device routes, with the same administrator login as `POST devices`:
  - `POST devices/{device_id}/live-activities` with `{activity_id, kind, subject_id?, platform, push_token?,
    environment?}` answers 201. `activity_id` is a UUID the app makes. `kind` is `scan` (with the library id as
    `subject_id`) or `backup` (no subject). iOS sends `platform: ios`, the activity's push token and its environment;
    Android sends `platform: android` and no token. The platform must match the device. Registering the same id again
    replaces the token (it rotated) and keeps the activity's lifetime. A device keeps five; a sixth replaces the oldest.
  - `DELETE devices/{device_id}/live-activities/{activity_id}` answers 204 when the activity ends on the phone.
  - `PUT devices/{device_id}/live-activity-start` with `{token, kinds}` stores an Apple device's push-to-start token
    (iOS 17.2+) and the kinds the person allowed, or clears both with `token: null`. Off until the person turns it on.
- **Tokens** are encrypted bound to their row, as device tokens are, and never logged. An activity's row and token go
  when the activity ends, when the device is removed or retired, when the device's login ends or its account stops
  being an admin, and when the push service reports the token gone. A start token goes with its device, on the same
  login and admin rule, and when the push service reports it gone.
- **What the relay sees.** Only the content state `{phase, progress?, processed?, total?, libraryID?, updatedAt}`:
  `phase` is `running`, `finished` or `failed`, `libraryID` is the library's numeric id, `progress` is a fraction from 0
  to 1, the counts are files, and `updatedAt` is whole Unix seconds. A push start adds the attributes type
  `AdminJobAttributes` and `{kind, pushServerID, startedAt}`, where `pushServerID` is the device id the app knows the
  server by. These are built from typed structs whose strings are words and digit ids; a state with anything else (a
  library name, a hostname, an e-mail address, a millisecond time) is refused before a request is built. The app turns
  ids into names from its own data. The relay refuses text by shape as a second line.
- **Following the job.** A scan through its `scan.*` events on the events hub: running while it runs, then `finished`
  (completed or cancelled) or `failed` as the end. A backup through the status the push watchers read each minute:
  running with the fraction done, then the latest run's outcome as the end. A backup's end reaches only activities
  registered before the run finished (plus two minutes), so a new activity is not ended by the previous run.
- **Pacing,** kept per activity in the database so every replica can follow the same job and each state goes once: at
  most one update every 30 seconds at low priority, with the newest state; a change of phase and the end at once at
  high priority; nothing after the end, and nothing past 8 hours after registration (iOS ends the activity then). A
  state equal to the one last sent (its `updatedAt` aside) is not sent again. Each send takes a timestamp larger than
  any the activity was sent, so APNs keeps the newest. An update carries a stale date two minutes on; the end the final
  state and a dismissal 15 minutes on.
- **Push starts** go only to Apple devices that allowed the kind, that have the matching alert switched on
  (`admin.scan_failed` for scans, `admin.backup_failed` for backups) and that do not show the job already: a scan once
  it has run a minute, a backup at once. Each job starts on a device once, claimed in `bloem_admin_live_starts`; a start
  that fails is not retried. The relay shows its fixed generic alert with a start.
- **Android.** No relay feature is involved. The server sends `private_alert` with the delivery id
  `{device_id}.live-{activity_id}-{unix seconds}` at the same pace; the app re-reads the job through the API and
  redraws its notification, never fetching content and never posting an alert for it. iOS sends carry the same id.
- **Relay answers.** A gone activity token deletes that activity only; the device keeps getting alerts. A relay
  without the mode answers `400 invalid_mode`, or `400 unexpected_field` if it predates the mode: live sends then stop
  for an hour on that replica and the server logs it once, keeping the newest state to send afterwards.
  `429 live_activity_rate_limited` (4 live sends per minute per token) holds the activity for its `Retry-After`. Other
  transient failures are tried again after at least 30 seconds.
- **Cleanup.** The hourly push purge deletes activities that ended and were sent their end, activities four hours past
  their lifetime, and start claims older than a job's lifetime.

## Discovery

`GET /api/bloem/v2/capabilities` lists `admin_push_v1` in `feature_tokens` only while the routes are
mounted and the operator has push delivery on for at least one platform. A server without the token is
treated as a plain Silo server by the apps. `admin_widget_wakes_v1` is advertised with it, and says the server
accepts the widget report and sends wakes. `admin_widget_health_v1` is advertised with both, and says the server
also accepts and wakes the `health` channel. `admin_live_activities_v1` is advertised with `admin_push_v1` only while
the operator has turned on `bloem.notifications.admin_live_activities_enabled` (off by default), and says the server
keeps Live Activities current. Without it the apps update their activities themselves while open, as before, and
the Live Activity routes answer 503 to a registration.

## Seams

No Silo-owned file gains a seam that did not already exist for Bloem. The edits to shared files are one
line each, in files `contracts/seams.txt` already declares: `internal/api/router.go` (wraps the request
lifecycle notifier) and `cmd/silo/main.go` (starts the workers). The sender is not modified; the admin
sender builds its own relay request because it needs a device-prefixed delivery id, a badge and a
collapse id that differs from the delivery id.

## Operating notes

- The relay must allow the Apple topic `com.bloemstudios.admin` and give it neutral lock-screen text.
  Until it does, iOS shows the relay's default text and the extension replaces it.
- Every Android app must sit in the relay's one Firebase project, or sends fail with
  `SENDER_ID_MISMATCH`.
- Per-device send limits: the relay allows 30 sends per minute per token. Collapse ids keep a flapping
  subject to one notification.
- Turn on `bloem.notifications.admin_live_activities_enabled` (an admin setting, kept as this server's own on a
  restore) only once the relay serving this server has the `live_activity` mode. Turning it off withdraws the
  capability at once; registered activities stop getting sends, and the apps can still end them and clear their
  start tokens.
