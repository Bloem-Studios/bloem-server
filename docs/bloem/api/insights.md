# Stream insights API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Where live streams come from and how they are connected, for platform administrators; limit use, refusals at a stream or transcode limit and viewing stats, for the platform and for an organization's own members.

For the plain-language picture, read the [Stream insights explainer](../../architecture/bloem-stream-insights.md). The invariants are in:

- [Admin app push notifications](../../architecture/bloem-admin-push.md)

## Operations (12)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/admin/organization/limits/refusals`](#get-apibloemv2adminorganizationlimitsrefusals) | List the members' refusals at a limit. |
| [`GET /api/bloem/v2/admin/organization/limits/usage`](#get-apibloemv2adminorganizationlimitsusage) | Show each live member's use of their limits. |
| [`GET /api/bloem/v2/admin/organization/stats/clients`](#get-apibloemv2adminorganizationstatsclients) | Count the members' sessions per client. |
| [`GET /api/bloem/v2/admin/organization/stats/heatmap`](#get-apibloemv2adminorganizationstatsheatmap) | Count the members' plays by weekday and hour. |
| [`GET /api/bloem/v2/admin/organization/stats/top-people`](#get-apibloemv2adminorganizationstatstop-people) | Rank members by watch time. |
| [`GET /api/bloem/v2/admin/organization/stats/transcode-causes`](#get-apibloemv2adminorganizationstatstranscode-causes) | Count why the members' sessions were adapted. |
| [`GET /api/bloem/v2/admin/platform/limits/refusals`](#get-apibloemv2adminplatformlimitsrefusals) | List refusals at a stream or transcode limit. |
| [`GET /api/bloem/v2/admin/platform/limits/usage`](#get-apibloemv2adminplatformlimitsusage) | Show each live person's use of their limits. |
| [`GET /api/bloem/v2/admin/platform/stream-insights/capabilities`](#get-apibloemv2adminplatformstream-insightscapabilities) | Say what stream insights can show. |
| [`GET /api/bloem/v2/admin/platform/streams/insights`](#get-apibloemv2adminplatformstreamsinsights) | List the live streams' routes, connections and limits. |
| [`GET /api/bloem/v2/admin/platform/streams/trails`](#get-apibloemv2adminplatformstreamstrails) | List streams' routes for a map. |
| [`GET /api/bloem/v2/admin/platform/streams/transport`](#get-apibloemv2adminplatformstreamstransport) | Count streams by how they are connected. |

## Reference

### `GET /api/bloem/v2/admin/organization/limits/refusals`

List the members' refusals at a limit.

Operation ID: `listBloemOrganizationLimitRefusals`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. Newest first, 100 a page.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 7). |
| `cursor` | query | string |  | next_cursor from the previous page. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLimitRefusals`](#bloemlimitrefusals) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90, or the cursor is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/organization/limits/usage`

Show each live member's use of their limits.

Operation ID: `getBloemOrganizationLimitsUsage`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. As the Platform route, for the organization's members.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLimitsUsage`](#bloemlimitsusage) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/organization/stats/clients`

Count the members' sessions per client.

Operation ID: `listBloemOrganizationClientStats`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. Sessions per client name and platform, and how many were transcoded, as Silo's Insights Devices tab counts them (names that differ only in case and spacing are one client).

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 30; 7 for refusals). |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemClientStats`](#bloemclientstats) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/organization/stats/heatmap`

Count the members' plays by weekday and hour.

Operation ID: `getBloemOrganizationPlayHeatmap`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. Seven rows, Monday first, of 24 counts of plays, as Silo's Insights heatmap counts them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 30). |
| `tz` | query | string |  | IANA time zone the weekday and hour are read in (default UTC). |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlayHeatmap`](#bloemplayheatmap) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90, or tz is not an IANA time zone. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/organization/stats/top-people`

Rank members by watch time.

Operation ID: `listBloemOrganizationTopPeople`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. The 50 members who watched most, counted as Silo's playback Insights counts (GET /api/v2/admin/insights/people): watch time over the sessions that started in the window, and plays as Silo's viewings.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 30; 7 for refusals). |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemTopPeople`](#bloemtoppeople) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/organization/stats/transcode-causes`

Count why the members' sessions were adapted.

Operation ID: `listBloemOrganizationTranscodeCauses`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative context; Platform contexts are refused with insufficient_organization_authority. Counts only the organization's members and never carries a location, edge, Cloudflare colo or transport. Servers that serve it list admin_org_stream_limits_v1. Sessions not played directly, per client and adaptation reason, from the reasons the playback history records, as Silo's Insights Devices tab counts them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 30; 7 for refusals). |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemTranscodeCauses`](#bloemtranscodecauses) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not this organization's administrative context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/limits/refusals`

List refusals at a stream or transcode limit.

Operation ID: `listBloemLimitRefusals`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. Newest first, 100 a page. Repeats of one person's refusal at one limit within a minute are one row.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `days` | query | integer (int64) |  | How many days back (default 7). |
| `cursor` | query | string |  | next_cursor from the previous page. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLimitRefusals`](#bloemlimitrefusals) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: days must be between 1 and 90, or the cursor is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/limits/usage`

Show each live person's use of their limits.

Operation ID: `getBloemLimitsUsage`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. A person's own stream and transcode limits, and the household and organization pools they share. A max of 0 is no limit; a pool that does not apply is absent.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLimitsUsage`](#bloemlimitsusage) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/stream-insights/capabilities`

Say what stream insights can show.

Operation ID: `getBloemStreamInsightsCapabilities`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. locations is false without a GeoLite2-City database: every route still answers, with no viewer locations.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemStreamInsightsCapabilities`](#bloemstreaminsightscapabilities) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/streams/insights`

List the live streams' routes, connections and limits.

Operation ID: `listBloemLiveStreamInsights`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. One item per live stream: the viewer's probable location (from the address, which is never stored), the Cloudflare colo and Bloem edge it passed (counted only from a trusted proxy), the server and node serving it (from the bloem.map.locations setting), how the viewer's leg is connected (its latest request), and the person's use of their limits. A hop that is not known is null.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemStreamInsights`](#bloemstreaminsights) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/streams/trails`

List streams' routes for a map.

Operation ID: `listBloemStreamTrails`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. Each trail lists, in order, the hops that have coordinates: viewer, cloudflare, edge, server, node. At most 2000 trails, newest first; truncated says more were left out.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `window` | query | string: `live`, `24h`, `7d` |  | live (default): the streams playing now; 24h or 7d: those started in that time. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemStreamTrails`](#bloemstreamtrails) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: window must be live, 24h or 7d. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

### `GET /api/bloem/v2/admin/platform/streams/transport`

Count streams by how they are connected.

Operation ID: `getBloemStreamTransport`.
Credentials: `bloemPlatformContext`.

Requires a Platform administrative context; organization contexts are refused with insufficient_platform_authority. counts the window's streams by the viewer's leg: udp (HTTP/3 or direct UDP), tcp (HTTP/1.1 or HTTP/2), cloudflare or unknown. A finished stream counts in the class that carried most of its requests. series has hourly buckets for 24h and six-hour buckets for 7d, by start time; it is empty for live.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `window` | query | string: `live`, `24h`, `7d` |  | live (default): the streams playing now; 24h or 7d: those started in that time. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TransportSummary`](#transportsummary) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not a Platform context. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed: window must be live, 24h or 7d. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | stream_insights_unavailable: the database or the live session list is unavailable. |

## Schemas

### BloemClientStats

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `clients` | array of [`ClientStat`](#clientstat) | yes |  |

### BloemLimitRefusals

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`Refusal`](#refusal) | yes |  |
| `next_cursor` | string |  |  |

### BloemLimitsUsage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `people` | array of [`PersonUsage`](#personusage) | yes |  |

### BloemPlayHeatmap

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `days` | integer (int64) | yes |  |
| `plays` | array of array of integer (int64) | yes | Seven rows, Monday first, of 24 hourly counts of plays, as Silo's Insights counts them. |
| `time_zone` | string | yes |  |

### BloemStreamCloudflare

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `city` | string |  |  |
| `colo` | string | yes |  |
| `lat` | number (double) |  |  |
| `lon` | number (double) |  |  |

### BloemStreamEdge

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `lat` | number (double) | yes |  |
| `lon` | number (double) | yes |  |
| `name` | string | yes |  |

### BloemStreamInsight

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cloudflare` | [`BloemStreamCloudflare`](#bloemstreamcloudflare) | yes |  |
| `edge` | [`BloemStreamEdge`](#bloemstreamedge) | yes |  |
| `limits` | [`PersonUsage`](#personusage) | yes |  |
| `node` | [`BloemStreamPlace`](#bloemstreamplace) | yes |  |
| `server` | [`BloemStreamPlace`](#bloemstreamplace) | yes |  |
| `session_id` | string | yes |  |
| `transport` | [`BloemStreamTransport`](#bloemstreamtransport) | yes |  |
| `viewer` | [`BloemStreamViewer`](#bloemstreamviewer) | yes |  |

### BloemStreamInsights

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`BloemStreamInsight`](#bloemstreaminsight) | yes |  |

### BloemStreamInsightsCapabilities

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `available` | boolean | yes |  |
| `edge_header` | string | yes | The header a Bloem edge adds to every request it forwards, accepted only from a trusted proxy. |
| `locations` | boolean | yes | Whether viewer locations resolve: false without a GeoLite2-City database. |
| `retention_days` | integer (int64) | yes |  |

### BloemStreamPlace

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `label` | string | yes |  |
| `lat` | number (double) | yes |  |
| `lon` | number (double) | yes |  |

### BloemStreamTrails

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `trails` | array of [`Trail`](#trail) | yes |  |
| `truncated` | boolean | yes | More streams than the 2000 listed were in the window. |

### BloemStreamTransport

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `changed` | boolean | yes | The stream's requests came over more than one class. |
| `server_proto` | string |  | The protocol requests reached this server with. |
| `viewer` | string: `udp`, `tcp`, `cloudflare`, `unknown` | yes | The viewer's leg: udp (HTTP/3 or direct UDP), tcp (HTTP/1.1 or HTTP/2), cloudflare (the viewer reached Cloudflare) or unknown. |
| `viewer_proto` | string |  |  |

### BloemStreamViewer

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `approximate` | boolean | yes |  |
| `city` | string | yes |  |
| `country` | string | yes |  |
| `country_code` | string | yes |  |
| `lat` | number (double) | yes |  |
| `lon` | number (double) | yes |  |

### BloemTopPeople

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `people` | array of [`TopPerson`](#topperson) | yes |  |

### BloemTranscodeCauses

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `causes` | array of [`TranscodeCause`](#transcodecause) | yes |  |

### ClientStat

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `client_name` | string | yes |  |
| `device_platform` | string | yes |  |
| `sessions` | integer (int64) | yes |  |
| `transcode_sessions` | integer (int64) | yes |  |

### Count

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `active` | integer (int64) | yes |  |
| `max` | integer (int64) | yes |  |

### Hop

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `approximate` | boolean | yes |  |
| `kind` | string | yes |  |
| `label` | string | yes |  |
| `lat` | number (double) | yes |  |
| `lon` | number (double) | yes |  |

### PersonUsage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `household` | [`PoolUsage`](#poolusage) |  |  |
| `organization` | [`PoolUsage`](#poolusage) |  |  |
| `streams` | [`Count`](#count) | yes |  |
| `transcodes` | [`Count`](#count) | yes |  |
| `user_id` | integer (int64) | yes |  |
| `username` | string | yes |  |

### PoolUsage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `streams` | [`Count`](#count) |  |  |
| `transcodes` | [`Count`](#count) |  |  |

### Refusal

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | yes |  |
| `refused_at` | string (date-time) | yes |  |
| `scope` | string | yes |  |
| `user_id` | integer (int64) | yes |  |
| `username` | string | yes |  |

### TopPerson

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `plays` | integer (int64) | yes |  |
| `user_id` | integer (int64) | yes |  |
| `username` | string | yes |  |
| `watched_seconds` | number (double) | yes |  |

### Trail

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `hops` | array of [`Hop`](#hop) | yes |  |
| `live` | boolean | yes |  |
| `session_id` | string | yes |  |
| `transport` | string | yes |  |

### TranscodeCause

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `client_name` | string | yes |  |
| `reason` | string | yes |  |
| `sessions` | integer (int64) | yes |  |

### TransportBucket

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `at` | string (date-time) | yes |  |
| `cloudflare` | integer (int64) | yes |  |
| `tcp` | integer (int64) | yes |  |
| `udp` | integer (int64) | yes |  |
| `unknown` | integer (int64) | yes |  |

### TransportCounts

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cloudflare` | integer (int64) | yes |  |
| `tcp` | integer (int64) | yes |  |
| `udp` | integer (int64) | yes |  |
| `unknown` | integer (int64) | yes |  |

### TransportSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `counts` | [`TransportCounts`](#transportcounts) | yes |  |
| `series` | array of [`TransportBucket`](#transportbucket) | yes |  |
