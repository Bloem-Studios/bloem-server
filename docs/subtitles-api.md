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
