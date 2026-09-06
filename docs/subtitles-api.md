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
