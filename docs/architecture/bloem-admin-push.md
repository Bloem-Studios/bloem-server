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
   - Scan failures: the events hub (`scan.failed`).
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

## Discovery

`GET /api/bloem/v2/capabilities` lists `admin_push_v1` in `feature_tokens` only while the routes are
mounted and the operator has push delivery on for at least one platform. A server without the token is
treated as a plain Silo server by the apps.

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
