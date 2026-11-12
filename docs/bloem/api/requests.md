# Requests API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Bloem's audiobook and ebook request lanes: filing and reading requests, the organization queues, and the acquisition connections. Movie and series requests are Silo's, under /api/v2.

For the plain-language picture, read the [Requests explainer](../../architecture/bloem-requests.md). The invariants are in:

- [Bloem request policy](../../architecture/bloem-request-policy.md)
- [Audiobook requests](../../architecture/bloem-audiobook-requests.md)
- [Ebook requests](../../architecture/bloem-ebook-request-foundation.md)

## Operations (33)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/admin/organization/audiobook-request-integrations`](#get-apibloemv2adminorganizationaudiobook-request-integrations) | Read audiobook acquisition connections and eligible libraries/installations. |
| [`POST /api/bloem/v2/admin/organization/audiobook-request-integrations`](#post-apibloemv2adminorganizationaudiobook-request-integrations) | Create an audiobook acquisition connection. |
| [`PUT /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}`](#put-apibloemv2adminorganizationaudiobook-request-integrationsid) | Edit an audiobook acquisition connection and rotate its credentials. |
| [`PUT /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}/library`](#put-apibloemv2adminorganizationaudiobook-request-integrationsidlibrary) | Bind an acquisition connection to a local audiobook library and download directories. |
| [`POST /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}/test`](#post-apibloemv2adminorganizationaudiobook-request-integrationsidtest) | Run the audiobook plugin's connection checks for a saved connection. |
| [`GET /api/bloem/v2/admin/organization/book-requests`](#get-apibloemv2adminorganizationbook-requests) | List audiobook requests in the selected administrative organization. |
| [`GET /api/bloem/v2/admin/organization/book-requests/acquisitions`](#get-apibloemv2adminorganizationbook-requestsacquisitions) | List audiobook acquisitions by state in the selected organization. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/accept-download`](#post-apibloemv2adminorganizationbook-requestsidaccept-download) | Accept the selected audiobook download for import. |
| [`GET /api/bloem/v2/admin/organization/book-requests/{id}/acquisition`](#get-apibloemv2adminorganizationbook-requestsidacquisition) | Read an audiobook acquisition and its recent decisions. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/approve`](#post-apibloemv2adminorganizationbook-requestsidapprove) | Approve an audiobook request; keep it awaiting fulfillment. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/approve-candidate`](#post-apibloemv2adminorganizationbook-requestsidapprove-candidate) | Approve a held audiobook candidate for a new acquisition attempt. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/decline`](#post-apibloemv2adminorganizationbook-requestsiddecline) | Decline a pending audiobook request and refund its quota slot. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/fulfil`](#post-apibloemv2adminorganizationbook-requestsidfulfil) | Manually mark an audiobook request fulfilled. |
| [`POST /api/bloem/v2/admin/organization/book-requests/{id}/retry`](#post-apibloemv2adminorganizationbook-requestsidretry) | Retry or recover an audiobook acquisition. |
| [`GET /api/bloem/v2/admin/organization/ebook-request-integrations`](#get-apibloemv2adminorganizationebook-request-integrations) | Read ebook policy, connections and eligible libraries/installations. |
| [`POST /api/bloem/v2/admin/organization/ebook-request-integrations`](#post-apibloemv2adminorganizationebook-request-integrations) | Create an explicit Bookwarehouse ebook fulfillment connection. |
| [`PUT /api/bloem/v2/admin/organization/ebook-request-integrations/{id}/library`](#put-apibloemv2adminorganizationebook-request-integrationsidlibrary) | Bind a connection to an eligible native Bookwarehouse ebook library. |
| [`POST /api/bloem/v2/admin/organization/ebook-request-integrations/{id}/test`](#post-apibloemv2adminorganizationebook-request-integrationsidtest) | Run the Bookwarehouse plugin's connection test for a saved connection. |
| [`PUT /api/bloem/v2/admin/organization/ebook-request-policy`](#put-apibloemv2adminorganizationebook-request-policy) | Explicitly enable or disable ebook request admission and dispatch. |
| [`GET /api/bloem/v2/admin/organization/ebook-requests`](#get-apibloemv2adminorganizationebook-requests) | List ebook requests in the selected default organization. |
| [`POST /api/bloem/v2/admin/organization/ebook-requests/{id}/approve`](#post-apibloemv2adminorganizationebook-requestsidapprove) | Approve a pending ebook request. |
| [`POST /api/bloem/v2/admin/organization/ebook-requests/{id}/decline`](#post-apibloemv2adminorganizationebook-requestsiddecline) | Decline a pending ebook request and refund its quota slot. |
| [`POST /api/bloem/v2/admin/organization/ebook-requests/{id}/retry`](#post-apibloemv2adminorganizationebook-requestsidretry) | Recover uncertain provider state or retry a safely failed ebook dispatch. |
| [`POST /api/bloem/v2/book-requests`](#post-apibloemv2book-requests) | File an audiobook request. |
| [`GET /api/bloem/v2/book-requests/capability`](#get-apibloemv2book-requestscapability) | Read audiobook request capability and current viewer policy. |
| [`GET /api/bloem/v2/book-requests/mine`](#get-apibloemv2book-requestsmine) | List this profile's audiobook requests in the current organization. |
| [`GET /api/bloem/v2/book-requests/{id}`](#get-apibloemv2book-requestsid) | Read this profile's audiobook request. |
| [`POST /api/bloem/v2/book-requests/{id}/cancel`](#post-apibloemv2book-requestsidcancel) | Withdraw a pending or approved audiobook request. |
| [`POST /api/bloem/v2/ebook-requests`](#post-apibloemv2ebook-requests) | File an ISBN ebook request. |
| [`GET /api/bloem/v2/ebook-requests/capability`](#get-apibloemv2ebook-requestscapability) | Read ebook request capability and current viewer policy. |
| [`GET /api/bloem/v2/ebook-requests/mine`](#get-apibloemv2ebook-requestsmine) | List this profile's ebook request history. |
| [`GET /api/bloem/v2/ebook-requests/{id}`](#get-apibloemv2ebook-requestsid) | Read an ebook request owned by this profile. |
| [`POST /api/bloem/v2/ebook-requests/{id}/cancel`](#post-apibloemv2ebook-requestsidcancel) | Withdraw a pending or approved ebook request locally. |

## Reference

### `GET /api/bloem/v2/admin/organization/audiobook-request-integrations`

Read audiobook acquisition connections and eligible libraries/installations.

Operation ID: `getBloemAudiobookRequestSetup`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store. Each connection's saved plugin_config and the names of its saved credentials (stored_credentials) are returned only to a platform administrator context; an absent plugin_config then means the saved settings could not be read. Organization administrators receive each connection's identity, binding and readiness.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AudiobookRequestSetup`](#audiobookrequestsetup) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the account or its membership in the organization no longer qualifies. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured. |

### `POST /api/bloem/v2/admin/organization/audiobook-request-integrations`

Create an audiobook acquisition connection.

Operation ID: `createBloemAudiobookRequestIntegration`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store. The installation and capability must be an eligible ordinary audiobook request plugin. Supported media types are fixed to audiobook. Unknown JSON fields and extra bodies are rejected.

Request body (required, `application/json`): [`AudiobookRequestConnectionInput`](#audiobookrequestconnectioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`AudiobookIntegrationSummary`](#audiobookintegrationsummary) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; a name missing or over 500 bytes, installation_id and capability_id not naming an eligible installation, a plugin_config carrying service_kind, or incomplete or invalid credentials. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the context lacks platform_admin effective authority, the account is no longer a platform administrator, or its membership no longer qualifies. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured. |

### `PUT /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}`

Edit an audiobook acquisition connection and rotate its credentials.

Operation ID: `updateBloemAudiobookRequestIntegration`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store. The connection keeps its ID, library binding and in-flight acquisitions. plugin_config is merged over the saved settings: a submitted key replaces its saved value, null removes it, and an omitted key keeps it. A credential that is omitted or empty keeps its saved value only while the URL it is bound to is unchanged; a changed URL requires the credential again, and a URL cleared to an empty string or removed with null drops it. Changing qbittorrent_url or nzbget_url makes downloads already handed to the previous client fail their status checks. Silo's generic request-integration editor refuses audiobook connections. Unknown JSON fields and extra bodies are rejected.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Opaque request integration ID, often a decimal Sonyflake string. |

Request body (required, `application/json`): [`AudiobookRequestConnectionUpdate`](#audiobookrequestconnectionupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AudiobookIntegrationSummary`](#audiobookintegrationsummary) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; a name over 500 bytes, a plugin_config carrying service_kind, a credential missing after its URL changed, or settings the plugin rejected (its validation text is included with credentials withheld). |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the context lacks platform_admin effective authority, the account is no longer a platform administrator, or its membership no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the connection is absent or not usable by this organization's audiobook lane. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: the connection changed concurrently, or its saved installation or capability no longer matches the eligible connection. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured. |

### `PUT /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}/library`

Bind an acquisition connection to a local audiobook library and download directories.

Operation ID: `setBloemAudiobookRequestIntegrationLibrary`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store. library_id=0 clears the binding. Otherwise one to four download roots are required: clean absolute directories inside the selected library's filesystem paths, distinct, not nested, and each holding at most 10000 catalogued files. Existing dispatched work retains its original library, roots and configuration identity. Unknown JSON fields and extra bodies are rejected.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Opaque request integration ID, often a decimal Sonyflake string. |

Request body (required, `application/json`): [`BloemAudiobookRequestLibraryInputBody`](#bloemaudiobookrequestlibraryinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AudiobookIntegrationSummary`](#audiobookintegrationsummary) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; a missing, null or negative library_id, an ID that is not an eligible connection, a library that is not an eligible audiobook library of this organization, invalid download roots, or a connection bound by another organization. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the context lacks platform_admin effective authority, the account is no longer a platform administrator, or its membership no longer qualifies. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured. |

### `POST /api/bloem/v2/admin/organization/audiobook-request-integrations/{id}/test`

Run the audiobook plugin's connection checks for a saved connection.

Operation ID: `testBloemAudiobookRequestIntegration`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store. The request has no body. A failed check is HTTP 200 with ok=false. When a saved credential can no longer be read or no longer matches its URL, the host answers ok=false without calling the plugin; it also withholds a plugin message that repeats a credential.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Opaque request integration ID, often a decimal Sonyflake string. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`RequestIntegrationTestResult`](#requestintegrationtestresult) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the context lacks platform_admin effective authority, the account is no longer a platform administrator, or its membership no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the connection is absent or not usable by this organization's audiobook lane. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: the saved connection's installation or capability no longer matches the eligible connection. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured. |

### `GET /api/bloem/v2/admin/organization/book-requests`

List audiobook requests in the selected administrative organization.

Operation ID: `listBloemAdminBookRequests`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. Results are newest first; pagination is applied after organization isolation. Responses are private and no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | integer (int64) |  |  |
| `offset` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ListResponse`](#listresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: limit must be 1 to 100 and offset nonnegative. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `GET /api/bloem/v2/admin/organization/book-requests/acquisitions`

List audiobook acquisitions by state in the selected organization.

Operation ID: `listBloemAudiobookAcquisitions`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store. Results are ordered by newest change; next_cursor resumes the listing.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `state` | query | string: `all`, `active`, `review`, `failed`, `stuck` |  | Acquisition state filter; omitted means all. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. |
| `limit` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AudiobookAcquisitionList`](#audiobookacquisitionlist) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: limit outside 1 to 100, an unknown state, or a cursor that is not an unmodified next_cursor. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the account or its membership in the organization no longer qualifies. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/accept-download`

Accept the selected audiobook download for import.

Operation ID: `acceptBloemAudiobookDownload`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store. expected_revision must match the current request. Unknown JSON fields and extra bodies are rejected. Accepts a download held for verification; ambiguous multiple-item folders are refused. This does not mark the request fulfilled. A matching item must still be scanned into the retained library.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the account or its membership in the organization no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is not active and approved, it is not held for verification review, or verification found multiple items. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `GET /api/bloem/v2/admin/organization/book-requests/{id}/acquisition`

Read an audiobook acquisition and its recent decisions.

Operation ID: `getBloemAudiobookAcquisition`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store. Includes at most the newest 20 stored decisions.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AudiobookAcquisition`](#audiobookacquisition) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the account or its membership in the organization no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/approve`

Approve an audiobook request; keep it awaiting fulfillment.

Operation ID: `approveBloemBookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. A matching expected_revision is required. Other organizations' requests return 404. Unknown JSON fields and extra bodies are rejected. Responses are private and no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is no longer active, or it is not pending. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/approve-candidate`

Approve a held audiobook candidate for a new acquisition attempt.

Operation ID: `approveBloemAudiobookCandidate`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store. expected_revision must match the current request. candidate_ref must identify a candidate in the newest held selection decision of the current attempt; a malformed reference, including an arbitrary URL, returns 400 and a well-formed reference outside that decision returns 409. Unknown JSON fields and extra bodies are rejected.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`AudiobookCandidateApproval`](#audiobookcandidateapproval).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, a malformed candidate_ref, or expected_revision missing or not positive. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the account or its membership in the organization no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is not active and approved, it is not held for selection review, the candidate is not in the newest held selection decision, its connection was removed or replaced, or the attempt limit was reached. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/decline`

Decline a pending audiobook request and refund its quota slot.

Operation ID: `declineBloemBookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. A matching expected_revision is required. Other organizations' requests return 404. Unknown JSON fields and extra bodies are rejected. Responses are private and no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is no longer active, or it is not pending. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/fulfil`

Manually mark an audiobook request fulfilled.

Operation ID: `fulfilBloemBookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. A matching expected_revision is required. Other organizations' requests return 404. Unknown JSON fields and extra bodies are rejected. Responses are private and no-store. Any active, not yet completed request can be fulfilled. Manual fulfilment records fulfilment=manual without claiming a playable library item.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, or the request is completed or no longer active. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/book-requests/{id}/retry`

Retry or recover an audiobook acquisition.

Operation ID: `retryBloemAudiobookAcquisition`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store. expected_revision must match the current request. Unknown JSON fields and extra bodies are rejected. Recovery retains the existing dispatch identity; uncertain downloads are checked instead of blindly submitted again. A failed, held or stuck acquisition starts a new attempt; a retrying one is checked again. reason is accepted but replaced by the server's requester-safe reason.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is not active and approved, it has no acquisition dispatch, its connection was removed or replaced, nothing is failed, held, stuck or retrying, or the attempt limit was reached. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `GET /api/bloem/v2/admin/organization/ebook-request-integrations`

Read ebook policy, connections and eligible libraries/installations.

Operation ID: `getBloemEbookRequestSetup`.
Credentials: `bloemOrganizationContext`; or `bloemPlatformContext`.

Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store. Any administrator of the default organization may read the setup. An organization context for a hosted organization receives available=false and empty lists.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`EbookRequestSetup`](#ebookrequestsetup) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured. |

### `POST /api/bloem/v2/admin/organization/ebook-request-integrations`

Create an explicit Bookwarehouse ebook fulfillment connection.

Operation ID: `createBloemEbookRequestIntegration`.
Credentials: `bloemOrganizationContext`; or `bloemPlatformContext`.

Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store. Supported media types are fixed to book and EPUB is the default. HTTP requires explicit allow_http for a trusted local endpoint. Native storage installations cannot serve this capability.

Request body (required, `application/json`): [`EbookRequestConnectionInput`](#ebookrequestconnectioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`EbookIntegration`](#ebookintegration) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; name (1 to 500 bytes), base_url (up to 4096 bytes) or api_key (up to 8192 bytes) missing or too long, or installation_id and capability_id not naming an eligible Bookwarehouse installation. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured. |

### `PUT /api/bloem/v2/admin/organization/ebook-request-integrations/{id}/library`

Bind a connection to an eligible native Bookwarehouse ebook library.

Operation ID: `setBloemEbookRequestIntegrationLibrary`.
Credentials: `bloemOrganizationContext`; or `bloemPlatformContext`.

Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store. library_id=0 clears the binding. Existing dispatched work retains its original library and configuration identity.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Opaque request integration ID, often a decimal Sonyflake string. |

Request body (required, `application/json`): [`BloemEbookRequestLibraryInputBody`](#bloemebookrequestlibraryinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`EbookIntegration`](#ebookintegration) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; a missing, null or negative library_id, or an ID that is not an enabled, eligible connection or a library_id that is not an eligible native Bookwarehouse ebook library for this organization. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured. |

### `POST /api/bloem/v2/admin/organization/ebook-request-integrations/{id}/test`

Run the Bookwarehouse plugin's connection test for a saved connection.

Operation ID: `testBloemEbookRequestIntegration`.
Credentials: `bloemOrganizationContext`; or `bloemPlatformContext`.

Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store. The request has no body. A failed test is HTTP 200 with ok=false. A plugin message that repeats the saved API key is withheld.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Opaque request integration ID, often a decimal Sonyflake string. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`RequestIntegrationTestResult`](#requestintegrationtestresult) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the connection is absent or not usable by this organization's ebook lane. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: the saved connection's installation or capability no longer matches the eligible connection. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured. |

### `PUT /api/bloem/v2/admin/organization/ebook-request-policy`

Explicitly enable or disable ebook request admission and dispatch.

Operation ID: `setBloemEbookRequestPolicy`.
Credentials: `bloemOrganizationContext`; or `bloemPlatformContext`.

Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store. The default is false. Disabling prevents new requests and new provider work; work already submitted may continue at Bookwarehouse. History and local cancellation remain available.

Request body (required, `application/json`): [`EbookRequestPolicy`](#ebookrequestpolicy).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`EbookRequestPolicy`](#ebookrequestpolicy) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, or enabled missing or null. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store, platform authority check or plugin host failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured. |

### `GET /api/bloem/v2/admin/organization/ebook-requests`

List ebook requests in the selected default organization.

Operation ID: `listBloemAdminEbookRequests`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. The organization must be the durable default organization. Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Results are newest first.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `limit` | query | integer (int64) |  |  |
| `offset` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ListResponse`](#listresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: limit must be 1 to 100 and offset nonnegative. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the organization is not the durable default organization, or the account or membership no longer qualifies. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/ebook-requests/{id}/approve`

Approve a pending ebook request.

Operation ID: `approveBloemEbookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. The organization must be the durable default organization. Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. A matching expected_revision is required. Unknown JSON fields and extra bodies are rejected. Manual ebook completion is unavailable.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the organization is not the durable default organization, or the account or membership no longer qualifies. requests_disabled: ebook requests are opted out. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an ebook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is no longer active, or it is not pending. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/ebook-requests/{id}/decline`

Decline a pending ebook request and refund its quota slot.

Operation ID: `declineBloemEbookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. The organization must be the durable default organization. Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. A matching expected_revision is required. Unknown JSON fields and extra bodies are rejected. Manual ebook completion is unavailable.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the organization is not the durable default organization, or the account or membership no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an ebook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is no longer active, or it is not pending. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/admin/organization/ebook-requests/{id}/retry`

Recover uncertain provider state or retry a safely failed ebook dispatch.

Operation ID: `retryBloemEbookRequest`.
Credentials: `bloemOrganizationContext`.

Requires an organization administrative-context token; platform-scoped contexts are refused. The organization must be the durable default organization. Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. A matching expected_revision is required. Unknown JSON fields and extra bodies are rejected. Manual ebook completion is unavailable. Uncertain submissions are recovered through read-only provider checks; they are never blindly resubmitted.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended. request_forbidden: the organization is not the durable default organization, or the account or membership no longer qualifies. requests_disabled: ebook requests or ebook acquisition are opted out. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the request is absent, its ID is not a UUID, or it is not an ebook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, the request is not active and approved, its fulfilment is not failed or retrying, it has no dispatch, its connection or library binding changed, or the bound library is no longer an eligible ebook library. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured. |

### `POST /api/bloem/v2/book-requests`

File an audiobook request.

Operation ID: `createBloemBookRequest`.
Credentials: `bearerAuth`.

Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store. ISBN-10 is normalized to ISBN-13; ASIN is uppercased. A title plus author can be filed without identifiers and can be fulfilled manually. An identical Idempotency-Key replay returns HTTP 200 with the existing request, including after completion or withdrawal; changed normalized input returns 409. A different key for an active identity returns 409 without revealing another profile's request. Unknown JSON fields and extra bodies are rejected.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `Idempotency-Key` | header | string | yes | A durable key scoped to organization, account and profile, without spaces, tabs or line breaks. Reuse only for the same normalized request. |

Request body (required, `application/json`): [`CreateInput`](#createinput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | The Idempotency-Key was already used for the same normalized input; the existing request is returned unchanged. |
| 201 | [`Request`](#request) | A new request was filed. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value; a missing title, a title or author over 500 bytes, a note over 2000 bytes, an invalid ISBN or ASIN, no author when neither identifier is given, or an Idempotency-Key that is not 16 to 128 characters without whitespace. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: request policy (profile group, account limits or approval mode) does not let this profile file requests; requests_disabled: requests are disabled. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | already_requested: an active request for the same ISBN, ASIN or title and author exists in this organization; request_state_changed: the Idempotency-Key was used with different normalized input. |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) or [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | request_quota_exceeded: the independent audiobook quota is exhausted (plain native error); rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/book-requests/capability`

Read audiobook request capability and current viewer policy.

Operation ID: `getBloemBookRequestCapability`.
Credentials: `bearerAuth`.

Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Capability`](#capability) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the profile, account, membership or organization no longer qualifies for request policy. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/book-requests/mine`

List this profile's audiobook requests in the current organization.

Operation ID: `listBloemBookRequests`.
Credentials: `bearerAuth`.

Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store. Results are newest first.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `limit` | query | integer (int64) |  |  |
| `offset` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ListResponse`](#listresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: limit must be 1 to 100 and offset nonnegative. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/book-requests/{id}`

Read this profile's audiobook request.

Operation ID: `getBloemBookRequest`.
Credentials: `bearerAuth`.

Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. not_found: the request is absent, its ID is not a UUID, or it is not this profile's audiobook request in this organization. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `POST /api/bloem/v2/book-requests/{id}/cancel`

Withdraw a pending or approved audiobook request.

Operation ID: `cancelBloemBookRequest`.
Credentials: `bearerAuth`.

Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store. expected_revision must match the current row. Withdrawal retains its quota usage.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. not_found: the request is absent, its ID is not a UUID, or it is not this profile's audiobook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, or the request is completed or no longer active. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `POST /api/bloem/v2/ebook-requests`

File an ISBN ebook request.

Operation ID: `createBloemEbookRequest`.
Credentials: `bearerAuth`.

Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Enabled accounts, active membership and a verified current profile are required. Replaying the same normalized payload with its durable Idempotency-Key returns HTTP 200; a changed payload returns 409. Unknown JSON fields, ASIN and extra bodies are rejected.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `Idempotency-Key` | header | string | yes | A durable key scoped to organization, account and profile, without spaces, tabs or line breaks. Reuse only for the same normalized request. |

Request body (required, `application/json`): [`BloemEbookRequestCreateInputBody`](#bloemebookrequestcreateinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | The Idempotency-Key was already used for the same normalized input; the existing request is returned unchanged. |
| 201 | [`Request`](#request) | A new request was filed. |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value (asin is an unknown field here); a missing title or ISBN, an invalid ISBN, a title or author over 500 bytes, a note over 2000 bytes, or an Idempotency-Key that is not 16 to 128 characters without whitespace. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies. It is also returned when request policy (profile group, account limits or approval mode) does not let this profile file requests; requests_disabled: ebook requests are opted out or requests are disabled. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | already_requested: an active request for the same ISBN exists in this organization; request_state_changed: the Idempotency-Key was used with different normalized input. |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) or [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | request_quota_exceeded: the independent ebook quota is exhausted (plain native error); rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/ebook-requests/capability`

Read ebook request capability and current viewer policy.

Operation ID: `getBloemEbookRequestCapability`.
Credentials: `bearerAuth`.

Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Enabled accounts, active membership and a verified current profile are required. An opted-out lane reports enabled=false and can_request=false rather than an error.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Capability`](#capability) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/ebook-requests/mine`

List this profile's ebook request history.

Operation ID: `listBloemEbookRequests`.
Credentials: `bearerAuth`.

Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Enabled accounts, active membership and a verified current profile are required. Results are newest first.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `limit` | query | integer (int64) |  |  |
| `offset` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ListResponse`](#listresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: limit must be 1 to 100 and offset nonnegative. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `GET /api/bloem/v2/ebook-requests/{id}`

Read an ebook request owned by this profile.

Operation ID: `getBloemEbookRequest`.
Credentials: `bearerAuth`.

Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Enabled accounts, active membership and a verified current profile are required.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. not_found: the request is absent, its ID is not a UUID, or it is not this profile's ebook request in this organization. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

### `POST /api/bloem/v2/ebook-requests/{id}/cancel`

Withdraw a pending or approved ebook request locally.

Operation ID: `cancelBloemEbookRequest`.
Credentials: `bearerAuth`.

Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store. Enabled accounts, active membership and a verified current profile are required. A matching expected_revision is required; quota usage is retained.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string |  | The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another. |
| `X-Profile-Token` | header | string |  | PIN verification when required. |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`ActionInput`](#actioninput).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Request`](#request) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required for this session. invalid_request: invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value, expected_revision missing or not positive, or reason over 2000 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended. request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected profile does not exist for this account. not_found: the request is absent, its ID is not a UUID, or it is not this profile's ebook request in this organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | request_state_changed: expected_revision is stale, or the request is completed or no longer active. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access or the request store failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later. |

## Schemas

### AcquisitionDecision

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `admin_approved` | boolean | yes |  |
| `ai` | [`DecisionAI`](#decisionai) |  |  |
| `ai_calls` | integer (int64) | yes |  |
| `audiobook_id` | string |  |  |
| `candidates` | array of [`DecisionCandidate`](#decisioncandidate) |  |  |
| `chosen` | [`DecisionCandidate`](#decisioncandidate) |  |  |
| `detail` | string |  |  |
| `heuristic` | [`DecisionHeuristic`](#decisionheuristic) |  |  |
| `issues` | array of string |  |  |
| `outcome` | string | yes |  |
| `phase` | string | yes |  |
| `queries` | array of string |  |  |
| `sources` | array of [`DecisionSource`](#decisionsource) |  |  |
| `v` | integer (int64) | yes |  |

### AcquisitionDecisionRecord

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `attempt` | integer (int64) | yes |  |
| `created_at` | string (date-time) | yes |  |
| `decision` | [`AcquisitionDecision`](#acquisitiondecision) | yes |  |
| `id` | integer (int64) | yes |  |
| `outcome` | string | yes |  |
| `phase` | string | yes |  |

### ActionInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes |  |
| `reason` | string |  |  |

### AudiobookAcquisition

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ai_calls` | integer (int64) | yes |  |
| `approved_candidate` | string |  |  |
| `attempt` | integer (int64) | yes |  |
| `attempt_started_at` | string (date-time) |  |  |
| `decisions` | array of [`AcquisitionDecisionRecord`](#acquisitiondecisionrecord) |  |  |
| `dispatched` | boolean | yes |  |
| `download_roots` | array of string |  |  |
| `external_status` | string | yes |  |
| `failure_count` | integer (int64) | yes |  |
| `integration_id` | string |  |  |
| `integration_name` | string |  |  |
| `library_id` | integer (int64) |  |  |
| `marker` | string |  |  |
| `next_attempt_at` | string (date-time) |  |  |
| `provider_completed_at` | string (date-time) |  |  |
| `provider_status` | string | yes |  |
| `request` | [`Request`](#request) | yes |  |
| `review_kind` | string |  |  |
| `search_passes` | integer (int64) | yes |  |
| `stuck` | boolean | yes |  |
| `stuck_reason` | string |  |  |

### AudiobookAcquisitionList

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `acquisitions` | array of [`AudiobookAcquisition`](#audiobookacquisition) | yes |  |
| `next_cursor` | string |  |  |

### AudiobookCandidateApproval

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `candidate_ref` | string | yes |  |
| `expected_revision` | integer (int64) | yes |  |

### AudiobookIntegrationSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `binding_revision` | integer (int64) | yes |  |
| `capability_id` | string | yes |  |
| `download_roots` | array of string | yes |  |
| `id` | string | yes |  |
| `installation_id` | integer (int64) | yes |  |
| `library_id` | integer (int64) | yes |  |
| `library_name` | string |  |  |
| `name` | string | yes |  |
| `native` | boolean |  |  |
| `plugin_config` | object of any |  |  |
| `ready` | boolean | yes |  |
| `stored_credentials` | array of string |  |  |

### AudiobookLibrary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) | yes |  |
| `language` | string | yes |  |
| `name` | string | yes |  |
| `paths` | array of string | yes |  |

### AudiobookRequestConnectionInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `capability_id` | string | yes |  |
| `credentials` | object of string | yes |  |
| `installation_id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `plugin_config` | object of any | yes |  |

### AudiobookRequestConnectionUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `credentials` | object of string |  |  |
| `name` | string |  |  |
| `plugin_config` | object of any |  |  |

### AudiobookRequestSetup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `available` | boolean | yes |  |
| `installations` | array of [`EbookRequestInstallation`](#ebookrequestinstallation) | yes |  |
| `integrations` | array of [`AudiobookIntegrationSummary`](#audiobookintegrationsummary) | yes |  |
| `libraries` | array of [`AudiobookLibrary`](#audiobooklibrary) | yes |  |

### BloemAudiobookRequestLibraryInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `download_roots` | array of string | yes | Local download directories within the selected library's filesystem paths. |
| `library_id` | integer (int64) | yes | Eligible audiobook library; zero clears the binding. |

### BloemEbookRequestCreateInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `author` | string |  | At most 500 bytes. |
| `isbn` | string | yes | Valid ISBN-10 or ISBN-13; normalized to canonical ISBN-13. |
| `note` | string |  | At most 2000 bytes. |
| `title` | string | yes | Trimmed; at most 500 bytes. |

### BloemEbookRequestLibraryInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `library_id` | integer (int64) | yes | Eligible native Bookwarehouse ebook library; zero removes the binding. |

### BloemNativeRateLimitError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `rate_limit_exceeded` | yes |  |
| `message` | string | yes |  |
| `retry_after` | integer (int64) | yes | Seconds to wait before retrying. |

### Capability

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `auto_approve` | boolean | yes |  |
| `available` | boolean | yes |  |
| `can_request` | boolean | yes |  |
| `enabled` | boolean | yes |  |
| `fulfilment_backend` | string | yes |  |
| `limit` | integer (int64) | yes |  |
| `manual_fulfilment` | boolean | yes |  |
| `media_types` | array of string | yes |  |
| `remaining` | integer (int64) | yes |  |
| `unlimited` | boolean | yes |  |
| `used` | integer (int64) | yes |  |
| `window_days` | integer (int64) | yes |  |

### CreateInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asin` | string |  | Ten letters or digits; uppercased. |
| `author` | string |  | At most 500 bytes. Required when neither isbn nor asin is supplied. |
| `isbn` | string |  | Valid ISBN-10 or ISBN-13; normalized to ISBN-13. |
| `note` | string |  | At most 2000 bytes. |
| `title` | string | yes | Trimmed; 1 to 500 bytes. |

### DecisionAI

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `choice` | string | yes |  |
| `confidence` | number (double) | yes |  |
| `error` | string | yes |  |
| `flags` | array of string | yes |  |
| `model` | string | yes |  |
| `reason` | string | yes |  |
| `used` | boolean | yes |  |

### DecisionCandidate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `format` | string |  |  |
| `h` | number (double) | yes |  |
| `ref` | string | yes |  |
| `size_mb` | integer (int64) | yes |  |
| `source` | string | yes |  |
| `title` | string | yes |  |

### DecisionHeuristic

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `h` | number (double) | yes |  |
| `reason` | string | yes |  |

### DecisionSource

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes |  |
| `status` | string | yes |  |

### EbookIntegration

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `library_id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### EbookLibrary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### EbookRequestConnectionInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allow_http` | boolean |  |  |
| `api_key` | string | yes |  |
| `base_url` | string | yes |  |
| `capability_id` | string | yes |  |
| `installation_id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### EbookRequestInstallation

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `capability_id` | string | yes |  |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `native` | boolean |  |  |

### EbookRequestPolicy

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean | yes |  |

### EbookRequestSetup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `available` | boolean | yes |  |
| `enabled` | boolean | yes |  |
| `installations` | array of [`EbookRequestInstallation`](#ebookrequestinstallation) | yes |  |
| `integrations` | array of [`EbookIntegration`](#ebookintegration) | yes |  |
| `libraries` | array of [`EbookLibrary`](#ebooklibrary) | yes |  |

### ListResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `requests` | array of [`Request`](#request) | yes |  |

### Request

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asin` | string |  |  |
| `author` | string | yes |  |
| `completed_at` | string (date-time) |  |  |
| `created_at` | string (date-time) | yes |  |
| `fulfilment` | string | yes |  |
| `id` | string | yes |  |
| `isbn` | string |  |  |
| `library_content_id` | string |  |  |
| `media_type` | string | yes |  |
| `note` | string |  |  |
| `organization_id` | string | yes |  |
| `outcome` | string | yes |  |
| `outcome_reason` | string |  |  |
| `requested_by_profile_id` | string | yes |  |
| `requested_by_user_id` | integer (int64) | yes |  |
| `revision` | integer (int64) | yes |  |
| `status` | string | yes |  |
| `title` | string | yes |  |
| `updated_at` | string (date-time) | yes |  |

### RequestIntegrationTestResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `message` | string | yes |  |
| `ok` | boolean | yes |  |
