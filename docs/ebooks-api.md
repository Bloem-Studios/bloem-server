# Ebook reader API

The v2 reader progress operations share the existing reader stores and media
authorization with the frozen v1 bridge. They require account authentication and
a verified `X-Profile-Id`. Current item access is checked on reads; writes also
check that the accessible ebook file belongs to the requested item.

| Operation | Method and path |
| --- | --- |
| getEbookCapability | GET `/api/v2/capabilities/ebooks` |
| getEbookProgress | GET `/api/v2/ebooks/{content_id}/progress` |
| saveEbookProgress | PUT `/api/v2/ebooks/{content_id}/progress` |

The capability reports `ordered_progress` independently of `kindle_conversion`.
Conversion retains its source formats, served format, and fallback header.
Capability availability describes configuration, not a health check.

Progress reads and writes return `{"progress": {...}}`. When no position is
saved, the progress member is absent. A position contains `content_id`, a string
`file_id`, `location`, fractional `progress` from 0 to 1, and `updated_at` in
UTC with millisecond precision. Responses are private and must not be cached.

A write requires `file_id`, `location`, `progress`, and a client event time in
`updated_at`. Clients capture the event time when the user changes position and
retain that exact value for a retry. The server rejects future timestamps.
The database accepts an event only when its timestamp is strictly newer than
the stored timestamp; equal and older events keep the current file, location,
and progress. The response contains the current saved position, which may be
newer than the submitted event.

The existing finished-book rule remains: after a book reaches the finished
threshold, ordinary autosaves can change its location but cannot mark it unread.
Explicit unread deletes the saved progress. V1 continues assigning server times
and does not gain v2 retry guarantees.

The progress request body is capped at 16 KiB; locations are limited to 8192
characters. Invalid input returns a validation problem. Inaccessible files and
files belonging to another item return not found. Reader config, annotations,
and binary delivery migration are tracked separately; the progress capability
does not advertise completion of those flows.

## Reader configuration

`GET /api/v2/ebooks/{content_id}/reader-config` returns `content_id`, a `config`
object, optional `updated_at`, and an `ETag` header. A profile that has never
saved configuration sees an empty object with its own validator.

`PUT` on the same path takes `{"config": {...}}` and requires `If-Match` from
that read. Missing validators return 428; stale validators return 412 with the
current ETag. `If-None-Match`, when supplied, is evaluated after `If-Match`.
The configuration object is client-owned, and the entire request is limited to
256 KiB. The server does not interpret renderer-specific keys.

The database locks the same configuration row that v1 writes. Concurrent first
writes serialize; a failed guard leaves no persisted default. A legacy write
invalidates the v2 validator. Clients should retain their validator while the
user edits and handle a conflict by reloading and reconciling, rather than
retrying with an unconditional overwrite. The capability's `guarded_config`
field advertises this contract.
