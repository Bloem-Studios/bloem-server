# Initial playback API

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

The typed v2 adapter calls the shared playback application service. Its routes
are always registered and require an authenticated profile. The capability
reports `state` (`not_configured`, `not_admitted`, or `available`), `allowed`,
a revision, supported deliveries and features. When configured, `installation_id`
is the persisted server instance UUID also used by diagnostics; clients must
capture it and include it in each mutation. A mismatch returns 409
`installation_changed`. Validation failures use 422; unavailable dependencies
use 503. The decision protocol remains version 3.

| Operation | Method and path | Success |
| --- | --- | --- |
| Capability | GET `/api/v2/playback/capabilities` | 200 capability state |
| Start | POST `/api/v2/playback/start` | 201 decision |
| Progress | POST `/api/v2/playback/{session_id}/progress` | 200 accepted state |
| Stop | DELETE `/api/v2/playback/{session_id}` | 202 draining or 200 receipt |

Start uses the version 3 decision request plus `installation_id`. File IDs in
v2 requests and decisions are opaque JSON strings; track indices remain numbers.
Allocate one attempt ID and retain the exact request for uncertain retries.
Returned media URLs remain opaque URLs through the existing v1 media bridge.
The capability does not advertise replan or route-event support.

The existing v1 routes use the same mutation service for bound sessions:

| Operation | Method and path | Success |
| --- | --- | --- |
| Progress | POST `/api/v1/playback/{session_id}/progress` | 200 accepted state |
| Stop | DELETE `/api/v1/playback/{session_id}` | 202 draining, then 200 terminal receipt |

Progress accepts `{"sequence":42,"position":120,"is_paused":false}`; v2 also requires `installation_id`. Sequence
is a positive signed 64-bit integer scoped to the captured playback session.
Allocate it once per sample and preserve the entire body on retry. A higher
sequence supersedes an earlier sample even when its position moves backward.
An older sequence returns the latest accepted state. An equal sequence with a
changed payload returns 409 `progress_conflict`.

The response has `outcome` (`applied`, `replayed` or `stale_sample`) and optional
`accepted`, containing `sequence`, `position` and `is_paused`. Accepted position
is the raw winning sample, not a maximum position or resume-policy projection.

Stop accepts `{"stop_id":"<canonical UUID>"}`; v2 also requires `installation_id`. It may also include `sequence`,
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

The web client persists the exact start request before dispatch, then persists
sequence allocation, pending progress and stop bodies in browser storage.
Web Locks serialize mutations across tabs. Records bind the installation,
account, profile, origin and session; credentials are not stored. Identity changes
quarantine old requests. Reload exposes an explicit Retry action: recovering an
uncertain start confirms the original attempt and offers to stop it, without
autoplay. An unresolved start blocks a different attempt and legacy fallback.
Definitive pre-reservation validation rejection releases its journal. Storage
or Web Locks unavailability fails configured playback before dispatch. Clearing
or evicting browser storage loses recovery state; unload cannot guarantee polling.

Runtime wiring is explicit through `NewInitialPlaybackRuntime` and the router's
`InitialPlayback` dependency. Normal application startup does not enable it.
The constructor verifies the persisted installation identity and requires source,
owner and media-grant policies. Ordinary starts only read source admission.
`internal/playback/testfixture.ProvisionPostgres` enrolls a newly created synthetic
account transactionally; it cannot enroll an existing account.

An explicit `InitialPlaybackReconcileAccounts` list enables bounded account scans.
Expired or withdrawn pending/installed starts move through durable abort intents;
aborting intents retry their exact source operation. A stopping intent completes
only after its matching source terminal receipt and drain deadline exist, then
closes matching local runtime state. If the source stop has not committed, the
original client request must retry: reconciliation does not invent or omit a final
sample. The runner does not adopt active owners or implement takeover, restore,
replacement, cutover or general production source provisioning.
