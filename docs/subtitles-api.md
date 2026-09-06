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
paths. Upload, download, delete, and AI-job mutations remain separate migration
scopes; their existing retry limitations still apply.
