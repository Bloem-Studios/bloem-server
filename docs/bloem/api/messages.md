# Messages and promotions API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

The native inbox, announcements, seasonal packs and their public artwork, the campaign cards a viewer sees, campaign authoring, the upgrade prompt, the admin apps' push notifications and the server's registration with Bloem's push relay.

For the plain-language picture, read the [Messages and promotions explainer](../../architecture/bloem-messages-and-promotions.md). The invariants are in:

- [Server admin announcements](../../architecture/admin-announcements.md)
- [Playback overlays](../../architecture/playback-overlays.md)
- [Seasonal scheduling](../../architecture/seasonal-scheduling.md)
- [Presentation evaluators](../../architecture/bloem-presentation-evaluators.md)
- [Campaign and seasonal asset storage](../../architecture/bloem-campaign-asset-storage.md)
- [Admin app push notifications](../../architecture/bloem-admin-push.md)
- [How push notifications reach each app](../../architecture/bloem-push-relays.md)

## Operations (37)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/admin/platform/ambience/`](#get-apibloemv2adminplatformambience) | List ambience packs and authoring capabilities. |
| [`POST /api/bloem/v2/admin/platform/ambience/`](#post-apibloemv2adminplatformambience) | Create a registry entry. |
| [`POST /api/bloem/v2/admin/platform/ambience/assets`](#post-apibloemv2adminplatformambienceassets) | Upload a standalone authoring asset without attaching it to a pack. |
| [`PUT /api/bloem/v2/admin/platform/ambience/{id}`](#put-apibloemv2adminplatformambienceid) | Replace every editable field of a registry entry. |
| [`DELETE /api/bloem/v2/admin/platform/ambience/{id}`](#delete-apibloemv2adminplatformambienceid) | Delete a registry entry. |
| [`POST /api/bloem/v2/admin/platform/ambience/{id}/assets`](#post-apibloemv2adminplatformambienceidassets) | Upload artwork and attach it to an ambience pack. |
| [`GET /api/bloem/v2/admin/platform/announcements`](#get-apibloemv2adminplatformannouncements) | List published announcements, including withdrawn ones. |
| [`POST /api/bloem/v2/admin/platform/announcements`](#post-apibloemv2adminplatformannouncements) | Publish an announcement to its audience's inboxes. |
| [`DELETE /api/bloem/v2/admin/platform/announcements/{id}`](#delete-apibloemv2adminplatformannouncementsid) | Withdraw an announcement. |
| [`POST /api/bloem/v2/admin/platform/notifications/push/relay/register`](#post-apibloemv2adminplatformnotificationspushrelayregister) | Register the server with Bloem's push relay again, or rotate its credential there. |
| [`GET /api/bloem/v2/admin/platform/promotions/`](#get-apibloemv2adminplatformpromotions) | List platform-managed campaigns and supported delivery surfaces. |
| [`POST /api/bloem/v2/admin/platform/promotions/`](#post-apibloemv2adminplatformpromotions) | Create a registry entry. |
| [`PUT /api/bloem/v2/admin/platform/promotions/{id}`](#put-apibloemv2adminplatformpromotionsid) | Replace every editable field of a registry entry. |
| [`DELETE /api/bloem/v2/admin/platform/promotions/{id}`](#delete-apibloemv2adminplatformpromotionsid) | Delete a registry entry. |
| [`GET /api/bloem/v2/admin/platform/upgrade-prompt`](#get-apibloemv2adminplatformupgrade-prompt) | Read the upgrade prompt. |
| [`PUT /api/bloem/v2/admin/platform/upgrade-prompt`](#put-apibloemv2adminplatformupgrade-prompt) | Replace the upgrade prompt. |
| [`GET /api/bloem/v2/admin/push/content/{delivery_id}`](#get-apibloemv2adminpushcontentdelivery_id) | Read the text and behavior of a notification pushed to this device. |
| [`POST /api/bloem/v2/admin/push/devices`](#post-apibloemv2adminpushdevices) | Register an admin app installation for push. |
| [`DELETE /api/bloem/v2/admin/push/devices/{device_id}`](#delete-apibloemv2adminpushdevicesdevice_id) | Remove one of the account's devices. |
| [`PATCH /api/bloem/v2/admin/push/devices/{device_id}/kinds`](#patch-apibloemv2adminpushdevicesdevice_idkinds) | Switch notification kinds on or off for one device. |
| [`POST /api/bloem/v2/admin/push/devices/{device_id}/live-activities`](#post-apibloemv2adminpushdevicesdevice_idlive-activities) | Register a scan or backup Live Activity the app runs. |
| [`DELETE /api/bloem/v2/admin/push/devices/{device_id}/live-activities/{activity_id}`](#delete-apibloemv2adminpushdevicesdevice_idlive-activitiesactivity_id) | Forget a Live Activity that ended. |
| [`PUT /api/bloem/v2/admin/push/devices/{device_id}/live-activity-start`](#put-apibloemv2adminpushdevicesdevice_idlive-activity-start) | Store or clear the device's push-to-start token. |
| [`POST /api/bloem/v2/admin/push/devices/{device_id}/retire`](#post-apibloemv2adminpushdevicesdevice_idretire) | Stop pushes to a device using its display token. |
| [`PUT /api/bloem/v2/admin/push/devices/{device_id}/widgets`](#put-apibloemv2adminpushdevicesdevice_idwidgets) | Report the widgets the app has placed. |
| [`GET /api/bloem/v2/ambience`](#get-apibloemv2ambience) | Read seasonal presentation for the current verified tenant viewer. |
| [`GET /api/bloem/v2/ambience/assets/{ref}`](#get-apibloemv2ambienceassetsref) | Serve uploaded seasonal and campaign artwork. |
| [`GET /api/bloem/v2/ambience/public`](#get-apibloemv2ambiencepublic) | List the active deployment-wide seasonal packs. |
| [`GET /api/bloem/v2/notifications`](#get-apibloemv2notifications) | The acting profile's inbox, newest first, with the alert fields a row needs to render. |
| [`POST /api/bloem/v2/notifications/read-all`](#post-apibloemv2notificationsread-all) | Mark every inbox row up to a page's read_cutoff read, Bloem's own notices included. |
| [`GET /api/bloem/v2/notifications/sync`](#get-apibloemv2notificationssync) | Rows newer than a cursor, for a client waking from a push or reconnecting across a gap. |
| [`GET /api/bloem/v2/notifications/unread-count`](#get-apibloemv2notificationsunread-count) | How many inbox rows are unread, Bloem's own notices included. |
| [`GET /api/bloem/v2/notifications/{id}`](#get-apibloemv2notificationsid) | One inbox row. Another profile's row is absent, not forbidden. |
| [`POST /api/bloem/v2/notifications/{id}/read`](#post-apibloemv2notificationsidread) | Mark one inbox row read. Repeating it is not an error. |
| [`GET /api/bloem/v2/promotions`](#get-apibloemv2promotions) | List the campaign cards a profile sees on a surface. |
| [`PUT /api/bloem/v2/promotions/{id}/dismissals/{surface}`](#put-apibloemv2promotionsiddismissalssurface) | Stop showing a campaign to a profile on a surface. |
| [`POST /api/bloem/v2/promotions/{id}/save`](#post-apibloemv2promotionsidsave) | Save an in-playback campaign to the profile's inbox. |

## Reference

### `GET /api/bloem/v2/admin/platform/ambience/`

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

### `POST /api/bloem/v2/admin/platform/ambience/`

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

### `POST /api/bloem/v2/admin/platform/ambience/assets`

Upload a standalone authoring asset without attaching it to a pack.

Operation ID: `uploadBloemPlatformAmbienceAsset`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. Requires configured asset upload storage, reported as storage_available by the ambience list. Send multipart/form-data with a file part of at most 8 MiB; the entire form is capped at 9 MiB. PNG, WebP, JPEG and GIF are accepted by sniffing the bytes, regardless of declared content type. Stored artwork is content-addressed; url is its public server path under /api/bloem/v2/ambience/assets/. asset_id is optional. Reusing it with identical bytes returns the existing asset; different bytes replace its recorded reference.

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

### `PUT /api/bloem/v2/admin/platform/ambience/{id}`

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

### `DELETE /api/bloem/v2/admin/platform/ambience/{id}`

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

### `POST /api/bloem/v2/admin/platform/ambience/{id}/assets`

Upload artwork and attach it to an ambience pack.

Operation ID: `attachBloemPlatformAmbienceAsset`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. Requires configured asset upload storage, reported as storage_available by the ambience list. Send multipart/form-data with a file part of at most 8 MiB; the entire form is capped at 9 MiB. PNG, WebP, JPEG and GIF are accepted by sniffing the bytes, regardless of declared content type. Stored artwork is content-addressed; url is its public server path under /api/bloem/v2/ambience/assets/. The default slot is banner; sprite appends to the pack. Do not automatically retry sprite attachment: it can append twice.

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

### `GET /api/bloem/v2/admin/platform/announcements`

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

### `POST /api/bloem/v2/admin/platform/announcements`

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

### `DELETE /api/bloem/v2/admin/platform/announcements/{id}`

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

### `POST /api/bloem/v2/admin/platform/notifications/push/relay/register`

Register the server with Bloem's push relay again, or rotate its credential there.

Operation ID: `registerBloemPlatformPushRelay`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Takes no body. Bloem's push relay delivers the Bloem Admin app's notifications; Silo's apps go through Silo's relay, which Silo's push settings register (registerAdminNotificationRelay). A working credential is rotated, and a relay that no longer accepts it gets a fresh registration. A missing credential, or one parked because the relay disabled this deployment, is replaced by a fresh registration; that is the only way to resume the Bloem Admin app's push after the relay disabled the deployment. The result is stored under bloem.notifications.push_relay_*; Silo's relay credential is never read or changed. The response never carries the credential.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPushRelayRegistration`](#bloempushrelayregistration) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | relay_deployment_rejected: Bloem's relay refused to register this server. |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) | relay_rate_limited: Bloem's relay is limiting registrations; Retry-After says when to try again. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 502 | [`BloemNativeError`](README.md#bloemnativeerror) | relay_error or the relay's own code: Bloem's relay failed or could not be reached. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | push_relay_unavailable: this server has no push backing. relay_unavailable: Bloem's relay is unavailable. tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v2/admin/platform/promotions/`

List platform-managed campaigns and supported delivery surfaces.

Operation ID: `listBloemPlatformPromotions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v2/capabilities lists playback_overlays_v1.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPromotionList`](#bloempromotionlist) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v2/admin/platform/promotions/`

Create a registry entry.

Operation ID: `createBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v2/capabilities lists playback_overlays_v1. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

Request body (required, `application/json`): [`BloemPromotionBody`](#bloempromotionbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`Promotion`](#promotion) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON of this shape or exceeds 64 KiB, or a field fails validation. A validation message names the field, including an organization_id that does not exist. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the registry operation failed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the promotion registry is not configured. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v2/admin/platform/promotions/{id}`

Replace every editable field of a registry entry.

Operation ID: `updateBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v2/capabilities lists playback_overlays_v1. JSON body limit: 64 KiB. Unknown fields and anything after the first JSON value are ignored. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again.

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

### `DELETE /api/bloem/v2/admin/platform/promotions/{id}`

Delete a registry entry.

Operation ID: `deleteBloemPlatformPromotion`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority. In-playback cards (surface in_playback) are available when /api/bloem/v2/capabilities lists playback_overlays_v1.

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

### `GET /api/bloem/v2/admin/platform/upgrade-prompt`

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

### `PUT /api/bloem/v2/admin/platform/upgrade-prompt`

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

### `GET /api/bloem/v2/admin/push/content/{delivery_id}`

Read the text and behavior of a notification pushed to this device.

Operation ID: `getBloemAdminPushContent`.
Credentials: `bloemAdminPushDisplayToken`.

Authenticated by the device's display token, not by a login: the iOS notification extension reads text while the phone is locked. The token opens only this device's own notifications and retire call. The push itself carries only an id; the relay never sees the text. A notification is visible only to a device it was sent to, for the account it was written for, until it expires (24 hours), and only while the account is still an enabled administrator.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `delivery_id` | path | string | yes | The delivery id a push carried, with or without its {device_id}. prefix. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAdminPushContent`](#bloemadminpushcontent) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_display_token: the token is missing, unknown or replaced by a newer registration. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required: the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such notification for this device, or it expired. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `POST /api/bloem/v2/admin/push/devices`

Register an admin app installation for push.

Operation ID: `registerBloemAdminPushDevice`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. Registers or refreshes the installation named by installation_id and returns the device id and a display token. Registering again with the same installation_id keeps the device id, the per-kind switches and the widget report, replaces the token and mints a new display token. A token already held by another account or installation moves to this one. The Apple topic must be com.bloemstudios.admin.

Request body (required, `application/json`): [`BloemAdminPushRegisterRequest`](#bloemadminpushregisterrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAdminPushRegistered`](#bloemadminpushregistered) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not valid JSON for this route, or exceeds 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required: the account is not an enabled administrator; account_session_required: the token is an API key, impersonation or administrative-context token. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: a field is missing or malformed, the topic is not com.bloemstudios.admin, or a kind id is unknown. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `DELETE /api/bloem/v2/admin/push/devices/{device_id}`

Remove one of the account's devices.

Operation ID: `deleteBloemAdminPushDevice`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. Stops every push to the device. Use the retire route when no login is available.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `PATCH /api/bloem/v2/admin/push/devices/{device_id}/kinds`

Switch notification kinds on or off for one device.

Operation ID: `setBloemAdminPushKinds`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. The server stops sending a switched-off kind to this device, including notifications already queued. Kinds not named keep their setting. The two reachability kinds are produced by the apps' own checks; their switches are stored here so they survive a reinstall.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

Request body (required, `application/json`): [`BloemAdminPushKindsRequest`](#bloemadminpushkindsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAdminPushKinds`](#bloemadminpushkinds) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not valid JSON for this route. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: a kind id is unknown. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `POST /api/bloem/v2/admin/push/devices/{device_id}/live-activities`

Register a scan or backup Live Activity the app runs.

Operation ID: `registerBloemAdminLiveActivity`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. The server builds each update from ids, counts, state words and Unix-second times only; names come from the app. Use only on a server that lists admin_live_activities_v1. On iOS send the activity's push token and its environment; the server sends start, update and end events through the push relay's live_activity mode: at most one update every 30 seconds with the newest state, a change of phase and the end at once, and nothing after the end or 8 hours after registration. On Android send no token; the server sends an ordinary push whose delivery id is {device_id}.live-{activity_id}-{unix seconds}, and the app re-reads the job and redraws its ongoing notification. Registering the same activity_id again replaces its token. A device keeps at most five activities; a sixth replaces the oldest.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

Request body (required, `application/json`): [`BloemAdminLiveActivityRequest`](#bloemadminliveactivityrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 |  | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not valid JSON for this route. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for device registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: a field is missing or malformed, the platform does not match the device, or the activity_id belongs to another device. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery or the operator's Live Activities switch is off on this server, or it has no push backing. |

### `DELETE /api/bloem/v2/admin/push/devices/{device_id}/live-activities/{activity_id}`

Forget a Live Activity that ended.

Operation ID: `deleteBloemAdminLiveActivity`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. Deletes the activity and its token; the server sends it nothing more. Call it when the activity ends on the phone. An activity the server no longer has is not an error.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |
| `activity_id` | path | string (uuid) | yes | The activity_id the app registered. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for device registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: activity_id is not a UUID. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery or the operator's Live Activities switch is off on this server, or it has no push backing. |

### `PUT /api/bloem/v2/admin/push/devices/{device_id}/live-activity-start`

Store or clear the device's push-to-start token.

Operation ID: `setBloemAdminLiveActivityStart`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. The server builds each update from ids, counts, state words and Unix-second times only; names come from the app. Apple devices on iOS 17.2 or later. With a token and kinds, the server may start an activity on this device: for a scan that has run 60 seconds, and for a backup at once, and only while the device has the matching alert kind switched on (admin.scan_failed for scans, admin.backup_failed for backups). Each job is started once per device. Send token null and no kinds when the person turns the setting off; the server then starts nothing. Clearing works with the operator's switch off.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

Request body (required, `application/json`): [`BloemAdminLiveStartRequest`](#bloemadminlivestartrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not valid JSON for this route. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for device registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: the token is malformed, a kind is unknown, kinds were sent without a token, or the device is not an Apple device. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery or the operator's Live Activities switch is off on this server, or it has no push backing. |

### `POST /api/bloem/v2/admin/push/devices/{device_id}/retire`

Stop pushes to a device using its display token.

Operation ID: `retireBloemAdminPushDevice`.
Credentials: `bloemAdminPushDisplayToken`.

Authenticated by the device's display token, not by a login: the iOS notification extension reads text while the phone is locked. The token opens only this device's own notifications and retire call. Lets an app that has signed out, or whose session expired, remove its device.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_display_token: the token is missing, unknown, replaced by a newer registration, or not this device's. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `PUT /api/bloem/v2/admin/push/devices/{device_id}/widgets`

Report the widgets the app has placed.

Operation ID: `setBloemAdminPushWidgets`.
Credentials: `bloemAccountSession`.

Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. Only a device that reports widgets_placed gets widget wakes, and only for the channels its widgets show (sessions, requests, libraries, nodes). A wake is a silent push (the relay's background_wake) with no content: its delivery id is {device_id}.widgets-{channels}-{unix seconds}, for example dev.widgets-nodes-requests-1763254800, and the app re-reads what the widgets show through the normal API. The server coalesces changes for 30 seconds, sends a device at most one wake every 10 minutes, and none within 2 minutes after a visible notification push. Call this whenever a widget is added or removed; send widgets_placed false when the last is removed.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `device_id` | path | string | yes | The device_id a registration returned. |

Request body (required, `application/json`): [`BloemAdminPushWidgetsRequest`](#bloemadminpushwidgetsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAdminPushWidgets`](#bloemadminpushwidgets) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not valid JSON for this route. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: no valid access token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_required or account_session_required, as for registration. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no device with this id belongs to the account. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_registration: a channel is unknown, or widgets_placed is true with no channel. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | admin_push_unavailable: push delivery is off on this server, or it has no push backing. |

### `GET /api/bloem/v2/ambience`

Read seasonal presentation for the current verified tenant viewer.

Operation ID: `getBloemSeasonalViewer`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile. Account, profile, active organization and membership must agree; policy and security revisions must be positive. Explicit tenant claims are revalidated; legacy account sessions resolve to the default organization, not an organization selected by a header. While that default organization is still initializing, a legacy session's verified viewer receives deployment-wide packs only. The profile's organization is checked before delivering public and organization-owned packs. This is private viewer delivery, separate from public login branding. Handler responses use Cache-Control: private, no-store. Availability is advertised by seasonal_viewer_v1 in /api/bloem/v2/capabilities feature_tokens.

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

### `GET /api/bloem/v2/ambience/assets/{ref}`

Serve uploaded seasonal and campaign artwork.

Operation ID: `getBloemAmbienceAsset`.
Credentials: none (public).

Public: no credential, because artwork shows on the sign-in page. Uploads under /api/bloem/v2/admin/platform/ambience return paths here. The bytes are a PNG, WebP, JPEG or GIF image with its sniffed Content-Type; refs are content-addressed, so responses are immutable (Cache-Control public, max-age one year) and carry the ref as ETag.

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

### `GET /api/bloem/v2/ambience/public`

List the active deployment-wide seasonal packs.

Operation ID: `getBloemPublicAmbience`.
Credentials: none (public).

Public: no credential, so a signed-out page can show the season. Only deployment-wide packs; a profile's own seasonal presentation is GET /api/bloem/v2/ambience. Always an array, empty when none are active.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPublicAmbienceResponse`](#bloempublicambienceresponse) | OK |
| default | `application/problem+json` [`Problem`](#problem) | Error |

### `GET /api/bloem/v2/notifications`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/notifications/read-all`

Mark every inbox row up to a page's read_cutoff read, Bloem's own notices included.

Operation ID: `markBloemNotificationsRead`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemNotificationReadAll`](#bloemnotificationreadall).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing, or through is missing or not a read_cutoff. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be updated. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/notifications/sync`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/notifications/unread-count`

How many inbox rows are unread, Bloem's own notices included.

Operation ID: `getBloemNotificationUnreadCount`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNotificationCount`](#bloemnotificationcount) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the inbox could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/notifications/{id}`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no row with this ID belongs to the selected profile, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the row could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `POST /api/bloem/v2/notifications/{id}/read`

Mark one inbox row read. Repeating it is not an error.

Operation ID: `markBloemNotificationRead`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `id` | path | string | yes | Delivery identity. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no row with this ID belongs to the selected profile, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the row could not be updated. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/promotions`

List the campaign cards a profile sees on a surface.

Operation ID: `listBloemPromotions`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does. Availability is advertised by promotions_viewer_v1 in /api/bloem/v2/capabilities feature_tokens. Lists the promotional cards for a surface. For the home surface, home cards come with home_position, the row index the campaign row is shown at. Cards the profile dismissed on the surface are left out.

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

### `PUT /api/bloem/v2/promotions/{id}/dismissals/{surface}`

Stop showing a campaign to a profile on a surface.

Operation ID: `dismissBloemPromotion`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does. Availability is advertised by promotions_viewer_v1 in /api/bloem/v2/capabilities feature_tokens. Dismisses a promotional card for the profile on that surface. Idempotent: repeating it refreshes the dismissal.

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

### `POST /api/bloem/v2/promotions/{id}/save`

Save an in-playback campaign to the profile's inbox.

Operation ID: `saveBloemPromotion`.
Credentials: `bearerAuth`.

Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does. Availability is advertised by promotions_viewer_v1 in /api/bloem/v2/capabilities feature_tokens. Saves a promotional card to the profile's inbox. Only a current in_playback card with a call to action for content_id can be saved. The inbox delivery ID is derived from the profile and campaign, so a retry does not add a second copy.

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

### AdminPushKindState

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean | yes |  |
| `kind` | string | yes |  |
| `server_emitted` | boolean | yes |  |

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

### BloemAdminLiveActivityRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `activity_id` | string (uuid) | yes | A UUID the app makes for the activity. Registering the same id again replaces its push token, for example after the token rotated. |
| `environment` | string: `production`, `sandbox` |  | The APNs environment of the token. Required for ios; omit for android. |
| `kind` | string: `scan`, `backup` | yes |  |
| `platform` | string: `ios`, `android` | yes | ios for an ActivityKit Live Activity, android for an ongoing notification. Must match the device's platform. |
| `push_token` | string |  | The activity's APNs push token, hex (64 to 200 characters). Required for ios; omit for android. |
| `subject_id` | string |  | The library id of a scan. Omit for a backup. |

### BloemAdminLiveStartRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kinds` | array of string: `scan`, `backup` | yes | The jobs the person allowed the server to start an activity for. Must be empty when token is null. |
| `token` | string or null | yes | The device's ActivityKit push-to-start token, hex (64 to 200 characters), or null to clear it. Apple devices only. |

### BloemAdminPushContent

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `actions` | array of string: `approve`, `decline` | yes | Buttons the notification offers. Empty for most kinds. |
| `badge` | integer (int64) |  | Waiting items to show on the app icon. |
| `body` | string | yes |  |
| `channel` | string: `servers`, `requests`, `libraries`, `backups`, `nodes`, `plugins` | yes |  |
| `created_at` | string (date-time) | yes |  |
| `delivery_id` | string | yes |  |
| `item_id` | string |  | The item on that page: a request id, library id, node id or plugin installation id. |
| `kind` | string | yes |  |
| `lock_screen` | string | yes | All a locked screen shows unless the device allows details. |
| `page` | string |  | App link page a tap opens: requests, libraries, backups, nodes or plugins. |
| `subject_type` | string |  |  |
| `title` | string | yes |  |

### BloemAdminPushKinds

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kinds` | array of [`AdminPushKindState`](#adminpushkindstate) | yes |  |

### BloemAdminPushKindsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kinds` | object of boolean | yes | Kind id to enabled. Kinds not named keep their setting. |

### BloemAdminPushRegisterRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `apns_environment` | string: `production`, `sandbox` |  | Required for apple. |
| `apns_topic` | string |  | Required for apple; must be com.bloemstudios.admin. |
| `disabled_kinds` | array of string |  | Kind ids this device has switched off. Replaces the stored switches when present; omit to keep them. |
| `installation_id` | string | yes | A key the app generates once per installation and keeps across token refreshes and sign-ins. |
| `platform` | string: `apple`, `android` | yes |  |
| `token` | string | yes | The APNs device token (hex) or the FCM registration token. |
| `widget_channels` | array of string: `sessions`, `requests`, `libraries`, `nodes`, `health` |  | What the placed widgets show: sessions (now playing), requests, libraries, nodes, health (Needs a look: backup and plugin failures; only to a server that lists admin_widget_health_v1). Required, and not empty, when widgets_placed is true. |
| `widgets_placed` | boolean |  | Whether the app has at least one home-screen widget placed. Only then does the server send widget wakes. Omit to keep what the server holds. Reporting false clears the channels. |

### BloemAdminPushRegistered

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device_id` | string | yes | Prefixes every delivery id pushed to this device, so the app can tell which saved server a push came from: delivery_id is {device_id}.{delivery}. |
| `display_token` | string | yes | Bearer token for reading this device's notification text and retiring it. Returned by every registration; a new one replaces the last. |
| `kinds` | array of [`AdminPushKindState`](#adminpushkindstate) | yes |  |
| `widget_channels` | array of string | yes |  |
| `widgets_placed` | boolean | yes |  |

### BloemAdminPushWidgets

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channels` | array of string | yes |  |
| `widgets_placed` | boolean | yes |  |

### BloemAdminPushWidgetsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `channels` | array of string: `sessions`, `requests`, `libraries`, `nodes`, `health` | yes | What the placed widgets show; health only to a server that lists admin_widget_health_v1. Required, and not empty, when widgets_placed is true; ignored when false. |
| `widgets_placed` | boolean | yes | Whether at least one widget is placed. Send false when the last widget is removed. |

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

### BloemNotificationCount

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `count` | integer (int64) | yes | Unread rows in the whole inbox, Bloem's own notices included. |

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

### BloemNotificationReadAll

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `through` | string | yes | read_cutoff of the inbox page the viewer saw. Rows that arrived after it stay unread. |

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

### BloemPublicAmbienceResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ambience` | array of [`Wire`](#wire) | yes |  |

### BloemPushRelayRegistration

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `api_key_configured` | boolean | yes | Always true: a credential is stored. |
| `apns_topics` | array of string |  | The Apple topics the relay sends for, when it says. |
| `deployment_id` | string | yes | This server's deployment at Bloem's relay. |
| `expires_at` | string (date-time) | yes | When the capability expires; the server renews it before then. |
| `key_prefix` | string | yes | The stored capability's public prefix. |
| `relay_request_id` | string |  | The relay's request id, for its support. |
| `relay_url` | string | yes | Bloem's push relay origin. |

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

### Problem

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `detail` | string | yes | Safe occurrence-specific explanation; not for control flow |
| `errors` | array of [`ProblemError`](#problemerror) |  | Field-level validation details |
| `instance` | string (uri) | yes | urn:silo:request:<request-id>, matching the X-Request-ID response header |
| `status` | integer (int64) | yes | HTTP status code, equal to the response status |
| `title` | string | yes | Short summary fixed for the problem type |
| `type` | string (uri) | yes | Stable problem type URI; the final segment is the problem identifier |

### ProblemError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `code` | string | yes | Stable machine-readable code for the failure |
| `detail` | string | yes | Safe human-readable explanation; never the rejected value |
| `location` | string | yes | Where the error occurred: body.*, query.*, path.* or header.* |

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
