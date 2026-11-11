# Live TV API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Channels, the programme guide, live sessions and their delivery, recordings and recording rules, and the tuner, provider and guide administration behind them.

For the plain-language picture, read the [Live TV explainer](../../architecture/bloem-live-tv.md). The invariants are in:

- [Live TV client access](../../architecture/live-tv-client-access.md)
- [Xtream live TV providers](../../architecture/xtream-live-tv.md)
- [Live TV tuner discovery](../../livetv-tuner-discovery.md)
- [Media ownership across API replicas](../../architecture/bloem-cluster-media.md)

## Operations (30)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/livetv/capability`](#get-apibloemv2livetvcapability) | Whether this build and viewer support Live TV and a channel is available. |
| [`GET /api/bloem/v2/livetv/channels`](#get-apibloemv2livetvchannels) | The channel lineup this viewer may watch. |
| [`PATCH /api/bloem/v2/livetv/channels/{channelId}`](#patch-apibloemv2livetvchannelschannelid) | Change channel enablement, number override or guide mapping. |
| [`POST /api/bloem/v2/livetv/channels/{channelId}/session`](#post-apibloemv2livetvchannelschannelidsession) | Tune a channel and take a tuner lease. |
| [`GET /api/bloem/v2/livetv/guide`](#get-apibloemv2livetvguide) | Program guide for a window. |
| [`GET /api/bloem/v2/livetv/guide-sources`](#get-apibloemv2livetvguide-sources) | List configured guide sources. |
| [`POST /api/bloem/v2/livetv/guide-sources`](#post-apibloemv2livetvguide-sources) | Create a guide source. |
| [`POST /api/bloem/v2/livetv/guide-sources/schedules-direct/lineups`](#post-apibloemv2livetvguide-sourcesschedules-directlineups) | Look up Schedules Direct lineups by postal code. |
| [`POST /api/bloem/v2/livetv/guide-sources/xml-sync/lineups`](#post-apibloemv2livetvguide-sourcesxml-synclineups) | Look up Gracenote providers by postal code. |
| [`PATCH /api/bloem/v2/livetv/guide-sources/{sourceId}`](#patch-apibloemv2livetvguide-sourcessourceid) | Merge supplied guide-source fields with current configuration. |
| [`DELETE /api/bloem/v2/livetv/guide-sources/{sourceId}`](#delete-apibloemv2livetvguide-sourcessourceid) | Delete a guide source. |
| [`POST /api/bloem/v2/livetv/guide-sources/{sourceId}/sync`](#post-apibloemv2livetvguide-sourcessourceidsync) | Synchronize a saved guide source. |
| [`GET /api/bloem/v2/livetv/live-hls/{playbackId}/{name}`](#get-apibloemv2livetvlive-hlsplaybackidname) | Fetch a session-bound HLS playlist or MPEG-TS segment. |
| [`GET /api/bloem/v2/livetv/programs/{programId}`](#get-apibloemv2livetvprogramsprogramid) | Read a guide program. |
| [`GET /api/bloem/v2/livetv/recordings`](#get-apibloemv2livetvrecordings) | List recordings visible to this viewer. |
| [`POST /api/bloem/v2/livetv/recordings`](#post-apibloemv2livetvrecordings) | Schedule a guide-program or manual recording. |
| [`DELETE /api/bloem/v2/livetv/recordings/{recordingId}`](#delete-apibloemv2livetvrecordingsrecordingid) | Cancel an owned recording and return its updated record. |
| [`GET /api/bloem/v2/livetv/series-rules`](#get-apibloemv2livetvseries-rules) | List viewer-owned DVR series rules. |
| [`POST /api/bloem/v2/livetv/series-rules`](#post-apibloemv2livetvseries-rules) | Create a viewer-owned DVR series rule. |
| [`DELETE /api/bloem/v2/livetv/series-rules/{ruleId}`](#delete-apibloemv2livetvseries-rulesruleid) | Delete an owned DVR series rule. |
| [`DELETE /api/bloem/v2/livetv/sessions/{sessionId}`](#delete-apibloemv2livetvsessionssessionid) | Release an owned tuner lease and return the session record. |
| [`POST /api/bloem/v2/livetv/sessions/{sessionId}/heartbeat`](#post-apibloemv2livetvsessionssessionidheartbeat) | Renew an owned active tuner session. |
| [`GET /api/bloem/v2/livetv/sessions/{sessionId}/stream`](#get-apibloemv2livetvsessionssessionidstream) | Stream MPEG-TS media. |
| [`HEAD /api/bloem/v2/livetv/sessions/{sessionId}/stream`](#head-apibloemv2livetvsessionssessionidstream) | Check MPEG-TS stream headers. |
| [`GET /api/bloem/v2/livetv/tuners`](#get-apibloemv2livetvtuners) | List configured tuner metadata. |
| [`POST /api/bloem/v2/livetv/tuners`](#post-apibloemv2livetvtuners) | Add an HDHomeRun-compatible tuner. |
| [`POST /api/bloem/v2/livetv/tuners/discover`](#post-apibloemv2livetvtunersdiscover) | Discover local HDHomeRun-compatible tuner candidates. |
| [`POST /api/bloem/v2/livetv/tuners/xtream`](#post-apibloemv2livetvtunersxtream) | Authenticate and import an encrypted Xtream live-TV provider. |
| [`DELETE /api/bloem/v2/livetv/tuners/{tunerId}`](#delete-apibloemv2livetvtunerstunerid) | Delete a tuner. |
| [`POST /api/bloem/v2/livetv/tuners/{tunerId}/scan`](#post-apibloemv2livetvtunerstuneridscan) | Scan a saved tuner and refresh its channel lineup. |

## Reference

### `GET /api/bloem/v2/livetv/capability`

Whether this build and viewer support Live TV and a channel is available.

Operation ID: `getBloemLiveTVCapability`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Does not require Live TV permission. allowed reports whether the selected profile holds Live TV permission; available is computed only when it does, and channel metadata is never returned. Served with Cache-Control: no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`CapabilityResponse`](#capabilityresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/livetv/channels`

The channel lineup this viewer may watch.

Operation ID: `listBloemLiveTVChannels`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `tuner_id` | query | string |  | Optional tuner filter. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ChannelsResponse`](#channelsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `PATCH /api/bloem/v2/livetv/channels/{channelId}`

Change channel enablement, number override or guide mapping.

Operation ID: `patchBloemLiveTVChannel`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `channelId` | path | string | yes |  |

Request body (required, `application/json`): [`BloemLiveTVChannelPatch`](#bloemlivetvchannelpatch).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ChannelResponse`](#channelresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the channel does not exist, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/channels/{channelId}/session`

Tune a channel and take a tuner lease.

Operation ID: `startBloemLiveTVSession`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires both current Live TV and playback permission. Optional client codec capabilities are read from a body of at most 16 KiB; omit for native broadcast-codec support. Returns 201 and canonical same-origin delivery URLs. HLS URLs carry a session-bound st ticket; MPEG-TS proxy URLs require account/profile authentication. Repeating a start can allocate another lease; do not automatically retry.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `channelId` | path | string | yes |  |

Request body (optional, `application/json`): [`BloemLiveTVClientCapabilities`](#bloemlivetvclientcapabilities).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`SessionStartResponse`](#sessionstartresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the capability body is not valid JSON or exceeds 16 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. playback_forbidden: playback permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the channel does not exist or is disabled, its tuner is gone, unreachable or refused the channel, or the channel has no signal; or X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | no_tuner: every tuner is in use or unavailable. limit_exceeded: live transcode capacity or the Xtream provider's connection budget is in use. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. unavailable: Live TV delivery signing is not configured. |

### `GET /api/bloem/v2/livetv/guide`

Program guide for a window.

Operation ID: `getBloemLiveTVGuide`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `start` | query | string (date-time) |  | RFC3339; defaults to now. |
| `end` | query | string (date-time) |  | RFC3339; defaults to start plus twelve hours. |
| `channels` | query | string |  | Comma-separated channel IDs. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`GuideResponse`](#guideresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_start or invalid_end: start or end is not RFC 3339. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/livetv/guide-sources`

List configured guide sources.

Operation ID: `listBloemLiveTVGuideSources`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLiveTVGuideSourcesResponse`](#bloemlivetvguidesourcesresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/guide-sources`

Create a guide source.

Operation ID: `createBloemLiveTVGuideSource`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. At most three guide sources may be enabled. New Schedules Direct credentials or lineups are verified with Schedules Direct before saving.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVGuideSourceCreate`](#bloemlivetvguidesourcecreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`GuideSource`](#guidesource) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: the type is unsupported; Schedules Direct username, password or password_sha1 (a SHA-1 hex digest) or lineup is missing, or Schedules Direct rejected them; an XML sync postal code or lineup is missing; or an Xtream guide source names no configured provider or one that already has a guide source. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the Xtream provider was removed while the source was saved, or X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | limit_exceeded: three guide sources are already enabled. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, Schedules Direct could not be reached, or saving failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `POST /api/bloem/v2/livetv/guide-sources/schedules-direct/lineups`

Look up Schedules Direct lineups by postal code.

Operation ID: `lookupBloemLiveTVSchedulesDirectLineups`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. Supply password or password_sha1; credentials are used for the lookup and not returned. country defaults to the Schedules Direct default.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVSchedulesDirectLineupsRequest`](#bloemlivetvschedulesdirectlineupsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLiveTVSchedulesDirectLineupsResponse`](#bloemlivetvschedulesdirectlineupsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: username, a password or password_sha1, and postalcode are required, or Schedules Direct rejected the request. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or Schedules Direct could not be reached; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/guide-sources/xml-sync/lineups`

Look up Gracenote providers by postal code.

Operation ID: `lookupBloemLiveTVXMLSyncLineups`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVXMLSyncLineupsRequest`](#bloemlivetvxmlsynclineupsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLiveTVXMLSyncLineupsResponse`](#bloemlivetvxmlsynclineupsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: postalcode is required. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the provider lookup failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `PATCH /api/bloem/v2/livetv/guide-sources/{sourceId}`

Merge supplied guide-source fields with current configuration.

Operation ID: `updateBloemLiveTVGuideSource`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. At most three guide sources may be enabled. Changed Schedules Direct credentials or lineups are verified with Schedules Direct before saving.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sourceId` | path | string | yes |  |

Request body (required, `application/json`): [`BloemLiveTVGuideSourcePatch`](#bloemlivetvguidesourcepatch).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`GuideSource`](#guidesource) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: the type is unsupported; Schedules Direct username, password or password_sha1 (a SHA-1 hex digest) or lineup is missing, or Schedules Direct rejected them; an XML sync postal code or lineup is missing; or an Xtream guide source names no configured provider or one that already has a guide source. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the guide source does not exist, or its Xtream provider was removed while it was saved; or X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | limit_exceeded: three other guide sources are already enabled. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, Schedules Direct could not be reached, or saving failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `DELETE /api/bloem/v2/livetv/guide-sources/{sourceId}`

Delete a guide source.

Operation ID: `deleteBloemLiveTVGuideSource`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. Deleting an absent guide source also answers 204.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sourceId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/guide-sources/{sourceId}/sync`

Synchronize a saved guide source.

Operation ID: `syncBloemLiveTVGuideSource`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. Runs the synchronization before answering. An Xtream guide synchronization already running for the source answers 409.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sourceId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`GuideSource`](#guidesource) or [`BloemLiveTVSyncFallback`](#bloemlivetvsyncfallback) | The refreshed record, or a minimal status record if it disappeared after the operation. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_argument: the source type is unsupported, an Xtream guide source is disabled or its provider configuration is not allowed, required configuration is missing, no channel maps to a guide station, or the guide provider rejected the stored configuration. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the guide source or its Xtream provider does not exist, or X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | conflict: an Xtream synchronization of this source is already running, or the source changed as it started. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, the guide provider could not be reached, or saving failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `GET /api/bloem/v2/livetv/live-hls/{playbackId}/{name}`

Fetch a session-bound HLS playlist or MPEG-TS segment.

Operation ID: `getBloemLiveTVHLSMedia`.
Credentials: `bloemLiveTVDeliveryTicket`.

Requires the signed st query delivery ticket from the session-start URL; an account bearer alone is insufficient, and no account, tenant or profile header is read. The ticket must have purpose=bloem_livetv_hls, match this playback ID, and carry user/profile identity. Current viewer policy and ownership are resolved on every fetch: both Live TV and playback permission remain required, and only the ticket's own viewer may fetch. name is index.m3u8 or seg_*.ts, without additional path separators or escaped path components. Playlist relative segment URLs retain only the delivery ticket. Playlists use Cache-Control: no-store; segments support standard file range and conditional requests. A session owned by another API process is proxied to it.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `playbackId` | path | string | yes |  |
| `name` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `application/vnd.apple.mpegurl` string | Playlist text or MPEG-TS segment bytes. |
| 206 | `video/mp2t` string (binary) | Partial MPEG-TS segment. |
| 304 |  | Segment has not changed; no body. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Plain text: the delivery path is invalid, or the st ticket is missing, invalid, expired, for another playback ID or purpose, or names no resolvable viewer. unauthorized (JSON): the ticket viewer's access could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | live_tv_forbidden: Live TV permission is not granted. playback_forbidden: playback permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found (JSON): the playback session does not exist, is no longer active, belongs to another viewer, or has no such playlist or segment. Plain text: the segment disappeared while being served. |
| 412 |  | A segment If-Match or If-Unmodified-Since precondition failed; no body. |
| 416 | `text/plain` string | The segment range is not satisfiable. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | dependency_unavailable: the API process that owns this session is temporarily unreachable; retry after Retry-After. |

### `GET /api/bloem/v2/livetv/programs/{programId}`

Read a guide program.

Operation ID: `getBloemLiveTVProgram`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `programId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Program`](#program) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the program does not exist, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/livetv/recordings`

List recordings visible to this viewer.

Operation ID: `listBloemLiveTVRecordings`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `status` | query | string |  | Optional recording status filter. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`RecordingsResponse`](#recordingsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/recordings`

Schedule a guide-program or manual recording.

Operation ID: `scheduleBloemLiveTVRecording`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. User and profile ownership come from authentication, never from the body. A program_id takes its channel and time window from the guide. Tuner capacity is claimed when the recording starts, not when it is scheduled. Creation is not replay-safe.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVScheduleRecordingRequest`](#bloemlivetvschedulerecordingrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Recording`](#recording) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: channel_id, start and stop are required without a program_id. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: program_id names no guide program, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `DELETE /api/bloem/v2/livetv/recordings/{recordingId}`

Cancel an owned recording and return its updated record.

Operation ID: `cancelBloemLiveTVRecording`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `recordingId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Recording`](#recording) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the recording does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/livetv/series-rules`

List viewer-owned DVR series rules.

Operation ID: `listBloemLiveTVSeriesRules`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`SeriesRulesResponse`](#seriesrulesresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/series-rules`

Create a viewer-owned DVR series rule.

Operation ID: `createBloemLiveTVSeriesRule`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Series ID, channel ID or title match is required. User/profile ownership comes from authentication. Creation is not replay-safe.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVCreateSeriesRuleRequest`](#bloemlivetvcreateseriesrulerequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`SeriesRule`](#seriesrule) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON. invalid_argument: series_id, channel_id or a non-blank title_match is required. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `DELETE /api/bloem/v2/livetv/series-rules/{ruleId}`

Delete an owned DVR series rule.

Operation ID: `deleteBloemLiveTVSeriesRule`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries. For such an administrator, deleting an absent rule also answers 204.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `ruleId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the rule does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `DELETE /api/bloem/v2/livetv/sessions/{sessionId}`

Release an owned tuner lease and return the session record.

Operation ID: `releaseBloemLiveTVSession`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Does not require Live TV permission. Owned cleanup therefore remains possible after revocation. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries. Returns the serialized session record rather than a bodyless response; releasing an already released session returns it again, unchanged.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sessionId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLiveTVSessionResponse`](#bloemlivetvsessionresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the session does not exist or belongs to another viewer, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/sessions/{sessionId}/heartbeat`

Renew an owned active tuner session.

Operation ID: `heartbeatBloemLiveTVSession`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires the freshly resolved Live TV permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sessionId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the session does not exist, is no longer active or belongs to another viewer, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/livetv/sessions/{sessionId}/stream`

Stream MPEG-TS media.

Operation ID: `streamBloemLiveTVSession`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires both current Live TV and playback permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries. Requires an active session. Delivery tickets are not accepted on this route. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect. HEAD checks the session and its source and returns media headers without fetching stream bytes.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sessionId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `video/mp2t` string (binary) | MPEG-TS media stream. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_argument: the channel's stored source is not an allowed media address. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. playback_forbidden: playback permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the session does not exist, is no longer active or belongs to another viewer, or its channel or source is gone; or X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | limit_exceeded: the Xtream provider's connection budget is in use. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 502 | [`BloemNativeError`](README.md#bloemnativeerror) | upstream_error: the tuner stream could not be opened or answered a non-success status. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `HEAD /api/bloem/v2/livetv/sessions/{sessionId}/stream`

Check MPEG-TS stream headers.

Operation ID: `probeBloemLiveTVSession`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires both current Live TV and playback permission. Sessions, recordings and series rules belong to the account and profile that created them; another viewer's entry answers 404. An administrator whose selected profile is the account's primary, non-child profile may manage every viewer's entries. Requires an active session. Delivery tickets are not accepted on this route. HEAD checks the session and its source and returns media headers without fetching stream bytes or opening the upstream. GET proxies the MPEG-TS stream and renews the tuner lease until disconnect.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `sessionId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 |  | MPEG-TS media headers; no body. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_argument: the channel's stored source is not an allowed media address. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. live_tv_forbidden: Live TV permission is not granted. playback_forbidden: playback permission is not granted. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the session does not exist, is no longer active or belongs to another viewer, or its channel or source is gone; or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `GET /api/bloem/v2/livetv/tuners`

List configured tuner metadata.

Operation ID: `listBloemLiveTVTuners`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLiveTVTunersResponse`](#bloemlivetvtunersresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/tuners`

Add an HDHomeRun-compatible tuner.

Operation ID: `addBloemLiveTVTuner`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. JSON body limit 32 KiB. Xtream providers use /tuners/xtream. The device is discovered at the address and its lineup scanned before the response; if the device answers discovery but its lineup can't be read, the tuner is saved with the failure in last_error and the request answers 201 with status error; scan it again later. Adding a device that is already saved updates that tuner.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemLiveTVAddTunerRequest`](#bloemlivetvaddtunerrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Tuner`](#tuner) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON or exceeds 32 KiB. invalid_argument: type is xtream or another non-HDHomeRun type, provider credentials or max_connections were supplied, the address is missing, invalid or not an allowed media address, or the device reported no device ID. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, the device could not be discovered, or saving the tuner or its channels failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/tuners/discover`

Discover local HDHomeRun-compatible tuner candidates.

Operation ID: `discoverBloemLiveTVTuners`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. The JSON body is optional. timeout_ms defaults to 2000 and is bounded to 10000; probe_urls selects HTTP discovery candidates. Probe failures are reported in notes rather than as errors. Discovery does not save a tuner.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (optional, `application/json`): [`BloemLiveTVDiscoverRequest`](#bloemlivetvdiscoverrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`DiscoverTunersResult`](#discovertunersresult) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the optional body is not valid JSON. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the Live TV request failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/livetv/tuners/xtream`

Authenticate and import an encrypted Xtream live-TV provider.

Operation ID: `createBloemXtreamProvider`.
Credentials: `bearerAuth`.

Feature detection: GET /api/bloem/v2/livetv/capability advertises xtream_supported. Uses the same authenticated viewer, selected primary profile/PIN and acting-administrator gates as other Live TV administration. Administrative-context tokens do not replace those gates, and direct-profile sessions are not admitted. JSON body limit 32 KiB. Imports live channels only, not VOD or series; playback requires direct MPEG-TS. A configured SECRET_KEY cipher and PostgreSQL store are required. Credentials are write-only. Creation is not replay-safe: after an uncertain response, read /livetv/tuners and review the saved providers before trying again. An account at the same canonical base URL cannot be configured twice, and at most 64 providers can be configured. Upgrade the full API/worker fleet before enabling providers.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemXtreamProviderBody`](#bloemxtreamproviderbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Tuner`](#tuner) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_body: the body is not valid JSON or exceeds 32 KiB. invalid_argument: type is not xtream; the URL, name, credentials or max_connections are invalid; the provider refused authentication or listed no live channels; or the same provider account is already configured. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | limit_exceeded: 64 Xtream providers are already configured. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, the provider's channel list could not be read, the provider did not answer in time, or persistence failed; no raw provider response is returned. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `DELETE /api/bloem/v2/livetv/tuners/{tunerId}`

Delete a tuner.

Operation ID: `deleteBloemLiveTVTuner`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile. Deleting an absent tuner also answers 204. Deleting an Xtream provider also deletes its guide source, and is refused while it has active streams or recordings.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `tunerId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | limit_exceeded: the Xtream provider still has active streams or recordings. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the deletion failed; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

### `POST /api/bloem/v2/livetv/tuners/{tunerId}/scan`

Scan a saved tuner and refresh its channel lineup.

Operation ID: `scanBloemLiveTVTuner`.
Credentials: `bearerAuth`.

Uses native account authentication and selected-profile authority, including PIN verification when required. Administrative-context tokens do not replace this authority, and direct-profile sessions are not admitted. Requires acting-administrator authority: the admin role, exercised through the account's primary profile.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `tunerId` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Tuner`](#tuner) or [`BloemLiveTVScanFallback`](#bloemlivetvscanfallback) | The refreshed record, or a minimal status record if it disappeared after the operation. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. invalid_argument: the tuner's stored address or Xtream provider configuration is not allowed. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. forbidden: the caller lacks the admin role or is not acting through the account's primary profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the tuner or its stored Xtream credentials do not exist, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access or acting-administrator authority could not be resolved, or the device or provider could not be scanned; internal details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. dependency_unavailable: encrypted Xtream provider storage is unavailable. |

## Schemas

### BloemLiveTVAddTunerRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device_id` | string |  | Legacy address alias; not the discovered hardware ID. |
| `discover_url` | string |  | Legacy address alias. |
| `max_connections` | integer (int64) |  |  |
| `name` | string |  |  |
| `password` | string |  |  |
| `type` | string |  | Xtream is rejected here; use the dedicated provider endpoint. |
| `url` | string |  | Preferred base URL, host or discover.json URL; one address alias is required. |
| `username` | string |  |  |

### BloemLiveTVChannelPatch

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean |  |  |
| `guide_station_id` | string |  |  |
| `number_override` | string |  |  |

### BloemLiveTVClientCapabilities

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audio_passthrough_codecs` | array of string |  |  |
| `codecs_audio` | array of string |  |  |
| `codecs_video` | array of string |  |  |
| `max_audio_channels` | integer (int64) |  |  |
| `max_resolution` | string |  |  |

### BloemLiveTVCreateSeriesRuleRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string |  |  |
| `enabled` | boolean |  | Defaults to true. |
| `keep_last` | integer (int64) |  |  |
| `new_only` | boolean |  |  |
| `series_id` | string |  |  |
| `title_match` | string |  |  |

### BloemLiveTVDiscoverRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `include_udp` | boolean |  |  |
| `probe_urls` | array of string |  |  |
| `timeout_ms` | integer (int64) |  |  |

### BloemLiveTVGuideSourceCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `config` | object of string |  |  |
| `display_name` | string |  |  |
| `enabled` | boolean |  |  |
| `id` | string |  |  |
| `last_error` | string |  |  |
| `last_sync_at` | string (date-time) |  |  |
| `next_sync_at` | string (date-time) |  |  |
| `priority` | integer (int64) |  |  |
| `status` | string |  |  |
| `type` | string | yes |  |

### BloemLiveTVGuideSourcePatch

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `config` | object of string |  |  |
| `display_name` | string |  |  |
| `enabled` | boolean |  |  |
| `priority` | integer (int64) |  |  |
| `type` | string |  |  |

### BloemLiveTVGuideSourcesResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `guide_sources` | array of [`GuideSource`](#guidesource) | yes |  |

### BloemLiveTVScanFallback

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `status` | string: `scanned` | yes |  |

### BloemLiveTVScheduleRecordingRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string |  |  |
| `program_id` | string |  | Select a guide program, or supply a manual channel and time window. |
| `start` | string (date-time) |  |  |
| `stop` | string (date-time) |  |  |
| `title` | string |  |  |

### BloemLiveTVSchedulesDirectLineup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `headend` | string | yes |  |
| `lineup` | string | yes |  |
| `location` | string | yes |  |
| `name` | string | yes |  |
| `transport` | string | yes |  |

### BloemLiveTVSchedulesDirectLineupsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `country` | string |  |  |
| `password` | string |  |  |
| `password_sha1` | string |  |  |
| `postalcode` | string | yes |  |
| `username` | string | yes |  |

### BloemLiveTVSchedulesDirectLineupsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `lineups` | array of [`BloemLiveTVSchedulesDirectLineup`](#bloemlivetvschedulesdirectlineup) | yes |  |

### BloemLiveTVSessionResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string | yes |  |
| `created_at` | string (date-time) | yes |  |
| `hls_url` | string |  |  |
| `id` | string | yes |  |
| `last_seen_at` | string (date-time) | yes |  |
| `note` | string |  |  |
| `playback_session_id` | string |  |  |
| `profile_id` | string |  |  |
| `released_at` | string (date-time) |  |  |
| `status` | string | yes |  |
| `stream_url` | string | yes |  |
| `transport` | string |  |  |
| `tuner_id` | string | yes |  |
| `tuner_index` | integer (int64) | yes |  |
| `user_id` | integer (int64) |  |  |

### BloemLiveTVSyncFallback

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `status` | string: `ready` | yes |  |

### BloemLiveTVTunersResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `tuners` | array of [`Tuner`](#tuner) | yes |  |

### BloemLiveTVXMLSyncLineup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device` | string | yes |  |
| `headend` | string | yes |  |
| `lineup` | string | yes |  |
| `location` | string | yes |  |
| `name` | string | yes |  |
| `transport` | string | yes |  |

### BloemLiveTVXMLSyncLineupsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `country` | string |  |  |
| `lang` | string |  |  |
| `postalcode` | string | yes |  |

### BloemLiveTVXMLSyncLineupsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `lineups` | array of [`BloemLiveTVXMLSyncLineup`](#bloemlivetvxmlsynclineup) | yes |  |

### BloemNativeRateLimitError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `rate_limit_exceeded` | yes |  |
| `message` | string | yes |  |
| `retry_after` | integer (int64) | yes | Seconds to wait before retrying. |

### BloemXtreamProviderBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `max_connections` | integer (int64) |  | Local shared physical-connection budget; omitted or zero means one. Clamped to a positive provider-reported maximum. |
| `name` | string |  | Optional public provider label, at most 128 UTF-8 bytes and no control characters. Defaults to Xtream; returned in the tuner's model field. |
| `password` | string | yes | Same path-segment constraints as username. Encrypted server-side; never returned or passed to FFmpeg. |
| `type` | string: `xtream` |  | Optional; this endpoint always creates an Xtream provider. |
| `url` | string | yes | HTTP(S) provider base URL, at most 2048 bytes. No embedded credentials, query, fragment, encoded path, traversal, loopback or cloud metadata destinations. Provider redirects are refused. |
| `username` | string | yes | At most 512 UTF-8 bytes. Slash, backslash, NUL, CR, LF and dot-only path segments are not accepted. Encrypted server-side; never returned. |

### CapabilityResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed` | boolean | yes |  |
| `available` | boolean | yes |  |
| `heartbeat_interval_seconds` | integer (int64) | yes |  |
| `supported` | boolean | yes |  |
| `xtream_supported` | boolean |  |  |

### ChannelResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `callsign` | string | yes |  |
| `enabled` | boolean | yes |  |
| `guide_station_id` | string | yes |  |
| `hd` | boolean | yes |  |
| `id` | string | yes |  |
| `logo_url` | string | yes |  |
| `name` | string | yes |  |
| `number` | string | yes |  |
| `number_override` | string |  |  |
| `stream_url` | string | yes |  |
| `tuner_id` | string | yes |  |

### ChannelsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channels` | array of [`ChannelResponse`](#channelresponse) | yes |  |

### DiscoverTunersResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `candidates` | array of [`DiscoveredTuner`](#discoveredtuner) | yes |  |
| `notes` | array of string |  |  |

### DiscoveredTuner

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `already_added` | boolean | yes |  |
| `base_url` | string | yes |  |
| `device_id` | string | yes |  |
| `discover_url` | string | yes |  |
| `firmware` | string | yes |  |
| `friendly_name` | string | yes |  |
| `kind` | string | yes |  |
| `model` | string | yes |  |
| `source` | string | yes |  |
| `tuner_count` | integer (int64) | yes |  |

### GuideResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `end` | string (date-time) | yes |  |
| `programs` | array of [`Program`](#program) | yes |  |
| `start` | string (date-time) | yes |  |

### GuideSource

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `config` | object of string | yes |  |
| `display_name` | string | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `last_error` | string | yes |  |
| `last_sync_at` | string (date-time) |  |  |
| `next_sync_at` | string (date-time) |  |  |
| `priority` | integer (int64) | yes |  |
| `status` | string | yes |  |
| `type` | string | yes |  |

### Program

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string | yes |  |
| `description` | string | yes |  |
| `episode` | integer (int64) |  |  |
| `external_id` | string |  |  |
| `genres` | array of string | yes |  |
| `id` | string | yes |  |
| `image_url` | string | yes |  |
| `is_live` | boolean | yes |  |
| `is_new` | boolean | yes |  |
| `season` | integer (int64) |  |  |
| `series_id` | string | yes |  |
| `source_id` | string |  |  |
| `start` | string (date-time) | yes |  |
| `stop` | string (date-time) | yes |  |
| `subtitle` | string | yes |  |
| `title` | string | yes |  |

### Recording

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string | yes |  |
| `id` | string | yes |  |
| `last_error` | string |  |  |
| `library_item_id` | string |  |  |
| `path` | string |  |  |
| `profile_id` | string |  |  |
| `program_id` | string |  |  |
| `series_rule_id` | string |  |  |
| `start` | string (date-time) | yes |  |
| `status` | string | yes |  |
| `stop` | string (date-time) | yes |  |
| `title` | string | yes |  |
| `user_id` | integer (int64) |  |  |

### RecordingsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `recordings` | array of [`Recording`](#recording) | yes |  |

### SeriesRule

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channel_id` | string |  |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `keep_last` | integer (int64) | yes |  |
| `new_only` | boolean | yes |  |
| `profile_id` | string |  |  |
| `series_id` | string | yes |  |
| `title_match` | string | yes |  |
| `user_id` | integer (int64) |  |  |

### SeriesRulesResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `series_rules` | array of [`SeriesRule`](#seriesrule) | yes |  |

### SessionStartResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `hls_url` | string | yes |  |
| `note` | string |  |  |
| `playback_ticket` | string | yes |  |
| `session_id` | string | yes |  |
| `stream_url` | string | yes |  |
| `transport` | string |  |  |

### Tuner

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `base_url` | string | yes |  |
| `channel_count` | integer (int64) | yes |  |
| `device_id` | string | yes |  |
| `discover_url` | string | yes |  |
| `firmware` | string | yes |  |
| `id` | string | yes |  |
| `last_error` | string | yes |  |
| `last_scan_at` | string (date-time) |  |  |
| `model` | string | yes |  |
| `status` | string | yes |  |
| `transcode_codecs` | array of string or null | yes |  |
| `tuner_count` | integer (int64) | yes |  |
| `type` | string | yes |  |
