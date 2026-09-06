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
