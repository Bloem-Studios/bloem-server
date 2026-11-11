# Plans and preview API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v2/openapi.json`](../../../contracts/api/bloem/v2/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Entitlement templates (plans) and their revisions, applying them to accounts one at a time or in reviewed bulk jobs, and the storefront preview: its administration and the guest sessions.

For the plain-language picture, read the [Plans and preview explainer](../../architecture/bloem-plans.md). The invariants are in:

- [Entitlement templates guide](../../wiki/admin/entitlement-templates.md)
- [Entitlement template operations](../../operations/entitlement-templates.md)
- [Bulk policy cohorts](../../operations/bulk-policy-cohorts.md)
- [Browse-only content policy](../../architecture/bloem-browse-only-content-policy.md)

## Operations (34)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v2/admin/organization/entitlement-cohorts`](#get-apibloemv2adminorganizationentitlement-cohorts) | List safe entitlement cohort projections. |
| [`GET /api/bloem/v2/admin/organization/entitlement-cohorts/{cohort_id}`](#get-apibloemv2adminorganizationentitlement-cohortscohort_id) | Read a safe organization cohort projection. |
| [`POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs`](#post-apibloemv2adminplatformaccountsentitlement-bulkpolicy-jobs) | Enqueue a confirmed bulk entitlement policy job. |
| [`GET /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}`](#get-apibloemv2adminplatformaccountsentitlement-bulkpolicy-jobsjob_id) | Read a bulk entitlement policy job. |
| [`POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel`](#post-apibloemv2adminplatformaccountsentitlement-bulkpolicy-jobsjob_idcancel) | Cancel an eligible bulk entitlement policy job. |
| [`POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-previews`](#post-apibloemv2adminplatformaccountsentitlement-bulkpolicy-previews) | Create a selection and preview a bulk policy command. |
| [`POST /api/bloem/v2/admin/platform/accounts/entitlement-snapshots`](#post-apibloemv2adminplatformaccountsentitlement-snapshots) | Read bounded account policy snapshots in one observation. |
| [`GET /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement`](#get-apibloemv2adminplatformaccountsaccount_identitlement) | Read authoritative account and profile entitlement policies. |
| [`POST /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement/apply`](#post-apibloemv2adminplatformaccountsaccount_identitlementapply) | Apply a pinned entitlement template. |
| [`POST /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement/dry-run`](#post-apibloemv2adminplatformaccountsaccount_identitlementdry-run) | Preview a pinned entitlement template. |
| [`GET /api/bloem/v2/admin/platform/entitlement-templates`](#get-apibloemv2adminplatformentitlement-templates) | List the entitlement template catalog. |
| [`POST /api/bloem/v2/admin/platform/entitlement-templates`](#post-apibloemv2adminplatformentitlement-templates) | Create a versioned entitlement template. |
| [`GET /api/bloem/v2/admin/platform/entitlement-templates/{key}`](#get-apibloemv2adminplatformentitlement-templateskey) | Read the latest or a selected entitlement template revision. |
| [`POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/archive`](#post-apibloemv2adminplatformentitlement-templateskeyarchive) | Archive a template using its current revision. |
| [`POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/clone`](#post-apibloemv2adminplatformentitlement-templateskeyclone) | Clone a template revision into a disabled new template. |
| [`GET /api/bloem/v2/admin/platform/entitlement-templates/{key}/history`](#get-apibloemv2adminplatformentitlement-templateskeyhistory) | List immutable template revisions (history alias). |
| [`GET /api/bloem/v2/admin/platform/entitlement-templates/{key}/revisions`](#get-apibloemv2adminplatformentitlement-templateskeyrevisions) | List immutable template revisions. |
| [`POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/revisions`](#post-apibloemv2adminplatformentitlement-templateskeyrevisions) | Create a new revision or copy a historical policy into a new revision. |
| [`POST /api/bloem/v2/admin/platform/preview/account`](#post-apibloemv2adminplatformpreviewaccount) | Ensure the issuer's shared preview account and primary profile. |
| [`PUT /api/bloem/v2/admin/platform/preview/accounts/{account_id}/presentation`](#put-apibloemv2adminplatformpreviewaccountsaccount_idpresentation) | Set the personal preview end date for display. |
| [`GET /api/bloem/v2/admin/platform/preview/capacity`](#get-apibloemv2adminplatformpreviewcapacity) | Read private guest capacity and account readiness. |
| [`GET /api/bloem/v2/admin/platform/preview/catalog`](#get-apibloemv2adminplatformpreviewcatalog) | Find metadata visible to the preview for a showcase picker. |
| [`PUT /api/bloem/v2/admin/platform/preview/keys`](#put-apibloemv2adminplatformpreviewkeys) | Replace the Ed25519 launch-key allowlist. |
| [`PUT /api/bloem/v2/admin/platform/preview/libraries`](#put-apibloemv2adminplatformpreviewlibraries) | Revise the preview library selection and reapply it atomically. |
| [`DELETE /api/bloem/v2/admin/platform/preview/sessions`](#delete-apibloemv2adminplatformpreviewsessions) | Revoke all guest sessions without disabling the preview. |
| [`GET /api/bloem/v2/admin/platform/preview/settings`](#get-apibloemv2adminplatformpreviewsettings) | Read preview settings and public launch keys. |
| [`PUT /api/bloem/v2/admin/platform/preview/settings`](#put-apibloemv2adminplatformpreviewsettings) | Merge explicitly supplied preview settings. |
| [`POST /api/bloem/v2/admin/platform/preview/verify`](#post-apibloemv2adminplatformpreviewverify) | Verify a freshly signed launch ticket without consuming it. |
| [`GET /api/bloem/v2/admin/platform/users/{user_id}/entitlement`](#get-apibloemv2adminplatformusersuser_identitlement) | Read the legacy managed account entitlement projection. |
| [`POST /api/bloem/v2/admin/platform/users/{user_id}/entitlement/apply`](#post-apibloemv2adminplatformusersuser_identitlementapply) | Apply a pinned entitlement template. |
| [`POST /api/bloem/v2/admin/platform/users/{user_id}/entitlement/dry-run`](#post-apibloemv2adminplatformusersuser_identitlementdry-run) | Preview a pinned entitlement template. |
| [`POST /api/bloem/v2/preview/events`](#post-apibloemv2previewevents) | Increment a daily aggregate preview counter. |
| [`POST /api/bloem/v2/preview/sessions`](#post-apibloemv2previewsessions) | Exchange a signed launch ticket for a bounded guest session. |
| [`GET /api/bloem/v2/preview/status`](#get-apibloemv2previewstatus) | Discover preview availability and host-specific presentation. |

## Reference

### `GET /api/bloem/v2/admin/organization/entitlement-cohorts`

List safe entitlement cohort projections.

Operation ID: `listBloemOrganizationEntitlementCohorts`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `include_archived` | query | boolean |  | true or false; default false. Other query parameters return 422. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementCohortsResponse`](#bloementitlementcohortsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: include_archived is not true or false, or another query parameter is present. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v2/admin/organization/entitlement-cohorts/{cohort_id}`

Read a safe organization cohort projection.

Operation ID: `getBloemOrganizationEntitlementCohort`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `cohort_id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementCohortResponse`](#bloementitlementcohortresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the cohort ID is malformed or the cohort is absent from the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs`

Enqueue a confirmed bulk entitlement policy job.

Operation ID: `createBloemPlatformDirectPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound. An idempotency_key of 1 to 128 characters is required, in the body or the Idempotency-Key header. Repeating the command with the same key returns the original job; the same key with a different command returns 409 idempotency_conflict. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `Idempotency-Key` | header | string |  | The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match. |

Request body (required, `application/json`): [`BloemPolicyBulkAction`](#bloempolicybulkaction).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. The selection token may also be invalid, or the command's template or cohort absent. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | idempotency_conflict: the idempotency_key was already used for a different command; policy_confirmation_stale: the preview changed or expired; selection_expired: the selection expired; authorization_state_changed: authorization changed since the preview. idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: idempotency_key is missing or longer than 128 characters, selection_token or confirmation_token is missing, or the command is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `GET /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}`

Read a bulk entitlement policy job.

Operation ID: `getBloemPlatformDirectPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `job_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. The job may also be absent from the organization, longer than 128 characters or not a policy job. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel`

Cancel an eligible bulk entitlement policy job.

Operation ID: `cancelBloemPlatformDirectPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound. Send an empty JSON object; missing JSON is invalid. A completed, failed or cancelled job is returned unchanged.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `job_id` | path | string | yes |  |

Request body (required, `application/json`): [`BloemJSONInputStruct {}Body`](#bloemjsoninputstruct-body).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. The job may also be absent from the organization or longer than 128 characters. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | job_not_cancellable: the job is not a policy job; authorization_state_changed: the actor's authority changed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/accounts/entitlement-bulk/policy-previews`

Create a selection and preview a bulk policy command.

Operation ID: `previewBloemPlatformDirectPolicy`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound.

Request body (required, `application/json`): [`BloemPlatformPolicyPreviewRequest`](#bloemplatformpolicypreviewrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPlatformPolicyPreviewResponse`](#bloemplatformpolicypreviewresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. Every account must be an active member, and the command's template or cohort must exist. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | authorization_state_changed: the actor's authority changed during the preview; selection_expired: the selection expired before the preview completed. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: account_ids is empty, has more than 10000 entries, or contains a non-positive or duplicate ID; or the command is invalid (fields.request). |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/accounts/entitlement-snapshots`

Read bounded account policy snapshots in one observation.

Operation ID: `readBloemDirectAccountPolicySnapshots`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Strict JSON body limit is 1 MiB; account_ids contains 1 to 10000 positive account IDs. Per-account failures use items[].error, not an HTTP failure.

Request body (required, `application/json`): [`BloemAccountPolicySnapshotsRequest`](#bloemaccountpolicysnapshotsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAccountPolicySnapshotsResponse`](#bloemaccountpolicysnapshotsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: account_ids is empty, has more than 10000 entries or contains a non-positive ID. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `GET /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement`

Read authoritative account and profile entitlement policies.

Operation ID: `getBloemDirectAccountPolicy`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AccountPolicySnapshot`](#accountpolicysnapshot) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: account_id is malformed or the account is not a member of the deployment default organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement/apply`

Apply a pinned entitlement template.

Operation ID: `applyBloemAccountEntitlement`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementAccountsKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper. The confirmation token is checked before the idempotency receipt, so a repeat after the token expires returns 422. Repeating an apply with the same idempotency_key returns the original result without applying again. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |
| `Idempotency-Key` | header | string |  | The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match. |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementApplyResponse`](#bloementitlementapplyresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the account ID is malformed, the account is not an active member of the deployment default organization, or the template revision does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | confirmation_stale: the preview no longer matches current state, dry-run again; revision_conflict: this idempotency_key was already used for a different template selection. idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: the template selection, confirmation_token or idempotency_key is missing or invalid, or the confirmation token is expired or bound to another target or actor. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/accounts/{account_id}/entitlement/dry-run`

Preview a pinned entitlement template.

Operation ID: `previewBloemAccountEntitlement`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementAccountsKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementDryRunResponse`](#bloementitlementdryrunresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the account ID is malformed, the account is not an active member of the deployment default organization, or the template revision does not exist. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: template_key or template_revision is missing. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `GET /api/bloem/v2/admin/platform/entitlement-templates`

List the entitlement template catalog.

Operation ID: `listBloemEntitlementTemplates`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementAccountsKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `include_archived` | query | string |  | Defaults to true. Parsed with Go strconv.ParseBool; an unparsable value is treated as false. |
| `status` | query | string |  | enabled (case-insensitive) returns only enabled, unarchived templates; other values are ignored. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementTemplatesResponse`](#bloementitlementtemplatesresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v2/admin/platform/entitlement-templates`

Create a versioned entitlement template.

Operation ID: `createBloemEntitlementTemplate`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Catalog creation requires a platform context even though catalog reads accept an API key; an API key reaches the handler and is refused with 403 insufficient_platform_authority.

Request body (required, `application/json`): [`BloemEntitlementTemplateCreate`](#bloementitlementtemplatecreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemEntitlementTemplateResponse`](#bloementitlementtemplateresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | template_conflict: the key or name already exists; protected_template: the key is reserved for a protected template. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: key or name is missing, or the policy is invalid (fields.policy). |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `GET /api/bloem/v2/admin/platform/entitlement-templates/{key}`

Read the latest or a selected entitlement template revision.

Operation ID: `getBloemEntitlementTemplate`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |
| `revision` | query | integer |  | Positive revision; omit for latest. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementTemplateResponse`](#bloementitlementtemplateresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the template or selected revision does not exist. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: revision is not a positive integer. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/archive`

Archive a template using its current revision.

Operation ID: `archiveBloemEntitlementTemplate`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |

Request body (required, `application/json`): [`BloemRevisionRequest`](#bloemrevisionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementTemplateResponse`](#bloementitlementtemplateresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the template does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | revision_conflict: expected_revision is not the current revision, or the template is already archived; protected_template: protected templates cannot be archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/clone`

Clone a template revision into a disabled new template.

Operation ID: `cloneBloemEntitlementTemplate`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |

Request body (required, `application/json`): [`BloemEntitlementTemplateClone`](#bloementitlementtemplateclone).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemEntitlementTemplateResponse`](#bloementitlementtemplateresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the source template revision does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | template_conflict: the new key or name already exists; protected_template: the new key is reserved for a protected template. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: key or name is missing. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `GET /api/bloem/v2/admin/platform/entitlement-templates/{key}/history`

List immutable template revisions (history alias).

Operation ID: `listBloemEntitlementTemplateHistory`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Revisions are ordered newest first.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementRevisionsResponse`](#bloementitlementrevisionsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the template does not exist. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `GET /api/bloem/v2/admin/platform/entitlement-templates/{key}/revisions`

List immutable template revisions.

Operation ID: `listBloemEntitlementTemplateRevisions`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Revisions are ordered newest first.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementRevisionsResponse`](#bloementitlementrevisionsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the template does not exist. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/entitlement-templates/{key}/revisions`

Create a new revision or copy a historical policy into a new revision.

Operation ID: `reviseBloemEntitlementTemplate`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `key` | path | string | yes |  |

Request body (required, `application/json`): [`BloemEntitlementTemplateRevise`](#bloementitlementtemplaterevise).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemEntitlementTemplateResponse`](#bloementitlementtemplateresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the template or source_revision does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | revision_conflict: expected_revision is not the template's current revision; protected_template: a protected template cannot be weakened; template_conflict: the name is already used. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: name, expected_revision or policy is missing or invalid. template_unavailable without fields: the template is archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/preview/account`

Ensure the issuer's shared preview account and primary profile.

Operation ID: `ensureBloemPreviewAccount`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused. Setup is atomic and idempotent. A reserved name collision or second issuer is refused. Password sign-in is disabled and the protected preview template is applied.

Request body (required, `application/json`): [`BloemPreviewIssuerInputBody`](#bloempreviewissuerinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPreviewAccountOutputBody`](#bloempreviewaccountoutputbody) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_preview_settings: issuer is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_setup_conflict: issuer differs from the configured issuer, the reserved preview username belongs to another account, or the existing shared account is no longer eligible. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v2/admin/platform/preview/accounts/{account_id}/presentation`

Set the personal preview end date for display.

Operation ID: `setBloemPreviewPersonalPresentation`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused. Only current personal preview accounts can be targeted. This metadata does not expire an account; the storefront owns expiration. A null ends_at clears the date.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemPreviewPersonalPresentationInputBody`](#bloempreviewpersonalpresentationinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_request is also returned for an account_id that is not a positive integer. invalid_preview_settings: issuer is missing or is not the configured issuer, no issuer is configured yet, the account is the shared preview account, absent or not a current personal preview, or ends_at is outside years 1970 to 9999. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v2/admin/platform/preview/capacity`

Read private guest capacity and account readiness.

Operation ID: `getBloemPreviewCapacity`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Capacity`](#capacity) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v2/admin/platform/preview/catalog`

Find metadata visible to the preview for a showcase picker.

Operation ID: `searchBloemPreviewCatalog`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. Keyword and provider-ID lookup apply the selected enabled libraries and maturity ceiling. Provider and provider_id must appear together. Results are movies, series, audiobooks and ebooks, at most 50. No external calls, file preparation or grants are involved.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `query` | query | string |  |  |
| `provider` | query | string: `tmdb`, `imdb`, `tvdb` |  |  |
| `provider_id` | query | string |  |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPreviewCatalogOutputBody`](#bloempreviewcatalogoutputbody) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_preview_settings: query over 256 bytes, provider_id over 128 bytes, an unsupported provider, or only one of provider and provider_id. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_not_configured: the shared preview account is missing or not ready. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v2/admin/platform/preview/keys`

Replace the Ed25519 launch-key allowlist.

Operation ID: `rotateBloemPreviewKeys`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused.

Request body (required, `application/json`): [`BloemPreviewKeysInputBody`](#bloempreviewkeysinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_preview_settings: issuer is missing or is not the configured issuer. It is also returned unless public_keys holds one or two distinct base64-encoded 32-byte keys. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_not_configured: ensure the shared preview account first. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v2/admin/platform/preview/libraries`

Revise the preview library selection and reapply it atomically.

Operation ID: `setBloemPreviewLibraries`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused.

Request body (required, `application/json`): [`BloemPreviewLibrariesInputBody`](#bloempreviewlibrariesinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPreviewLibrariesOutputBody`](#bloempreviewlibrariesoutputbody) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_preview_settings: issuer is missing or is not the configured issuer. It is also returned when library_ids is null. invalid_preview_libraries: more than 1000 IDs, or an ID that is not an existing enabled library. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_not_configured: ensure the shared preview account first. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `DELETE /api/bloem/v2/admin/platform/preview/sessions`

Revoke all guest sessions without disabling the preview.

Operation ID: `revokeBloemPreviewSessions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused. The issuer JSON body is required even for DELETE.

Request body (required, `application/json`): [`BloemPreviewIssuerInputBody`](#bloempreviewissuerinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_preview_settings: issuer is missing or is not the configured issuer. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v2/admin/platform/preview/settings`

Read preview settings and public launch keys.

Operation ID: `getBloemPreviewSettings`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPreviewSettingsOutputBody`](#bloempreviewsettingsoutputbody) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v2/admin/platform/preview/settings`

Merge explicitly supplied preview settings.

Operation ID: `updateBloemPreviewSettings`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused. Account ID and signing keys are read-only here. The existing issuer cannot change. Canonical and served hosts are refused; links require HTTPS without credentials. Turning enabled off revokes guests.

Request body (required, `application/json`): [`SettingsPatch`](#settingspatch).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. invalid_preview_settings: issuer is missing or is not the configured issuer. It is also returned for an invalid host, a link that is not an absolute HTTPS URL without credentials, a session lifetime outside 300 to 86400 seconds, an idle lifetime outside 60 to 7200 seconds or longer than the session, max_concurrent outside 1 to 10000, an unknown maturity_ceiling, overlong text, a host that already serves the application, or enabling without a host and launch key. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_not_configured: ensure the shared preview account first. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `POST /api/bloem/v2/admin/platform/preview/verify`

Verify a freshly signed launch ticket without consuming it.

Operation ID: `verifyBloemPreviewTicket`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused. Success always reports valid=true and account_ready=true; refusals use the error statuses.

Request body (required, `application/json`): [`BloemPreviewVerifyInputBody`](#bloempreviewverifyinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPreviewVerifyOutputBody`](#bloempreviewverifyoutputbody) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. preview_ticket_invalid: the ticket is malformed, unsigned by an allowlisted key, expired, or not for this issuer, server and preview host. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_not_configured: the shared preview account is missing or not ready (default-organization membership, preview template and library selection). |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v2/admin/platform/users/{user_id}/entitlement`

Read the legacy managed account entitlement projection.

Operation ID: `getBloemLegacyAccountEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Legacy alias projection; /accounts/{account_id}/entitlement returns the authoritative account and profile policy snapshot instead.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `user_id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLegacyAccountEntitlementRead`](#bloemlegacyaccountentitlementread) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: user_id is malformed, or the account is not an active member of the deployment default organization with an assigned access group. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/users/{user_id}/entitlement/apply`

Apply a pinned entitlement template.

Operation ID: `applyBloemLegacyAccountEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper. The confirmation token is checked before the idempotency receipt, so a repeat after the token expires returns 422. Repeating an apply with the same idempotency_key returns the original result without applying again. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `user_id` | path | integer (int64) | yes |  |
| `Idempotency-Key` | header | string |  | The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match. |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementApplyResponse`](#bloementitlementapplyresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the account ID is malformed, the account is not an active member of the deployment default organization, or the template revision does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | confirmation_stale: the preview no longer matches current state, dry-run again; revision_conflict: this idempotency_key was already used for a different template selection. idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: the template selection, confirmation_token or idempotency_key is missing or invalid, or the confirmation token is expired or bound to another target or actor. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/admin/platform/users/{user_id}/entitlement/dry-run`

Preview a pinned entitlement template.

Operation ID: `previewBloemLegacyAccountEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `user_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementDryRunResponse`](#bloementitlementdryrunresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the account ID is malformed, the account is not an active member of the deployment default organization, or the template revision does not exist. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: template_key or template_revision is missing. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v2/preview/events`

Increment a daily aggregate preview counter.

Operation ID: `recordBloemPreviewEvent`.
Credentials: `bearerAuth`.

Requires an authenticated preview session (a guest or a personal preview account); ordinary account sessions and API keys are refused. Accepts exactly one JSON object with one allowlisted event field, capped at 1 KiB. Counters retain only UTC day, event name and count; no identity or arbitrary payload. Per-process session limit: one per ten seconds, burst three. Aggregate limit: five per second, burst twenty. Guests also spend the preview's guest traffic budgets. Retries increment again. Responses are not cacheable.

Request body (required, `application/json`): [`BloemPreviewEventInputBody`](#bloemprevieweventinputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: the body is not exactly one JSON object with an allowlisted event, has unknown fields or exceeds 1 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or revoked, or a guest session is no longer valid because the preview was disabled or the request used another host. Preview host denials add code preview_session_invalid or preview_host. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden with code preview_only: the caller is not a preview principal or has no session; forbidden: another credential gate refused the request. |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) | rate_limited: this session's event budget (Retry-After 10) or the aggregate event budget is exhausted, or, with code preview_busy, the guest's request budget; the preview's shared guest traffic budget also returns rate_limited. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview counters are unavailable; service_unavailable: the credential or guest session could not be checked; unavailable: the preview host settings could not be read. |

### `POST /api/bloem/v2/preview/sessions`

Exchange a signed launch ticket for a bounded guest session.

Operation ID: `createBloemPreviewGuestSession`.
Credentials: none (public).

Public ticket exchange on the configured preview host only. Accepts a JSON body up to 16 KiB. The Ed25519 ticket must match the issuer, server identity audience and host and be unexpired and unused. Consumes its ticket ID atomically. An existing valid guest refresh token may reuse a session without extending its absolute lifetime. Responses are not cacheable. The per-client exchange budget is twenty per hour; guest capacity and per-client eviction remain server-controlled. No credential is needed; one that is sent is still checked against the preview host.

Request body (required, `application/json`): [`BloemPreviewSessionInputBody`](#bloempreviewsessioninputbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPreviewSession`](#bloempreviewsession) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: a launch ticket is required in a valid bounded JSON body. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: an account or API-key credential was sent to the preview host (code preview_host), or a guest credential to another host (code preview_session_invalid). |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_ticket_invalid: the ticket is invalid, expired or already consumed. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_unavailable: preview is disabled or the request used another host. |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) | rate_limited: too many starts for this client (twenty per hour), or the preview's shared guest traffic budget is exhausted. Retry-After gives the wait. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the preview session could not start. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | preview_busy: guest capacity is exhausted; unavailable: the preview host settings could not be read while a credential was sent. |

### `GET /api/bloem/v2/preview/status`

Discover preview availability and host-specific presentation.

Operation ID: `getBloemPreviewPublicStatus`.
Credentials: none (public).

Public. Successful responses are not cacheable. Returns enabled on all served hosts. Only the configured preview host receives brand and storefront, launch and subscription links, even when disabled. Never exposes capacity, keys or account identity. No credential is needed; one that is sent is still checked against the preview host.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPreviewPublicStatus`](#bloempreviewpublicstatus) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: an account or API-key credential was sent to the preview host (code preview_host), or a guest credential to another host (code preview_session_invalid). |
| 429 | [`BloemNativeError`](README.md#bloemnativeerror) | rate_limited: the preview's shared guest traffic budget (sixty requests per second, burst 120) is exhausted; retry after one second. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | unavailable: preview status, or the preview host settings while a credential was sent, could not be read. |

## Schemas

### AccountPolicySnapshot

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `cohort_id` | string |  |  |
| `cohort_revision` | integer (int64) |  |  |
| `group_id` | integer (int64) | yes |  |
| `observed_at` | string (date-time) | yes |  |
| `organization_id` | string | yes |  |
| `policy` | [`EffectivePolicySnapshot`](#effectivepolicysnapshot) | yes |  |
| `policy_revision` | integer (int64) | yes |  |
| `profiles` | array of [`ProfilePolicySnapshot`](#profilepolicysnapshot) | yes |  |
| `source_template_key` | string |  |  |
| `source_template_revision` | integer (int64) |  |  |
| `state` | string | yes |  |

### AccountPolicySnapshotResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `error` | string |  |  |
| `snapshot` | [`AccountPolicySnapshot`](#accountpolicysnapshot) |  |  |

### BloemAccountPolicySnapshotsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_ids` | array of integer (int64) | yes |  |

### BloemAccountPolicySnapshotsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`AccountPolicySnapshotResult`](#accountpolicysnapshotresult) | yes |  |
| `observed_at` | string (date-time) | yes |  |

### BloemEntitlementApplyRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `confirmation_token` | string |  |  |
| `dry_run_token` | string |  |  |
| `idempotency_key` | string |  |  |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |

### BloemEntitlementApplyResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) |  |  |
| `changed` | boolean | yes |  |
| `dry_run` | boolean | yes |  |
| `group_id` | integer (int64) |  |  |
| `organization_id` | string | yes |  |
| `policy` | [`BloemEntitlementPolicy`](#bloementitlementpolicy) | yes |  |
| `previous_template_key` | string |  |  |
| `previous_template_revision` | integer (int64) |  |  |
| `profiles_moved` | integer (int64) |  |  |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |

### BloemEntitlementCohort

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_group_id` | integer (int64) | yes |  |
| `archived` | boolean | yes |  |
| `cohort_id` | string | yes |  |
| `created_at` | string (date-time) | yes |  |
| `created_by_account_id` | integer (int64) |  |  |
| `derivation_kind` | string | yes |  |
| `member_count` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `organization_id` | string | yes |  |
| `parent_cohort_id` | string |  |  |
| `policy` | [`PolicyView`](#policyview) | yes |  |
| `policy_digest` | string | yes |  |
| `revision` | integer (int64) | yes |  |
| `source_template_key` | string |  |  |
| `source_template_revision` | integer (int64) |  |  |

### BloemEntitlementCohortResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cohort` | [`BloemEntitlementCohort`](#bloementitlementcohort) | yes |  |

### BloemEntitlementCohortsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cohorts` | array of [`BloemEntitlementCohort`](#bloementitlementcohort) | yes |  |

### BloemEntitlementDryRunChange

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `after` | [`BloemEntitlementDryRunChangeAfterStruct`](#bloementitlementdryrunchangeafterstruct) | yes |  |
| `before` | [`BloemEntitlementDryRunChangeBeforeStruct`](#bloementitlementdryrunchangebeforestruct) | yes |  |
| `field` | string | yes |  |

### BloemEntitlementDryRunChangeAfterStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |

### BloemEntitlementDryRunChangeBeforeStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |

### BloemEntitlementDryRunResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `changed` | boolean | yes |  |
| `changes` | array of [`BloemEntitlementDryRunChange`](#bloementitlementdryrunchange) | yes |  |
| `confirmation_token` | string | yes |  |
| `dry_run_token` | string | yes |  |
| `expires_at` | string (date-time) | yes |  |
| `result` | [`BloemEntitlementApplyResponse`](#bloementitlementapplyresponse) | yes |  |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |
| `warnings` | array of string | yes |  |

### BloemEntitlementManagedGroup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `policy` | [`BloemEntitlementPolicy`](#bloementitlementpolicy) | yes |  |

### BloemEntitlementPolicy

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `all_libraries` | boolean | yes |  |
| `allowed_permissions` | array of string or null | yes |  |
| `download_allowed` | boolean | yes |  |
| `download_transcode_allowed` | boolean | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `max_playback_quality` | string | yes |  |
| `max_profiles` | integer (int64) | yes |  |
| `max_streams` | integer (int64) | yes |  |
| `max_transcodes` | integer (int64) | yes |  |
| `playback_allowed` | boolean | yes |  |
| `requests_allowed` | boolean | yes |  |
| `transcode_allowed` | boolean | yes |  |

### BloemEntitlementPolicyInput

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `all_libraries` | boolean |  |  |
| `allowed_permissions` | array of string or null |  |  |
| `download_allowed` | boolean |  |  |
| `download_transcode_allowed` | boolean |  |  |
| `library_ids` | array of integer (int64) or null |  |  |
| `max_playback_quality` | string |  |  |
| `max_profiles` | integer (int64) |  |  |
| `max_streams` | integer (int64) |  |  |
| `max_transcodes` | integer (int64) |  |  |
| `playback_allowed` | boolean |  |  |
| `requests_allowed` | boolean |  |  |
| `transcode_allowed` | boolean |  |  |

### BloemEntitlementRevisionsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `revisions` | array of [`BloemEntitlementTemplate`](#bloementitlementtemplate) | yes |  |

### BloemEntitlementTemplate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `archived` | boolean | yes |  |
| `created_at` | string (date-time) | yes |  |
| `enabled` | boolean | yes |  |
| `key` | string | yes |  |
| `name` | string | yes |  |
| `policy` | [`BloemEntitlementPolicy`](#bloementitlementpolicy) | yes |  |
| `revision` | integer (int64) | yes |  |
| `status` | string | yes |  |

### BloemEntitlementTemplateClone

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `key` | string | yes |  |
| `name` | string | yes |  |
| `source_revision` | integer (int64) | yes |  |

### BloemEntitlementTemplateCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean |  |  |
| `key` | string | yes |  |
| `name` | string | yes |  |
| `policy` | [`BloemEntitlementPolicyInput`](#bloementitlementpolicyinput) |  |  |

### BloemEntitlementTemplateResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `template` | [`BloemEntitlementTemplate`](#bloementitlementtemplate) | yes |  |

### BloemEntitlementTemplateRevise

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `enabled` | boolean |  |  |
| `expected_revision` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `policy` | [`BloemEntitlementPolicyInput`](#bloementitlementpolicyinput) or null |  |  |
| `source_revision` | integer (int64) |  | If positive, copy this historical revision's policy into a new revision. |

### BloemEntitlementTemplatesResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `templates` | array of [`BloemEntitlementTemplate`](#bloementitlementtemplate) | yes |  |

### BloemJSONInputStruct {}Body

Type: object.

### BloemLegacyAccountEntitlementRead

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `last_reconciled_at` | string or null (date-time) | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `managed_default_group` | [`BloemEntitlementManagedGroup`](#bloementitlementmanagedgroup) or null | yes |  |
| `managed_group` | [`BloemEntitlementManagedGroup`](#bloementitlementmanagedgroup) |  |  |
| `organization_id` | string | yes |  |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |

### BloemNativeRevisionConflict

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `current_revision` | integer (int64) |  | Current authorization revision when supplied for authorization_state_changed. Its scope follows the operation (organization policy, membership or person security); absent for other conflicts. |
| `error` | string | yes |  |
| `message` | string | yes |  |

### BloemNativeValidationError

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string: `validation_failed` | yes |  |
| `fields` | object of string | yes | Validation messages keyed by request field. |
| `message` | string | yes |  |

### BloemPeopleJobResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `job` | [`BulkResult`](#bulkresult) | yes |  |

### BloemPlatformPolicyPreviewRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_ids` | array of integer (int64) | yes |  |
| `command` | [`BloemPolicyCommand`](#bloempolicycommand) | yes |  |

### BloemPlatformPolicyPreviewResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `preview` | [`PolicyPreview`](#policypreview) | yes |  |
| `selection` | [`Selection`](#selection) | yes |  |

### BloemPolicyBulkAction

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `command` | [`BloemPolicyCommand`](#bloempolicycommand) | yes |  |
| `confirmation_token` | string | yes |  |
| `idempotency_key` | string |  | Required unless supplied by the Idempotency-Key header; when both are present they must match. |
| `selection_token` | string | yes |  |

### BloemPolicyCommand

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cohort_id` | string |  |  |
| `include_custom_profiles` | boolean |  |  |
| `kind` | string | yes |  |
| `name` | string |  |  |
| `patch` | [`PolicyPatch`](#policypatch) |  |  |
| `template_key` | string |  |  |
| `template_revision` | integer (int64) |  |  |

### BloemPreviewAccountOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |

### BloemPreviewCatalogOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`CatalogItem`](#catalogitem) | yes |  |

### BloemPreviewEventInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `event` | string: `cta_shown`, `cta_clicked`, `session_ended` | yes |  |

### BloemPreviewIssuerInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `issuer` | string | yes |  |

### BloemPreviewKeysInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `issuer` | string | yes |  |
| `public_keys` | array of string | yes | Distinct base64-encoded 32-byte Ed25519 public keys. Replaces the current allowlist; two keys permit overlap during rotation. |

### BloemPreviewLibrariesInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `issuer` | string | yes |  |
| `library_ids` | array of integer (int64) | yes | An empty array grants no libraries. All IDs must exist and be enabled. |

### BloemPreviewLibrariesOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `template_revision` | integer (int64) | yes |  |

### BloemPreviewPersonalPresentationInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ends_at` | string or null (date-time) | yes |  |
| `issuer` | string | yes |  |

### BloemPreviewPublicStatus

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `brand` | string |  |  |
| `cta_label` | string |  |  |
| `cta_url` | string |  |  |
| `enabled` | boolean | yes |  |
| `launch_url` | string |  |  |
| `storefront_url` | string |  |  |

### BloemPreviewSession

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_token` | string | yes |  |
| `expires_at` | string (date-time) | yes |  |
| `expires_in` | integer (int64) | yes |  |
| `item` | string | yes | Initial catalog item verified from the signed launch ticket; empty when absent. |
| `ref` | string | yes | Referral verified from the signed launch ticket; empty when absent. |
| `refresh_token` | string | yes |  |
| `token_type` | string: `Bearer` | yes |  |

### BloemPreviewSessionInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `refresh_token` | string |  | The current guest refresh token, if any. A valid token may reuse its bounded guest session. |
| `ticket` | string | yes |  |

### BloemPreviewSettingsOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `brand` | string | yes |  |
| `cta_label` | string | yes |  |
| `cta_message` | string | yes |  |
| `cta_url` | string | yes |  |
| `enabled` | boolean | yes |  |
| `free_play_enabled` | boolean | yes |  |
| `free_play_label` | string | yes |  |
| `free_play_max_active` | integer (int64) | yes |  |
| `free_play_seconds` | integer (int64) | yes |  |
| `free_play_supported` | boolean | yes | True on servers that honor and meter free-play allowances. |
| `free_play_url` | string | yes |  |
| `host` | string | yes |  |
| `idle_ttl_seconds` | integer (int64) | yes |  |
| `issuer` | string | yes |  |
| `item_cta_label` | string | yes |  |
| `launch_url` | string | yes |  |
| `maturity_ceiling` | string | yes |  |
| `max_concurrent` | integer (int64) | yes |  |
| `session_ttl_seconds` | integer (int64) | yes |  |
| `storefront_url` | string | yes |  |
| `subscribe_label` | string | yes |  |
| `subscribe_url` | string | yes |  |
| `ticket_public_keys` | array of string | yes |  |

### BloemPreviewVerifyInputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `ticket` | string | yes |  |

### BloemPreviewVerifyOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_ready` | boolean | yes |  |
| `expires_at` | string (date-time) | yes |  |
| `item` | string | yes |  |
| `valid` | boolean | yes |  |

### BloemRevisionRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes |  |

### BulkResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `failed` | array of [`RecordResult`](#recordresult) | yes |  |
| `job_id` | string | yes |  |
| `progress_current` | integer (int64) | yes |  |
| `progress_total` | integer (int64) | yes |  |
| `skipped` | array of [`RecordResult`](#recordresult) | yes |  |
| `status` | string | yes |  |
| `succeeded` | integer (int64) | yes |  |
| `target_cohort_id` | string |  |  |
| `target_cohort_revision` | integer (int64) |  |  |
| `target_group_id` | integer (int64) |  |  |

### Capacity

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_ready` | boolean | yes |  |
| `active` | integer (int64) | yes |  |
| `enabled` | boolean | yes |  |
| `maximum` | integer (int64) | yes |  |

### CatalogItem

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `content_id` | string | yes |  |
| `kind` | string | yes |  |
| `overview` | string | yes |  |
| `title` | string | yes |  |
| `year` | integer (int64) | yes |  |

### CohortDistribution

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cohort_id` | string |  |  |
| `cohort_revision` | integer (int64) |  |  |
| `count` | integer (int64) | yes |  |
| `group_id` | integer (int64) |  |  |
| `source_template_key` | string |  |  |
| `source_template_revision` | integer (int64) |  |  |
| `state` | string | yes |  |

### EffectivePolicySnapshot

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_permissions` | array of string or null | yes |  |
| `audio_transcode_allowed` | boolean | yes |  |
| `download_allowed` | boolean | yes |  |
| `download_transcode_allowed` | boolean | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `max_playback_quality` | string | yes |  |
| `max_profiles` | integer (int64) | yes |  |
| `max_streams` | integer (int64) | yes |  |
| `max_transcodes` | integer (int64) | yes |  |
| `playback_allowed` | boolean | yes |  |
| `requests_allowed` | boolean | yes |  |
| `transcode_allowed` | boolean | yes |  |

### PolicyFieldDiff

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `changed_accounts` | integer (int64) | yes |  |
| `field` | string | yes |  |

### PolicyPatch

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `download_allowed` | boolean |  |  |
| `download_transcode_allowed` | boolean |  |  |
| `libraries` | [`SetOperationInt`](#setoperationint) |  |  |
| `max_playback_quality` | string |  |  |
| `max_profiles` | integer (int64) |  |  |
| `max_streams` | integer (int64) |  |  |
| `max_transcodes` | integer (int64) |  |  |
| `permissions` | [`SetOperationString`](#setoperationstring) |  |  |
| `playback_allowed` | boolean |  |  |
| `requests_allowed` | boolean |  |  |
| `transcode_allowed` | boolean |  |  |

### PolicyPreview

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `already_compliant` | integer (int64) | yes |  |
| `confirmation_expires_at` | string (date-time) | yes |  |
| `confirmation_token` | string | yes |  |
| `current_cohorts` | array of [`CohortDistribution`](#cohortdistribution) | yes |  |
| `custom_profiles_will_move` | integer (int64) | yes |  |
| `custom_profiles_will_remain` | integer (int64) | yes |  |
| `diff` | array of [`PolicyFieldDiff`](#policyfielddiff) | yes |  |
| `excluded` | integer (int64) | yes |  |
| `ineligible_or_stale` | integer (int64) | yes |  |
| `inherited_profiles_will_move` | integer (int64) | yes |  |
| `matched` | integer (int64) | yes |  |
| `selection_expires_at` | string (date-time) | yes |  |
| `target` | [`PolicyTarget`](#policytarget) | yes |  |

### PolicyTarget

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `cohort_id` | string |  |  |
| `cohort_revision` | integer (int64) |  |  |
| `group_id` | integer (int64) |  |  |
| `kind` | string | yes |  |
| `name` | string |  |  |
| `parent_cohort_id` | string |  |  |
| `policy` | [`PolicyView`](#policyview) | yes |  |
| `policy_digest` | string | yes |  |
| `template_key` | string |  |  |
| `template_revision` | integer (int64) |  |  |

### PolicyView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_permissions` | array of string or null | yes |  |
| `download_allowed` | boolean | yes |  |
| `download_transcode_allowed` | boolean | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `max_playback_quality` | string | yes |  |
| `max_profiles` | integer (int64) | yes |  |
| `max_streams` | integer (int64) | yes |  |
| `max_transcodes` | integer (int64) | yes |  |
| `playback_allowed` | boolean | yes |  |
| `requests_allowed` | boolean | yes |  |
| `transcode_allowed` | boolean | yes |  |

### ProfilePolicySnapshot

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group_id` | integer (int64) | yes |  |
| `inherits_account` | boolean | yes |  |
| `policy` | [`EffectivePolicySnapshot`](#effectivepolicysnapshot) | yes |  |
| `profile_id` | string | yes |  |
| `profile_name` | string | yes |  |
| `state` | string | yes |  |

### RecordResult

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `reason` | string | yes |  |

### Selection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `excluded` | integer (int64) | yes |  |
| `expires_at` | string (date-time) | yes |  |
| `matched` | integer (int64) | yes |  |
| `token` | string | yes |  |

### SetOperationInt

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `mode` | string | yes |  |
| `values` | array of integer (int64) |  |  |

### SetOperationString

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `mode` | string | yes |  |
| `values` | array of string |  |  |

### SettingsPatch

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `brand` | string |  |  |
| `cta_label` | string |  |  |
| `cta_message` | string |  | The subscribe dialog's sentence; empty keeps the default. |
| `cta_url` | string |  |  |
| `enabled` | boolean |  |  |
| `free_play_enabled` | boolean |  | Lets a guest whose launch ticket carries a signed allowance stream video until it is used. |
| `free_play_label` | string |  | The free-play button's wording; empty keeps the client's (Play free for N minutes). |
| `free_play_max_active` | integer (int64) |  | Allowances that may stream at once; a further start is refused as busy. |
| `free_play_seconds` | integer (int64) |  | The allowance the storefront offers, for the button's wording; the ticket carries the real one. |
| `free_play_url` | string |  | The storefront page where a guest asks for free play; empty shows no free-play button. |
| `host` | string |  |  |
| `idle_ttl_seconds` | integer (int64) |  |  |
| `issuer` | string | yes |  |
| `item_cta_label` | string |  | Replaces Subscribe to watch, read or listen on title pages; empty keeps that wording. |
| `launch_url` | string |  |  |
| `maturity_ceiling` | string |  |  |
| `max_concurrent` | integer (int64) |  |  |
| `session_ttl_seconds` | integer (int64) |  |  |
| `storefront_url` | string |  |  |
| `subscribe_label` | string |  | The banner's subscription button beside the trial; empty keeps the client's wording. |
| `subscribe_url` | string |  | Where the banner's subscription button goes; empty keeps the plain exit to the storefront. |
