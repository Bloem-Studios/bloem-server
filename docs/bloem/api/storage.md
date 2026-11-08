# Storage sources API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Storage plugins, sources, credential rotation, bucket and folder browsing, and library locations, in the platform scope and in an organization's own scope.

For the plain-language picture, read the [Storage sources explainer](../../architecture/bloem-storage-sources.md). The invariants are in:

- [Native storage](../../architecture/bloem-native-storage.md)
- [Native video playback](../../architecture/bloem-native-video-playback.md)
- [Native audiobook presentations](../../architecture/bloem-native-audio-playback.md)

## Operations (42)

| Method and path | Summary |
| --- | --- |
| [`POST /api/bloem/v1/admin/organization/library-locations`](#post-apibloemv1adminorganizationlibrary-locations) | Read or atomically save filesystem paths and selected plugin directories. |
| [`GET /api/bloem/v1/admin/organization/library-locations/{library_id}`](#get-apibloemv1adminorganizationlibrary-locationslibrary_id) | Read or atomically save filesystem paths and selected plugin directories. |
| [`PUT /api/bloem/v1/admin/organization/library-locations/{library_id}`](#put-apibloemv1adminorganizationlibrary-locationslibrary_id) | Read or atomically save filesystem paths and selected plugin directories. |
| [`GET /api/bloem/v1/admin/organization/native-storage/artifacts`](#get-apibloemv1adminorganizationnative-storageartifacts) | List automatically verified catalog releases and any optional manual artifacts. |
| [`GET /api/bloem/v1/admin/organization/native-storage/capabilities`](#get-apibloemv1adminorganizationnative-storagecapabilities) | Read composition-dependent flags; backend verification remains false. |
| [`POST /api/bloem/v1/admin/organization/native-storage/catalog/installations`](#post-apibloemv1adminorganizationnative-storagecataloginstallations) | Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload. |
| [`POST /api/bloem/v1/admin/organization/native-storage/catalog/installations/discover`](#post-apibloemv1adminorganizationnative-storagecataloginstallationsdiscover) | Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download. |
| [`POST /api/bloem/v1/admin/organization/native-storage/catalog/installations/{installation_id}/upgrade`](#post-apibloemv1adminorganizationnative-storagecataloginstallationsinstallation_idupgrade) | Download and verify a catalog upgrade, retaining configuration and library. |
| [`POST /api/bloem/v1/admin/organization/native-storage/installations`](#post-apibloemv1adminorganizationnative-storageinstallations) | Install an approved artifact and source without creating a library or scan. |
| [`POST /api/bloem/v1/admin/organization/native-storage/installations/discover`](#post-apibloemv1adminorganizationnative-storageinstallationsdiscover) | Discover the root of a new approved S3 installation and publish it atomically without a library or scan. |
| [`DELETE /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}`](#delete-apibloemv1adminorganizationnative-storageinstallationsinstallation_id) | Uninstall and detach a source no library reads from; its catalog and reading progress are retained. |
| [`POST /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}/disable`](#post-apibloemv1adminorganizationnative-storageinstallationsinstallation_iddisable) | Disable and detach a source no library reads from; its catalog and reading progress are retained. |
| [`POST /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}/upgrade`](#post-apibloemv1adminorganizationnative-storageinstallationsinstallation_idupgrade) | Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept. |
| [`POST /api/bloem/v1/admin/organization/native-storage/s3/buckets`](#post-apibloemv1adminorganizationnative-storages3buckets) | List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses. |
| [`POST /api/bloem/v1/admin/organization/native-storage/s3/folders`](#post-apibloemv1adminorganizationnative-storages3folders) | List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing. |
| [`GET /api/bloem/v1/admin/organization/native-storage/sources`](#get-apibloemv1adminorganizationnative-storagesources) | List visible sources and their current configuration revisions. |
| [`GET /api/bloem/v1/admin/organization/native-storage/sources/{source_key}`](#get-apibloemv1adminorganizationnative-storagesourcessource_key) | Read a visible source without configuration secrets. |
| [`DELETE /api/bloem/v1/admin/organization/native-storage/sources/{source_key}`](#delete-apibloemv1adminorganizationnative-storagesourcessource_key) | Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched. |
| [`GET /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/browse`](#get-apibloemv1adminorganizationnative-storagesourcessource_keybrowse) | Browse authorized directories without starting discovery or opening files. |
| [`PUT /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/configuration`](#put-apibloemv1adminorganizationnative-storagesourcessource_keyconfiguration) | Replace complete configuration only while the source namespace is empty. |
| [`POST /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/credentials`](#post-apibloemv1adminorganizationnative-storagesourcessource_keycredentials) | Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned. |
| [`POST /api/bloem/v1/admin/platform/library-locations`](#post-apibloemv1adminplatformlibrary-locations) | Read or atomically save filesystem paths and selected plugin directories. |
| [`GET /api/bloem/v1/admin/platform/library-locations/{library_id}`](#get-apibloemv1adminplatformlibrary-locationslibrary_id) | Read or atomically save filesystem paths and selected plugin directories. |
| [`PUT /api/bloem/v1/admin/platform/library-locations/{library_id}`](#put-apibloemv1adminplatformlibrary-locationslibrary_id) | Read or atomically save filesystem paths and selected plugin directories. |
| [`GET /api/bloem/v1/admin/platform/native-storage/artifacts`](#get-apibloemv1adminplatformnative-storageartifacts) | List automatically verified catalog releases and any optional manual artifacts. |
| [`GET /api/bloem/v1/admin/platform/native-storage/capabilities`](#get-apibloemv1adminplatformnative-storagecapabilities) | Read composition-dependent flags; backend verification remains false. |
| [`POST /api/bloem/v1/admin/platform/native-storage/catalog/installations`](#post-apibloemv1adminplatformnative-storagecataloginstallations) | Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload. |
| [`POST /api/bloem/v1/admin/platform/native-storage/catalog/installations/discover`](#post-apibloemv1adminplatformnative-storagecataloginstallationsdiscover) | Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download. |
| [`POST /api/bloem/v1/admin/platform/native-storage/catalog/installations/{installation_id}/upgrade`](#post-apibloemv1adminplatformnative-storagecataloginstallationsinstallation_idupgrade) | Download and verify a catalog upgrade, retaining configuration and library. |
| [`POST /api/bloem/v1/admin/platform/native-storage/installations`](#post-apibloemv1adminplatformnative-storageinstallations) | Install an approved artifact and source without creating a library or scan. |
| [`POST /api/bloem/v1/admin/platform/native-storage/installations/discover`](#post-apibloemv1adminplatformnative-storageinstallationsdiscover) | Discover the root of a new approved S3 installation and publish it atomically without a library or scan. |
| [`DELETE /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}`](#delete-apibloemv1adminplatformnative-storageinstallationsinstallation_id) | Uninstall and detach a source no library reads from; its catalog and reading progress are retained. |
| [`POST /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}/disable`](#post-apibloemv1adminplatformnative-storageinstallationsinstallation_iddisable) | Disable and detach a source no library reads from; its catalog and reading progress are retained. |
| [`POST /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}/upgrade`](#post-apibloemv1adminplatformnative-storageinstallationsinstallation_idupgrade) | Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept. |
| [`POST /api/bloem/v1/admin/platform/native-storage/s3/buckets`](#post-apibloemv1adminplatformnative-storages3buckets) | List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses. |
| [`POST /api/bloem/v1/admin/platform/native-storage/s3/folders`](#post-apibloemv1adminplatformnative-storages3folders) | List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing. |
| [`GET /api/bloem/v1/admin/platform/native-storage/sources`](#get-apibloemv1adminplatformnative-storagesources) | List visible sources and their current configuration revisions. |
| [`GET /api/bloem/v1/admin/platform/native-storage/sources/{source_key}`](#get-apibloemv1adminplatformnative-storagesourcessource_key) | Read a visible source without configuration secrets. |
| [`DELETE /api/bloem/v1/admin/platform/native-storage/sources/{source_key}`](#delete-apibloemv1adminplatformnative-storagesourcessource_key) | Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched. |
| [`GET /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/browse`](#get-apibloemv1adminplatformnative-storagesourcessource_keybrowse) | Browse authorized directories without starting discovery or opening files. |
| [`PUT /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/configuration`](#put-apibloemv1adminplatformnative-storagesourcessource_keyconfiguration) | Replace complete configuration only while the source namespace is empty. |
| [`POST /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/credentials`](#post-apibloemv1adminplatformnative-storagesourcessource_keycredentials) | Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned. |

## Reference

### `POST /api/bloem/v1/admin/organization/library-locations`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `createBloemOrganizationLibraryLocations`.
Credentials: `bloemOrganizationContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

Request body (required, `application/json`): [`BloemOrganizationLibraryLocationsCreate`](#bloemorganizationlibrarylocationscreate).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemLibraryLocationsCreated`](#bloemlibrarylocationscreated) | Created |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: malformed JSON, unknown, duplicate or missing fields, no path or selection, organization_id in organization scope, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: ordinary library validation failed, including legacy storage_source, blank paths, metadata_language and artwork-dependent options. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: a selected source, or the owner named by organization_id, is absent or hidden; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_selection_reference: a selection_ref is invalid, expired or no longer matches its source; browse again. revision_conflict with current_source_revision: a selection's configuration_revision is stale. conflict: another library already uses a requested filesystem path. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing. |
| 500 | [`BloemNativeStorageError`](#bloemnativestorageerror) | internal_error: the library could not be created. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. A plugin selection for a library type the host does not admit is also native_storage_unavailable. mutation_outcome_unknown returns operation_id, operation and library_id: reconcile using the library_id, never assume rollback or retry. |

### `GET /api/bloem/v1/admin/organization/library-locations/{library_id}`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `getBloemOrganizationLibraryLocations`.
Credentials: `bloemOrganizationContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `library_id` | path | integer | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`LibrarySelection`](#libraryselection) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an absent or hidden library or source, or an invalid library_id; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict: a concurrent filesystem path write holds the library's paths; returns current_library_revision. Retry the read. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/organization/library-locations/{library_id}`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `replaceBloemOrganizationLibraryLocations`.
Credentials: `bloemOrganizationContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `library_id` | path | integer | yes |  |

Request body (required, `application/json`): [`ReplaceLocationsRequest`](#replacelocationsrequest).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`LibrarySelection`](#libraryselection) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: malformed JSON, unknown, duplicate or missing fields, absent or null paths or selections, both empty, a nonpositive expected_revision, an empty expected_edit_token, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: a blank filesystem path. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an absent or hidden library or source, or an invalid library_id; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict with current_library_revision: expected_revision or expected_edit_token is stale, or a concurrent path write holds the library's paths; reload. invalid_selection_reference: a selection_ref is invalid, expired or no longer matches its source; browse again. revision_conflict with current_source_revision: a selection's configuration_revision is stale. conflict: another library already uses a requested filesystem path. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. A plugin selection for a library type the host does not admit is also native_storage_unavailable. mutation_outcome_unknown returns operation_id, operation and library_id: reconcile using the library_id, never assume rollback or retry. |

### `GET /api/bloem/v1/admin/organization/native-storage/artifacts`

List automatically verified catalog releases and any optional manual artifacts.

Operation ID: `listArtifactsBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageArtifacts`](#bloemnativestorageartifacts) | List automatically verified catalog releases and any optional manual artifacts. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: the catalog could not be refreshed and no artifact is approved. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/organization/native-storage/capabilities`

Read composition-dependent flags; backend verification remains false.

Operation ID: `getCapabilitiesBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageCapabilities`](#bloemnativestoragecapabilities) | Read composition-dependent flags; backend verification remains false. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/catalog/installations`

Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload.

Operation ID: `catalogInstallBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. S3 sources are added only through catalog discovery: an S3 catalog key is artifact_rejected for a platform administrator and not_found for an organization administrator. A key with no catalog release is artifact_rejected; a failed download or checksum is native_storage_unavailable.

Request body (required, `application/json`): [`BloemNativeStorageInstallBody`](#bloemnativestorageinstallbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. organization_id is rejected in organization scope, and source_key and expected_revision go together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the retained source_key is absent or hidden, or the actor's storage owner is no longer visible. An S3 catalog key is also not_found. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the release is not an approved artifact, or the configuration does not satisfy its declared schema. artifact_rejected also covers a key with no catalog release. s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/catalog/installations/discover`

Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download.

Operation ID: `catalogDiscoverInstallBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageDiscoverInstallBody`](#bloemnativestoragediscoverinstallbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Any field besides artifact_key, provider_source_id, enabled and config, including organization_id, is rejected, as is a config with state_path or with namespaces other than connection. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the actor's storage owner is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | source_attached: the generated source identity already exists. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the key is not an admitted S3 catalog release, or the configuration does not satisfy its declared schema. s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. This includes the per-administrator rate limit (10 per minute, burst 3; Retry-After is set), discovery not being composed, and the provider failing to start or describe the source within 30 seconds. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/catalog/installations/{installation_id}/upgrade`

Download and verify a catalog upgrade, retaining configuration and library.

Operation ID: `catalogUpgradeBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. An organization administrator receives not_found for the S3 plugin, also for an S3 source the organization owns; a platform administrator, or a manual upload of an approved artifact, upgrades it. A key with no catalog release is artifact_rejected; a failed download or checksum is native_storage_unavailable.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageUpgradeBody`](#bloemnativestorageupgradebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download and verify a catalog upgrade, retaining configuration and library. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. An organization administrator also receives it for the S3 plugin. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the release is not an approved artifact or is for a different plugin. artifact_rejected also covers a key with no catalog release. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/installations`

Install an approved artifact and source without creating a library or scan.

Operation ID: `installBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Install an approved artifact and source without creating a library or scan. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. organization_id is rejected in organization scope, and source_key and expected_revision go together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the retained source_key is absent or hidden, or the actor's storage owner is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB, the whole body 258 MiB, or the serialized configuration 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or the configuration does not satisfy its declared schema. s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/installations/discover`

Discover the root of a new approved S3 installation and publish it atomically without a library or scan.

Operation ID: `discoverInstallBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. Only a host-approved bloem.storage.s3 artifact qualifies. The provider runs once in a temporary process to report the selected source's root; the caller never supplies a root, source key or state_path.

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Discover the root of a new approved S3 installation and publish it atomically without a library or scan. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. organization_id is rejected in organization scope, and config must hold only the connection namespace, without state_path. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the actor's storage owner is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | source_attached: the generated source identity already exists. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB, the whole body 258 MiB, or the serialized configuration 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not a host-approved bloem.storage.s3 artifact, the uploaded bytes do not match it, or the configuration does not satisfy its declared schema. s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. This includes discovery not being composed and the provider failing to start or describe the source within 30 seconds. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}`

Uninstall and detach a source no library reads from; its catalog and reading progress are retained.

Operation ID: `uninstallBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageRemoveBody`](#bloemnativestorageremovebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageRemoval`](#bloemnativestorageremoval) | Uninstall and detach a source no library reads from; its catalog and reading progress are retained. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_in_use: a library reads from a source of this installation; change the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}/disable`

Disable and detach a source no library reads from; its catalog and reading progress are retained.

Operation ID: `disableBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageRemoveBody`](#bloemnativestorageremovebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageRemoval`](#bloemnativestorageremoval) | Disable and detach a source no library reads from; its catalog and reading progress are retained. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_in_use: a library reads from a source of this installation; change the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/installations/{installation_id}/upgrade`

Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept.

Operation ID: `upgradeBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB or the whole body 258 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or it is for a different plugin. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/s3/buckets`

List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses.

Operation ID: `listS3BucketsBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageS3BucketsBody`](#bloemnativestorages3bucketsbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Credentials are write-only and never stored.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageS3Buckets`](#bloemnativestorages3buckets) | List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Region, credential, bucket, prefix and cursor bounds apply as documented on the fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_endpoint_rejected: the endpoint is not an https origin, has userinfo, a path, query or fragment, an invalid or (organization scope) non-S3 port, a host resolving to any loopback, private, link-local, unique-local, CGNAT, multicast, unspecified or metadata address, or a host that does not resolve. s3_access_denied: the backend refused the credentials. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_backend_error: the backend failed, timed out, redirected or returned a malformed or oversized response. Messages are fixed and never quote the backend. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the per-administrator rate limit (30 per minute), the per-administrator cap of two concurrent requests or the server-wide cap of four (one reserved for platform administrators) refused the request; Retry-After is set for those refusals. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/s3/folders`

List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing.

Operation ID: `listS3FoldersBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageS3FoldersBody`](#bloemnativestorages3foldersbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Credentials are write-only and never stored.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageS3Folders`](#bloemnativestorages3folders) | List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Region, credential, bucket, prefix and cursor bounds apply as documented on the fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_endpoint_rejected: the endpoint is not an https origin, has userinfo, a path, query or fragment, an invalid or (organization scope) non-S3 port, a host resolving to any loopback, private, link-local, unique-local, CGNAT, multicast, unspecified or metadata address, or a host that does not resolve. s3_access_denied: the backend refused the credentials. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_backend_error: the backend failed, timed out, redirected or returned a malformed or oversized response. Messages are fixed and never quote the backend. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the per-administrator rate limit (30 per minute), the per-administrator cap of two concurrent requests or the server-wide cap of four (one reserved for platform administrators) refused the request; Retry-After is set for those refusals. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/organization/native-storage/sources`

List visible sources and their current configuration revisions.

Operation ID: `listSourcesBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `after` | query | string (uuid) |  | Exclusive keyset cursor returned as next_after; omit on the first page. |
| `limit` | query | integer |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`SourcePage`](#sourcepage) | List visible sources and their current configuration revisions. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: unknown, repeated or empty query parameters, a limit outside 1-100, an invalid after, or a request body. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the actor's storage owner, or a source on the page, is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/organization/native-storage/sources/{source_key}`

Read a visible source without configuration secrets.

Operation ID: `getSourceBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Read a visible source without configuration secrets. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/organization/native-storage/sources/{source_key}`

Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched.

Operation ID: `deleteRemovedSourceBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageDeleteSourceBody`](#bloemnativestoragedeletesourcebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSourceDeletion`](#bloemnativestoragesourcedeletion) | Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source still has its plugin installation; remove it first. source_in_use: a library reads from the source; delete the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/browse`

Browse authorized directories without starting discovery or opening files.

Operation ID: `browseBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Starts at the retained configured source root. Directory references and cursors are opaque host evidence bound to source/configuration/generation, actor, scope and expiry; a cursor is used only with its directory_ref. Requests have no body; repeated, empty and unknown query fields are rejected. Combined encoded query is limited to 6144 bytes. At most 100 directories per response. Inspect native-storage library_location_browsing first. Audio providers must explicitly support directory-only listing; browsing does not establish audio readiness.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string | yes | Retained authorized storage source UUID. |
| `directory_ref` | query | string |  | Opaque host directory reference: a selection_ref from a previous page (current, breadcrumbs or directories). Omit to start at the retained configured root. Never a filesystem path or provider URL. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page, sent with the same directory_ref and limit. A cursor without directory_ref is refused; to page the configured root, send that page's current.selection_ref as directory_ref. Combined encoded query is at most 6144 bytes. Expired, evicted or changed pages require restarting browsing. |
| `limit` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BrowsePage`](#browsepage) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: unknown, repeated or empty query parameters, a request body, a limit outside 1-100, a cursor without directory_ref, or references too long for the query bound. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an invalid source_key or an absent or hidden source; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_selection_reference: directory_ref is invalid, expired or no longer matches the source; browse_restart_required: the cursor's pagination changed or expired; restart browsing. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | browse_limit_exceeded: the encoded query exceeds 6144 bytes, or the directory depth, page count or continuation bound is exceeded. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_provider_page: the provider returned an invalid directory page. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/configuration`

Replace complete configuration only while the source namespace is empty.

Operation ID: `replaceConfigurationBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. The source and its installation must be enabled and attached (otherwise not_found). The configuration must satisfy the installed artifact's declared schema (otherwise invalid_request).

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageConfigurationBody`](#bloemnativestorageconfigurationbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageConfiguration`](#bloemnativestorageconfiguration) | Replace complete configuration only while the source namespace is empty. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. The configuration must also satisfy the installed artifact's declared schema. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. A disabled or detached source is also not_found. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. configuration_namespace_unverified: a source of this installation has a library location, entries or file references. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_endpoint_rejected: config connection.endpoint (S3) or connection.base_url (Bookwarehouse) is not an https URL on a well-known S3 port whose host resolves only to public addresses, or allow_loopback_http or allow_insecure_http is set; case variants of these keys are invalid_request. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/organization/native-storage/sources/{source_key}/credentials`

Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned.

Operation ID: `rotateCredentialsBloemOrganizationNativeStorage`.
Credentials: `bloemOrganizationContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. Only an enabled, attached S3 source qualifies: another plugin's source, or a disabled or detached one, is not_found.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageCredentialsBody`](#bloemnativestoragecredentialsbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageConfiguration`](#bloemnativestorageconfiguration) | Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Credentials must be nonempty (session_token may be empty) and free of control characters. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. A disabled or detached source is also not_found. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/library-locations`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `createBloemPlatformLibraryLocations`.
Credentials: `bloemPlatformContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

Request body (required, `application/json`): [`BloemLibraryLocationsCreateRequest`](#bloemlibrarylocationscreaterequest).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemLibraryLocationsCreated`](#bloemlibrarylocationscreated) | Created |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: malformed JSON, unknown, duplicate or missing fields, no path or selection, organization_id in organization scope, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: ordinary library validation failed, including legacy storage_source, blank paths, metadata_language and artwork-dependent options. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: a selected source, or the owner named by organization_id, is absent or hidden; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_selection_reference: a selection_ref is invalid, expired or no longer matches its source; browse again. revision_conflict with current_source_revision: a selection's configuration_revision is stale. conflict: another library already uses a requested filesystem path. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing. |
| 500 | [`BloemNativeStorageError`](#bloemnativestorageerror) | internal_error: the library could not be created. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. A plugin selection for a library type the host does not admit is also native_storage_unavailable. mutation_outcome_unknown returns operation_id, operation and library_id: reconcile using the library_id, never assume rollback or retry. |

### `GET /api/bloem/v1/admin/platform/library-locations/{library_id}`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `getBloemPlatformLibraryLocations`.
Credentials: `bloemPlatformContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `library_id` | path | integer | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`LibrarySelection`](#libraryselection) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an absent or hidden library or source, or an invalid library_id; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict: a concurrent filesystem path write holds the library's paths; returns current_library_revision. Retry the read. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/platform/library-locations/{library_id}`

Read or atomically save filesystem paths and selected plugin directories.

Operation ID: `replaceBloemPlatformLibraryLocations`.
Credentials: `bloemPlatformContext`.

Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `library_id` | path | integer | yes |  |

Request body (required, `application/json`): [`ReplaceLocationsRequest`](#replacelocationsrequest).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`LibrarySelection`](#libraryselection) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: malformed JSON, unknown, duplicate or missing fields, absent or null paths or selections, both empty, a nonpositive expected_revision, an empty expected_edit_token, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: a blank filesystem path. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an absent or hidden library or source, or an invalid library_id; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict with current_library_revision: expected_revision or expected_edit_token is stale, or a concurrent path write holds the library's paths; reload. invalid_selection_reference: a selection_ref is invalid, expired or no longer matches its source; browse again. revision_conflict with current_source_revision: a selection's configuration_revision is stale. conflict: another library already uses a requested filesystem path. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. A plugin selection for a library type the host does not admit is also native_storage_unavailable. mutation_outcome_unknown returns operation_id, operation and library_id: reconcile using the library_id, never assume rollback or retry. |

### `GET /api/bloem/v1/admin/platform/native-storage/artifacts`

List automatically verified catalog releases and any optional manual artifacts.

Operation ID: `listArtifactsBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageArtifacts`](#bloemnativestorageartifacts) | List automatically verified catalog releases and any optional manual artifacts. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: the catalog could not be refreshed and no artifact is approved. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/native-storage/capabilities`

Read composition-dependent flags; backend verification remains false.

Operation ID: `getCapabilitiesBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageCapabilities`](#bloemnativestoragecapabilities) | Read composition-dependent flags; backend verification remains false. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/catalog/installations`

Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload.

Operation ID: `catalogInstallBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. S3 sources are added only through catalog discovery: an S3 catalog key is artifact_rejected for a platform administrator and not_found for an organization administrator. A key with no catalog release is artifact_rejected; a failed download or checksum is native_storage_unavailable.

Request body (required, `application/json`): [`BloemNativeStoragePlatformInstallBody`](#bloemnativestorageplatforminstallbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download and checksum-verify the trusted catalog plugin, then install without manual approval or binary upload. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. organization_id is rejected in organization scope, and source_key and expected_revision go together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the retained source_key, or the owner named by organization_id, is absent or hidden. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the key names the S3 plugin, the release is not an approved artifact, or the configuration does not satisfy its declared schema. artifact_rejected also covers a key with no catalog release. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/catalog/installations/discover`

Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download.

Operation ID: `catalogDiscoverInstallBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageDiscoverInstallBody`](#bloemnativestoragediscoverinstallbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download the trusted catalog plugin (an S3 release must match a pre-approved digest), discover the root of the selected source and publish it atomically without a library or scan. The source is owned by the actor's scope: the platform, or the organization administrator's own organization; organization_id is rejected. Rate limited to 10 per minute per administrator (503 native_storage_unavailable with Retry-After); a key that is not an admitted S3 catalog release is artifact_rejected before any download. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Any field besides artifact_key, provider_source_id, enabled and config, including organization_id, is rejected, as is a config with state_path or with namespaces other than connection. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the actor's storage owner is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | source_attached: the generated source identity already exists. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the key is not an admitted S3 catalog release, or the configuration does not satisfy its declared schema. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. This includes the per-administrator rate limit (10 per minute, burst 3; Retry-After is set), discovery not being composed, and the provider failing to start or describe the source within 30 seconds. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/catalog/installations/{installation_id}/upgrade`

Download and verify a catalog upgrade, retaining configuration and library.

Operation ID: `catalogUpgradeBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. An organization administrator receives not_found for the S3 plugin, also for an S3 source the organization owns; a platform administrator, or a manual upload of an approved artifact, upgrades it. A key with no catalog release is artifact_rejected; a failed download or checksum is native_storage_unavailable.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageUpgradeBody`](#bloemnativestorageupgradebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Download and verify a catalog upgrade, retaining configuration and library. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. An organization administrator also receives it for the S3 plugin. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the release is not an approved artifact or is for a different plugin. artifact_rejected also covers a key with no catalog release. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. native_storage_unavailable also covers an unavailable catalog and a failed download or checksum. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/installations`

Install an approved artifact and source without creating a library or scan.

Operation ID: `installBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Install an approved artifact and source without creating a library or scan. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. organization_id is rejected in organization scope, and source_key and expected_revision go together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the retained source_key, or the owner named by organization_id, is absent or hidden. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source is already attached, enabled or present. retained_namespace_unverified: the retained source's identity differs from the request or its namespace is populated. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB, the whole body 258 MiB, or the serialized configuration 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or the configuration does not satisfy its declared schema. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/installations/discover`

Discover the root of a new approved S3 installation and publish it atomically without a library or scan.

Operation ID: `discoverInstallBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. Only a host-approved bloem.storage.s3 artifact qualifies. The provider runs once in a temporary process to report the selected source's root; the caller never supplies a root, source key or state_path.

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Discover the root of a new approved S3 installation and publish it atomically without a library or scan. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. organization_id is rejected in organization scope, and config must hold only the connection namespace, without state_path. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the owner named by organization_id is absent or hidden. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | source_attached: the generated source identity already exists. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB, the whole body 258 MiB, or the serialized configuration 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not a host-approved bloem.storage.s3 artifact, the uploaded bytes do not match it, or the configuration does not satisfy its declared schema. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. This includes discovery not being composed and the provider failing to start or describe the source within 30 seconds. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}`

Uninstall and detach a source no library reads from; its catalog and reading progress are retained.

Operation ID: `uninstallBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageRemoveBody`](#bloemnativestorageremovebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageRemoval`](#bloemnativestorageremoval) | Uninstall and detach a source no library reads from; its catalog and reading progress are retained. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_in_use: a library reads from a source of this installation; change the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}/disable`

Disable and detach a source no library reads from; its catalog and reading progress are retained.

Operation ID: `disableBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageRemoveBody`](#bloemnativestorageremovebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageRemoval`](#bloemnativestorageremoval) | Disable and detach a source no library reads from; its catalog and reading progress are retained. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_in_use: a library reads from a source of this installation; change the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/installations/{installation_id}/upgrade`

Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept.

Operation ID: `upgradeBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `installation_id` | path | integer | yes |  |

Request body (required, `multipart/form-data`): object.
Exactly two parts, request JSON (at most 1 MiB) and nonempty binary (at most 256 MiB); total at most 258 MiB. Duplicate/extra parts and Content-Transfer-Encoding are rejected. Request keys are exact, unique and bounded; no trailing JSON.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Install a newer approved artifact of the same plugin; the source, its configuration, library and catalog are kept. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not exactly one request part and one nonempty binary part, or the request JSON has unknown, duplicate, missing or invalid fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the request part exceeds 1 MiB, the binary 256 MiB or the whole body 258 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | artifact_rejected: the artifact is not host-approved, the uploaded bytes do not match it, or it is for a different plugin. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/s3/buckets`

List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses.

Operation ID: `listS3BucketsBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageS3BucketsBody`](#bloemnativestorages3bucketsbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Credentials are write-only and never stored.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageS3Buckets`](#bloemnativestorages3buckets) | List the buckets the supplied S3 credentials can see, for choosing a new source. Stateless: the credentials are used for this request only and never stored, logged or returned. The endpoint is checked against server-side request forgery and the connection is pinned to the checked addresses. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Region, credential, bucket, prefix and cursor bounds apply as documented on the fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_endpoint_rejected: the endpoint is not an https origin, has userinfo, a path, query or fragment, an invalid or (organization scope) non-S3 port, a host resolving to any loopback, private, link-local, unique-local, CGNAT, multicast, unspecified or metadata address, or a host that does not resolve. s3_access_denied: the backend refused the credentials. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_backend_error: the backend failed, timed out, redirected or returned a malformed or oversized response. Messages are fixed and never quote the backend. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the per-administrator rate limit (30 per minute), the per-administrator cap of two concurrent requests or the server-wide cap of four (one reserved for platform administrators) refused the request; Retry-After is set for those refusals. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/s3/folders`

List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing.

Operation ID: `listS3FoldersBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

Request body (required, `application/json`): [`BloemNativeStorageS3FoldersBody`](#bloemnativestorages3foldersbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Credentials are write-only and never stored.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageS3Folders`](#bloemnativestorages3folders) | List one page (at most 200) of the immediate child folders of a prefix in a bucket, for choosing a new source's prefix. Files are never returned. Stateless, with the same endpoint guard and credential handling as the bucket listing. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Region, credential, bucket, prefix and cursor bounds apply as documented on the fields. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_endpoint_rejected: the endpoint is not an https origin, has userinfo, a path, query or fragment, an invalid or (organization scope) non-S3 port, a host resolving to any loopback, private, link-local, unique-local, CGNAT, multicast, unspecified or metadata address, or a host that does not resolve. s3_access_denied: the backend refused the credentials. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | s3_backend_error: the backend failed, timed out, redirected or returned a malformed or oversized response. Messages are fixed and never quote the backend. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the per-administrator rate limit (30 per minute), the per-administrator cap of two concurrent requests or the server-wide cap of four (one reserved for platform administrators) refused the request; Retry-After is set for those refusals. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/native-storage/sources`

List visible sources and their current configuration revisions.

Operation ID: `listSourcesBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `after` | query | string (uuid) |  | Exclusive keyset cursor returned as next_after; omit on the first page. |
| `limit` | query | integer |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`SourcePage`](#sourcepage) | List visible sources and their current configuration revisions. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: unknown, repeated or empty query parameters, a limit outside 1-100, an invalid after, or a request body. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: the actor's storage owner, or a source on the page, is no longer visible. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/native-storage/sources/{source_key}`

Read a visible source without configuration secrets.

Operation ID: `getSourceBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSource`](#bloemnativestoragesource) | Read a visible source without configuration secrets. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters or a request body were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/native-storage/sources/{source_key}`

Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched.

Operation ID: `deleteRemovedSourceBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageDeleteSourceBody`](#bloemnativestoragedeletesourcebody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageSourceDeletion`](#bloemnativestoragesourcedeletion) | Delete a removed source no library reads from, with its discovered entries, scan history and a disabled installation it leaves behind. The storage itself is not touched. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. source_attached: the source still has its plugin installation; remove it first. source_in_use: a library reads from the source; delete the library first. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `GET /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/browse`

Browse authorized directories without starting discovery or opening files.

Operation ID: `browseBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Starts at the retained configured source root. Directory references and cursors are opaque host evidence bound to source/configuration/generation, actor, scope and expiry; a cursor is used only with its directory_ref. Requests have no body; repeated, empty and unknown query fields are rejected. Combined encoded query is limited to 6144 bytes. At most 100 directories per response. Inspect native-storage library_location_browsing first. Audio providers must explicitly support directory-only listing; browsing does not establish audio readiness.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string | yes | Retained authorized storage source UUID. |
| `directory_ref` | query | string |  | Opaque host directory reference: a selection_ref from a previous page (current, breadcrumbs or directories). Omit to start at the retained configured root. Never a filesystem path or provider URL. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page, sent with the same directory_ref and limit. A cursor without directory_ref is refused; to page the configured root, send that page's current.selection_ref as directory_ref. Combined encoded query is at most 6144 bytes. Expired, evicted or changed pages require restarting browsing. |
| `limit` | query | integer (int64) |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BrowsePage`](#browsepage) | OK |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: unknown, repeated or empty query parameters, a request body, a limit outside 1-100, a cursor without directory_ref, or references too long for the query bound. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: an invalid source_key or an absent or hidden source; hidden resources retain not_found precedence. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_selection_reference: directory_ref is invalid, expired or no longer matches the source; browse_restart_required: the cursor's pagination changed or expired; restart browsing. authorization_state_changed: the storage owner, source or library changed while the request waited for its locks; reload and retry. |
| 422 | [`BloemNativeStorageError`](#bloemnativestorageerror) | browse_limit_exceeded: the encoded query exceeds 6144 bytes, or the directory depth, page count or continuation bound is exceeded. |
| 502 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_provider_page: the provider returned an invalid directory page. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: browsing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked. |

### `PUT /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/configuration`

Replace complete configuration only while the source namespace is empty.

Operation ID: `replaceConfigurationBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. The source and its installation must be enabled and attached (otherwise not_found). The configuration must satisfy the installed artifact's declared schema (otherwise invalid_request).

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageConfigurationBody`](#bloemnativestorageconfigurationbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageConfiguration`](#bloemnativestorageconfiguration) | Replace complete configuration only while the source namespace is empty. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. The configuration must also satisfy the installed artifact's declared schema. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. A disabled or detached source is also not_found. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. configuration_namespace_unverified: a source of this installation has a library location, entries or file references. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body or the serialized configuration exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

### `POST /api/bloem/v1/admin/platform/native-storage/sources/{source_key}/credentials`

Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned.

Operation ID: `rotateCredentialsBloemPlatformNativeStorage`.
Credentials: `bloemPlatformContext`.

Experimental native storage sources served by host-approved storage plugins; the capabilities list which source kinds and media this host supports. A library uses a source as its storage location through the library-locations operations or the v2 library API. Requires the matching signed administrative context with current account/session, scope and actual resource authority. Entitlement to use a shared source does not permit configuring or removing its installation. Hidden resources retain not_found precedence. Inspect this scope's capabilities before offering mutations; backend_verified remains false. Reads have no body. Query parameters other than the documented pagination fields are rejected, including repeated or empty values. Reconcile unknown native-storage mutation outcomes instead of automatically replaying them. Only an enabled, attached S3 source qualifies: another plugin's source, or a disabled or detached one, is not_found.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `source_key` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemNativeStorageCredentialsBody`](#bloemnativestoragecredentialsbody).
Strict application/json object, at most 1 MiB. Exact field names; duplicate or unknown keys, trailing JSON and invalid UTF-8 are rejected. Revisions are positive integers.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNativeStorageConfiguration`](#bloemnativestorageconfiguration) | Replace only the S3 access key, secret key and session token of an enabled source, including one a library uses. Credentials are never returned. |
| 400 | [`BloemNativeStorageError`](#bloemnativestorageerror) | invalid_request: query parameters were sent, or the body is not a strict application/json object with exactly the documented fields and valid values. Credentials must be nonempty (session_token may be empty) and free of control characters. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority or insufficient_organization_authority: the administrative context's scope does not match the route, or the account lost that authority; organization_suspended: the organization context's organization is suspended. |
| 404 | [`BloemNativeStorageError`](#bloemnativestorageerror) | not_found: invalid path identity, absent resource or hidden authority. A disabled or detached source is also not_found. |
| 409 | [`BloemNativeStorageError`](#bloemnativestorageerror) | revision_conflict returns current_revision, the source's current configuration revision. authorization_state_changed: the storage owner or source changed while the request waited for its locks; reload and retry. |
| 413 | [`BloemNativeStorageError`](#bloemnativestorageerror) | request_too_large: the JSON body exceeds 1 MiB. |
| 503 | [`BloemNativeStorageError`](#bloemnativestorageerror) | native_storage_unavailable: native storage, the source, its provider or the database is unavailable. mutation_outcome_unknown returns operation_id, operation and authorized known recovery identifiers; a lost commit acknowledgement is not rollback. tenant_unavailable: administrative authority could not be checked. |

## Schemas

### AdminFormConditionView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `equals` | array of string | yes |  |
| `field` | string | yes |  |

### AdminFormFieldView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `control` | string | yes |  |
| `default_value` | any |  |  |
| `description` | string |  |  |
| `dynamic_options` | boolean |  |  |
| `exclusive_group_field` | string |  |  |
| `key` | string | yes |  |
| `label` | string | yes |  |
| `multiline` | boolean | yes |  |
| `options` | array of [`AdminFormOptionView`](#adminformoptionview) |  |  |
| `placeholder` | string |  |  |
| `required` | boolean | yes |  |
| `rows` | integer (int32) |  |  |
| `secret` | boolean | yes |  |
| `show_when` | array of [`AdminFormConditionView`](#adminformconditionview) |  |  |
| `validation` | [`AdminFormValidationView`](#adminformvalidationview) |  |  |

### AdminFormOptionView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `description` | string |  |  |
| `label` | string | yes |  |
| `value` | string | yes |  |

### AdminFormSectionView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `collapsed_default` | boolean | yes |  |
| `collapsible` | boolean | yes |  |
| `description` | string |  |  |
| `field_keys` | array of string | yes |  |
| `key` | string | yes |  |
| `show_when` | array of [`AdminFormConditionView`](#adminformconditionview) |  |  |
| `title` | string | yes |  |

### AdminFormValidationView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `has_max` | boolean |  |  |
| `has_min` | boolean |  |  |
| `max` | number (double) |  |  |
| `max_length` | integer (int32) |  |  |
| `min` | number (double) |  |  |
| `min_length` | integer (int32) |  |  |
| `pattern` | string |  |  |

### AdminFormView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `fields` | array of [`AdminFormFieldView`](#adminformfieldview) | yes |  |
| `sections` | array of [`AdminFormSectionView`](#adminformsectionview) |  |  |
| `submit_label` | string |  |  |

### BloemLibraryCreateOptions

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `chapter_thumbnails_enabled` | boolean |  |  |
| `intro_detection_enabled` | boolean |  |  |
| `metadata_language` | string |  |  |
| `name` | string | yes |  |
| `paths` | array of string | yes |  |
| `realtime_monitoring` | boolean |  |  |
| `trailer_kinds` | array of string |  |  |
| `trickplay_enabled` | boolean |  |  |
| `type` | string | yes |  |

### BloemLibraryLocationsCreateRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `library` | [`BloemLibraryCreateOptions`](#bloemlibrarycreateoptions) | yes |  |
| `organization_id` | string |  |  |
| `selections` | array of [`RootSelection`](#rootselection) | yes |  |

### BloemLibraryLocationsCreated

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `library` | [`LibraryResponse`](#libraryresponse) | yes |  |
| `locations` | [`LibrarySelection`](#libraryselection) | yes |  |

### BloemNativeStorageArtifacts

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifacts` | array of [`NativeStorageArtifactResponse`](#nativestorageartifactresponse) | yes |  |

### BloemNativeStorageCapabilities

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `approved_artifact_install` | boolean | yes |  |
| `backend_verified` | boolean: `False` | yes |  |
| `configuration_replace_unbound` | boolean | yes |  |
| `credential_rotation_in_use` | boolean | yes | Credential rotation is composed and works on a source that backs a library. It overlays only access key, secret key and session token and needs an enabled source and its current revision. |
| `disable` | boolean | yes |  |
| `enable` | boolean: `False` | yes |  |
| `library_location_atomic_editing` | boolean | yes | Scoped library-locations operations are composed with the ordinary library creator. Replacement requires revision and actor-bound path edit token. |
| `library_location_browsing` | boolean | yes | The retained host browser and shared pagination store are composed. Individual source authority and media declarations are checked separately. |
| `library_location_mixing` | boolean | yes | One library may combine filesystem paths with plugin sources or use several plugin sources. While false, a library keeps filesystem paths or one plugin source over its lifetime, and other shapes return location_mixing_unavailable. |
| `native_audio_libraries` | boolean | yes | Requires the complete native audio scanner, catalog and playback composition. |
| `native_audio_playback` | boolean | yes | Always equal to native_audio_libraries: true only when the native audio scanner, catalog and playback are all composed. |
| `native_movie_libraries` | boolean | yes | Movie attachment and scanning are composed with native video playback; individual sources still require admission under their retained read policy. |
| `native_video_downloads` | boolean: `False` | yes |  |
| `native_video_jellyfin` | boolean: `False` | yes |  |
| `native_video_playback` | boolean | yes | Native API video playback is composed; this does not certify a backend or client runtime. |
| `native_video_remote_execution` | boolean | yes | Shared worker grants and header-auth serving recipes are composed for remote HLS; the selected worker still needs its own approved runtime and retained provider identity. |
| `retained_namespace_reinstall` | boolean: `False` | yes |  |
| `s3_bucket_browse` | boolean | yes | The stateless S3 bucket and folder browse operations are composed. Organization administrators are limited to well-known S3 endpoint ports. |
| `s3_catalog_discovery` | boolean | yes | Administrators can add an S3 source from a catalog release whose digest the host has pre-approved, discovering its root without a manual upload. Platform administrators create a platform-owned source and organization administrators one owned by their organization. |
| `s3_checked_etag_reads` | boolean | yes | Explicit checked-etag admission is supported for approved S3 source configuration. This does not enable a source or prove immutable bytes. |
| `s3_initial_root_discovery` | boolean | yes |  |
| `schema` | integer (int64): `2` | yes |  |
| `source_management` | boolean | yes |  |
| `supported_source_media` | array of [`BloemNativeStorageSourceMedia`](#bloemnativestoragesourcemedia) | yes | Host-supported source/media combinations; this does not prove that a provider instance supports or is available for a kind. |
| `uninstall` | boolean | yes |  |
| `upgrade` | boolean | yes |  |

### BloemNativeStorageConfiguration

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configuration_revision` | integer (int64) | yes |  |
| `source_key` | string | yes |  |

### BloemNativeStorageConfigurationBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `config` | object of object | yes | Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid. |
| `expected_revision` | integer (int64) | yes | Current source configuration revision. Replacement requires an empty namespace without bindings, entries or references. |

### BloemNativeStorageCredentialsBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_key` | string | yes | Replacement S3 access key id. No control characters. Encrypted at rest, never returned. |
| `expected_revision` | integer (int64) | yes | Current source configuration revision. The source must be enabled. |
| `secret_key` | string | yes | Replacement S3 secret access key. No control characters. Encrypted at rest, never returned. |
| `session_token` | string |  | Optional replacement session token; omitted or empty clears any stored token. Encrypted at rest, never returned. |

### BloemNativeStorageDeleteSourceBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Current source configuration revision. |

### BloemNativeStorageDiscoverInstallBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifact_key` | string | yes | Host-approved bloem.storage.s3 artifact key, at most 1024 UTF-8 bytes. The upload cannot approve its own artifact. |
| `config` | object of object | yes | Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid. |
| `enabled` | boolean | yes | Committed enabled state. Even false requires a configured temporary discovery process. |
| `provider_source_id` | string | yes | Exact opaque provider source to select from a fresh Describe, at most 1024 UTF-8 bytes. Its root is discovered, never supplied by the caller. |

### BloemNativeStorageError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `current_library_revision` | integer (int64) |  |  |
| `current_revision` | integer (int64) |  |  |
| `current_source_revision` | integer (int64) |  |  |
| `error` | string | yes |  |
| `library_id` | integer (int64) |  |  |
| `message` | string | yes |  |
| `operation` | string: `install`, `upgrade`, `configuration`, `disable`, `uninstall`, `create_library`, `replace_library_locations` |  |  |
| `operation_id` | string |  |  |
| `source_key` | string |  |  |

### BloemNativeStorageInstallBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifact_key` | string | yes | Host-approved artifact key, at most 1024 UTF-8 bytes; the upload cannot approve its own manifest or digest. |
| `config` | object of object | yes | Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid. |
| `enabled` | boolean | yes |  |
| `expected_revision` | integer or null (int64) |  | Required when source_key is nonnull; otherwise omit or null. Current source configuration revision. |
| `provider_source_id` | string | yes | Opaque provider source identity, at most 1024 UTF-8 bytes. |
| `root_entry_id` | string | yes | Opaque provider root identity, at most 1024 UTF-8 bytes; not a mounted path. |
| `source_key` | string or null |  | Retained source to reinstall. Requires expected_revision, verified lineage and an empty namespace; populated reattachment is unsupported. |

### BloemNativeStoragePlatformDiscoverInstallBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifact_key` | string | yes | Host-approved bloem.storage.s3 artifact key, at most 1024 UTF-8 bytes. The upload cannot approve its own artifact. |
| `config` | object of object | yes | Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid. |
| `enabled` | boolean | yes | Committed enabled state. Even false requires a configured temporary discovery process. |
| `organization_id` | string or null |  | Optional active organization ownership. Omitted or null uses platform ownership. Organization-scoped routes reject this field, including null. |
| `provider_source_id` | string | yes | Exact opaque provider source to select from a fresh Describe, at most 1024 UTF-8 bytes. Its root is discovered, never supplied by the caller. |

### BloemNativeStoragePlatformInstallBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifact_key` | string | yes | Host-approved artifact key, at most 1024 UTF-8 bytes; the upload cannot approve its own manifest or digest. |
| `config` | object of object | yes | Complete encrypted provider configuration. Each keyed value must be an object; empty configuration is valid. |
| `enabled` | boolean | yes |  |
| `expected_revision` | integer or null (int64) |  | Required when source_key is nonnull; otherwise omit or null. Current source configuration revision. |
| `organization_id` | string or null |  | Optional active organization ownership. Omitted or null uses platform ownership. Organization-scoped routes reject this field. |
| `provider_source_id` | string | yes | Opaque provider source identity, at most 1024 UTF-8 bytes. |
| `root_entry_id` | string | yes | Opaque provider root identity, at most 1024 UTF-8 bytes; not a mounted path. |
| `source_key` | string or null |  | Retained source to reinstall. Requires expected_revision, verified lineage and an empty namespace; populated reattachment is unsupported. |

### BloemNativeStorageRemoval

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `installation_id` | integer (int64) | yes |  |
| `retained` | boolean: `True` | yes |  |
| `state` | string: `disabled_detached`, `uninstalled_detached` | yes |  |

### BloemNativeStorageRemoveBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Current source configuration revision, not the library revision. |
| `source_key` | string | yes |  |

### BloemNativeStorageS3Buckets

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `buckets` | array of [`S3Bucket`](#s3bucket) | yes | Bucket names usable as an S3 source, sorted ascending, at most 1000. |

### BloemNativeStorageS3BucketsBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_key` | string | yes | S3 access key id, at most 1024 bytes. Used for this request only; never stored, logged or returned. Trimmed. |
| `endpoint` | string | yes | S3-compatible https origin, for example https://s3.example.com:9000. No userinfo, path, query or fragment. Every resolved address must be public; organization administrators may only use ports 443, 7480, 8443, 9000 and 9443. Trimmed. |
| `region` | string | yes | Signing region: lowercase letters, digits and hyphens, at most 64. Trimmed. |
| `secret_key` | string | yes | S3 secret access key, at most 4096 bytes. Used for this request only; never stored, logged or returned. Trimmed. |
| `session_token` | string |  | Optional session token, at most 8192 bytes. Used for this request only; never stored, logged or returned. Trimmed. |

### BloemNativeStorageS3Folders

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `folders` | array of [`S3Folder`](#s3folder) | yes | Immediate child folders only, at most 200; files are never returned. A page may be empty and still continue. |
| `next_cursor` | string | yes | Opaque continuation token for the next page; empty when the listing is complete. |
| `prefix` | string | yes | The listed prefix. |

### BloemNativeStorageS3FoldersBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_key` | string | yes | S3 access key id, at most 1024 bytes. Used for this request only; never stored, logged or returned. Trimmed. |
| `bucket` | string | yes | Bucket to list, as returned by the bucket listing. Trimmed. |
| `cursor` | string |  | Opaque next_cursor of the previous page for the same bucket and prefix, at most 4096 bytes; omit for the first page. |
| `endpoint` | string | yes | S3-compatible https origin, for example https://s3.example.com:9000. No userinfo, path, query or fragment. Every resolved address must be public; organization administrators may only use ports 443, 7480, 8443, 9000 and 9443. Trimmed. |
| `prefix` | string |  | Folder to list: empty for the bucket root, else a prefix ending in /, as returned in folders[].prefix. UTF-8, at most 1024 bytes, no backslash, control characters or . and .. segments. Not trimmed. |
| `region` | string | yes | Signing region: lowercase letters, digits and hyphens, at most 64. Trimmed. |
| `secret_key` | string | yes | S3 secret access key, at most 4096 bytes. Used for this request only; never stored, logged or returned. Trimmed. |
| `session_token` | string |  | Optional session token, at most 8192 bytes. Used for this request only; never stored, logged or returned. Trimmed. |

### BloemNativeStorageSource

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `source` | [`SourceView`](#sourceview) | yes |  |

### BloemNativeStorageSourceDeletion

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `deleted` | boolean: `True` | yes |  |
| `source_key` | string | yes |  |

### BloemNativeStorageSourceMedia

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `media_kinds` | array of string | yes |  |
| `source_kind` | string: `filesystem`, `s3`, `bookwarehouse` | yes |  |

### BloemNativeStorageUpgradeBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `artifact_key` | string | yes | Host-approved artifact of the same plugin, at most 1024 UTF-8 bytes; the upload cannot approve its own manifest or digest. |
| `expected_revision` | integer (int64) | yes | Current source configuration revision; the upgrade keeps it. |
| `source_key` | string | yes | The source this installation serves. |

### BloemOrganizationLibraryLocationsCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `library` | [`BloemLibraryCreateOptions`](#bloemlibrarycreateoptions) | yes |  |
| `selections` | array of [`RootSelection`](#rootselection) | yes |  |

### BrowseDirectory

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `logical_path` | string | yes |  |
| `name` | string | yes |  |
| `selection_ref` | string | yes |  |

### BrowsePage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `breadcrumbs` | array of [`BrowseDirectory`](#browsedirectory) | yes |  |
| `current` | [`BrowseDirectory`](#browsedirectory) | yes |  |
| `directories` | array of [`BrowseDirectory`](#browsedirectory) | yes |  |
| `next_cursor` | string | yes |  |

### ConfigSchemaView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `admin_form` | [`AdminFormView`](#adminformview) |  |  |
| `description` | string | yes |  |
| `json_schema` | string | yes |  |
| `key` | string | yes |  |
| `required` | boolean | yes |  |
| `title` | string | yes |  |

### LibraryLocationView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configuration_revision` | integer (int64) | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `roots` | array of [`LibraryRootView`](#libraryrootview) | yes |  |
| `selection_revision` | integer (int64) | yes |  |
| `source_key` | string | yes |  |

### LibraryResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `auto_translate_metadata` | boolean | yes |  |
| `chapter_thumbnails_enabled` | boolean | yes |  |
| `chapter_thumbnails_supported` | boolean | yes |  |
| `enabled` | boolean | yes |  |
| `id` | integer (int64) | yes |  |
| `intro_detection_enabled` | boolean | yes |  |
| `last_scanned_at` | string (date-time) |  |  |
| `metadata_language` | string | yes |  |
| `name` | string | yes |  |
| `paths` | array of string | yes |  |
| `poster_url` | string |  |  |
| `scan_warning_at` | string (date-time) |  |  |
| `scan_warning_code` | string |  |  |
| `scan_warning_message` | string |  |  |
| `sort_order` | integer (int64) | yes |  |
| `trailer_kinds` | array of string | yes |  |
| `type` | string | yes |  |

### LibraryRootView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `directory_id` | string | yes |  |
| `enabled` | boolean | yes |  |
| `id` | string | yes |  |
| `logical_path` | string | yes |  |
| `name` | string | yes |  |

### LibrarySelection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `edit_token` | string | yes |  |
| `locations` | array of [`LibraryLocationView`](#librarylocationview) | yes |  |
| `paths` | array of string | yes |  |
| `revision` | integer (int64) | yes |  |
| `selections` | array of [`SavedRootSelection`](#savedrootselection) | yes |  |

### NativeStorageArtifactResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `arch` | string | yes |  |
| `artifact_key` | string | yes |  |
| `catalog` | boolean | yes |  |
| `config_schema` | array of [`ConfigSchemaView`](#configschemaview) | yes |  |
| `os` | string | yes |  |
| `plugin_id` | string | yes |  |
| `version` | string | yes |  |

### ReplaceLocationsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_edit_token` | string | yes |  |
| `expected_revision` | integer (int64) | yes |  |
| `paths` | array of string | yes |  |
| `selections` | array of [`RootSelection`](#rootselection) | yes |  |

### RootSelection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configuration_revision` | integer (int64) | yes |  |
| `selection_ref` | string | yes |  |
| `source_key` | string | yes |  |

### S3Bucket

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes |  |

### S3Folder

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes |  |
| `prefix` | string | yes |  |

### SavedRootSelection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configuration_revision` | integer (int64) | yes |  |
| `logical_path` | string | yes |  |
| `name` | string | yes |  |
| `selection_ref` | string | yes |  |
| `source_key` | string | yes |  |

### SourceLibrary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |

### SourcePage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `next_after` | string or null | yes |  |
| `sources` | array of [`SourceView`](#sourceview) | yes |  |

### SourceView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `configuration_revision` | integer (int64) | yes |  |
| `configured` | boolean | yes |  |
| `connection_summary` | object of string | yes |  |
| `enabled` | boolean | yes |  |
| `in_use` | boolean | yes |  |
| `installation_id` | integer or null (int64) | yes |  |
| `libraries` | array of [`SourceLibrary`](#sourcelibrary) | yes |  |
| `organization_id` | string or null | yes |  |
| `owner_kind` | string | yes |  |
| `plugin_id` | string | yes |  |
| `provider_source_id` | string | yes |  |
| `root_entry_id` | string | yes |  |
| `source_key` | string | yes |  |
| `state` | string | yes |  |
