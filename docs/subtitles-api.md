# Subtitle API

## Provider administration inspection

GET `/api/v2/admin/subtitle-providers` returns `providers[]` with provider name,
enabled state, credential-presence flags and an optional `updated_at` UTC instant.
Built-in providers without saved settings remain visible; their update time is
omitted. Credentials never appear in this response.

POST `/api/v2/admin/subtitle-providers/{provider}/test` accepts a JSON draft with
optional `enabled`, `api_key`, `username` and `password`. Blank credentials retain
stored values for the test. It performs one search with a 15-second deadline and
returns `success` plus a safe `error` on failure. The draft is not persisted.
Send this command once; a lost response does not authorize automatic retries or
authentication replay because the provider may already have processed the search.

Both operations require the acting administrator and honor the demo restriction.
The web settings and setup wizard use these typed inspection operations. Saving
provider settings remains on the bridge until guarded configuration updates and
live application across nodes are implemented. No native provider-administration
consumer or Jellyfin counterpart is changed by these inspection operations.

## Subtitle AI state

GET `/api/v2/subtitles/ai/jobs?media_file_id=<string ID>` returns `jobs[]` for up to
50 recent jobs on an accessible media file. It retains the bridge's bounded
recent-activity view. GET `/api/v2/subtitles/ai/jobs/{job_id}` returns `job` after
checking access to that job's media file. Job, media-file and result-subtitle IDs
are strings; `result_subtitle_id` is null until a result exists. Track indices
remain numbers. Creation and update times are UTC instants with millisecond
precision. Failures expose a safe summary instead of upstream diagnostics.

GET `/api/v2/subtitles/ai/quota` returns `limited`, `limit`, `used`, `remaining` and
`period`. The budget belongs to the account. An admin account acting through a
non-primary household profile still has a budget; profile lookup failures do not
grant an exemption. The shared legacy policy also exempts an admin acting without
a profile, or when no profile store is configured. The web translation modal reads
this typed quota operation through its captured player configuration.

These operations read persisted state and do not enqueue or cancel jobs. Job
creation and cancellation remain separate migration work. Native consumers must
retain string identifiers and the captured account/profile when following jobs.

The v2 subtitle capability probes are always registered. They require an
authenticated account. A supplied profile must belong to that account and pass
viewer-access checks; a profile header is optional, as on the bridge API.

| Operation | Method and path | Response |
| --- | --- | --- |
| Provider status | GET `/api/v2/subtitles/providers/status` | `schema_version`, `enabled`, `providers` |
| AI status | GET `/api/v2/subtitles/ai/status` | `enabled`, `transcribe_enabled` |

Both return 200 and `Cache-Control: no-store`. When providers are absent,
`enabled` is false and `providers` is an empty array. Registered provider names
are sorted and contain no credentials. `schema_version` remains 1. When the AI
service is absent, both AI flags are false. These reads do not search providers,
start jobs, or contact an external engine.

The web subtitle menu uses the typed v2 AI probe with the player's credentials.
Apple and Android adoption is coordinated separately. Generation and delivery retain their existing routes until their own
migration scopes land. Jellyfin compatibility uses its existing subtitle
protocol and needs no equivalent native capability route.

## Stored tracks and provider search

GET `/api/v2/subtitles/{media_file_id}` lists stored subtitle metadata under
`subtitles`. POST `/api/v2/subtitles/search` accepts `media_file_id` as an opaque
string and `languages` as an array, and returns `results` and `warnings`. Search
is read-only and can be retried; a retry makes a fresh query and can return
different provider results. The request allows at most 100 languages.

Both operations require the same account and optional-profile access as the
capability probes. They authorize the file and its parent item before accessing
stored tracks or contacting providers. Missing and inaccessible files return
404. Missing subtitle dependencies return 503. Invalid identifiers return 422.

Stored subtitle IDs and file IDs are strings. Search result IDs remain opaque
provider identifiers. Timestamps use UTC with millisecond precision; an unknown
search upload date is omitted. Arrays are empty rather than null. Stored object
keys and uploader identities are not exposed. Provider failures preserve partial
results with a generic warning; their raw error details are not part of v2.

The web detail dialog and player search use typed v2 requests. The existing web
track selector still needs numeric stored IDs; its adapter rejects IDs that it
cannot represent safely. Native consumers need the string-ID models and new
paths. Provider download and multipart uploads are described below. Delete and
AI-job mutations remain separate migration scopes; their retry limitations still apply.

## Provider download

`POST /api/v2/subtitles/download` downloads a selected provider search result for
an accessible media file. It requires account authentication and applies the
current profile's file and parent-item access rules before contacting the
provider. Demo mode refuses the mutation. Provider availability is exposed by
`GET /api/v2/subtitles/providers/status`.

The JSON body contains `media_file_id`, `provider`, `subtitle_id`, `language`,
`release_name`, `score`, and `hearing_impaired`. Media-file IDs and opaque provider
result IDs are strings. The provider determines the downloaded format; clients
do not supply it. Uploader attribution comes from the authenticated account.

Success returns `200` with `subtitle`, using the same public stored-track fields
as `GET /api/v2/subtitles/{media_file_id}`. Stored IDs are strings and timestamps
are canonical instants. Object keys, uploader identity, and upstream failure
diagnostics are not returned. Failures use the standard native Problem Details
contract.

This operation is `non_retryable`. Each request contacts the provider before
stored-content deduplication. Deduplication can reuse identical content but does
not replay a provider response or suppress repeated upstream requests. An
uncertain response must not trigger automatic retry or authentication replay.
The immutable object publication rules and best-effort cleanup limits are
specified in [subtitle storage](architecture/subtitle-storage.md).

The bridge download retains its existing request and response contract. This
operation does not change Jellyfin subtitle delivery. Both native clients must
adopt the new download operation separately; AI creation/cancellation and user
multipart uploads remain separate migration scopes.


## Multipart upload and language detection

`POST /api/v2/subtitles/upload` takes a `multipart/form-data` request with `file`
and string `media_file_id`. Optional fields are `language`, `language_override`,
`release_name`, and `hearing_impaired`. Boolean fields use `true` or `false` text.
The file is limited to 5 MiB; the whole form is limited to 5 MiB plus 256 KiB for
framing and other fields. Supported filename extensions are SRT, VTT, ASS, SSA,
and SUB. The filename determines format; the file part can use
`application/octet-stream`.

Authentication and profile gates precede multipart parsing. The shared service
checks file and parent-item access before storage and derives uploader identity
from the authenticated account. Demo mode refuses uploads. Language selection
retains filename, metadata and content detection with a manual fallback;
`language_override=true` explicitly selects the supplied valid language. Success
returns the same `200` public `subtitle` projection as provider download.

Uploads are `non_retryable`. A full-content match may reuse a stored row, but it
is not a durable receipt across later edits or deletion. Clients must not repeat
an uncertain upload automatically or replay it after authentication refresh.
The storage foundation's all-writer rollout and best-effort cleanup limitations
still apply. Missing upload dependencies return a dependency-unavailable problem.

`POST /api/v2/subtitles/detect-language` takes `file` and optional `language`, under
the same byte limits. It returns `language` and `source` (`filename`, `metadata`,
`content`, or `manual`) and never stores the file. It requires account/profile
authority but works without subtitle storage. This read-only POST is naturally
idempotent. Detection uses the supplied language only as fallback, not override.

Web detail and player callers use these forms with captured authority and suppress
stale completions. Apple and Android multipart adoption is separate required work;
bridge multipart behavior and Jellyfin subtitle delivery remain unchanged.

## V2 administrator stored-subtitle list

`GET /api/v2/admin/subtitles` requires an acting administrator and applies the
existing demo restriction. It returns `{items, page, total, uploads,
provider_downloads}`. Subtitle, media-file and uploader identifiers are decimal
strings; `created_at` is a canonical UTC instant. Missing uploader/content
identifiers are omitted. Source file paths remain administrator-only inspection
data; storage object keys are not exposed.

The optional filters are `provider`, `language`, `user_id`, `media_file_id` and
`q`. IDs must be positive decimal strings. String filters are trimmed; `q`
retains the existing case-insensitive release-name search, including `%` and `_`
wildcards. `limit` defaults to 50 and accepts 1–200. Offset pagination is rejected.
Rows sort by `created_at DESC, id DESC`; pass `page.next_cursor` unchanged when
`page.has_more` is true. Cursors are signed and bound to the administrator,
declared profile, filters, page size and ordering.

Each page's rows and filtered counts use one read-only database snapshot. Later
requests read the live collection: additions before the cursor do not appear on
later pages, and deletions do not invalidate its ordering position. Counts may
change between requests. `uploads` counts provider `upload`; `provider_downloads`
counts every other provider, preserving the existing administration statistics.
An unavailable service returns `503 dependency_unavailable`; database failures
return a redacted `500 internal_error`.

The bundled administrator page uses cursor history and authority-scoped read
caches, discards stale account/profile/PIN replies and preserves string IDs in
its existing controls. Metadata edits, deletion and byte downloads retain their
separate bridge transports. This list does not add a native client screen or a
Jellyfin counterpart. Its ordinary migration row remains proposed until the
independent review and actual consumer inventory requirements are satisfied.

### AI job cancellation

`POST /api/v2/subtitles/ai/jobs/{job_id}/cancel` takes a positive string job
identifier in the path and no body. It returns an empty `204` after the
guarded cancellation request succeeds. Authentication and the selected
profile's media-file access are required; the existing shared-file rule is
preserved, so cancellation is not restricted to the account that requested
the job. Hidden or missing jobs return `404`, unavailable service returns
`503`, and an uncertain database outcome returns a problem response rather
than success. Demo-mode writes remain blocked.

Cancellation is naturally idempotent for the same immutable job ID. A terminal
job remains unchanged, and completion can win a concurrent cancellation. Read
`GET /api/v2/subtitles/ai/jobs/{job_id}` to determine the actual outcome. A `204`
does not promise immediate provider shutdown, removal of previously committed
transcripts, or revocation of cues already delivered. The publication and
all-worker rollout limits in `docs/architecture/subtitle-storage.md` apply.
No new enqueue or durable request-replay receipt is implied.

The web player has no existing job-cancellation caller; closing its creation
modal is a local UI action. Native callers adopt this operation separately,
retaining the exact job ID and captured account/profile/PIN authority for
requests and any completion-driven UI updates. Creation and live streaming
ownership remain separate migration work.

### V2 AI creation and live delivery

`POST /api/v2/subtitles/ai/translate` accepts a string `media_file_id`, explicit
`kind` (`translate`, `transcribe`, or `transcribe_translate`), `source_index`,
`source_language`, `target_language`, and nonnegative `start_position` in seconds.
`source_index` preserves the combined subtitle ordinal for translation and the
audio ordinal for transcription (`-1` selects the default audio track).
Plain transcription may use an empty target language. Optional `session_id`
requests live cues for the caller's local playback session. File authorization
and account/profile/session matching precede enqueue; account and quota
attribution come from authentication, never request fields.

The response is `202` with `job` in the existing v2 job projection (string IDs)
and `live_delivery_attached`. That boolean reports whether this request attached
its live notifier to a newly created job. It does not acknowledge socket delivery.
An existing active job is returned without attaching another viewer's stream.
Clients should poll the returned job or refresh the file's subtitle inventory.

Creation is `non_retryable`: send once, with no automatic authentication replay
or offline queue. Active-job deduplication ends when a job becomes terminal and
is not a durable request receipt. An uncertain response requires reconciliation
through job reads; resubmission may create another job. Persisted job state and
atomic subtitle publication do not turn process-local execution dispatch into a
durable worker queue. Missing engine/live-delivery dependencies return `503`,
hidden files or foreign/missing playback sessions return `404`, and transcription
quota rejection returns a `429` `rate_limited` Problem.

Live notifications capture account, profile, effective/requested file, session
start, executor namespace and initial activation binding. A check before sending
suppresses events when the local runtime no longer matches. This check does not
hold session-manager locks over socket writes, validate a new media grant, or
revoke cues already queued or written. Socket delivery remains best effort;
reconnect does not replay missed cues. Finished-track notification retains the
existing file broadcast. Atomic publication, cancellation races, worker adoption
and uncertain commit behavior retain the limits described above.

The player sends the typed request once, rejects stale decoded responses after
modal/media/session/config/account/profile/PIN changes, checks returned job/file
identity, and reports background progress when no live notifier was attached.
Apple and Android must adopt the explicit kind, string file ID, single-send
semantics and returned job projection before this ordinary row can be ratified.
There is no Jellyfin AI creation counterpart. Production activation and account
enrollment are unchanged.
