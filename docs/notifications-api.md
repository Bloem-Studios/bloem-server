# Notification inbox API

The v2 inbox uses the existing durable delivery rows and preference store. All
routes below `/api/v2/notifications` require an authenticated, verified profile
and retain the demo write guard. Websocket transport, email links, and
push-registration protocol retain their existing shapes.

| Method and suffix | Result |
| --- | --- |
| `GET /capabilities` | Existing in-app, Apple/Android push, web push, webhook, email, and Discord availability |
| `GET /` | Newest-first `items`, `page`, and signed `read_cutoff` |
| `GET /sync` | Ascending `items`, `page`, `sync_cursor`, `unread_count`, and `initial_snapshot` |
| `GET /{id}` | One delivery; another profile's delivery is 404 |
| `GET /unread-count` | `{ "count": 0 }` |
| `POST /{id}/read` | 204; already read succeeds, unknown delivery is 404 |
| `POST /read-all` | 204; body requires `{ "through": "<read_cutoff>" }` |
| `GET /preferences` | Profile ID and five preference booleans |
| `PUT /preferences` | Updated preferences; omitted fields retain their values |

The inbox list accepts `status=all|unread`, `limit` up to 200, and `cursor`.
A cursor binds the acting account/profile, filter, page size, last delivery tuple,
and original newest-delivery boundary. `page.has_more` is based on a lookahead
row. The final page omits `page.next_cursor`. An empty inbox still supplies a
signed cutoff describing an empty set.

Delivery IDs and linked library/item IDs are strings. Public timestamps use UTC
milliseconds; the signed cursors retain the database timestamp precision. The
`reason_flags` object retains event-specific data used by existing clients.

Delivery insertion serializes each profile's timestamp allocation through a
database row lock held until commit. A trigger assigns `created_at` strictly
above that profile's previous committed timestamp, even when a transaction
started earlier or the clock moves backwards. The boundary survives delivery
retention. A later delivery therefore cannot commit behind an observed cutoff.
Fanout transactions lock their complete recipient profile union in sorted order
before inserting; unrelated profiles can progress independently. Existing rows
retain their timestamps, and migration seeds the boundary from their maximum.

## Fixed read cutoff

Capture the displayed list's `read_cutoff` when the user chooses to mark the inbox
read. Reusing the same body only marks deliveries at or before that original
`(created_at, id)` boundary. It cannot expand to newer deliveries on retry. An
empty cutoff remains a no-op. Tokens from another account/profile or operation
are rejected; clients must not substitute a fresh cutoff after an uncertain
response without a new user intent.

The database update is authoritative. Best-effort `notification.read` events for
this operation contain `profile_id`, `through_created_at` at full precision, and
`through_id`; they do not claim `all: true`. Clients reread lists/counts for these
events rather than compare rounded public timestamps. Existing single-ID read
and legacy all-read events retain their shapes. No durable websocket delivery is
promised.

## Forward sync

Without a cursor, sync returns the existing bounded newest snapshot in ascending
order and sets `initial_snapshot: true`. It is not a complete historical inbox
export; use the list for older pages. Persist `sync_cursor` even when
`page.has_more` is false. An empty initial snapshot also supplies a checkpoint.

Subsequent calls pass that checkpoint as `cursor`, return newer deliveries in
ascending order, and set `initial_snapshot: false`. Continue immediately through
`page.next_cursor` while `page.has_more` is true. When caught up, retain
`sync_cursor` for the next wake or reconnect. The page size is part of the cursor
scope; legacy v1 cursors cannot be reused.

## Preference writes

`enabled`, `notify_favorites`, `notify_watchlist`, `notify_continue_watching`, and
`notify_next_up` are optional booleans. Explicit false is preserved; v2 rejects
null. Both API versions now apply partial fields in one database statement,
preventing independent concurrent toggles from overwriting each other. Missing
preference rows retain the existing all-enabled defaults.

The web captures account/profile authority for requests and mutations, validates
pagination, and surfaces initial and partial failures. It does not automatically
retry mutations or optimistically mark every cached delivery read.

### API v2 Apple push display

`GET /api/v2/notifications/push/apple/display/{delivery_id}`
(`getNotificationApplePushDisplay`) returns compact display metadata from the
same delivery row and renderer as the bridge endpoint. The response fields are
`delivery_id` (string), `title`, optional `body` and `thread_id`, `category`, and
`url`. Responses use `Cache-Control: no-store`; missing or other-profile
notifications return a 404 problem. Delivery IDs must be UUIDs.

The route accepts ordinary bearer/API-key authentication with `X-Profile-Id`,
or the existing Apple display token in the Authorization header. A display
token binds its own profile, ignores a supplied profile header, and requires a
valid login session and a still-owned profile. It is rejected by other API v2
operations. Query-string display credentials are not accepted. Existing
pre-auth and post-auth rate limits apply. This read neither marks a notification
read nor sends a push. Apple registration and display-token issuance retain
the bridge contract until their separate migration is accepted.

The Apple notification extension is the consumer; Android does not call this
Apple display endpoint. Jellyfin compatibility has no equivalent display-token
flow and needs no route change.

### API v2 administrator test push

`POST /api/v2/admin/notifications/push/apple/test`
(`testAdminApplePushNotification`) and
`POST /api/v2/admin/notifications/push/fcm/test`
(`testAdminAndroidPushNotification`) use acting-administrator authorization.
The body requires `profile_id` and optionally selects `server_device_id`; both
identifiers are strings. An omitted device selector retains the existing
platform-specific device selection behavior.

Each request calls the existing test dispatcher once. It creates and claims a
new outbox attempt, invokes the configured sender, and returns HTTP 200 with
`attempt_id`, `push_device_id`, `server_device_id`, `outcome`, and any existing
relay/upstream diagnostic fields. A `retrying` or `failed` outcome is still a
successful HTTP report of that attempt, not proof that a push was delivered.
Background attempt recovery and sender retries retain their existing semantics.
These operations are `non_retryable`: clients must not repeat a lost request
automatically because another request creates another test attempt.

Invalid input returns a 422 problem, a missing target a 404, and unavailable
push delivery a 503. Demo restrictions and no-store responses apply. No current
web, Apple, or Android caller invokes these administrator test routes; this port
adds no test-send UI. Jellyfin compatibility has no corresponding operation.

### API v2 relay administration

`POST /api/v2/admin/notifications/push/relay/register`
(`registerAdminNotificationRelay`) and
`DELETE /api/v2/admin/notifications/push/relay`
(`clearAdminNotificationRelay`) require acting-administrator authorization.
Registration accepts an optional `relay_url` and otherwise uses the existing
default. It preserves relay origin allowlisting, initial registration versus
credential rotation, explicit re-registration after rejection, and atomic
credential persistence. Responses expose only relay/deployment identifiers,
key prefix, configured status, optional request/topics metadata, and an
`expires_at` UTC instant with millisecond precision. The reusable key is never
returned. Clearing removes the local credential and returns a bodyless 204.

Both operations are `non_retryable`. A repeated registration can rotate again;
a delayed clear can remove a newer credential. The existing web controls
capture administrator authority, permit one in-flight command, and disable
automatic authentication replay. A changed authority discards the response.
An administrator must explicitly decide whether to repeat an uncertain command.
This migration does not introduce generation guards or promise safe replay.

Relay errors become v2 problems, with `Retry-After` retained when supplied.
Bridge 400 validation becomes 422 and bridge 502 upstream failures become the
shared 500 `internal_error`; other existing supported statuses retain their
meaning. V1 response statuses, error codes, timestamp format, and headers remain
unchanged. Native clients and Jellyfin compatibility do not manage relay
credentials and need no consumer change.

### API v2 email and Discord preferences

`GET` and `PUT /api/v2/notifications/email-preferences` read and set the acting
profile's email mode. The response retains `mode`, `custom_email`,
`pending_email`, and `can_edit_address`. Each profile uses its own verified
address; there is no login-account email fallback. Address verification and
removal remain separate bridge operations pending their durable dispatch and
callback migration.

`GET` and `PUT /api/v2/notifications/discord-preferences` read and set the login
account's Discord mode. A selected profile is optional; when supplied, the
existing viewer/PIN gate still validates it. Responses retain `linked`, optional
`discord_username` and `link_failure`, and `mode`. The underlying Discord user
identifier and credentials are not returned.

Both mode writes accept only `mode`: `off`, `per_episode`, `daily_digest`, or
`per_episode_and_digest`. Existing allowance, verified-address, and linked-account
checks still apply; rejected modes return a 422 problem. Writes call the existing
setter once and then reread state. They are `non_retryable`: these setters also
reset delivery backoff and can advance the delivery watermark. The v2 ports do
not change those effects or present repeated writes as harmless. Web queries use
captured-authority cache keys and mode writes disable automatic retries and
401 authentication replay. No current native email/Discord preference caller was
found; Jellyfin compatibility has no equivalent preference surface.

### API v2 destination lists

`GET /api/v2/notifications/web-push/subscriptions`,
`GET /api/v2/notifications/webhooks`, and
`GET /api/v2/admin/notifications/server-channels` return bounded
`items`/`page` collections. Personal lists require the acting profile; server
channels require an acting administrator. Signed cursors bind the operation,
account/profile authority, page size, and exact `(created_at, id)` boundary.
Records retain creation order, with ID resolving timestamp ties. Deleting a
previous boundary row does not invalidate continuation. Indexes support the
profile-filtered and administrator paging queries.

Web-push metadata retains the endpoint needed to identify the current browser,
but excludes subscription keys. Webhook and server-channel metadata exposes only
`url_host`, never destination URL ciphertext or stored signing secrets. Delivery
health fields remain readable, with nullable timestamps using the v2 UTC instant
format. Reads do not reset backoff, mutate destinations, or dispatch a send.

Existing web lists drain bounded pages under captured authority and use scoped
cache keys. Missing/repeated continuations and authority changes fail explicitly
without returning a partial list. The current web display limit is 100 pages of
100 records; exceeding it is an explicit error. Destination creation, updates,
deletes, tests, and secret rotation remain separate bridge operations until their
own guarded mutation migration. No native destination-management caller exists;
Jellyfin compatibility has no equivalent list surface.

### API v2 administrator Discord credential test

`POST /api/v2/admin/notifications/discord/test`
(`testAdminDiscordNotification`) verifies the stored bot token by fetching the
bot's own identity once. It sends no message and does not change Discord links.
Acting-administrator authorization, demo restrictions, and no-store responses
apply. There is no request body. HTTP 200 reports `ok`, nonnegative `duration_ms`,
and `message`; a failed verification still returns this result. A missing token
reports “Bot token is not configured”; provider failures use a generic message
instead of exposing upstream diagnostics. An unavailable service returns 503.

The operation is `non_retryable`. The existing administrator test button captures
authority, prevents overlapping tests, disables authentication replay, and refuses
stale results after an account/profile switch. Apple and Android have no caller;
Jellyfin compatibility has no corresponding operation. Discord link initiation,
OAuth callbacks, and unlinking retain their bridge routes pending their separate
migration.

### API v2 webhook and server-channel test delivery

`POST /api/v2/notifications/webhooks/{id}/test` (`testNotificationWebhook`)
requires the acting profile; `POST /api/v2/admin/notifications/server-channels/{id}/test`
(`testAdminNotificationServerChannel`) requires an acting administrator. Both
accept an opaque destination ID and no request body, enforce demo restrictions,
and call the existing synchronous sample sender once. Personal webhook tests
retain the administrator's webhooks-enabled gate and profile ownership check.
Server-channel tests retain the existing administrator diagnostic behavior.

HTTP 200 reports `ok`, optional `http_status`, nonnegative `duration_ms`, and an
optional sanitized sender `message`. A destination's 429/5xx is a failed delivery
result, not an API failure or an instruction to retry. Test sends neither enqueue
retries nor update failure counters, auto-disable state, or delivery watermarks.
Missing destinations return 404; disabled personal webhooks return 403; unavailable
services return 503. Responses are no-store. Both operations are `non_retryable`.

Existing web cards capture authority, prevent overlapping test calls, disable
authentication replay, and suppress stale results. Configuration changes and
secret rotation retain their bridge routes pending guarded mutation migration.
Apple and Android have no destination-test callers. Jellyfin compatibility has
no corresponding operation.

### API v2 tokenized notification email links

Notification verification emails now link to
`GET /api/v2/notifications/email/verify?token=...`
(`verifyNotificationEmailAddress`). Notification email footers and
List-Unsubscribe headers use `/api/v2/notifications/email/unsubscribe?token=...`:
GET is `unsubscribeNotificationEmail`; the RFC 8058 one-click POST is
`unsubscribeNotificationEmailOneClick`.

These public token-authorized routes render the existing standalone HTML pages.
They require no Silo login or profile header and accept mail-client HTML
negotiation and one-click form bodies. Verification consumes the single-use
proof and promotes the pending address; success is 200, expired/consumed/missing
proof is 400, an address ownership conflict is 409, and storage errors are 500.
Unsubscribe retains the existing profile capability token and switches email
mode off: 200 on success, 400 for invalid/missing proof, and 500 on storage error.
An unavailable service returns a 503 problem. API v2 adds no-store and
no-referrer headers; pages never reflect the proof or internal error text.

The one-click POST is `non_retryable`: the existing capability token can turn
email off again after the profile later re-enables it. This migration adds no
generation guard or delivery retry. Address request/clear transports remain on
the bridge until their separate migration. Previously sent bridge links keep
working through the bridge release. Mail clients follow the emitted URLs;
Apple and Android have no in-app callback consumer, and Jellyfin compatibility
has no corresponding operation.

### API v2 Discord account-link consent and callback

`POST /api/v2/notifications/discord/link/init` (`beginNotificationDiscordLink`)
starts one consent flow for the authenticated login account. A supplied profile
still passes viewer/PIN checks; no profile is required for this account-level
operation. Demo restrictions apply. The response contains `url`, using Discord's
consent endpoint, the stored client ID, `identify` scope, a random one-time state,
and the exact v2 callback URI. This operation is `non_retryable`; web captures
authority, prevents overlapping initiation, disables authentication replay, and
checks authority again before navigating.

`GET /api/v2/notifications/discord/link/callback`
(`completeNotificationDiscordLink`) is public and authenticates through the
stored state rather than browser login headers. It retains the existing channel
enabled check, consent-denied and malformed-callback handling, one-time state
consumption, account lookup, and code exchange. The exchange uses the same
`<public URL>/api/v2/notifications/discord/link/callback` redirect URI as consent.
The result is HTTP 302 with Location pointing to notification settings and the
existing success/error query fields, plus the usual HTML redirect body. It never
invents a JSON 200 response. API v2 applies no-store/no-referrer headers.

Register the exact v2 redirect URI in the Discord application's OAuth settings
before using v2 linking. Keep the bridge redirect registered while bridge flows
remain supported; their consent and exchange still use the v1 URI. This port
preserves the existing link-state and account-link write semantics. It adds no
generation guard for overlapping consent flows or unlinking; unlink migration
remains separate. Apple and Android have no notification Discord-link callers,
and Jellyfin compatibility has no equivalent operation.

### API v2 destination creation

`POST /api/v2/notifications/webhooks` creates a webhook for the active profile.
`POST /api/v2/admin/notifications/server-channels` creates a server channel for
an acting administrator. Both require `name` and `url`; optional type and event
flags retain the existing service defaults. Creation validates and stores the
destination through the same service as the bridge and does not send a message.

A successful response is `201` with `id`, `name`, `type`, `url_host`, and an
optional one-time `signing_secret`. It never returns the destination URL or
stored credential ciphertext. Responses use `Cache-Control: no-store`.
Disabled personal webhooks return `403`; administrators may prepare server
channels while delivery is disabled. Invalid configuration or a quota limit
returns `422`. Missing services return `503`.

Creation is `non_retryable`: a lost response can leave a created destination
whose signing secret was not received. Clients must inspect the destination
list and explicitly manage or rotate that destination instead of automatically
resubmitting creation. There is no durable creation receipt or secret recovery
promise. The web forms capture request authority, prevent overlapping submissions,
and suppress results after an account or profile change. Native clients have no
destination-management caller; Jellyfin compatibility has no matching operation.

### Unlink Discord

`DELETE /api/v2/notifications/discord-link` (`unlinkNotificationDiscord`) unlinks the authenticated login account's Discord identity and turns its Discord delivery mode off. A profile header is optional; a supplied profile remains subject to the normal access checks. The demo guard applies. The operation returns bodyless `204`, including when there is no linked identity. An unavailable API service returns `503`; storage failure returns `500`.

Unlink is **non-retryable**: a delayed repeat can clear an identity established by a subsequent OAuth relink. No generation precondition or cancellation of in-flight OAuth/provider work is provided. The existing identity, DM-channel and mode clearing behavior remains unchanged. The settings action captures authority, sends once without authentication replay, rejects stale receipts, and invalidates only its exact Discord preferences cache. A successful receipt does not prove that an already-dispatched Discord message was cancelled. Native caller closure is separate from this server and web operation.
