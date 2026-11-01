# Messages and promotions API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

The native inbox, announcements, seasonal packs and their public artwork, the campaign cards a viewer sees, campaign authoring and the upgrade prompt.

For the plain-language picture, read the [Messages and promotions explainer](../../architecture/bloem-messages-and-promotions.md). The invariants are in:

- [Server admin announcements](../../architecture/admin-announcements.md)
- [Playback overlays](../../architecture/playback-overlays.md)
- [Seasonal scheduling](../../architecture/seasonal-scheduling.md)
- [Presentation evaluators](../../architecture/bloem-presentation-evaluators.md)
- [Campaign and seasonal asset storage](../../architecture/bloem-campaign-asset-storage.md)

## Operations (23)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v1/admin/platform/ambience/`](#get-apibloemv1adminplatformambience) | List ambience packs and authoring capabilities. |
| [`POST /api/bloem/v1/admin/platform/ambience/`](#post-apibloemv1adminplatformambience) | Create a registry entry. |
| [`POST /api/bloem/v1/admin/platform/ambience/assets`](#post-apibloemv1adminplatformambienceassets) | Upload a standalone authoring asset without attaching it to a pack. |
| [`PUT /api/bloem/v1/admin/platform/ambience/{id}`](#put-apibloemv1adminplatformambienceid) | Replace every editable field of a registry entry. |
| [`DELETE /api/bloem/v1/admin/platform/ambience/{id}`](#delete-apibloemv1adminplatformambienceid) | Delete a registry entry. |
| [`POST /api/bloem/v1/admin/platform/ambience/{id}/assets`](#post-apibloemv1adminplatformambienceidassets) | Upload artwork and attach it to an ambience pack. |
| [`GET /api/bloem/v1/admin/platform/announcements`](#get-apibloemv1adminplatformannouncements) | List published announcements, including withdrawn ones. |
| [`POST /api/bloem/v1/admin/platform/announcements`](#post-apibloemv1adminplatformannouncements) | Publish an announcement to its audience's inboxes. |
| [`DELETE /api/bloem/v1/admin/platform/announcements/{id}`](#delete-apibloemv1adminplatformannouncementsid) | Withdraw an announcement. |
| [`GET /api/bloem/v1/admin/platform/promotions/`](#get-apibloemv1adminplatformpromotions) | List platform-managed campaigns and supported delivery surfaces. |
| [`POST /api/bloem/v1/admin/platform/promotions/`](#post-apibloemv1adminplatformpromotions) | Create a registry entry. |
| [`PUT /api/bloem/v1/admin/platform/promotions/{id}`](#put-apibloemv1adminplatformpromotionsid) | Replace every editable field of a registry entry. |
| [`DELETE /api/bloem/v1/admin/platform/promotions/{id}`](#delete-apibloemv1adminplatformpromotionsid) | Delete a registry entry. |
| [`GET /api/bloem/v1/admin/platform/upgrade-prompt`](#get-apibloemv1adminplatformupgrade-prompt) | Read the upgrade prompt. |
| [`PUT /api/bloem/v1/admin/platform/upgrade-prompt`](#put-apibloemv1adminplatformupgrade-prompt) | Replace the upgrade prompt. |
| [`GET /api/bloem/v1/ambience`](#get-apibloemv1ambience) | Read seasonal presentation for the current verified tenant viewer. |
| [`GET /api/bloem/v1/ambience/assets/{ref}`](#get-apibloemv1ambienceassetsref) | Serve uploaded seasonal and campaign artwork. |
| [`GET /api/bloem/v1/notifications`](#get-apibloemv1notifications) | The acting profile's inbox, newest first, with the alert fields a row needs to render. |
| [`GET /api/bloem/v1/notifications/sync`](#get-apibloemv1notificationssync) | Rows newer than a cursor, for a client waking from a push or reconnecting across a gap. |
| [`GET /api/bloem/v1/notifications/{id}`](#get-apibloemv1notificationsid) | One inbox row. Another profile's row is absent, not forbidden. |
| [`GET /api/bloem/v1/promotions`](#get-apibloemv1promotions) | List the campaign cards a profile sees on a surface. |
| [`PUT /api/bloem/v1/promotions/{id}/dismissals/{surface}`](#put-apibloemv1promotionsiddismissalssurface) | Stop showing a campaign to a profile on a surface. |
| [`POST /api/bloem/v1/promotions/{id}/save`](#post-apibloemv1promotionsidsave) | Save an in-playback campaign to the profile's inbox. |

## Reference

### `GET /api/bloem/v1/admin/platform/ambience/`

List ambience packs and authoring capabilities.

Operation ID: `listBloemPlatformAmbience`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAmbienceList`](#bloemambiencelist) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/ambience/`

Create a registry entry.

Operation ID: `createBloemPlatformAmbience`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

Request body (required, `application/json`): [`BloemAmbienceBody`](#bloemambiencebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Pack`](#pack) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON of this shape or exceeds 64 KiB, or a field fails validation. A validation message names the field, including an organization_id that does not exist. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/ambience/assets`

Upload a standalone authoring asset without attaching it to a pack.

Operation ID: `uploadBloemPlatformAmbienceAsset`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. Requires configured asset upload storage, reported as storage_available by the ambience list. Send multipart/form-data with a file part of at most 8 MiB; the entire form is capped at 9 MiB. PNG, WebP, JPEG and GIF are accepted by sniffing the bytes, regardless of declared content type. Stored artwork is content-addressed; url is its public server path under /api/bloem/v1/ambience/assets/. Paths under /api/v1/ambience/assets/ from earlier uploads stay valid in pack and campaign bodies. asset_id is optional. Reusing it with identical bytes returns the existing asset; different bytes replace its recorded reference.

Request body (required, `multipart/form-data`): [`BloemAmbienceUploadForm`](#bloemambienceuploadform).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemAmbienceUploadResponse`](#bloemambienceuploadresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: invalid multipart form, missing file part, checksum mismatch, asset_id longer than 128 bytes or an unknown kind. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 413 | [`BloemNativeError`](README.md#bloemnativeerror) | too_large: artwork or the multipart body exceeds its limit. |
| 415 | [`BloemNativeError`](README.md#bloemnativeerror) | unsupported_image: bytes are not a supported raster image. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry or asset upload storage is not configured. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/platform/ambience/{id}`

Replace every editable field of a registry entry.

Operation ID: `updateBloemPlatformAmbience`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Stored ambience pack identity. |

Request body (required, `application/json`): [`BloemAmbienceBody`](#bloemambiencebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Pack`](#pack) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON of this shape or exceeds 64 KiB, or a field fails validation. A validation message names the field, including an organization_id that does not exist. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: registry entry does not exist (including a repeated deletion). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/ambience/{id}`

Delete a registry entry.

Operation ID: `deleteBloemPlatformAmbience`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. Stored artwork objects are left in place.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Stored registry identity. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: registry entry does not exist (including a repeated deletion). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/ambience/{id}/assets`

Upload artwork and attach it to an ambience pack.

Operation ID: `attachBloemPlatformAmbienceAsset`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. Requires configured asset upload storage, reported as storage_available by the ambience list. Send multipart/form-data with a file part of at most 8 MiB; the entire form is capped at 9 MiB. PNG, WebP, JPEG and GIF are accepted by sniffing the bytes, regardless of declared content type. Stored artwork is content-addressed; url is its public server path under /api/bloem/v1/ambience/assets/. Paths under /api/v1/ambience/assets/ from earlier uploads stay valid in pack and campaign bodies. The default slot is banner; sprite appends to the pack. Do not automatically retry sprite attachment: it can append twice.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Stored ambience pack identity. |

Request body (required, `multipart/form-data`): [`BloemAmbienceAttachForm`](#bloemambienceattachform).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemAmbienceAttachResponse`](#bloemambienceattachresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: invalid multipart form, missing file part, a slot other than banner or sprite, or a sprite for a pack that already has 32. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: ambience pack does not exist. |
| 413 | [`BloemNativeError`](README.md#bloemnativeerror) | too_large: artwork or the multipart body exceeds its limit. |
| 415 | [`BloemNativeError`](README.md#bloemnativeerror) | unsupported_image: bytes are not a supported raster image. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the ambience registry or asset upload storage is not configured. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/announcements`

List published announcements, including withdrawn ones.

Operation ID: `listBloemPlatformAnnouncements`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAnnouncementList`](#bloemannouncementlist) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: announcements are not configured. tenant_unavailable or service_unavailable: the credential could not be checked. |

### `POST /api/bloem/v1/admin/platform/announcements`

Publish an announcement to its audience's inboxes.

Operation ID: `createBloemPlatformAnnouncement`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Publishes at once: the audience is resolved and every recipient's inbox receives it. type is system.announcement (informational) or system.alert (operational). targeting.audience is all, role, organization, library or explicit. There is no draft, schedule or edit.

Request body (required, `application/json`): [`AnnouncementInput`](#announcementinput).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Announcement`](#announcement) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON, exceeds 64 KiB, or a field fails validation (title up to 120 characters, body up to 2,000, button label up to 40, links up to 2,048; a critical message cannot be dismissible). |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | no_recipients: the targeting matches no one. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: announcements are not configured. tenant_unavailable or service_unavailable: the credential could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/announcements/{id}`

Withdraw an announcement.

Operation ID: `withdrawBloemPlatformAnnouncement`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. The record stays, with withdrawn_at set. Idempotent.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The announcement ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such announcement. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: announcements are not configured. tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v1/admin/platform/promotions/`

List platform-managed campaigns and supported delivery surfaces.

Operation ID: `listBloemPlatformPromotions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v1/capabilities lists playback_overlays_v1.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPromotionList`](#bloempromotionlist) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/promotions/`

Create a registry entry.

Operation ID: `createBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v1/capabilities lists playback_overlays_v1. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

Request body (required, `application/json`): [`BloemPromotionBody`](#bloempromotionbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Promotion`](#promotion) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON of this shape or exceeds 64 KiB, or a field fails validation. A validation message names the field, including an organization_id that does not exist. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/platform/promotions/{id}`

Replace every editable field of a registry entry.

Operation ID: `updateBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v1/capabilities lists playback_overlays_v1. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Stored campaign identity. |

Request body (required, `application/json`): [`BloemPromotionBody`](#bloempromotionbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Promotion`](#promotion) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON of this shape or exceeds 64 KiB, or a field fails validation. A validation message names the field, including an organization_id that does not exist. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: registry entry does not exist (including a repeated deletion). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/promotions/{id}`

Delete a registry entry.

Operation ID: `deleteBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v1/capabilities lists playback_overlays_v1.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Stored registry identity. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: registry entry does not exist (including a repeated deletion). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/upgrade-prompt`

Read the upgrade prompt.

Operation ID: `getBloemUpgradePrompt`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. The prompt is the inbox alert, with a button to the storefront, that a viewer gets when a stream is refused because their plan's simultaneous-stream limit is reached. Off until set.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`UpgradePrompt`](#upgradeprompt) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | upgrade_prompt_unavailable: the database is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v1/admin/platform/upgrade-prompt`

Replace the upgrade prompt.

Operation ID: `putBloemUpgradePrompt`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. Turning it on needs a title, a button label and an https link. A viewer gets at most one prompt per profile per UTC day, through the operational inbox and its push channels, unless their notifications are off.

Request body (required, `application/json`): [`UpgradePrompt`](#upgradeprompt).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`UpgradePrompt`](#upgradeprompt) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not an upgrade prompt. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_upgrade_prompt: a field is too long, the link is not https, or a prompt turned on lacks its title, button label or link. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | upgrade_prompt_unavailable: the database is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v1/ambience`

Read seasonal presentation for the current verified tenant viewer.

Operation ID: `getBloemSeasonalViewer`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile. Account, profile, active organization and membership must agree; policy and security revisions must be positive. Explicit tenant claims are revalidated; legacy account sessions resolve to the default organization, not an organization selected by a header. While that default organization is still initializing, a legacy session's verified viewer receives deployment-wide packs only. The profile's organization is checked before delivering public and organization-owned packs. The existing default-deny route boundary excludes direct-profile sessions. This is private viewer delivery, separate from public login branding. Handler responses use Cache-Control: private, no-store. Availability is advertised by seasonal_viewer_v1 in /api/bloem/v1/capabilities feature_tokens.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | Verified profile belonging to the authenticated account and the resolved active organization. |
| `X-Profile-Token` | header | string |  | PIN verification proof for a locked profile when required by viewer resolution. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemSeasonalViewerResponse`](#bloemseasonalviewerresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required, profile_unverified or an authority denial: select and verify a profile in an active organization. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: viewer profile is unavailable to this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: viewer access could not be resolved. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the session or viewer access could not be checked; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. unavailable: seasonal presentation could not be read. |

### `GET /api/bloem/v1/ambience/assets/{ref}`

Serve uploaded seasonal and campaign artwork.

Operation ID: `getBloemAmbienceAsset`.
Credentials: none (public).

Public: no credential, because artwork shows on the sign-in page. Uploads under /api/bloem/v1/admin/platform/ambience return paths here. The bytes are a PNG, WebP, JPEG or GIF image with its sniffed Content-Type; refs are content-addressed, so responses are immutable (Cache-Control public, max-age one year) and carry the ref as ETag. Paths under /api/v1/ambience/assets/ serve the same files.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `ref` | path | string | yes | The content-addressed reference from an upload's url. |
| `If-None-Match` | header | string |  | The ETag of a copy already held; a match answers 304. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `image/*` string (binary) | The image bytes. |
| 304 |  | Not modified: If-None-Match matched the ETag. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no artwork has this ref. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the artwork could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: seasonal packs or their asset storage are not configured. |

### `GET /api/bloem/v1/notifications`

The acting profile's inbox, newest first, with the alert fields a row needs to render.

Operation ID: `listBloemNotifications`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `status` | query | string: `all`, `unread` |  | unread lists only unread rows; any other value lists all rows. |
| `limit` | query | integer (int64) |  | Maximum rows to return. Values above 100 are treated as 100; a missing, non-numeric or non-positive value uses the default. |
| `cursor` | query | string |  | Opaque cursor from page.next_cursor of a previous inbox page; an invalid cursor answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNotificationPage`](#bloemnotificationpage) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or cursor is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/notifications/sync`

Rows newer than a cursor, for a client waking from a push or reconnecting across a gap.

Operation ID: `syncBloemNotifications`.
Credentials: `bearerAuth`.

Without a cursor, returns the newest rows up to limit, oldest first, with has_more false. With a cursor, returns the rows after it and has_more reports whether more remain; an empty page returns the sent cursor as next_cursor.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `limit` | query | integer (int64) |  | Maximum rows to return. Values above 100 are treated as 100; a missing, non-numeric or non-positive value uses the default. |
| `cursor` | query | string |  | Opaque cursor from page.next_cursor of a previous sync; omit for the newest snapshot. Inbox cursors are not accepted here. An invalid cursor answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNotificationSyncPage`](#bloemnotificationsyncpage) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or cursor is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/notifications/{id}`

One inbox row. Another profile's row is absent, not forbidden.

Operation ID: `getBloemNotification`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `id` | path | string | yes | Delivery identity. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`DeliveryRowPayload`](#deliveryrowpayload) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no row with this ID belongs to the selected profile, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the row could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/promotions`

List the campaign cards a profile sees on a surface.

Operation ID: `listBloemPromotions`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does; the existing default-deny route boundary excludes direct-profile sessions. Availability is advertised by promotions_viewer_v1 in /api/bloem/v1/capabilities feature_tokens. Replaces GET /api/v1/promotions and, for the home surface, the promoted row of the /api/v1 home layout: home cards come with home_position, the row index the campaign row is shown at. Cards the profile dismissed on the surface are left out.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | Verified profile belonging to the authenticated account. |
| `X-Profile-Token` | header | string |  | PIN verification proof for a locked profile when required by viewer resolution. |
| `surface` | query | string: `home`, `detail`, `pre_playback`, `in_playback` | yes | The surface to deliver cards for. |
| `content_id` | query | string |  | The title the cards are shown with. Required for in_playback; narrows detail and pre_playback; ignored for home. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPromotionsResponse`](#bloempromotionsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required, surface is missing or unknown, or content_id is missing for in_playback. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required, profile_unverified or an authority denial: select and verify a profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: viewer profile is unavailable to this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: campaigns could not be loaded. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: campaigns are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked. |

### `PUT /api/bloem/v1/promotions/{id}/dismissals/{surface}`

Stop showing a campaign to a profile on a surface.

Operation ID: `dismissBloemPromotion`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does; the existing default-deny route boundary excludes direct-profile sessions. Availability is advertised by promotions_viewer_v1 in /api/bloem/v1/capabilities feature_tokens. Replaces PUT /api/v1/home/dismissals/promo:{surface}/{id}, and writes the same per-profile dismissal. Idempotent: repeating it refreshes the dismissal.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | Verified profile belonging to the authenticated account. |
| `X-Profile-Token` | header | string |  | PIN verification proof for a locked profile when required by viewer resolution. |
| `id` | path | string | yes | The campaign ID. |
| `surface` | path | string: `home`, `detail`, `pre_playback`, `in_playback` | yes | The surface the campaign is no longer shown on. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required, the surface is unknown, or the campaign ID is longer than 64 bytes. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required, profile_unverified or an authority denial: select and verify a profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: viewer profile is unavailable to this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the dismissal could not be saved. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: campaigns or dismissals are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked. |

### `POST /api/bloem/v1/promotions/{id}/save`

Save an in-playback campaign to the profile's inbox.

Operation ID: `saveBloemPromotion`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does; the existing default-deny route boundary excludes direct-profile sessions. Availability is advertised by promotions_viewer_v1 in /api/bloem/v1/capabilities feature_tokens. Replaces POST /api/v1/promotions/{id}/save. Only a current in_playback card with a call to action for content_id can be saved. The inbox delivery ID is derived from the profile and campaign, so a retry does not add a second copy.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | Verified profile belonging to the authenticated account. |
| `X-Profile-Token` | header | string |  | PIN verification proof for a locked profile when required by viewer resolution. |
| `id` | path | string | yes | The campaign ID. |
| `content_id` | query | string | yes | The title being played; the campaign must be a current in_playback card for it. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPromotionSaveResponse`](#bloempromotionsaveresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is required; unavailable: the campaign cannot be saved. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | viewer_required, profile_unverified or an authority denial: select and verify a profile. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the campaign is no longer available to this profile, or the viewer profile is unavailable. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the campaign could not be saved. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: campaigns or the inbox are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked. |

## Schemas

### AlertBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `body` | string | yes |  |
| `cta` | [`AlertCTA`](#alertcta) |  |  |
| `deeplink` | string |  |  |
| `dismissible` | boolean | yes |  |
| `expires_at` | string (date-time) |  |  |
| `image_url` | string |  |  |
| `severity` | string | yes |  |
| `title` | string | yes |  |

### AlertCTA

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `label` | string | yes |  |
| `url` | string | yes |  |

### Announcement

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `body` | [`AlertBody`](#alertbody) | yes |  |
| `created_at` | string (date-time) | yes |  |
| `created_by` | integer or null (int64) | yes |  |
| `id` | string | yes |  |
| `recipient_count` | integer (int64) | yes |  |
| `targeting` | [`AnnouncementTargeting`](#announcementtargeting) | yes |  |
| `type` | string | yes |  |
| `withdrawn_at` | string or null (date-time) | yes |  |

### AnnouncementInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `body` | [`AlertBody`](#alertbody) | yes |  |
| `targeting` | [`AnnouncementTargeting`](#announcementtargeting) | yes |  |
| `type` | string | yes |  |

### AnnouncementTargeting

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audience` | string | yes |  |
| `library_id` | integer (int64) |  |  |
| `organization_id` | string |  |  |
| `profile_ids` | array of string |  |  |
| `role` | string |  |  |
| `user_ids` | array of integer (int64) |  |  |

### Assets

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `banner_url` | string |  |  |
| `sprites` | array of string |  |  |

### BloemAmbienceAttachForm

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `file` | string (binary) | yes | Image bytes, at most 8 MiB. The server sniffs the actual image type. |
| `slot` | string: `banner`, `sprite` |  | banner replaces the banner URL; sprite appends one sprite, up to 32. |

### BloemAmbienceAttachResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `pack` | [`Pack`](#pack) | yes |  |
| `slot` | string: `banner`, `sprite` | yes |  |
| `url` | string | yes |  |

### BloemAmbienceBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `assets` | [`Assets`](#assets) |  | Optional banner and at most 32 sprites, each an HTTPS URL or a server ambience asset path. |
| `effect_id` | string | yes | Required lowercase effect slug; surrounding whitespace is trimmed. |
| `intensity` | number or null (double) |  | Defaults to 1 when omitted or null. |
| `organization_id` | string or null |  | Null or omitted means deployment-wide; otherwise this pack belongs to the organization. |
| `surfaces` | array of string |  | all, home or login; omitted or empty defaults to all. Values are normalized to lowercase and all absorbs other surfaces. |
| `window` | [`Window`](#window) | yes | starts_at must precede ends_at. timezone defaults to UTC and must be an IANA zone; a repeat_yearly window must be shorter than one year in that zone. |

### BloemAmbienceList

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `packs` | array of [`Pack`](#pack) | yes |  |
| `storage_available` | boolean | yes | Whether server asset upload storage is configured. |
| `yearly_scheduling` | boolean | yes | True: yearly recurring windows are supported. |

### BloemAmbienceUploadForm

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asset_id` | string |  | Optional stable authoring identity, trimmed. Reusing it upserts the stored asset; identical bytes return the same content-addressed object. |
| `checksum` | string |  | Optional SHA-256 hex digest; compared case-insensitively to the uploaded bytes. |
| `content_type` | string |  | Accepted but ignored: the server sniffs the bytes. |
| `file` | string (binary) | yes | Image bytes, at most 8 MiB. The server sniffs the actual image type. |
| `kind` | string: `campaign_card_16x9`, `season_banner`, `season_sprite` |  | Optional authoring asset kind, trimmed. |

### BloemAmbienceUploadResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asset` | [`StoredAsset`](#storedasset) | yes |  |
| `url` | string | yes |  |

### BloemAnnouncementList

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `announcements` | array of [`Announcement`](#announcement) | yes |  |

### BloemNativeRateLimitError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `rate_limit_exceeded` | yes |  |
| `message` | string | yes |  |
| `retry_after` | integer (int64) | yes | Seconds to wait before retrying. |

### BloemNotificationPage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`DeliveryRowPayload`](#deliveryrowpayload) | yes | Inbox rows, newest first. Empty, never null. |
| `page` | [`BloemNotificationPageInfo`](#bloemnotificationpageinfo) | yes |  |
| `read_cutoff` | string | yes | Opaque boundary this traversal was taken against. page.next_cursor already carries it. |

### BloemNotificationPageInfo

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `has_more` | boolean | yes | Whether another page exists. Always false on an initial sync snapshot. |
| `next_cursor` | string |  | Inbox: cursor for the next page, absent on the last page. Sync: the position to resume from, present whenever the page has rows or a cursor was sent. |

### BloemNotificationSyncPage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `initial_snapshot` | boolean | yes | True when no cursor was sent, so this page is the newest snapshot rather than a replayed gap. |
| `items` | array of [`DeliveryRowPayload`](#deliveryrowpayload) | yes | Rows newer than the cursor, oldest first. Empty, never null. |
| `page` | [`BloemNotificationPageInfo`](#bloemnotificationpageinfo) | yes |  |
| `unread_count` | integer (int64) | yes | Unread rows across the whole inbox, not just this page. |

### BloemPromotionBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cta` | [`CTA`](#cta) or null |  | Optional button with a nonblank label of at most 40 bytes and an HTTPS URL, bloem:// deeplink or app path. |
| `deeplink` | string |  | HTTPS URL, bloem:// deeplink or app path. |
| `dismissible` | boolean or null |  | Defaults to true when omitted or null. Must be true for in_playback. |
| `ends_at` | string (date-time) | yes |  |
| `headline` | string | yes | Required nonblank trimmed text, at most 120 bytes. |
| `image_height` | integer or null (int64) |  |  |
| `image_url` | string | yes | HTTPS URL or a server ambience asset path. |
| `image_width` | integer or null (int64) |  | Supply with image_height; declared artwork must be 16:9 within one percent. |
| `kicker` | string |  | Trimmed text, at most 40 bytes. |
| `organization_id` | string or null |  | Null or omitted means deployment-wide; otherwise the campaign belongs to this organization. |
| `placement` | [`Placement`](#placement) |  | in_playback requires playback_style card or pip and dismissible=true. duration_seconds is allowed only with a playback_style, defaults to 10 and must be 5-60; video_url requires pip and HTTPS. home_position must be zero or positive. Detail/pre-playback content_ids are limited to 64 nonblank entries. |
| `priority` | integer (int64) |  |  |
| `starts_at` | string (date-time) | yes | Required start instant, strictly before ends_at. |
| `subtitle` | string |  | Trimmed text, at most 200 bytes. |
| `surfaces` | array of string | yes | One or more of home, detail, pre_playback, in_playback; normalized to lowercase and deduplicated. |
| `targeting` | [`BloemPromotionTargeting`](#bloempromotiontargeting) |  | Defaults to all; fields outside the chosen audience are discarded. |

### BloemPromotionList

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `promotions` | array of [`Promotion`](#promotion) | yes |  |
| `surfaces` | array of string | yes | Supported delivery surfaces: home, detail, pre_playback, in_playback. |

### BloemPromotionSaveResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `status` | string: `saved` | yes |  |

### BloemPromotionTargeting

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audience` | string |  | all (default), role, organization, library or explicit; normalized to lowercase. |
| `library_id` | integer (int64) |  | For library targeting: a positive library id. |
| `organization_id` | string |  | For organization targeting: an organization UUID. |
| `profile_ids` | array of string |  |  |
| `role` | string |  | For role targeting: admin or user. |
| `user_ids` | array of integer (int64) |  | For explicit targeting: account ids; at least one account or profile is required. |

### BloemPromotionsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `home_position` | integer (int64) |  | Home surface only: the index among the profile's home rows at which the campaign row is shown. A value past the last row means after the last row. |
| `promotions` | array of [`Card`](#card) | yes | Cards to show, in order. Empty when none apply. |
| `surface` | string | yes | The surface the cards are for. |

### BloemSeasonalViewerResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ambience` | array of [`Wire`](#wire) | yes | Currently active deployment-wide packs plus packs for the resolved organization. Empty when none apply; entries omit administrative ownership and audit fields. |

### CTA

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `label` | string | yes |  |
| `url` | string | yes |  |

### Card

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cta` | [`CTA`](#cta) |  |  |
| `deeplink` | string |  |  |
| `dismissible` | boolean | yes |  |
| `duration_seconds` | integer (int64) |  |  |
| `expires_at` | string (date-time) | yes |  |
| `headline` | string | yes |  |
| `id` | string | yes |  |
| `image_url` | string | yes |  |
| `kicker` | string | yes |  |
| `playback_style` | string |  |  |
| `subtitle` | string |  |  |
| `video_url` | string |  |  |

### DeliveryRowPayload

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `body` | string |  |  |
| `created_at` | string (date-time) | yes |  |
| `cta` | [`AlertCTA`](#alertcta) |  |  |
| `deeplink` | string |  |  |
| `dismissed_at` | string (date-time) |  |  |
| `dismissible` | boolean |  |  |
| `episode_id` | string |  |  |
| `episode_number` | integer (int64) |  |  |
| `episode_title` | string |  |  |
| `expires_at` | string (date-time) |  |  |
| `id` | string | yes |  |
| `image_url` | string |  |  |
| `library_id` | integer (int64) |  |  |
| `poster_path` | string |  |  |
| `poster_thumbhash` | string |  |  |
| `poster_url` | string |  |  |
| `profile_id` | string | yes |  |
| `read_at` | string or null (date-time) | yes |  |
| `reason_flags` | any | yes |  |
| `season_number` | integer (int64) |  |  |
| `series_id` | string |  |  |
| `series_title` | string |  |  |
| `severity` | string |  |  |
| `title` | string |  |  |
| `type` | string | yes |  |

### Pack

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `assets` | [`Assets`](#assets) | yes |  |
| `created_at` | string (date-time) | yes |  |
| `created_by` | integer (int64) | yes |  |
| `effect_id` | string | yes |  |
| `id` | string | yes |  |
| `intensity` | number (double) | yes |  |
| `organization_id` | string or null | yes |  |
| `surfaces` | array of string | yes |  |
| `updated_at` | string (date-time) | yes |  |
| `window` | [`Window`](#window) | yes |  |

### Placement

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `content_ids` | array of string |  |  |
| `detail_slot` | string |  |  |
| `duration_seconds` | integer (int64) |  |  |
| `home_position` | integer (int64) |  |  |
| `playback_style` | string |  |  |
| `video_url` | string |  |  |

### Promotion

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `created_at` | string (date-time) | yes |  |
| `created_by` | integer (int64) | yes |  |
| `cta` | [`CTA`](#cta) or null | yes |  |
| `deeplink` | string | yes |  |
| `dismissible` | boolean | yes |  |
| `ends_at` | string (date-time) | yes |  |
| `headline` | string | yes |  |
| `id` | string | yes |  |
| `image_height` | integer (int64) |  |  |
| `image_url` | string | yes |  |
| `image_width` | integer (int64) |  |  |
| `kicker` | string | yes |  |
| `organization_id` | string or null | yes |  |
| `placement` | [`Placement`](#placement) | yes |  |
| `priority` | integer (int64) | yes |  |
| `starts_at` | string (date-time) | yes |  |
| `subtitle` | string | yes |  |
| `surfaces` | array of string | yes |  |
| `targeting` | [`AnnouncementTargeting`](#announcementtargeting) | yes |  |
| `updated_at` | string (date-time) | yes |  |

### StoredAsset

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `asset_id` | string | yes |  |
| `checksum` | string | yes |  |
| `content_type` | string | yes |  |
| `kind` | string | yes |  |
| `ref` | string | yes |  |
| `size_bytes` | integer (int64) | yes |  |
| `url` | string | yes |  |

### UpgradePrompt

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `body` | string | yes |  |
| `cta_label` | string | yes |  |
| `cta_url` | string | yes | HTTPS address the button opens, usually the storefront's plan page |
| `enabled` | boolean | yes |  |
| `title` | string | yes |  |
| `updated_at` | string (date-time) | yes |  |

### Window

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ends_at` | string (date-time) | yes |  |
| `repeat_yearly` | boolean |  |  |
| `starts_at` | string (date-time) | yes |  |
| `timezone` | string |  |  |

### Wire

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `assets` | [`Assets`](#assets) | yes |  |
| `effect_id` | string | yes |  |
| `id` | string | yes |  |
| `intensity` | number (double) | yes |  |
| `surfaces` | array of string | yes |  |
| `window` | [`Window`](#window) | yes |  |
