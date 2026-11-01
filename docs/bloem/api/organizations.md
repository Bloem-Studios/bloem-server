# Organizations and administration API

> **Generated** by `scripts/bloem/api-topic-docs.py` from
> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).
> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then
> the script. Shared conventions, credentials and the error body are on the [index](README.md).

Administrative contexts, organization administration (people, groups, invitations, libraries, grants, audit and policy decisions), the platform's organization lifecycle and reports, the storefront's reseller organizations (tenants) and their members, and an account's profiles, devices and sign-in sessions.

For the plain-language picture, read the [Organizations and administration explainer](../../architecture/bloem-organizations.md). The invariants are in:

- [Multitenant administration](../../architecture/multitenant-administration.md)
- [Resource tenancy foundation](../../architecture/resource-tenancy-foundation.md)
- [OPA tenant authorization](../../architecture/opa-tenant-authorization.md)
- [Lifecycle safety](../../architecture/bloem-lifecycle-safety.md)

## Operations (88)

| Method and path | Summary |
| --- | --- |
| [`GET /api/bloem/v1/admin/organization/activity`](#get-apibloemv1adminorganizationactivity) | Page the selected organization's sanitized activity and entitlement audit. |
| [`PUT /api/bloem/v1/admin/organization/entitlements/{folder_id}`](#put-apibloemv1adminorganizationentitlementsfolder_id) | Set an organization library entitlement status. |
| [`DELETE /api/bloem/v1/admin/organization/entitlements/{folder_id}`](#delete-apibloemv1adminorganizationentitlementsfolder_id) | Remove an organization library entitlement. |
| [`GET /api/bloem/v1/admin/organization/groups/`](#get-apibloemv1adminorganizationgroups) | List access groups in the selected organization. |
| [`POST /api/bloem/v1/admin/organization/groups/`](#post-apibloemv1adminorganizationgroups) | Create an organization access group. |
| [`GET /api/bloem/v1/admin/organization/groups/{id}`](#get-apibloemv1adminorganizationgroupsid) | Read one organization access group. |
| [`PUT /api/bloem/v1/admin/organization/groups/{id}`](#put-apibloemv1adminorganizationgroupsid) | Update an organization access group. |
| [`DELETE /api/bloem/v1/admin/organization/groups/{id}`](#delete-apibloemv1adminorganizationgroupsid) | Delete an organization access group and report its impact. |
| [`GET /api/bloem/v1/admin/organization/invitations`](#get-apibloemv1adminorganizationinvitations) | List invitation metadata without claim tokens. |
| [`POST /api/bloem/v1/admin/organization/invitations`](#post-apibloemv1adminorganizationinvitations) | Create a user invitation and disclose its claim token once. |
| [`DELETE /api/bloem/v1/admin/organization/invitations/{id}`](#delete-apibloemv1adminorganizationinvitationsid) | Revoke an unaccepted, unrevoked user invitation in the selected organization. |
| [`POST /api/bloem/v1/admin/organization/invitations/{id}/resend`](#post-apibloemv1adminorganizationinvitationsidresend) | Rotate an unaccepted, unrevoked user invitation and return its new claim token once. |
| [`GET /api/bloem/v1/admin/organization/libraries`](#get-apibloemv1adminorganizationlibraries) | List the organization library entitlement projection. |
| [`GET /api/bloem/v1/admin/organization/overview`](#get-apibloemv1adminorganizationoverview) | Read the selected organization overview. |
| [`GET /api/bloem/v1/admin/organization/people/`](#get-apibloemv1adminorganizationpeople) | Page organization people and their profiles. |
| [`POST /api/bloem/v1/admin/organization/people/bulk-jobs`](#post-apibloemv1adminorganizationpeoplebulk-jobs) | Enqueue a people bulk mutation. |
| [`GET /api/bloem/v1/admin/organization/people/bulk-jobs/{job_id}`](#get-apibloemv1adminorganizationpeoplebulk-jobsjob_id) | Read progress and per-record results of a people job. |
| [`POST /api/bloem/v1/admin/organization/people/policy-jobs`](#post-apibloemv1adminorganizationpeoplepolicy-jobs) | Enqueue a confirmed people policy command. |
| [`GET /api/bloem/v1/admin/organization/people/policy-jobs/{job_id}`](#get-apibloemv1adminorganizationpeoplepolicy-jobsjob_id) | Read progress and per-record results of a people job. |
| [`POST /api/bloem/v1/admin/organization/people/policy-jobs/{job_id}/cancel`](#post-apibloemv1adminorganizationpeoplepolicy-jobsjob_idcancel) | Cancel an eligible people policy job. |
| [`POST /api/bloem/v1/admin/organization/people/policy-previews`](#post-apibloemv1adminorganizationpeoplepolicy-previews) | Preview a policy command and obtain a bound confirmation token. |
| [`POST /api/bloem/v1/admin/organization/people/selections`](#post-apibloemv1adminorganizationpeopleselections) | Create an immutable expiring selection from people filters. |
| [`GET /api/bloem/v1/admin/organization/people/{account_id}/`](#get-apibloemv1adminorganizationpeopleaccount_id) | Read an organization person and profile summary. |
| [`PATCH /api/bloem/v1/admin/organization/people/{account_id}/memberships/current`](#patch-apibloemv1adminorganizationpeopleaccount_idmembershipscurrent) | Change an organization person membership status. |
| [`PATCH /api/bloem/v1/admin/organization/people/{account_id}/profiles/{profile_id}`](#patch-apibloemv1adminorganizationpeopleaccount_idprofilesprofile_id) | Assign an organization person profile to an access group. |
| [`GET /api/bloem/v1/admin/organization/policy-decisions`](#get-apibloemv1adminorganizationpolicy-decisions) | Page sanitized organization policy decision explanations. |
| [`GET /api/bloem/v1/admin/organization/policy-decisions/{id}`](#get-apibloemv1adminorganizationpolicy-decisionsid) | Read a sanitized organization policy decision explanation. |
| [`POST /api/bloem/v1/admin/platform/accounts`](#post-apibloemv1adminplatformaccounts) | Create an account, optionally on a plan. |
| [`PUT /api/bloem/v1/admin/platform/accounts/{account_id}`](#put-apibloemv1adminplatformaccountsaccount_id) | Update an account, optionally moving it to a plan. |
| [`GET /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions`](#get-apibloemv1adminplatformaccountsaccount_idauth-sessions) | List an account's sign-in sessions. |
| [`DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions`](#delete-apibloemv1adminplatformaccountsaccount_idauth-sessions) | End every sign-in session of an account. |
| [`DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions/{session_id}`](#delete-apibloemv1adminplatformaccountsaccount_idauth-sessionssession_id) | End one of an account's sign-in sessions. |
| [`GET /api/bloem/v1/admin/platform/accounts/{account_id}/devices`](#get-apibloemv1adminplatformaccountsaccount_iddevices) | List an account's devices. |
| [`DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/devices/{device_id}`](#delete-apibloemv1adminplatformaccountsaccount_iddevicesdevice_id) | Remove an account's device. |
| [`GET /api/bloem/v1/admin/platform/accounts/{account_id}/profiles`](#get-apibloemv1adminplatformaccountsaccount_idprofiles) | List an account's profiles. |
| [`POST /api/bloem/v1/admin/platform/accounts/{account_id}/profiles`](#post-apibloemv1adminplatformaccountsaccount_idprofiles) | Create a profile for an account. |
| [`PUT /api/bloem/v1/admin/platform/accounts/{account_id}/profiles/{profile_id}`](#put-apibloemv1adminplatformaccountsaccount_idprofilesprofile_id) | Change an account's profile. |
| [`DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/profiles/{profile_id}`](#delete-apibloemv1adminplatformaccountsaccount_idprofilesprofile_id) | Delete an account's profile. |
| [`GET /api/bloem/v1/admin/platform/library-figures`](#get-apibloemv1adminplatformlibrary-figures) | Read item counts for every library. |
| [`GET /api/bloem/v1/admin/platform/new-arrivals`](#get-apibloemv1adminplatformnew-arrivals) | List recent additions to libraries. |
| [`GET /api/bloem/v1/admin/platform/organizations/`](#get-apibloemv1adminplatformorganizations) | Page organizations with current summary counts. |
| [`POST /api/bloem/v1/admin/platform/organizations/`](#post-apibloemv1adminplatformorganizations) | Create an organization for an existing owner account. |
| [`GET /api/bloem/v1/admin/platform/organizations/{id}/`](#get-apibloemv1adminplatformorganizationsid) | Read an organization summary. |
| [`PATCH /api/bloem/v1/admin/platform/organizations/{id}/`](#patch-apibloemv1adminplatformorganizationsid) | Change organization name or slug. |
| [`GET /api/bloem/v1/admin/platform/organizations/{id}/entitlement`](#get-apibloemv1adminplatformorganizationsidentitlement) | Read the managed organization entitlement. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/entitlement/apply`](#post-apibloemv1adminplatformorganizationsidentitlementapply) | Apply a pinned entitlement template. |
| [`GET /api/bloem/v1/admin/platform/organizations/{id}/entitlement/audit`](#get-apibloemv1adminplatformorganizationsidentitlementaudit) | Read organization entitlement audit history. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/entitlement/dry-run`](#post-apibloemv1adminplatformorganizationsidentitlementdry-run) | Preview a pinned entitlement template. |
| [`GET /api/bloem/v1/admin/platform/organizations/{id}/memberships`](#get-apibloemv1adminplatformorganizationsidmemberships) | Page an organization membership directory. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/memberships`](#post-apibloemv1adminplatformorganizationsidmemberships) | Create an organization membership for an existing account. |
| [`PATCH /api/bloem/v1/admin/platform/organizations/{id}/memberships/{membership_id}`](#patch-apibloemv1adminplatformorganizationsidmembershipsmembership_id) | Change a membership role or status. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/reactivate`](#post-apibloemv1adminplatformorganizationsidreactivate) | Reactivate an organization. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/suspend`](#post-apibloemv1adminplatformorganizationsidsuspend) | Suspend an organization. |
| [`POST /api/bloem/v1/admin/platform/organizations/{id}/transfer-ownership`](#post-apibloemv1adminplatformorganizationsidtransfer-ownership) | Transfer ownership after explicit confirmation and reauthentication. |
| [`GET /api/bloem/v1/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement`](#get-apibloemv1adminplatformorganizationsorganization_idaccountsaccount_identitlement) | Read authoritative account and profile entitlement policies. |
| [`POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs`](#post-apibloemv1adminplatformorganizationsorganization_identitlement-bulkpolicy-jobs) | Enqueue a confirmed bulk entitlement policy job. |
| [`GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}`](#get-apibloemv1adminplatformorganizationsorganization_identitlement-bulkpolicy-jobsjob_id) | Read a bulk entitlement policy job. |
| [`POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel`](#post-apibloemv1adminplatformorganizationsorganization_identitlement-bulkpolicy-jobsjob_idcancel) | Cancel an eligible bulk entitlement policy job. |
| [`POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews`](#post-apibloemv1adminplatformorganizationsorganization_identitlement-bulkpolicy-previews) | Create a selection and preview a bulk policy command. |
| [`GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-cohorts`](#get-apibloemv1adminplatformorganizationsorganization_identitlement-cohorts) | List organization entitlement cohorts. |
| [`GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}`](#get-apibloemv1adminplatformorganizationsorganization_identitlement-cohortscohort_id) | Read one organization entitlement cohort. |
| [`POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-snapshots`](#post-apibloemv1adminplatformorganizationsorganization_identitlement-snapshots) | Read bounded account policy snapshots in one observation. |
| [`GET /api/bloem/v1/admin/platform/tenants`](#get-apibloemv1adminplatformtenants) | List reseller organizations with live usage. |
| [`POST /api/bloem/v1/admin/platform/tenants`](#post-apibloemv1adminplatformtenants) | Create a reseller organization. |
| [`GET /api/bloem/v1/admin/platform/tenants/{id}`](#get-apibloemv1adminplatformtenantsid) | Read one reseller organization. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{id}`](#delete-apibloemv1adminplatformtenantsid) | Retire a reseller organization. |
| [`POST /api/bloem/v1/admin/platform/tenants/{id}/freeze`](#post-apibloemv1adminplatformtenantsidfreeze) | Suspend a reseller organization. |
| [`PATCH /api/bloem/v1/admin/platform/tenants/{id}/limits`](#patch-apibloemv1adminplatformtenantsidlimits) | Change seats and shared conversions. |
| [`POST /api/bloem/v1/admin/platform/tenants/{id}/thaw`](#post-apibloemv1adminplatformtenantsidthaw) | Lift an administrative suspension. |
| [`GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members`](#get-apibloemv1adminplatformtenantstenant_idmembers) | List a reseller organization's members. |
| [`POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members`](#post-apibloemv1adminplatformtenantstenant_idmembers) | Add a member account to a reseller organization. |
| [`GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`](#get-apibloemv1adminplatformtenantstenant_idmembersuser_id) | Read one member. |
| [`PUT /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`](#put-apibloemv1adminplatformtenantstenant_idmembersuser_id) | Change a member's username or email. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`](#delete-apibloemv1adminplatformtenantstenant_idmembersuser_id) | Remove a member. |
| [`GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions`](#get-apibloemv1adminplatformtenantstenant_idmembersuser_idauth-sessions) | List a member's sign-in sessions. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions`](#delete-apibloemv1adminplatformtenantstenant_idmembersuser_idauth-sessions) | End every sign-in session of a member. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}`](#delete-apibloemv1adminplatformtenantstenant_idmembersuser_idauth-sessionssession_id) | End one of a member's sign-in sessions. |
| [`GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/devices`](#get-apibloemv1adminplatformtenantstenant_idmembersuser_iddevices) | List a member's devices. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/devices/{device_id}`](#delete-apibloemv1adminplatformtenantstenant_idmembersuser_iddevicesdevice_id) | Remove a member's device. |
| [`GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles`](#get-apibloemv1adminplatformtenantstenant_idmembersuser_idprofiles) | List a member's profiles. |
| [`POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles`](#post-apibloemv1adminplatformtenantstenant_idmembersuser_idprofiles) | Create a profile for a member. |
| [`PUT /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}`](#put-apibloemv1adminplatformtenantstenant_idmembersuser_idprofilesprofile_id) | Change a member's profile. |
| [`DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}`](#delete-apibloemv1adminplatformtenantstenant_idmembersuser_idprofilesprofile_id) | Delete a member's profile. |
| [`POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/reset-password`](#post-apibloemv1adminplatformtenantstenant_idmembersuser_idreset-password) | Set a member's password. |
| [`POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/resume`](#post-apibloemv1adminplatformtenantstenant_idmembersuser_idresume) | Resume a suspended member's account. |
| [`POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/suspend`](#post-apibloemv1adminplatformtenantstenant_idmembersuser_idsuspend) | Suspend a member's account. |
| [`POST /api/bloem/v1/admin/session`](#post-apibloemv1adminsession) | Exchange a login session for a scoped administrative context. |
| [`GET /api/bloem/v1/organizations`](#get-apibloemv1organizations) | The organizations this account belongs to. |

## Reference

### `GET /api/bloem/v1/admin/organization/activity`

Page the selected organization's sanitized activity and entitlement audit.

Operation ID: `listBloemOrganizationActivity`.
Credentials: `bloemOrganizationContext`.

Requires an organization-scoped administrative-context token matching the resolved account, organization and membership. The originating session, current authority and policy/security revisions are revalidated on each request. No organization path or header can broaden this scope. Returns at most 50 events ordered newest first by created_at, source and id. next_cursor is omitted on the last page. Events exclude before/after documents and credential data.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. Omit for the newest events; invalid cursors return 400 invalid_cursor. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`OrganizationAuditPage`](#organizationauditpage) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_cursor: the cursor is malformed or longer than 1024 characters; reload the audit list to reset pagination. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PUT /api/bloem/v1/admin/organization/entitlements/{folder_id}`

Set an organization library entitlement status.

Operation ID: `updateBloemOrganizationLibraryEntitlement`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. expected_revision is the entitlement security_revision returned by the libraries projection. On conflict, current_revision contains the organization policy revision; reload the libraries projection for the entitlement revision.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `folder_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemOrganizationEntitlementUpdate`](#bloemorganizationentitlementupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationEntitlementResponse`](#bloemorganizationentitlementresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: folder_id is not a positive integer, or the library entitlement is absent or hidden from the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the entitlement's security_revision; current_revision carries the organization policy revision. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.request): expected_revision is not positive or status is not active or suspended. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `DELETE /api/bloem/v1/admin/organization/entitlements/{folder_id}`

Remove an organization library entitlement.

Operation ID: `deleteBloemOrganizationLibraryEntitlement`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. expected_revision is the entitlement security_revision returned by the libraries projection. On conflict, current_revision contains the organization policy revision; reload the libraries projection for the entitlement revision.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `folder_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemRevisionRequest`](#bloemrevisionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: folder_id is not a positive integer, or the library entitlement is absent or hidden from the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the entitlement's security_revision; current_revision carries the organization policy revision. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/groups/`

List access groups in the selected organization.

Operation ID: `listBloemOrganizationGroups`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationGroupsResponse`](#bloemorganizationgroupsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/groups/`

Create an organization access group.

Operation ID: `createBloemOrganizationGroup`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

Request body (required, `application/json`): [`BloemOrganizationGroupCreate`](#bloemorganizationgroupcreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemOrganizationGroupMutationResponse`](#bloemorganizationgroupmutationresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or a body exceeding 32 KiB. bad_request: an invalid name, quality, library ID, permission or stream/transcode cap. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. conflict: the group name already exists, or is_default would replace a default group managed by entitlement policy. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/groups/{id}`

Read one organization access group.

Operation ID: `getBloemOrganizationGroup`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationGroupResponse`](#bloemorganizationgroupresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the group ID is not a positive integer or the group is absent from the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PUT /api/bloem/v1/admin/organization/groups/{id}`

Update an organization access group.

Operation ID: `updateBloemOrganizationGroup`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemOrganizationGroupUpdate`](#bloemorganizationgroupupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationGroupMutationResponse`](#bloemorganizationgroupmutationresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or a body exceeding 32 KiB. bad_request: an invalid name, quality, library ID, permission or stream/transcode cap. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the group ID is not a positive integer or the group is absent from the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. conflict: the group name already exists, is_default=false was sent for the current default group, or the group, or a default group it would replace, is managed by entitlement policy. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `DELETE /api/bloem/v1/admin/organization/groups/{id}`

Delete an organization access group and report its impact.

Operation ID: `deleteBloemOrganizationGroup`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemRevisionRequest`](#bloemrevisionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`GroupDeletionImpact`](#groupdeletionimpact) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the group ID is not a positive integer or the group is absent from the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. conflict: the group is the default group, no replacement default group exists, or the group is managed by entitlement policy. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/invitations`

List invitation metadata without claim tokens.

Operation ID: `listBloemOrganizationInvitations`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. Invitations are ordered newest first.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationInvitationsResponse`](#bloemorganizationinvitationsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/invitations`

Create a user invitation and disclose its claim token once.

Operation ID: `createBloemOrganizationInvitation`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Creates role=user only; default expiry is seven days. A pending invitation for the same address in this organization is revoked. Returns claim_token once; this operation does not send email or return a claim URL. Do not automatically retry creation.

Request body (required, `application/json`): [`BloemOrganizationInvitationCreate`](#bloemorganizationinvitationcreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemOrganizationInvitationResendResponse`](#bloemorganizationinvitationresendresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. email_taken: the email already belongs to an account. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive, email is not a bare email address, access_group_id is not one of this organization's groups, or library_ids lists a library the organization neither owns nor is entitled to. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `DELETE /api/bloem/v1/admin/organization/invitations/{id}`

Revoke an unaccepted, unrevoked user invitation in the selected organization.

Operation ID: `revokeBloemOrganizationInvitation`.
Credentials: `bloemOrganizationContext`.

Requires an organization-scoped administrative-context token matching the resolved account, organization and membership. The originating session, current authority and policy/security revisions are revalidated on each request. No organization path or header can broaden this scope. A JSON body containing expected_revision is required, including on DELETE. It must match the resolved organization's policy revision. Only role=user invitations that have not been accepted or revoked can be revoked here. Ineligible or already revoked invitations return 404. Success has no response body.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes | Invitation in the selected organization. Invalid, missing or hidden IDs return 404. |

Request body (required, `application/json`): [`BloemOrganizationInvitationRevision`](#bloemorganizationinvitationrevision).

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the invitation ID is not a positive integer, or the invitation is absent, hidden by organization scope, accepted, already revoked or not role=user. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/invitations/{id}/resend`

Rotate an unaccepted, unrevoked user invitation and return its new claim token once.

Operation ID: `resendBloemOrganizationInvitation`.
Credentials: `bloemOrganizationContext`.

Requires an organization-scoped administrative-context token matching the resolved account, organization and membership. The originating session, current authority and policy/security revisions are revalidated on each request. No organization path or header can broaden this scope. A JSON body containing expected_revision is required, including on DELETE. It must match the resolved organization's policy revision. Only role=user invitations that have not been accepted or revoked can be renewed; expired invitations remain eligible. Rotation invalidates the previous token and renews expiry for seven days. This endpoint returns claim_token, not claim_url or email_sent, and does not send email. The token is disclosed once and only its hash is stored. Do not automatically retry: a second successful call rotates the link again.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes | Invitation in the selected organization. Invalid, missing or hidden IDs return 404. |

Request body (required, `application/json`): [`BloemOrganizationInvitationRevision`](#bloemorganizationinvitationrevision).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemOrganizationInvitationResendResponse`](#bloemorganizationinvitationresendresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the invitation ID is not a positive integer, or the invitation is absent or hidden by organization scope. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. invitation_not_claimable: only unaccepted, unrevoked user invitations can be renewed; reload before trying again. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. Also returned when the invitation's email now belongs to an account. |

### `GET /api/bloem/v1/admin/organization/libraries`

List the organization library entitlement projection.

Operation ID: `listBloemOrganizationLibraries`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationLibrariesResponse`](#bloemorganizationlibrariesresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/overview`

Read the selected organization overview.

Operation ID: `getBloemOrganizationOverview`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationSummaryResponse`](#bloemorganizationsummaryresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected organization no longer exists. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/people/`

Page organization people and their profiles.

Operation ID: `listBloemOrganizationPeople`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `query` | query | string |  | Case-insensitive substring of the account email, username or a profile name. |
| `status` | query | string |  | Membership status filter: invited, active or suspended. Repeat the parameter or separate values with commas. |
| `group_id` | query | string |  | Access group filter of positive IDs. Repeat the parameter or separate values with commas. |
| `active_since` | query | string |  | RFC3339 timestamp; matches people whose membership or a profile changed at or after it. |
| `sort` | query | string: `name`, `email`, `recent_activity` |  | name (default), email or recent_activity. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. |
| `limit` | query | integer |  | Page size from 1 to 200; defaults to 50. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`Page`](#page) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: limit, group_id or active_since is malformed, a status or sort value is unknown, or cursor is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/people/bulk-jobs`

Enqueue a people bulk mutation.

Operation ID: `createBloemOrganizationPeopleBulkJob`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. kind is assign_group (with a positive group_id), suspend_memberships or reactivate_memberships (without group_id). Repeating the same kind and group for a selection returns the existing job.

Request body (required, `application/json`): [`BulkAction`](#bulkaction).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selection token is invalid or belongs to another organization. group_id may also not be a group in the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | selection_expired: create a new selection; authorization_state_changed: the actor's authority changed since the selection. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.request): kind or group_id is invalid for the action, or selection_token is missing. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/people/bulk-jobs/{job_id}`

Read progress and per-record results of a people job.

Operation ID: `getBloemOrganizationPeopleBulkJob`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `job_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the job is absent from the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/people/policy-jobs`

Enqueue a confirmed people policy command.

Operation ID: `createBloemOrganizationPeoplePolicyJob`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. An idempotency_key of 1 to 128 characters is required, in the body or the Idempotency-Key header. Repeating the command with the same key returns the original job; the same key with a different command returns 409 idempotency_conflict. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `Idempotency-Key` | header | string |  | The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match. |

Request body (required, `application/json`): [`BloemPolicyBulkAction`](#bloempolicybulkaction).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selection token is invalid or belongs to another organization. The command's template or cohort may also not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | idempotency_conflict: the idempotency_key was already used for a different command; policy_confirmation_stale: the preview changed or expired; selection_expired: the selection expired; authorization_state_changed: authorization changed since the preview. idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: idempotency_key is missing or longer than 128 characters, selection_token or confirmation_token is missing, or the command is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/people/policy-jobs/{job_id}`

Read progress and per-record results of a people job.

Operation ID: `getBloemOrganizationPeoplePolicyJob`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `job_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the job is absent from the organization or is not a policy job. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/people/policy-jobs/{job_id}/cancel`

Cancel an eligible people policy job.

Operation ID: `cancelBloemOrganizationPeoplePolicyJob`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. No request body is read. A completed, failed or cancelled job is returned unchanged.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `job_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the job is absent from the organization. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | job_not_cancellable: the job is not a policy job; authorization_state_changed: the actor's authority changed since the job was queued. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/people/policy-previews`

Preview a policy command and obtain a bound confirmation token.

Operation ID: `previewBloemOrganizationPeoplePolicy`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

Request body (required, `application/json`): [`BloemPeoplePreviewRequest`](#bloempeoplepreviewrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPeoplePreviewResponse`](#bloempeoplepreviewresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selection token is invalid or belongs to another organization. The command's template or cohort may also not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | selection_expired: create a new selection; authorization_state_changed: the actor's authority changed since the selection. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.request): the policy command is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/organization/people/selections`

Create an immutable expiring selection from people filters.

Operation ID: `createBloemOrganizationPeopleSelection`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Selections expire after 15 minutes.

Request body (required, `application/json`): [`BloemPeopleSelectionRequest`](#bloempeopleselectionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPeopleSelectionResponse`](#bloempeopleselectionresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.filters): a status or sort value is unknown, or a group ID is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/people/{account_id}/`

Read an organization person and profile summary.

Operation ID: `getBloemOrganizationPerson`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPersonResponse`](#bloempersonresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: account_id is not a positive integer or the account is not a member of the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PATCH /api/bloem/v1/admin/organization/people/{account_id}/memberships/current`

Change an organization person membership status.

Operation ID: `updateBloemOrganizationPersonMembership`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |

Request body (required, `application/json`): [`BloemPeopleMembershipUpdate`](#bloempeoplemembershipupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPersonResponse`](#bloempersonresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: account_id is not a positive integer or the account is not a member of the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the person's security_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.request): expected_revision is not positive, status is not active or suspended, or the organization owner would be suspended. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PATCH /api/bloem/v1/admin/organization/people/{account_id}/profiles/{profile_id}`

Assign an organization person profile to an access group.

Operation ID: `updateBloemOrganizationPersonProfile`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | integer (int64) | yes |  |
| `profile_id` | path | string | yes |  |

Request body (required, `application/json`): [`BloemPeopleProfileUpdate`](#bloempeopleprofileupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPersonResponse`](#bloempersonresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: account_id is not a positive integer or the account is not a member of the organization. The profile or group_id may also be absent from the organization. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the person's security_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed (fields.request): expected_revision or group_id is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/policy-decisions`

Page sanitized organization policy decision explanations.

Operation ID: `listBloemOrganizationPolicyDecisions`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope. Decisions are ordered newest first; next_cursor is omitted on the last page.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `decision_name` | query | string |  | Exact decision name filter. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. |
| `limit` | query | integer |  | Page size from 1 to 200; defaults to 100. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPolicyDecisionsResponse`](#bloempolicydecisionsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: limit is outside 1 to 200, or cursor is malformed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/organization/policy-decisions/{id}`

Read a sanitized organization policy decision explanation.

Operation ID: `getBloemOrganizationPolicyDecision`.
Credentials: `bloemOrganizationContext`.

Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPolicyDecisionResponse`](#bloempolicydecisionresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the decision ID is not a positive integer or the decision is absent from the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/accounts`

Create an account, optionally on a plan.

Operation ID: `createBloemPlatformAccount`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. With entitlement_template_key and entitlement_template_revision, the account is put on exactly that plan revision in the same request, and applied_entitlement_revision in the response reports it. Without organization_id the account is a direct account in the default organization. It runs Silo's own account creation (as POST /api/v2/admin/users does), then puts the account on the plan; if the plan cannot be applied, the account is deleted again. Unknown fields are ignored.

Request body (required, `application/json`): [`BloemPlatformAccountCreateRequest`](#bloemplatformaccountcreaterequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`AdminUserView`](#adminuserview) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or exceeds 64 KiB, a field is invalid (the message names it), or entitlement_template_key and a positive entitlement_template_revision are not given together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | duplicate: the username or email is already in use. Silo's account rules can also answer 409, such as break_glass_required. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | entitlement_template_unavailable: that plan revision cannot be applied. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 501 | [`BloemNativeError`](README.md#bloemnativeerror) | capability_unsupported: this server's account store cannot manage accounts transactionally. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | entitlements_unavailable: plans are not configured. service_unavailable: the server is too busy to hash the password; retry after Retry-After. tenant_unavailable: the credential could not be checked. |

### `PUT /api/bloem/v1/admin/platform/accounts/{account_id}`

Update an account, optionally moving it to a plan.

Operation ID: `updateBloemPlatformAccount`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. With entitlement_template_key and entitlement_template_revision, the account is put on exactly that plan revision in the same request, and applied_entitlement_revision in the response reports it. Without organization_id the account is a direct account in the default organization. It runs Silo's own account update (as PUT /api/v2/admin/users does, without its If-Match), then applies the plan. Fields left out are not changed; a nullable policy field set to null clears it.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

Request body (required, `application/json`): [`BloemPlatformAccountUpdateBody`](#bloemplatformaccountupdatebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AdminUserView`](#adminuserview) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or exceeds 64 KiB, a field is invalid (the message names it), or entitlement_template_key and a positive entitlement_template_revision are not given together. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | duplicate: the username or email is already in use. Silo's account rules can also answer 409, such as break_glass_required. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | entitlement_template_unavailable: that plan revision cannot be applied. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 501 | [`BloemNativeError`](README.md#bloemnativeerror) | capability_unsupported: this server's account store cannot manage accounts transactionally. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | entitlements_unavailable: plans are not configured. service_unavailable: the server is too busy to hash the password; retry after Retry-After. tenant_unavailable: the credential could not be checked. |

### `GET /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions`

List an account's sign-in sessions.

Operation ID: `listBloemPlatformAccountAuthSessions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`AdminUserAuthSessionResponse`](#adminuserauthsessionresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions`

End every sign-in session of an account.

Operation ID: `revokeAllBloemPlatformAccountAuthSessions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/auth-sessions/{session_id}`

End one of an account's sign-in sessions.

Operation ID: `revokeBloemPlatformAccountAuthSession`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |
| `session_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/accounts/{account_id}/devices`

List an account's devices.

Operation ID: `listBloemPlatformAccountDevices`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`DeviceResponse`](#deviceresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/devices/{device_id}`

Remove an account's device.

Operation ID: `deleteBloemPlatformAccountDevice`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |
| `device_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/accounts/{account_id}/profiles`

List an account's profiles.

Operation ID: `listBloemPlatformAccountProfiles`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`ProfileView`](#profileview) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/accounts/{account_id}/profiles`

Create a profile for an account.

Operation ID: `createBloemPlatformAccountProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |

Request body (required, `application/json`): [`ProfileCreateRequest`](#profilecreaterequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`ProfileView`](#profileview) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `PUT /api/bloem/v1/admin/platform/accounts/{account_id}/profiles/{profile_id}`

Change an account's profile.

Operation ID: `updateBloemPlatformAccountProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |
| `profile_id` | path | string | yes |  |

Request body (required, `application/json`): [`ProfileUpdateRequest`](#profileupdaterequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ProfileView`](#profileview) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/accounts/{account_id}/profiles/{profile_id}`

Delete an account's profile.

Operation ID: `deleteBloemPlatformAccountProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Platform scope: the whole account, whatever its organization.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `account_id` | path | string | yes | The account ID. |
| `profile_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such account, or a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/library-figures`

Read item counts for every library.

Operation ID: `getBloemLibraryFigures`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. Counts every library, enabled or not, in one read-only snapshot: no titles, paths or accounts. A full count of a large catalog takes about a second, so cache the result rather than poll. Responses are not cacheable.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemLibraryFigures`](#bloemlibraryfigures) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: query parameters were sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | library_figures_unavailable: the database is unavailable or the count timed out; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v1/admin/platform/new-arrivals`

List recent additions to libraries.

Operation ID: `getBloemNewArrivals`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. Query: days (1-31, default 7), limit (1-50, default 20) and any number of library IDs (default every enabled library; disabled libraries never count). Lists films first seen in a chosen library inside the window and series with episodes first seen there, newest first: titles and years only, no paths, files or accounts. Responses are not cacheable.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemNewArrivals`](#bloemnewarrivals) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: days, limit or a library ID is out of range, or an unknown query parameter was sent. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | new_arrivals_unavailable: the database is unavailable or the read timed out; tenant_unavailable or service_unavailable: the credential could not be checked. |

### `GET /api/bloem/v1/admin/platform/organizations/`

Page organizations with current summary counts.

Operation ID: `listBloemPlatformOrganizations`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Organizations are ordered by case-insensitive name; next_cursor is omitted on the last page.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `query` | query | string |  | Case-insensitive substring of the organization name or slug. |
| `status` | query | string: `initializing`, `active`, `suspended` |  | Organization status filter. |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. |
| `limit` | query | integer |  | Page size from 1 to 200; defaults to 50. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformOrganizationsResponse`](#bloemplatformorganizationsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: limit is outside 1 to 200, status is unknown or cursor is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/`

Create an organization for an existing owner account.

Operation ID: `createBloemPlatformOrganization`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

Request body (required, `application/json`): [`BloemPlatformOrganizationCreate`](#bloemplatformorganizationcreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPlatformOrganizationResponse`](#bloemplatformorganizationresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | organization_slug_conflict: the slug is already in use; membership_conflict: the owner membership conflicts with existing state. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: name or slug is invalid, or owner_account_id does not identify an enabled account. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/platform/organizations/{id}/`

Read an organization summary.

Operation ID: `getBloemPlatformOrganization`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationSummaryResponse`](#bloemorganizationsummaryresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or the organization does not exist. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PATCH /api/bloem/v1/admin/platform/organizations/{id}/`

Change organization name or slug.

Operation ID: `updateBloemPlatformOrganization`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. At least one of name or slug is required.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemPlatformOrganizationUpdate`](#bloemplatformorganizationupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformOrganizationResponse`](#bloemplatformorganizationresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or the organization does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. organization_slug_conflict: the slug is already in use. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive, neither name nor slug is present, or a value is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/platform/organizations/{id}/entitlement`

Read the managed organization entitlement.

Operation ID: `getBloemPlatformOrganizationEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationEntitlementRead`](#bloemorganizationentitlementread) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed, or the organization does not exist or is not externally provisioned. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/entitlement/apply`

Apply a pinned entitlement template.

Operation ID: `applyBloemOrganizationEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper. The confirmation token is checked before the idempotency receipt, so a repeat after the token expires returns 422. Repeating an apply with the same idempotency_key returns the original result without applying again. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |
| `Idempotency-Key` | header | string |  | The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match. |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementApplyResponse`](#bloementitlementapplyresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed, or the organization does not exist or is not externally provisioned. The template revision may also not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | confirmation_stale: the preview no longer matches current state, dry-run again; revision_conflict: this idempotency_key was already used for a different template selection. managed_cohort_immutable: the organization's managed cohort group cannot be changed to this template. idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: the template selection, confirmation_token or idempotency_key is missing or invalid, or the confirmation token is expired or bound to another target or actor. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `GET /api/bloem/v1/admin/platform/organizations/{id}/entitlement/audit`

Read organization entitlement audit history.

Operation ID: `listBloemPlatformOrganizationEntitlementAudit`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Returns at most 200 events, newest first. An unknown organization returns an empty list.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementAuditResponse`](#bloementitlementauditresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/entitlement/dry-run`

Preview a pinned entitlement template.

Operation ID: `previewBloemOrganizationEntitlement`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemEntitlementApplyRequest`](#bloementitlementapplyrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementDryRunResponse`](#bloementitlementdryrunresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed, or the organization does not exist or is not externally provisioned. The template revision may also not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | managed_cohort_immutable: the organization's managed cohort group cannot be changed to this template. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) or [`BloemNativeError`](README.md#bloemnativeerror) | validation_failed with fields: template_key or template_revision is missing. template_unavailable without fields: the template revision is disabled or archived. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. |

### `GET /api/bloem/v1/admin/platform/organizations/{id}/memberships`

Page an organization membership directory.

Operation ID: `listBloemPlatformOrganizationMemberships`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Memberships are ordered by account ID; next_cursor is omitted on the last page. An unknown organization returns an empty page.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |
| `cursor` | query | string |  | Opaque next_cursor from the previous page. |
| `limit` | query | integer |  | Page size from 1 to 200; defaults to 50. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformMembershipsResponse`](#bloemplatformmembershipsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: limit is outside 1 to 200 or cursor is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/memberships`

Create an organization membership for an existing account.

Operation ID: `createBloemPlatformOrganizationMembership`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemPlatformMembershipCreate`](#bloemplatformmembershipcreate).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`BloemPlatformMembershipResponse`](#bloemplatformmembershipresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or the organization does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. membership_conflict: the account is already a member. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: a field is invalid, or account_id does not identify an existing account. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `PATCH /api/bloem/v1/admin/platform/organizations/{id}/memberships/{membership_id}`

Change a membership role or status.

Operation ID: `updateBloemPlatformOrganizationMembership`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match. At least one of legacy_role or status is required.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |
| `membership_id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemPlatformMembershipUpdate`](#bloemplatformmembershipupdate).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformMembershipResponse`](#bloemplatformmembershipresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: an ID is malformed, or the organization or membership does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the membership's security_revision; current_revision carries the current value. membership_conflict: the owner's membership must stay an active admin. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive, neither legacy_role nor status is present, or a value is invalid. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/reactivate`

Reactivate an organization.

Operation ID: `reactivateBloemPlatformOrganization`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemRevisionRequest`](#bloemrevisionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformOrganizationResponse`](#bloemplatformorganizationresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or the organization does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/suspend`

Suspend an organization.

Operation ID: `suspendBloemPlatformOrganization`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemRevisionRequest`](#bloemrevisionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformOrganizationResponse`](#bloemplatformorganizationresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or the organization does not exist. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision is not positive. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization or the administration store is unavailable. |

### `POST /api/bloem/v1/admin/platform/organizations/{id}/transfer-ownership`

Transfer ownership after explicit confirmation and reauthentication.

Operation ID: `transferBloemPlatformOrganizationOwnership`.
Credentials: `bloemPlatformContext`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Field validation runs before the password check. The new owner must already be an active member and an enabled account. JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemPlatformOwnershipTransfer`](#bloemplatformownershiptransfer).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPlatformOrganizationResponse`](#bloemplatformorganizationresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. reauthentication_required: password is missing or incorrect. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed, the organization does not exist, or the new owner has no membership in it. |
| 409 | [`BloemNativeRevisionConflict`](#bloemnativerevisionconflict) | authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: expected_revision or owner_account_id is not positive, confirmed is not true, or the new owner is not an enabled active member. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization, password verification or the administration store is unavailable. |

### `GET /api/bloem/v1/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement`

Read authoritative account and profile entitlement policies.

Operation ID: `getBloemOrganizationAccountPolicy`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
| `account_id` | path | integer (int64) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`AccountPolicySnapshot`](#accountpolicysnapshot) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: an ID is malformed or the account is not a member of the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs`

Enqueue a confirmed bulk entitlement policy job.

Operation ID: `createBloemPlatformOrganizationPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound. An idempotency_key of 1 to 128 characters is required, in the body or the Idempotency-Key header. Repeating the command with the same key returns the original job; the same key with a different command returns 409 idempotency_conflict. A header key that differs from the body's returns 409 idempotency_key_conflict.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
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

### `GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}`

Read a bulk entitlement policy job.

Operation ID: `getBloemPlatformOrganizationPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
| `job_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemPeopleJobResponse`](#bloempeoplejobresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. The job may also be absent from the organization, longer than 128 characters or not a policy job. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel`

Cancel an eligible bulk entitlement policy job.

Operation ID: `cancelBloemPlatformOrganizationPolicyJob`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound. Send an empty JSON object; missing JSON is invalid. A completed, failed or cancelled job is returned unchanged.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
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

### `POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews`

Create a selection and preview a bulk policy command.

Operation ID: `previewBloemPlatformOrganizationPolicy`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |

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

### `GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-cohorts`

List organization entitlement cohorts.

Operation ID: `listBloemPlatformOrganizationEntitlementCohorts`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
| `include_archived` | query | boolean |  | true or false; defaults to false. Other query parameters return 422. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementCohortsResponse`](#bloementitlementcohortsresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: include_archived is not true or false, or another query parameter is present. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `GET /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}`

Read one organization entitlement cohort.

Operation ID: `getBloemPlatformOrganizationEntitlementCohort`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |
| `cohort_id` | path | string (uuid) | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemEntitlementCohortResponse`](#bloementitlementcohortresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization. The cohort may also be malformed or absent from the organization. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `POST /api/bloem/v1/admin/platform/organizations/{organization_id}/entitlement-snapshots`

Read bounded account policy snapshots in one observation.

Operation ID: `readBloemOrganizationAccountPolicySnapshots`.
Credentials: `bloemPlatformContext`; or `bloemEntitlementBulkKey`.

Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated. Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority. Strict JSON body limit is 1 MiB; account_ids contains 1 to 10000 positive account IDs. Per-account failures use items[].error, not an HTTP failure.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `organization_id` | path | string (uuid) | yes |  |

Request body (required, `application/json`): [`BloemAccountPolicySnapshotsRequest`](#bloemaccountpolicysnapshotsrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAccountPolicySnapshotsResponse`](#bloemaccountpolicysnapshotsresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session. With an API key, unauthorized: the key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership. With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the organization ID is malformed or nil. Accounts outside the organization are reported per item. |
| 422 | [`BloemNativeValidationError`](#bloemnativevalidationerror) | validation_failed: account_ids is empty, has more than 10000 entries or contains a non-positive ID. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable. With an API key, service_unavailable: the key could not be checked; retry after Retry-After. |

### `GET /api/bloem/v1/admin/platform/tenants`

List reseller organizations with live usage.

Operation ID: `listBloemPlatformTenants`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. A bare JSON array. A tenant is an organization created through this API, with seats (limits.slots), shared video conversions (limits.transcodes) and frozen when suspended: frozen_reason is quota (more members than seats; lifts by itself) or admin (lifts only on thaw).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`TenantResponse`](#tenantresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants`

Create a reseller organization.

Operation ID: `createBloemPlatformTenant`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Idempotent on external_ref.service_id: a replay returns the same organization instead of creating a second. The plan, when named, is applied to the new organization.

Request body (required, `application/json`): [`BloemTenantCreateBody`](#bloemtenantcreatebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`TenantResponse`](#tenantresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_json: the body is not valid JSON. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation: a tenant needs a name, an external service reference and at least one seat. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{id}`

Read one reseller organization.

Operation ID: `getBloemPlatformTenant`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The tenant's organization ID (UUID). A malformed or unknown ID is not_found. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantResponse`](#tenantresponse) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant (a malformed ID answers the same). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{id}`

Retire a reseller organization.

Operation ID: `retireBloemPlatformTenant`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. The organization stays on record, suspended, with its external reference cleared. Its memberships are removed; member accounts with no other organization and no platform obligation are deleted.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The tenant's organization ID (UUID). A malformed or unknown ID is not_found. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant (a malformed ID answers the same). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: retirement is not available, or the credential could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{id}/freeze`

Suspend a reseller organization.

Operation ID: `freezeBloemPlatformTenant`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Suspends with reason admin, for example for non-payment: members are refused new sessions and playback; data is kept.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The tenant's organization ID (UUID). A malformed or unknown ID is not_found. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant (a malformed ID answers the same). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `PATCH /api/bloem/v1/admin/platform/tenants/{id}/limits`

Change seats and shared conversions.

Operation ID: `updateBloemPlatformTenantLimits`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Applied in place. Dropping seats below the members in use suspends the organization at once with reason quota, until members are removed.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The tenant's organization ID (UUID). |

Request body (required, `application/json`): [`BloemTenantLimits`](#bloemtenantlimits).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantResponse`](#tenantresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_json: the body is not valid JSON. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant (a malformed ID answers the same). |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | validation: slots must be at least 1 and transcodes 0 or more. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{id}/thaw`

Lift an administrative suspension.

Operation ID: `thawBloemPlatformTenant`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. An organization still over its seats stays suspended, with reason quota.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `id` | path | string | yes | The tenant's organization ID (UUID). A malformed or unknown ID is not_found. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant (a malformed ID answers the same). |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members`

List a reseller organization's members.

Operation ID: `listBloemPlatformTenantMembers`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. A bare JSON array. status is active, or suspended when the account is disabled.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members`

Add a member account to a reseller organization.

Operation ID: `createBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Creates the account in the organization and takes one seat. A replay of the same Idempotency-Key and body returns the member with 200.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `Idempotency-Key` | header | string | yes | Required. A replay of the same key and body returns the member with 200; the same key for another body is idempotency_conflict. |

Request body (required, `application/json`): [`BloemTenantMemberCreateBody`](#bloemtenantmembercreatebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 |  | The member, for a replay of the same key and body. |
| 201 | [`TenantMemberResponse`](#tenantmemberresponse) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON. idempotency_key_required: the Idempotency-Key header is missing. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. idempotency_conflict: the key was used for a different command. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | slot_quota_exceeded: every seat is taken. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`

Read one member.

Operation ID: `getBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `PUT /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`

Change a member's username or email.

Operation ID: `updateBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

Request body (required, `application/json`): [`BloemTenantMemberUpdateBody`](#bloemtenantmemberupdatebody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}`

Remove a member.

Operation ID: `deleteBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Removes the membership and frees its seat; an account with no other organization is deleted.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions`

List a member's sign-in sessions.

Operation ID: `listBloemPlatformTenantMemberAuthSessions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`AdminUserAuthSessionResponse`](#adminuserauthsessionresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions`

End every sign-in session of a member.

Operation ID: `revokeAllBloemPlatformTenantMemberAuthSessions`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}`

End one of a member's sign-in sessions.

Operation ID: `revokeBloemPlatformTenantMemberAuthSession`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |
| `session_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/devices`

List a member's devices.

Operation ID: `listBloemPlatformTenantMemberDevices`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`DeviceResponse`](#deviceresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/devices/{device_id}`

Remove a member's device.

Operation ID: `deleteBloemPlatformTenantMemberDevice`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |
| `device_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `GET /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles`

List a member's profiles.

Operation ID: `listBloemPlatformTenantMemberProfiles`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | array of [`ProfileView`](#profileview) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles`

Create a profile for a member.

Operation ID: `createBloemPlatformTenantMemberProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

Request body (required, `application/json`): [`ProfileCreateRequest`](#profilecreaterequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 201 | [`ProfileView`](#profileview) | Created |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `PUT /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}`

Change a member's profile.

Operation ID: `updateBloemPlatformTenantMemberProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |
| `profile_id` | path | string | yes |  |

Request body (required, `application/json`): [`ProfileUpdateRequest`](#profileupdaterequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`ProfileView`](#profileview) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `DELETE /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}`

Delete a member's profile.

Operation ID: `deleteBloemPlatformTenantMemberProfile`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |
| `profile_id` | path | string | yes |  |

| Status | Body | Meaning |
| --- | --- | --- |
| 204 |  | No Content |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). Also a profile, device or session the account does not have. |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted. |
| 422 | [`BloemNativeError`](README.md#bloemnativeerror) | profile_limit_reached: the account's plan allows no more profiles. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/reset-password`

Set a member's password.

Operation ID: `resetBloemPlatformTenantMemberPassword`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

Request body (required, `application/json`): [`BloemTenantMemberPasswordBody`](#bloemtenantmemberpasswordbody).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/resume`

Resume a suspended member's account.

Operation ID: `resumeBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/platform/tenants/{tenant_id}/members/{user_id}/suspend`

Suspend a member's account.

Operation ID: `suspendBloemPlatformTenantMember`.
Credentials: `bloemPlatformContext`; or `bearerAuth`.

Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request.

| Parameter | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `tenant_id` | path | string | yes | The tenant's organization ID (UUID). |
| `user_id` | path | string | yes | The member's account ID. |

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`TenantMemberResponse`](#tenantmemberresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | bad_request: the body is not valid JSON or the request is invalid. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same). |
| 409 | [`BloemNativeError`](README.md#bloemnativeerror) | username_conflict: the username or email is already in use. |
| 500 | [`BloemNativeError`](README.md#bloemnativeerror) | internal_error: the operation failed; details are withheld. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable or service_unavailable: the credential or the account could not be checked. |

### `POST /api/bloem/v1/admin/session`

Exchange a login session for a scoped administrative context.

Operation ID: `createBloemAdminSession`.
Credentials: `bloemAdminLoginSession`.

Requires an authenticated account with a current login-session ID and account incarnation. API keys without a login session are refused. scope=platform forbids organization_id and requires current platform authority. scope=organization requires organization_id and current organization administrator or platform authority; membership and organization must be active. The new token is bound to the originating login session. Strict JSON; body limit 16 KiB.

Request body (required, `application/json`): [`BloemAdminSessionRequest`](#bloemadminsessionrequest).

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemAdminSessionResponse`](#bloemadminsessionresponse) | OK |
| 400 | [`BloemNativeError`](README.md#bloemnativeerror) | invalid_request: invalid scope, JSON, organization selection or body exceeding 16 KiB. |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: authentication is missing or invalid, the account identity is incomplete, or the credential has no login session (API keys cannot exchange); authorization_state_stale: the membership or its revisions changed during the exchange. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | insufficient_platform_authority: scope=platform without current platform authority; insufficient_organization_authority: the membership is not an organization administrator; organization_suspended: the organization or membership is suspended; forbidden or password_change_required: this session cannot use the route. |
| 404 | [`BloemNativeError`](README.md#bloemnativeerror) | not_found: the selected organization is absent, hidden or still initializing, or this account has no membership in it or only an invitation. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | tenant_unavailable: administrative authorization is unavailable; service_unavailable: the login session could not be checked, retry after Retry-After. |

### `GET /api/bloem/v1/organizations`

The organizations this account belongs to.

Operation ID: `listBloemOrganizations`.
Credentials: `bearerAuth`.

Requires an account bearer token or API key; no profile or organization needs to be selected. Lists only active memberships in active organizations that have an owner.

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | [`BloemOrganizationsOutputBody`](#bloemorganizationsoutputbody) | OK |
| 401 | [`BloemNativeError`](README.md#bloemnativeerror) | unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. |
| 403 | [`BloemNativeError`](README.md#bloemnativeerror) | forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. |
| 503 | [`BloemNativeError`](README.md#bloemnativeerror) | service_unavailable: the credential could not be checked; retry after Retry-After. tenant_unavailable: memberships or organizations could not be read, or tenant authorization is not configured. |

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

### AdminGroupRef

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | integer (int64) |  |  |
| `name` | string |  |  |

### AdminOrganizationRef

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `membership_id` | string |  |  |

### AdminSubjectRef

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) |  |  |
| `profile_id` | string |  |  |

### AdminUserAuthSessionResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `auth_method` | string |  |  |
| `created_at` | string (date-time) | yes |  |
| `device_id` | string |  |  |
| `device_name` | string |  |  |
| `expires_at` | string (date-time) | yes |  |
| `id` | string | yes |  |
| `ip_address` | string |  |  |
| `is_impersonation_session` | boolean | yes |  |
| `profile_id` | string |  |  |
| `revoked_at` | string (date-time) |  |  |

### AdminUserView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_group_id` | integer or null (int64) | yes |  |
| `applied_entitlement_revision` | integer (int64) |  |  |
| `audio_transcode_allowed` | boolean or null | yes |  |
| `created_at` | string (date-time) | yes |  |
| `download_allowed` | boolean or null | yes |  |
| `download_transcode_allowed` | boolean or null | yes |  |
| `effective_policy` | [`EffectivePolicyView`](#effectivepolicyview) | yes |  |
| `email` | string | yes |  |
| `enabled` | boolean | yes |  |
| `id` | integer (int64) | yes |  |
| `last_active_at` | string (date-time) |  |  |
| `library_ids` | array of integer (int64) | yes |  |
| `max_playback_quality` | string or null | yes |  |
| `max_profiles` | integer (int64) | yes |  |
| `max_streams` | integer or null (int64) | yes |  |
| `max_transcodes` | integer or null (int64) | yes |  |
| `permissions` | array of string | yes |  |
| `requests_allowed` | boolean or null | yes |  |
| `role` | string | yes |  |
| `transcode_allowed` | boolean or null | yes |  |
| `updated_at` | string (date-time) | yes |  |
| `username` | string | yes |  |

### AuditEvent

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string | yes |  |
| `actor_account_id` | integer (int64) |  |  |
| `created_at` | string (date-time) | yes |  |
| `id` | integer (int64) | yes |  |
| `organization_id` | string |  |  |
| `request_id` | string |  |  |
| `target_account_id` | integer (int64) |  |  |
| `template_key` | string |  |  |
| `template_revision` | integer (int64) |  |  |

### BloemAccountPolicySnapshotsRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_ids` | array of integer (int64) | yes |  |

### BloemAccountPolicySnapshotsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`AccountPolicySnapshotResult`](#accountpolicysnapshotresult) | yes |  |
| `observed_at` | string (date-time) | yes |  |

### BloemAdminContextSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `authority` | string | yes |  |
| `key` | string | yes |  |
| `membership_id` | string |  |  |
| `name` | string | yes |  |
| `organization_id` | string |  |  |
| `policy_revision` | integer (int64) |  |  |
| `scope` | string | yes |  |
| `security_revision` | integer (int64) |  |  |
| `status` | string | yes |  |

### BloemAdminSessionRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `organization_id` | string |  |  |
| `scope` | string: `platform`, `organization` | yes |  |

### BloemAdminSessionResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_token` | string | yes |  |
| `context` | [`BloemAdminContextSummary`](#bloemadmincontextsummary) | yes |  |
| `expires_at` | string (date-time) | yes |  |

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

### BloemEntitlementAuditResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `events` | array of [`AuditEvent`](#auditevent) | yes |  |

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

### BloemJSONInputStruct {}Body

Type: object.

### BloemLibraryFigure

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audiobooks` | integer (int64) | yes |  |
| `ebooks` | integer (int64) | yes |  |
| `enabled` | boolean | yes |  |
| `episodes` | integer (int64) | yes |  |
| `library_id` | integer (int64) | yes |  |
| `movies` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `series` | integer (int64) | yes |  |
| `type` | string | yes | Library kind (movies, series, mixed, audiobooks, ebooks, ...) |

### BloemLibraryFigures

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `counted_at` | string (date-time) | yes | When the snapshot was read (UTC) |
| `libraries` | array of [`BloemLibraryFigure`](#bloemlibraryfigure) | yes | Every library, in the server's library order |

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

### BloemNewArrival

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `added_at` | string (date-time) | yes | When the film, or the series' newest episode, was first seen in a chosen library |
| `content_id` | string | yes |  |
| `kind` | string: `movie`, `series` | yes |  |
| `new_episodes` | integer (int64) | yes |  |
| `new_series` | boolean | yes |  |
| `title` | string | yes |  |
| `year` | integer (int64) |  |  |

### BloemNewArrivals

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `items` | array of [`BloemNewArrival`](#bloemnewarrival) | yes |  |
| `since` | string (date-time) | yes | Start of the window (UTC) |

### BloemOrganization

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `default` | boolean | yes | True for the organization a client should select when the viewer has not chosen one. |
| `id` | string | yes | Organization identity. |
| `membership_id` | string | yes | The membership granting this account access. |
| `membership_role` | string | yes | The role this membership carries. |
| `name` | string | yes | Display name. |
| `policy_revision` | integer (int64) | yes | Increments when this organization's access policy changes. |
| `security_revision` | integer (int64) | yes | Increments when credentials or sessions are invalidated. |
| `slug` | string | yes | URL-safe short name. |

### BloemOrganizationEntitlementRead

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audit_history_href` | string | yes |  |
| `last_reconciled_at` | string or null (date-time) | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `managed_default_group` | [`BloemEntitlementManagedGroup`](#bloementitlementmanagedgroup) or null | yes |  |
| `template_key` | string | yes |  |
| `template_revision` | integer (int64) | yes |  |
| `tenant_limits` | [`BloemOrganizationEntitlementReadTenantLimitsStruct`](#bloemorganizationentitlementreadtenantlimitsstruct) | yes |  |

### BloemOrganizationEntitlementReadTenantLimitsStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `slots` | integer (int64) | yes |  |
| `transcodes` | integer (int64) | yes |  |

### BloemOrganizationEntitlementResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `entitlement` | [`LibraryEntitlement`](#libraryentitlement) | yes |  |

### BloemOrganizationEntitlementUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes |  |
| `status` | string: `active`, `suspended` | yes |  |

### BloemOrganizationGroup

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_permissions` | array of string or null | yes |  |
| `audio_transcode_allowed` | boolean | yes |  |
| `created_at` | string (date-time) | yes |  |
| `description` | string | yes |  |
| `download_allowed` | boolean | yes |  |
| `download_transcode_allowed` | boolean | yes |  |
| `id` | integer (int64) | yes |  |
| `is_default` | boolean | yes |  |
| `library_ids` | array of integer (int64) or null | yes |  |
| `max_playback_quality` | string | yes |  |
| `max_streams` | integer (int64) | yes |  |
| `max_transcodes` | integer (int64) | yes |  |
| `member_count` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `requests_allowed` | boolean | yes |  |
| `transcode_allowed` | boolean | yes |  |
| `updated_at` | string (date-time) | yes |  |

### BloemOrganizationGroupCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_permissions` | array of string or null |  | Null or omission uses the default permission policy. Group responses collapse empty permission arrays to null. |
| `audio_transcode_allowed` | boolean |  |  |
| `description` | string |  |  |
| `download_allowed` | boolean |  |  |
| `download_transcode_allowed` | boolean |  |  |
| `expected_revision` | integer (int64) | yes |  |
| `is_default` | boolean |  |  |
| `library_ids` | array of integer (int64) or null |  | Omit for the default policy; null adds no library restriction. Empty arrays are collapsed to null in group responses. |
| `max_playback_quality` | string |  |  |
| `max_streams` | integer (int64) |  |  |
| `max_transcodes` | integer (int64) |  |  |
| `name` | string | yes |  |
| `requests_allowed` | boolean |  |  |
| `transcode_allowed` | boolean |  |  |

### BloemOrganizationGroupMutationResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group` | [`BloemOrganizationGroup`](#bloemorganizationgroup) | yes |  |
| `policy_revision` | integer (int64) | yes |  |

### BloemOrganizationGroupResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group` | [`BloemOrganizationGroup`](#bloemorganizationgroup) | yes |  |

### BloemOrganizationGroupUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_permissions` | array of string or null |  |  |
| `audio_transcode_allowed` | boolean |  |  |
| `description` | string |  |  |
| `download_allowed` | boolean |  |  |
| `download_transcode_allowed` | boolean |  |  |
| `expected_revision` | integer (int64) | yes |  |
| `is_default` | boolean |  |  |
| `library_ids` | array of integer (int64) or null |  |  |
| `max_playback_quality` | string |  |  |
| `max_streams` | integer (int64) |  |  |
| `max_transcodes` | integer (int64) |  |  |
| `name` | string |  |  |
| `requests_allowed` | boolean |  |  |
| `transcode_allowed` | boolean |  |  |

### BloemOrganizationGroupsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `groups` | array of [`BloemOrganizationGroup`](#bloemorganizationgroup) | yes |  |

### BloemOrganizationInvitation

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `accepted_at` | string (date-time) |  |  |
| `accepted_user_id` | integer (int64) |  |  |
| `access_group_id` | integer (int64) |  |  |
| `create_profile` | boolean | yes |  |
| `created_at` | string (date-time) | yes |  |
| `email` | string | yes |  |
| `expires_at` | string (date-time) | yes |  |
| `id` | integer (int64) | yes |  |
| `invited_by` | integer (int64) | yes |  |
| `invited_by_name` | string |  |  |
| `library_ids` | array of integer (int64) |  |  |
| `note` | string |  |  |
| `role` | string | yes |  |
| `show_tour` | boolean | yes |  |
| `status` | string: `pending`, `accepted`, `expired`, `revoked` | yes |  |

### BloemOrganizationInvitationCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_group_id` | integer (int64) |  |  |
| `create_profile` | boolean |  | Defaults to true. |
| `email` | string | yes |  |
| `expected_revision` | integer (int64) | yes |  |
| `library_ids` | array of integer (int64) or null |  |  |
| `note` | string |  |  |
| `show_tour` | boolean |  | Defaults to true. |

### BloemOrganizationInvitationResendResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `claim_token` | string | yes | Single-use claim token returned only by this rotation response. The server retains only its hash; later reads cannot recover it. Treat it as a secret and deliver it directly to the invitee. |
| `invitation` | [`BloemOrganizationInvitation`](#bloemorganizationinvitation) | yes |  |

### BloemOrganizationInvitationRevision

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Current organization policy revision from the reviewed administrative context; not an invitation revision. A mismatch returns 409 authorization_state_changed with current_revision. |

### BloemOrganizationInvitationsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `invitations` | array of [`BloemOrganizationInvitation`](#bloemorganizationinvitation) | yes |  |

### BloemOrganizationLibrariesResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `libraries` | array of [`LibraryProjection`](#libraryprojection) | yes |  |

### BloemOrganizationSummaryResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `organization` | [`OrganizationSummary`](#organizationsummary) | yes |  |

### BloemOrganizationsOutputBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `organizations` | array of [`BloemOrganization`](#bloemorganization) | yes | Active memberships in active, owned organizations. Empty, never null. |

### BloemPeopleJobResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `job` | [`BulkResult`](#bulkresult) | yes |  |

### BloemPeopleMembershipUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Person security_revision, not organization policy_revision. |
| `status` | string: `active`, `suspended` | yes |  |

### BloemPeoplePreviewRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `command` | [`BloemPolicyCommand`](#bloempolicycommand) | yes |  |
| `selection_token` | string | yes |  |

### BloemPeoplePreviewResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `preview` | [`PolicyPreview`](#policypreview) | yes |  |

### BloemPeopleProfileUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Person security_revision, not organization policy_revision. |
| `group_id` | integer (int64) | yes |  |

### BloemPeopleSelectionRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `active_since` | string (date-time) |  |  |
| `group_ids` | array of integer (int64) or null |  |  |
| `query` | string |  |  |
| `sort` | string |  |  |
| `status` | array of string or null |  |  |

### BloemPeopleSelectionResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `selection` | [`Selection`](#selection) | yes |  |

### BloemPersonResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `person` | [`PersonSummary`](#personsummary) | yes |  |

### BloemPlatformAccountCreateRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_group_id` | integer (int64) |  |  |
| `audio_transcode_allowed` | boolean |  |  |
| `create_default_profile` | boolean |  |  |
| `default_profile_name` | string |  |  |
| `download_allowed` | boolean |  |  |
| `download_transcode_allowed` | boolean |  |  |
| `email` | string | yes | Email address. |
| `enabled` | boolean |  | A new account is enabled; false disables it straight after it is created. |
| `entitlement_template_key` | string |  | With entitlement_template_revision, the plan to put the account on. |
| `entitlement_template_revision` | integer (int64) |  | The exact plan revision. |
| `library_ids` | array of integer (int64) |  | Library IDs; null or omitted means every library. |
| `max_playback_quality` | string |  |  |
| `max_profiles` | integer (int64) |  |  |
| `max_streams` | integer (int64) |  |  |
| `max_transcodes` | integer (int64) |  |  |
| `password` | string | yes | Initial password. |
| `permissions` | array of string |  |  |
| `requests_allowed` | boolean |  |  |
| `require_password_change` | boolean |  | The account must replace its password at first sign-in. |
| `role` | string |  | user (the default) or admin. |
| `transcode_allowed` | boolean |  |  |
| `username` | string | yes | Sign-in name. |

### BloemPlatformAccountUpdateBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_group_id` | integer or null (int64) |  | null: no access group. |
| `audio_transcode_allowed` | boolean or null |  |  |
| `download_allowed` | boolean or null |  |  |
| `download_transcode_allowed` | boolean or null |  |  |
| `email` | string |  |  |
| `enabled` | boolean |  |  |
| `entitlement_template_key` | string |  | With entitlement_template_revision, the plan to move the account to. |
| `entitlement_template_revision` | integer (int64) |  | The exact plan revision. |
| `library_ids` | array of integer (int64) or null |  | null: every library. |
| `max_playback_quality` | string or null |  |  |
| `max_profiles` | integer or null (int64) |  | null clears the account's own value. |
| `max_streams` | integer or null (int64) |  |  |
| `max_transcodes` | integer or null (int64) |  |  |
| `password` | string |  |  |
| `permissions` | array of string |  |  |
| `requests_allowed` | boolean or null |  |  |
| `role` | string |  | user or admin. |
| `transcode_allowed` | boolean or null |  |  |
| `username` | string |  |  |

### BloemPlatformMembershipCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `expected_revision` | integer (int64) | yes | Organization policy revision. |
| `legacy_role` | string: `admin`, `user` | yes |  |
| `status` | string: `invited`, `active`, `suspended` |  |  |

### BloemPlatformMembershipResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `membership` | [`Membership`](#membership) | yes |  |
| `organization` | [`Organization`](#organization) | yes |  |

### BloemPlatformMembershipUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes | Membership security revision. |
| `legacy_role` | string: `admin`, `user` |  |  |
| `status` | string: `invited`, `active`, `suspended` |  |  |

### BloemPlatformMembershipsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `memberships` | array of [`MembershipSummary`](#membershipsummary) | yes |  |
| `next_cursor` | string |  |  |

### BloemPlatformOrganizationCreate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes |  |
| `owner_account_id` | integer (int64) | yes |  |
| `slug` | string | yes |  |

### BloemPlatformOrganizationResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `organization` | [`Organization`](#organization) | yes |  |

### BloemPlatformOrganizationUpdate

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes |  |
| `name` | string |  |  |
| `slug` | string |  |  |

### BloemPlatformOrganizationsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `next_cursor` | string |  |  |
| `organizations` | array of [`OrganizationSummary`](#organizationsummary) | yes |  |

### BloemPlatformOwnershipTransfer

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `confirmed` | boolean: `True` | yes |  |
| `expected_revision` | integer (int64) | yes |  |
| `owner_account_id` | integer (int64) | yes |  |
| `password` | string | yes | Current actor account password, required for reauthentication. |

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

### BloemPolicyDecisionResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `decision` | [`PolicyDecisionExplanation`](#policydecisionexplanation) | yes |  |

### BloemPolicyDecisionsResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `decisions` | array of [`PolicyDecisionExplanation`](#policydecisionexplanation) | yes |  |
| `next_cursor` | string |  |  |

### BloemRevisionRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `expected_revision` | integer (int64) | yes |  |

### BloemTenantCreateBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `entitlement_template_key` | string |  | Plan to apply to the organization. |
| `entitlement_template_revision` | integer (int64) |  | Exact revision of that plan. |
| `external_ref` | [`BloemTenantExternalRef`](#bloemtenantexternalref) | yes |  |
| `limits` | [`BloemTenantLimits`](#bloemtenantlimits) | yes |  |
| `name` | string | yes | Display name. |

### BloemTenantExternalRef

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `operator_id` | string |  | The storefront operator's reference. |
| `service_id` | string | yes | The storefront's service reference. Creation is idempotent on it. |

### BloemTenantLimits

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `slots` | integer (int64) | yes | Seats: the most members the organization may have. |
| `transcodes` | integer (int64) | yes | Video conversions shared by all members. |

### BloemTenantMemberCreateBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `email` | string | yes | Email address. |
| `password` | string | yes | Initial password. |
| `username` | string | yes | Sign-in name. |

### BloemTenantMemberPasswordBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `password` | string | yes | New password. |

### BloemTenantMemberUpdateBody

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `email` | string |  |  |
| `username` | string |  |  |

### BulkAction

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group_id` | integer (int64) |  |  |
| `kind` | string | yes |  |
| `selection_token` | string | yes |  |

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

### DeviceResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `changed_count` | integer (int64) | yes |  |
| `device_id` | string | yes |  |
| `device_name` | string | yes |  |
| `device_platform` | string | yes |  |
| `is_current_device` | boolean | yes |  |
| `last_seen_at` | string | yes |  |
| `profile_id` | string | yes |  |
| `profile_name` | string | yes |  |

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

### EffectivePolicyView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `audio_transcode_allowed` | boolean | yes |  |
| `download_allowed` | boolean | yes |  |
| `download_transcode_allowed` | boolean | yes |  |
| `library_ids` | array of integer (int64) | yes |  |
| `max_playback_quality` | string | yes |  |
| `max_profiles` | integer (int64) | yes |  |
| `max_streams` | integer (int64) | yes |  |
| `max_transcodes` | integer (int64) | yes |  |
| `permissions` | array of string | yes |  |
| `playback_allowed` | boolean | yes |  |
| `requests_allowed` | boolean | yes |  |
| `transcode_allowed` | boolean | yes |  |

### GroupDeletionImpact

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `default_group_id` | integer (int64) | yes |  |
| `profiles_reassigned` | integer (int64) | yes |  |

### LibraryEntitlement

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `organization_id` | string | yes |  |
| `security_revision` | integer (int64) | yes |  |
| `status` | string | yes |  |

### LibraryProjection

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `access_kind` | string | yes |  |
| `entitlement` | [`LibraryEntitlement`](#libraryentitlement) |  |  |
| `folder_id` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `type` | string | yes |  |

### Membership

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `id` | string | yes |  |
| `legacy_role` | string | yes |  |
| `organization_id` | string | yes |  |
| `security_revision` | integer (int64) | yes |  |
| `status` | string | yes |  |

### MembershipSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `email` | string | yes |  |
| `id` | string | yes |  |
| `legacy_role` | string | yes |  |
| `organization_id` | string | yes |  |
| `security_revision` | integer (int64) | yes |  |
| `status` | string | yes |  |
| `username` | string | yes |  |

### Organization

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `id` | string | yes |  |
| `is_default` | boolean | yes |  |
| `name` | string | yes |  |
| `owner_account_id` | integer (int64) |  |  |
| `policy_revision` | integer (int64) | yes |  |
| `slug` | string | yes |  |
| `status` | string | yes |  |

### OrganizationAuditEvent

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string | yes |  |
| `actor_account_id` | integer or null (int64) | yes |  |
| `created_at` | string (date-time) | yes |  |
| `id` | integer (int64) | yes |  |
| `outcome` | string | yes |  |
| `source` | string | yes |  |
| `target_id` | string | yes |  |

### OrganizationAuditPage

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `events` | array of [`OrganizationAuditEvent`](#organizationauditevent) | yes |  |
| `next_cursor` | string |  |  |

### OrganizationSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `active_membership_count` | integer (int64) | yes |  |
| `entitlement_count` | integer (int64) | yes |  |
| `id` | string | yes |  |
| `is_default` | boolean | yes |  |
| `library_count` | integer (int64) | yes |  |
| `membership_count` | integer (int64) | yes |  |
| `name` | string | yes |  |
| `owner_account_id` | integer (int64) |  |  |
| `policy_revision` | integer (int64) | yes |  |
| `profile_count` | integer (int64) | yes |  |
| `slug` | string | yes |  |
| `status` | string | yes |  |

### Page

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `approximate_total` | integer (int64) | yes |  |
| `items` | array of [`PersonSummary`](#personsummary) | yes |  |
| `next_cursor` | string |  |  |

### PersonSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `account_id` | integer (int64) | yes |  |
| `display_name` | string | yes |  |
| `email` | string | yes |  |
| `last_activity` | string (date-time) | yes |  |
| `legacy_role` | string | yes |  |
| `membership_id` | string | yes |  |
| `membership_status` | string | yes |  |
| `organization_id` | string | yes |  |
| `profiles` | array of [`ProfileSummary`](#profilesummary) | yes |  |
| `security_revision` | integer (int64) | yes |  |

### PolicyDecisionExplanation

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `action` | string | yes |  |
| `allowed` | boolean | yes |  |
| `group` | [`AdminGroupRef`](#admingroupref) | yes |  |
| `id` | integer (int64) | yes |  |
| `library_ceiling` | array of integer (int64) | yes |  |
| `organization` | [`AdminOrganizationRef`](#adminorganizationref) | yes |  |
| `policy_versions` | array of [`PolicyVersionRef`](#policyversionref) | yes |  |
| `reason_code` | string | yes |  |
| `resource` | object of any | yes |  |
| `subject` | [`AdminSubjectRef`](#adminsubjectref) | yes |  |
| `timestamp` | string (date-time) | yes |  |

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

### PolicyVersionRef

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `kind` | string | yes |  |
| `name` | string |  |  |
| `version` | integer (int64) | yes |  |

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

### ProfileCreateRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_library_ids` | array of integer (int64) | yes |  |
| `auto_play_next_preview` | boolean | yes |  |
| `auto_skip_credits` | boolean | yes |  |
| `auto_skip_intro` | boolean | yes |  |
| `auto_skip_recap` | boolean | yes |  |
| `avatar` | string |  |  |
| `is_child` | boolean | yes |  |
| `language` | string |  |  |
| `library_restrictions_enabled` | boolean | yes |  |
| `max_content_rating` | string |  |  |
| `max_playback_quality` | string | yes |  |
| `name` | string | yes |  |
| `pin` | string |  |  |
| `preferred_metadata_language` | string |  |  |
| `quality_preference` | string |  |  |
| `show_forced_subtitles` | boolean |  |  |
| `subtitle_language` | string |  |  |
| `subtitle_mode` | string |  |  |

### ProfilePolicySnapshot

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group_id` | integer (int64) | yes |  |
| `inherits_account` | boolean | yes |  |
| `policy` | [`EffectivePolicySnapshot`](#effectivepolicysnapshot) | yes |  |
| `profile_id` | string | yes |  |
| `profile_name` | string | yes |  |
| `state` | string | yes |  |

### ProfileSummary

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `group_id` | integer (int64) | yes |  |
| `group_name` | string | yes |  |
| `id` | string | yes |  |
| `name` | string | yes |  |
| `updated_at` | string (date-time) | yes |  |

### ProfileUpdateRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_library_ids` | array of integer (int64) |  |  |
| `auto_play_next_preview` | boolean |  |  |
| `auto_skip_credits` | boolean |  |  |
| `auto_skip_intro` | boolean |  |  |
| `auto_skip_recap` | boolean |  |  |
| `avatar` | string |  |  |
| `is_child` | boolean |  |  |
| `language` | string |  |  |
| `library_restrictions_enabled` | boolean |  |  |
| `max_content_rating` | string |  |  |
| `max_playback_quality` | string |  |  |
| `name` | string |  |  |
| `pin` | string |  |  |
| `preferred_metadata_language` | string |  |  |
| `quality_preference` | string |  |  |
| `show_forced_subtitles` | boolean |  |  |
| `subtitle_language` | string |  |  |
| `subtitle_mode` | string |  |  |

### ProfileView

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `allowed_library_ids` | array of integer (int64) | yes |  |
| `auto_play_next_preview` | boolean | yes |  |
| `auto_skip_credits` | boolean | yes |  |
| `auto_skip_intro` | boolean | yes |  |
| `auto_skip_recap` | boolean | yes |  |
| `avatar` | string |  |  |
| `avatar_source` | string |  |  |
| `avatar_url` | string |  |  |
| `created_at` | string | yes |  |
| `has_pin` | boolean | yes |  |
| `id` | string | yes |  |
| `is_child` | boolean | yes |  |
| `is_primary` | boolean | yes |  |
| `language` | string |  |  |
| `library_restrictions_enabled` | boolean | yes |  |
| `max_content_rating` | string |  |  |
| `max_playback_quality` | string | yes |  |
| `name` | string | yes |  |
| `preferred_metadata_language` | string |  |  |
| `quality_preference` | string |  |  |
| `show_forced_subtitles` | boolean | yes |  |
| `subtitle_language` | string |  |  |
| `subtitle_mode` | string |  |  |
| `updated_at` | string | yes |  |

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

### TenantMemberResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `email` | string | yes |  |
| `status` | string | yes |  |
| `user_id` | integer (int64) | yes |  |
| `username` | string | yes |  |

### TenantResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `applied_entitlement_revision` | integer (int64) |  |  |
| `external_ref` | [`TenantResponseExternalRefStruct`](#tenantresponseexternalrefstruct) | yes |  |
| `frozen` | boolean | yes |  |
| `frozen_reason` | string |  |  |
| `limits` | [`TenantResponseLimitsStruct`](#tenantresponselimitsstruct) | yes |  |
| `name` | string | yes |  |
| `tenant_id` | string | yes |  |
| `usage` | [`TenantResponseUsageStruct`](#tenantresponseusagestruct) | yes |  |

### TenantResponseExternalRefStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `operator_id` | string | yes |  |
| `service_id` | string | yes |  |

### TenantResponseLimitsStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `slots` | integer (int64) | yes |  |
| `transcodes` | integer (int64) | yes |  |

### TenantResponseUsageStruct

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `slots_used` | integer (int64) | yes |  |
