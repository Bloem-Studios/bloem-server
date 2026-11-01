# Apps and clients API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

What Bloem's own apps read beyond Silo's API: capabilities, server identity, the composed Watch documents, progress sync, people, item collections and profile sign-in credentials.

For the plain-language picture, read the [Apps and clients explainer](../../architecture/bloem-apps.md). The invariants are in:

- [Bloem native client surface](../../architecture/bloem-client-surface.md)
- [SDK compatibility and contract generation](../../architecture/bloem-sdk-compatibility.md)
- [Protocol compatibility](../../architecture/bloem-compatibility-removal.md)

## Operations (11)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v1/capabilities`](#get-apibloemv1capabilities) | What this build supports, for a client deciding which surfaces to use. |
| [`GET /api/bloem/v1/catalog/items/{content_id}/collections`](#get-apibloemv1catalogitemscontent_idcollections) | The visible server collections that contain one item. Visibility matches the server collection list; smart collections are evaluated lazily and are not included. An item the viewer may not see answers 404. |
| [`GET /api/bloem/v1/persons/{person_id}`](#get-apibloemv1personsperson_id) | One person and their credits, filtered to what this viewer may see. |
| [`GET /api/bloem/v1/profile-credentials/{id}`](#get-apibloemv1profile-credentialsid) | Read direct-profile credential status without exposing a password or hash. |
| [`PUT /api/bloem/v1/profile-credentials/{id}`](#put-apibloemv1profile-credentialsid) | Set or replace direct-profile login credentials. |
| [`DELETE /api/bloem/v1/profile-credentials/{id}`](#delete-apibloemv1profile-credentialsid) | Clear direct-profile login credentials. |
| [`GET /api/bloem/v1/server/identity`](#get-apibloemv1serveridentity) | Which server this is, which surfaces it serves, and whether it has been set up. |
| [`POST /api/bloem/v1/sync/progress`](#post-apibloemv1syncprogress) | Reconcile playback positions, including ones recorded offline. |
| [`GET /api/bloem/v1/watch/home`](#get-apibloemv1watchhome) | The composed home document for this viewer. |
| [`GET /api/bloem/v1/watch/items/{content_id}`](#get-apibloemv1watchitemscontent_id) | The composed document for one item. |
| [`GET /api/bloem/v1/watch/search`](#get-apibloemv1watchsearch) | A composed document for a search query. |

## Reference

### `GET /api/bloem/v1/capabilities`

What this build supports, for a client deciding which surfaces to use.

Operation ID: `getBloemCapabilities`.
Credentials: none (public).

Public and always answers 200; no authentication, rate limit or tenant gate runs in front of it.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemCapabilities`](#bloemcapabilities) | OK |

### `GET /api/bloem/v1/catalog/items/{content_id}/collections`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or item access or the collections could not be read. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/persons/{person_id}`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no person has this ID, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the person or filmography could not be loaded. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/profile-credentials/{id}`

Read direct-profile credential status without exposing a password or hash.

Operation ID: `getBloemProfileCredentials`.
Credentials: `bloemAccountSession`.

Requires a non-impersonated account login access token and household management authority: a server administrator, or the account's selected primary profile with PIN verification when locked. Ownership is always restricted to the caller's account. Direct-profile sessions, API keys and administrative contexts are refused.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Target profile in the authenticated account; other accounts' profiles return 404. |
| `X-Profile-Id` | header | string |  | The acting household primary profile. Required unless the account is a server administrator; it is separate from the target path id. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked acting primary profile; required for household management unless the account is a server administrator. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ProfileCredentialStatus`](#profilecredentialstatus) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: authentication is missing or invalid, or the login session is no longer valid. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | account_session_required: use a non-impersonated account login session; forbidden: select and verify the household primary profile, or this session type cannot use the route. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: profile does not belong to this account or does not exist. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: household management permissions could not be checked. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: profile credential management is unavailable; service_unavailable: the session could not be checked, retry after Retry-After. |

### `PUT /api/bloem/v1/profile-credentials/{id}`

Set or replace direct-profile login credentials.

Operation ID: `setBloemProfileCredentials`.
Credentials: `bloemAccountSession`.

Requires a non-impersonated account login access token and household management authority: a server administrator, or the account's selected primary profile with PIN verification when locked. Ownership is always restricted to the caller's account. Direct-profile sessions, API keys and administrative contexts are refused. The JSON body is required even on DELETE. The current account password is rechecked. expected_revision compares the target credential revision in the transaction; success increments it and revokes the target's direct-profile sessions. A stale revision is 409 credential_revision_conflict. Reload and review status after an uncertain result; do not automatically replay the mutation.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Target profile in the authenticated account; other accounts' profiles return 404. |
| `X-Profile-Id` | header | string |  | The acting household primary profile. Required unless the account is a server administrator; it is separate from the target path id. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked acting primary profile; required for household management unless the account is a server administrator. |

Request body (required, `application/json`): [`BloemProfileCredentialSetBody`](#bloemprofilecredentialsetbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: authentication is missing or invalid, or the login session is no longer valid. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | account_session_required, forbidden or reauthentication_failed: use an eligible account session, verify the primary profile when required, and supply the current account password. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: profile does not belong to this account or does not exist. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | credential_revision_conflict: reload the reviewed status; credential_email_in_use: the login email is already registered (PUT only). |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive, login_email is not a bare email address, or password is shorter than 8 characters or longer than 72 bytes. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: household management permissions could not be checked. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: profile credential management, the change or the account password check is unavailable; service_unavailable: the session could not be checked or password hashing is busy (PUT), retry after Retry-After. |

### `DELETE /api/bloem/v1/profile-credentials/{id}`

Clear direct-profile login credentials.

Operation ID: `clearBloemProfileCredentials`.
Credentials: `bloemAccountSession`.

Requires a non-impersonated account login access token and household management authority: a server administrator, or the account's selected primary profile with PIN verification when locked. Ownership is always restricted to the caller's account. Direct-profile sessions, API keys and administrative contexts are refused. The JSON body is required even on DELETE. The current account password is rechecked. expected_revision compares the target credential revision in the transaction; success increments it and revokes the target's direct-profile sessions. A stale revision is 409 credential_revision_conflict. Reload and review status after an uncertain result; do not automatically replay the mutation.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | Target profile in the authenticated account; other accounts' profiles return 404. |
| `X-Profile-Id` | header | string |  | The acting household primary profile. Required unless the account is a server administrator; it is separate from the target path id. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked acting primary profile; required for household management unless the account is a server administrator. |

Request body (required, `application/json`): [`BloemProfileCredentialClearBody`](#bloemprofilecredentialclearbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: authentication is missing or invalid, or the login session is no longer valid. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | account_session_required, forbidden or reauthentication_failed: use an eligible account session, verify the primary profile when required, and supply the current account password. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: profile does not belong to this account or does not exist. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | credential_revision_conflict: reload the reviewed status; credential_email_in_use: the login email is already registered (PUT only). |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: household management permissions could not be checked. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: profile credential management, the change or the account password check is unavailable; service_unavailable: the session could not be checked or password hashing is busy (PUT), retry after Retry-After. |

### `GET /api/bloem/v1/server/identity`

Which server this is, which surfaces it serves, and whether it has been set up.

Operation ID: `getBloemServerIdentity`.
Credentials: none (public).

Public. Mounted even without a database, so a 503 means this server has the endpoint and cannot answer yet; retry rather than falling back to another identity. The 200 response is served with Cache-Control: no-store.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemServerIdentity`](#bloemserveridentity) | OK |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: the installation identity or the setup state could not be read. The body never carries a provisional identifier. |

### `POST /api/bloem/v1/sync/progress`

Reconcile playback positions, including ones recorded offline.

Operation ID: `syncBloemProgress`.
Credentials: `bearerAuth`.

Accepts a JSON body of at most 1 MiB with 1 to 100 items. Item visibility is checked against the selected profile's libraries and rating limits before anything is written. A bad item does not fail the batch: it is reported per item, and the request still answers 200. Replaying the same batch converges on the same state, so an offline queue may retry it after an uncertain response.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `X-Profile-Id` | header | string | yes | The household profile acting for this request; it must belong to the authenticated account. |
| `X-Profile-Token` | header | string |  | Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification. |

Request body (required, `application/json`): [`BloemSyncProgressInputBody`](#bloemsyncprogressinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemSyncProgressOutputBody`](#bloemsyncprogressoutputbody) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: X-Profile-Id is missing, the body is not valid JSON, or it has no items or more than 100. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 413 | [`BloemNativeError`](README.md#bloemnativeerror) | payload_too_large: the body exceeds 1 MiB. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the profile's progress store could not be opened. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. unavailable: item visibility could not be resolved; nothing was written. |

### `GET /api/bloem/v1/watch/home`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the document could not be composed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/watch/items/{content_id}`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account. |
| 429 | [`BloemNativeRateLimitError`](#bloemnativeratelimiterror) | The configured rate limit was exceeded. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the viewer's access could not be resolved, or the document could not be composed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable. |

### `GET /api/bloem/v1/watch/search`

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
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid. |
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
| `direct_profile_login` | boolean | yes | A profile can hold its own credentials rather than logging in through its account. |
| `legacy_silo_v1` | boolean | yes | This build still answers Silo's frozen /api/v1 surface. |
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

### BloemNativeValidationError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `validation_failed` | yes |  |
| `fields` | object of string | yes | Validation messages keyed by request field. |
| `message` | string | yes |  |

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

### BloemProfileCredentialClearBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `current_password` | string | yes | Current account password, verified again before clearing credentials. |
| `expected_revision` | integer (int64) | yes | Positive credential revision from the reviewed status response's credential_revision; this is not an organization policy revision. |
| `login_email` | string |  | Accepted but ignored by DELETE. |
| `password` | string |  | Accepted but ignored by DELETE. |

### BloemProfileCredentialSetBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `current_password` | string | yes | Current account password, verified again before the change. |
| `expected_revision` | integer (int64) | yes | Positive credential revision from the reviewed status response's credential_revision; this is not an organization policy revision. |
| `login_email` | string (email) | yes | Direct-profile login email; surrounding whitespace is trimmed. |
| `password` | string | yes | New direct-profile password: at least 8 characters and at most 72 UTF-8 bytes. |

### BloemServerIdentity

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `api_versions` | array of integer (int64) | yes | Silo-compatible API majors this build serves. |
| `bloem_api` | array of string | yes | Native surface versions mounted under /api/bloem/. |
| `server_id` | string | yes | Stable identity of this installation. A client keys stored scope by it, so it survives renames and address changes. |
| `server_name` | string | yes | Display name, which may change at any time. Never use it as an identity. |
| `setup_complete` | boolean | yes | False while the server has no owner account, which is the only state where the setup routes answer. |
| `status` | string: `ok` | yes | Always ok. A server that cannot resolve its identity answers 503 instead. |

### BloemSyncProgressInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`BloemSyncProgressItem`](#bloemsyncprogressitem) | yes | Positions to reconcile, at most 100. |

### BloemSyncProgressItem

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `duration` | number (double) | yes | Total runtime in seconds, as the client observed it. |
| `force_overwrite` | boolean |  | Replace the stored position unconditionally. Ignored when updated_at is present. Defaults to false. |
| `media_item_id` | string | yes | Item this position belongs to. An empty value is rejected for this item only. |
| `position` | number (double) | yes | Playback position in seconds. |
| `updated_at` | string |  | RFC 3339 time the client recorded this position; the newer of it and the stored event wins. Future times are clamped. Omit for a live update. An unparsable value is rejected for this item only. |

### BloemSyncProgressOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `results` | array of [`BloemSyncProgressResult`](#bloemsyncprogressresult) | yes | One result per submitted item, in request order. |

### BloemSyncProgressResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string |  | Why this item was rejected: media_item_id is required, updated_at must be RFC3339, catalog item not found (absent or hidden from this profile) or failed to update progress. |
| `media_item_id` | string | yes | Item this result belongs to. |
| `status` | string: `updated`, `ignored`, `error` | yes | updated: the position was written. ignored: accepted but not written, because it fell below the minimum resume threshold or a newer stored event won; do not retry it. error: rejected; see error. |

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

### ProfileCredentialStatus

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configured` | boolean | yes |  |
| `credential_revision` | integer (int64) | yes |  |
| `login_email` | string | yes |  |
| `profile_id` | string | yes |  |
