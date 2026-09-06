# Playback session mutations

The explicitly configured initial playback flow advertises
`sequenced_progress_v1` in the successful start response's `server_features`.
The decision protocol remains version 3. Production configuration and source
enrollment are not enabled by this flow. Without that feature, clients retain
the existing playback mutation behavior.

The initial flow supports direct original-file delivery and local encoded HLS.
Progressive remux, proxy/remote execution and separate subtitle delivery remain
unsupported. Bound hardware execution requires a concrete device and unchanged
execution policy between preparation and start; multi-device configurations and
runtime hardware fallback are refused. Software transcode is covered by the
HTTP integration fixture.

These mutations use the existing authenticated native routes. A v2 adapter is
separate work; this capability does not change the URL prefix.

| Operation | Method and path | Success |
| --- | --- | --- |
| Progress | POST `/api/v1/playback/{session_id}/progress` | 200 accepted state |
| Stop | DELETE `/api/v1/playback/{session_id}` | 202 draining, then 200 terminal receipt |

Progress accepts `{"sequence":42,"position":120,"is_paused":false}`. Sequence
is a positive signed 64-bit integer scoped to the captured playback session.
Allocate it once per sample and preserve the entire body on retry. A higher
sequence supersedes an earlier sample even when its position moves backward.
An older sequence returns the latest accepted state. An equal sequence with a
changed payload returns 409 `progress_conflict`.

The response has `outcome` (`applied`, `replayed` or `stale_sample`) and optional
`accepted`, containing `sequence`, `position` and `is_paused`. Accepted position
is the raw winning sample, not a maximum position or resume-policy projection.

Stop accepts `{"stop_id":"<canonical UUID>"}`. It may also include `sequence`,
`position` and `is_paused` as one final sample; sequence and position must appear
together. Omitting both uses the last accepted sample. Generate the stop UUID
once and preserve the exact body through lost replies and draining retries.
Drain retries must not allocate another session or infer completion from a timeout.

A 202 response has `outcome="draining"`. Retry the same DELETE and body with
bounded backoff until 200, surfacing a pending or failed stop when the retry
budget expires. The completed response has `outcome="stopped"` or `"replayed"`,
the stable `stop_id`, optional `accepted`, and `history_id` when history was
created. No history is fabricated when persistence is disabled or no sample
qualifies. A client can persist the pending request to retry after restart;
that request is not authority and remains subject to server checks.

Unavailable or expired authority returns 503 without allocating replacement
rights. Authentication and profile checks still apply. The selected source,
fence and frozen progress policy are server-owned and cannot be supplied by a
client. The initial handler does not implement takeover or replacement.

Web consumption is feature gated. Apple and Android adoption is coordinated
separately and must be verified before production enablement. Jellyfin reports
cannot mutate a bound session through legacy writers; Jellyfin lifecycle
adoption remains outside this opt-in native flow.

The web client retains pending stop retries across player unmount within the
same page and exposes a Retry action. Its sequence and stop state are currently
in memory: a full page reload or process exit loses that state, and unload
cannot guarantee continued drain polling. Durable client retry storage remains
a prerequisite for claiming recovery across those boundaries.
