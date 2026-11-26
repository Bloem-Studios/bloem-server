# Apps and clients API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

What Bloem's own apps read beyond Silo's API: capabilities, server identity, the composed Watch documents, progress sync, people, item collections, profile sign-in credentials and the running version's source code.

For the plain-language picture, read the [Apps and clients explainer](../../architecture/bloem-apps.md). The invariants are in:

- [Bloem native client surface](../../architecture/bloem-client-surface.md)
- [SDK compatibility and contract generation](../../architecture/bloem-sdk-compatibility.md)
- [Protocol compatibility](../../architecture/bloem-protocol-compatibility.md)
- [How the source code offer works](../../architecture/bloem-source-code-offer.md)

## Operations (9)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/capabilities`](#get-apibloemv2capabilities) | What this build supports, for a client deciding which surfaces to use. |
| [`GET /api/bloem/v2/catalog/items/{content_id}/collections`](#get-apibloemv2catalogitemscontent_idcollections) | The visible server collections that contain one item. Visibility matches the server collection list; smart collections are evaluated lazily and are not included. An item the viewer may not see answers 404. |
| [`GET /api/bloem/v2/persons/{person_id}`](#get-apibloemv2personsperson_id) | One person and their credits, filtered to what this viewer may see. |
| [`GET /api/bloem/v2/server/identity`](#get-apibloemv2serveridentity) | Which server this is, which surfaces it serves, and whether it has been set up. |
| [`GET /api/bloem/v2/source-code`](#get-apibloemv2source-code) | Describe the complete source code of the running server version. |
| [`GET /api/bloem/v2/source-code/archive`](#get-apibloemv2source-codearchive) | Download the complete source code of the running server version. |
| [`GET /api/bloem/v2/watch/home`](#get-apibloemv2watchhome) | The composed home document for this viewer. |
| [`GET /api/bloem/v2/watch/items/{content_id}`](#get-apibloemv2watchitemscontent_id) | The composed document for one item. |
| [`GET /api/bloem/v2/watch/search`](#get-apibloemv2watchsearch) | A composed document for a search query. |

## Reference

### `GET /api/bloem/v2/capabilities`

What this build supports, for a client deciding which surfaces to use.

Operation ID: `getBloemCapabilities`.
Credentials: none (public).

Public and always answers 200; no authentication, rate limit or tenant gate runs in front of it.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemCapabilities`](#bloemcapabilities) | OK |

### `GET /api/bloem/v2/catalog/items/{content_id}/collections`

The visible server collections that contain one item. Visibility matches the server collection list; smart collections are evaluated lazily and are not included. An item the viewer may not see answers 404.

Operation ID: `listBloemItemCollections`.
Credentials: `bearerAuth`.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `content_id` | path | string | yes | Content id of the item. A blank value answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemItemCollections`](#bloemitemcollections) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or content_id is blank. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or item access or the collections could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/persons/{person_id}`

One person and their credits, filtered to what this viewer may see.

Operation ID: `getBloemPersonDetail`.
Credentials: `bearerAuth`.

The person's own details are not profile-scoped; the filmography is filtered by the selected profile's libraries and rating limits.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `person_id` | path | string | yes | Person identity: a positive integer. Anything else answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPersonDetail`](#bloempersondetail) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or person_id is not a positive integer. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no person has this ID, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the person or filmography could not be loaded. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/server/identity`

Which server this is, which surfaces it serves, and whether it has been set up.

Operation ID: `getBloemServerIdentity`.
Credentials: none (public).

Public. Mounted even without a database, so a 503 means this server has the endpoint and cannot answer yet; retry rather than falling back to another identity. The 200 response is served with Cache-Control: no-store.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemServerIdentity`](#bloemserveridentity) | OK |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the installation identity or the setup state could not be read. The body never carries a provisional identifier. |

### `GET /api/bloem/v2/source-code`

Describe the complete source code of the running server version.

Operation ID: `getBloemSourceCode`.
Credentials: `bearerAuth`.

Requires any signed-in account; no profile, organization or admin role. Bloem Server is offered under the GNU Affero General Public License v3, and this route names the archive of the exact build that answers it. Availability is advertised by source_code_v1 in /api/bloem/v2/capabilities feature_tokens.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemSourceCodeResponse`](#bloemsourcecoderesponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | source_unavailable: the archive of the running version is missing. The routes are absent, and source_code_v1 is not advertised, when the server has no archive directory or no clean build revision. |

### `GET /api/bloem/v2/source-code/archive`

Download the complete source code of the running server version.

Operation ID: `getBloemSourceCodeArchive`.
Credentials: `bearerAuth`.

Requires any signed-in account. A browser download that cannot send an Authorization header may pass the access token as the token query parameter, as media URLs do. The body is a gzip-compressed tar archive of the running build's commit; range requests are supported so a broken download can resume.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `application/gzip` string (binary) | The gzip-compressed tar archive of the running build's commit. |
| 206 | `application/gzip` string (binary) | The requested byte range of the archive. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | source_unavailable: the archive of the running version is missing. The routes are absent, and source_code_v1 is not advertised, when the server has no archive directory or no clean build revision. |

### `GET /api/bloem/v2/watch/home`

The composed home document for this viewer.

Operation ID: `getBloemWatchHome`.
Credentials: `bearerAuth`.

Composes up to 100 of the most recently added movies and series the selected profile may see, with that profile's progress. Served with Cache-Control: private, no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Document`](#document) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the document could not be composed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/watch/items/{content_id}`

The composed document for one item.

Operation ID: `getBloemWatchItem`.
Credentials: `bearerAuth`.

An item the selected profile may not see answers 404 exactly like an item that does not exist. Served with Cache-Control: private, no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `content_id` | path | string | yes | Catalog content identity. A blank value answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Document`](#document) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or content_id is blank. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the document could not be composed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v2/watch/search`

A composed document for a search query.

Operation ID: `searchBloemWatch`.
Credentials: `bearerAuth`.

Searches the movies and series the selected profile may see and composes at most 50 results. A deployment without a search provider answers an empty document rather than an error. Served with Cache-Control: private, no-store.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |
| `q` | query | string | yes | Search terms; surrounding whitespace is ignored and a blank value answers 400. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Document`](#document) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing or q is blank. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, the search failed, or the document could not be composed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

## Schemas

### BloemCapabilities

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `api` | string | yes | Identifier of the native surface this build serves. |
| `feature_tokens` | array of string | yes | Versioned capability tokens a client matches against. Some depend on what this deployment has configured; ignore tokens you do not recognize. |
| `features` | [`BloemCapabilityFeatures`](#bloemcapabilityfeatures) | yes |  |
| `identity_schema` | integer (int64) | yes | Version of the server/organization/account/profile identity model. |
| `media_types` | array of string | yes | The item types this build can serve. |

### BloemCapabilityFeatures

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `delegated_admin_roles` | boolean | yes | Administration can be delegated below the server owner. |
| `direct_profile_login` | boolean | yes | Always false: profiles do not sign in on their own. Kept so clients that read it keep working. |
| `legacy_silo_v1` | boolean | yes | Always false. Kept so clients that read it keep working. |
| `organization_memberships` | boolean | yes | Accounts can belong to organizations. |
| `shared_device_pairing` | boolean | yes | A device can be paired to a household rather than to one account. |
| `tenant_bounded_media_scope` | boolean | yes | Media visibility is bounded by the viewer's organization. |

### BloemItemCollection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `backdrop_thumbhash` | string |  |  |
| `backdrop_url` | string |  | Presigned, short-lived. |
| `collection_type` | string | yes | manual, mdblist, tmdb or trakt; smart collections are never listed. |
| `featured` | boolean |  |  |
| `group_id` | string |  | The collection group within that library, when it is grouped. |
| `group_name` | string |  |  |
| `id` | string | yes |  |
| `item_count` | integer (int64) | yes | All members of the collection, as the server collection list counts them. |
| `library_id` | string | yes | The library the viewer reaches this collection through: the item's own library when the collection is scoped to it, else the first accessible one in library order. |
| `library_name` | string | yes |  |
| `poster_thumbhash` | string |  |  |
| `poster_url` | string | yes | Presigned, short-lived; empty when none. |
| `title` | string | yes |  |

### BloemItemCollections

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `collections` | array of [`BloemItemCollection`](#bloemitemcollection) | yes | At most 100, ordered by library order, then featured first, then collection order and title. Empty, never null. |

### BloemNativeRateLimitError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `rate_limit_exceeded` | yes |  |
| `message` | string | yes |  |
| `retry_after` | integer (int64) | yes | Seconds to wait before retrying. |

### BloemPersonDetail

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `bio` | string |  |  |
| `birth_date` | string |  | Calendar date (YYYY-MM-DD), when known. |
| `birthplace` | string |  |  |
| `death_date` | string |  | Calendar date (YYYY-MM-DD), when known. |
| `filmography` | array of [`BloemPersonFilmographyEntry`](#bloempersonfilmographyentry) | yes | At most 100 movies and series this viewer may see, newest year first. Empty, never null. |
| `id` | string | yes |  |
| `name` | string | yes |  |
| `photo_url` | string |  |  |

### BloemPersonFilmographyEntry

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `content_id` | string | yes | Item this credit points at. |
| `kind` | string | yes | The item's kind, not the credit's. |
| `poster_url` | string |  |  |
| `role` | string |  | How this person was credited: a character name or a job title. |
| `title` | string | yes |  |
| `year` | integer (int64) |  |  |

### BloemServerIdentity

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `api_versions` | array of integer (int64) | yes | Silo-compatible API majors this build serves. |
| `bloem_api` | array of string | yes | Native surface versions mounted under /api/bloem/. |
| `server_id` | string | yes | Stable identity of this installation. A client keys stored scope by it, so it survives renames and address changes. |
| `server_name` | string | yes | Display name, which may change at any time. Never use it as an identity. |
| `setup_complete` | boolean | yes | False while the server has no owner account, which is the only state where the setup routes answer. |
| `status` | string: `ok` | yes | Always ok. A server that cannot resolve its identity answers 503 instead. |

### BloemSourceCodeResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `archive_name` | string | yes | File name of the archive, bloem-server-<revision>.tar.gz. |
| `archive_path` | string | yes | Path that downloads the archive: /api/bloem/v2/source-code/archive. |
| `license` | string | yes | SPDX identifier of the license the source is offered under: AGPL-3.0. |
| `revision` | string | yes | Full commit revision of the running server build; the archive holds exactly this commit. |
| `size_bytes` | integer (int64) | yes | Size of the archive in bytes. |

### Document

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `featured_content_id` | string |  |  |
| `items` | array of [`DocumentItem`](#documentitem) | yes |  |
| `progress` | array of [`DocumentProgress`](#documentprogress) | yes |  |
| `schema` | string | yes |  |
| `snapshot` | string | yes |  |

### DocumentCastMember

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `character` | string |  |  |
| `name` | string | yes |  |
| `person_id` | string |  |  |
| `photo_url` | string |  |  |

### DocumentChapter

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `end_seconds` | number (double) | yes |  |
| `index` | integer (int64) | yes |  |
| `start_seconds` | number (double) | yes |  |
| `title` | string |  |  |

### DocumentCrewMember

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `job` | string |  |  |
| `name` | string | yes |  |
| `person_id` | string |  |  |
| `photo_url` | string |  |  |

### DocumentEdition

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `codec_audio` | string |  |  |
| `codec_video` | string |  |  |
| `container` | string |  |  |
| `duration_seconds` | number (double) |  |  |
| `edition_key` | string |  |  |
| `edition_label` | string |  |  |
| `file_id` | integer (int64) | yes |  |
| `hdr` | boolean |  |  |
| `resolution` | string |  |  |

### DocumentItem

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cast` | array of [`DocumentCastMember`](#documentcastmember) |  |  |
| `chapters` | array of [`DocumentChapter`](#documentchapter) |  |  |
| `content_id` | string | yes |  |
| `credits_end_seconds` | number (double) |  |  |
| `credits_start_seconds` | number (double) |  |  |
| `crew` | array of [`DocumentCrewMember`](#documentcrewmember) |  |  |
| `editions` | array of [`DocumentEdition`](#documentedition) |  |  |
| `episode_number` | integer (int64) |  |  |
| `featured` | boolean |  |  |
| `file_id` | integer (int64) |  |  |
| `genres` | array of string |  |  |
| `intro_end_seconds` | number (double) |  |  |
| `intro_start_seconds` | number (double) |  |  |
| `keywords` | array of string |  |  |
| `kind` | string | yes |  |
| `overview` | string |  |  |
| `poster_url` | string |  |  |
| `preview_end_seconds` | number (double) |  |  |
| `preview_start_seconds` | number (double) |  |  |
| `rating` | string |  |  |
| `recap_end_seconds` | number (double) |  |  |
| `recap_start_seconds` | number (double) |  |  |
| `runtime_seconds` | integer (int64) |  |  |
| `season_count` | integer (int64) |  |  |
| `season_number` | integer (int64) |  |  |
| `seasons` | array of [`DocumentSeason`](#documentseason) |  |  |
| `series_id` | string |  |  |
| `title` | string | yes |  |
| `year` | integer (int64) |  |  |

### DocumentProgress

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `completed` | boolean | yes |  |
| `content_id` | string | yes |  |
| `duration_seconds` | number (double) | yes |  |
| `episode_id` | string |  |  |
| `position_seconds` | number (double) | yes |  |
| `updated_at` | string | yes |  |

### DocumentSeason

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `season_number` | integer (int64) | yes |  |
| `title` | string |  |  |
