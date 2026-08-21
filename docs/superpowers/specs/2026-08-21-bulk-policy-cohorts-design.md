# Bulk Policy Cohorts Design

**Date:** 2026-08-21
**Status:** Approved design
**Scope:** Server-side cohort policy management, retroactive bulk assignment, and generic platform administration APIs

## Summary

Administrators need to apply entitlement policies retroactively to large, reviewed sets of existing accounts. An organization may simultaneously contain Standard, Premium, Browse-only, and custom policy cohorts. Administrators must be able to move people between cohorts, apply an exact global template revision, derive a selection-specific policy, or restore the managed default without leaving anyone policy-less.

The design builds on two existing foundations:

- immutable entitlement template revisions materialized into protected access groups; and
- immutable people selections plus durable, resumable bulk jobs.

It does not add per-person policy overrides or dynamic rules that silently affect future matches. Every bulk operation acts on a reviewed snapshot and resolves to a managed access group.

## Goals

- Support multiple simultaneous template-backed policy cohorts per organization.
- Apply, move, fork, modify, or restore policy for up to 10,000 reviewed accounts per job.
- Preserve custom profile assignments by default while moving inherited profiles with the account.
- Keep global template revision and selection-specific policy changes separate.
- Provide restart-safe progress, idempotency, per-account results, retry, and immutable audits.
- Support organization administrators, platform administrators, and generic external platform clients with appropriately scoped APIs.
- Expose authoritative current account and profile policy state so external clients never infer live access from a provisioning expectation.
- Preserve fail-closed playback, download, profile, permission, and library enforcement.

## Non-goals

- Automatically applying a saved filter to future matching accounts.
- Storing independent policy documents on users or profiles.
- Editing a global template as a side effect of a bulk account operation.
- Removing all policy from an account.
- Replacing product/provisioning integrations that assign policies for future accounts.

## Concepts

### Global template

A reusable, platform-managed policy definition with immutable revisions. Revising a global template remains an explicit platform operation and never occurs through the people bulk workflow.

### Managed cohort

An organization-scoped, immutable effective policy instance backed by a protected access group. A cohort records its source template and revision, derivation lineage, complete materialized policy, creator, and creation time.

An exact application of an unchanged template reuses the organization's existing cohort for that exact template revision. A selection-specific change creates a new derived cohort instead of changing the source template or affecting people outside the selection.

### Managed default

The organization's protected default access group. “Remove policy” means restoring selected accounts to this managed default. Direct accounts use the Server default organization's managed default when no product-specific cohort is selected.

### Inherited and custom profiles

A profile inherits account policy when its access group matches the account's current access group. Those profiles move with the account. A profile assigned to another group is custom and remains unchanged unless the operator explicitly confirms `include_custom_profiles`.

## Data model

### Cohort records

Add organization-scoped cohort lineage and immutable cohort-revision records. The schema must capture:

- stable cohort identity and display name;
- organization ownership;
- immutable cohort revision;
- protected access-group identity;
- exact source template key and revision;
- optional parent cohort revision for a derived policy;
- derivation kind: `exact_template`, `policy_patch`, or `managed_default`;
- complete effective policy, including resolved library IDs;
- canonical policy digest;
- creator and creation time;
- archived state for discovery only, never destructive deletion.

The relationship between a cohort revision and its access group is one-to-one. Cohort-backed groups are protected from generic access-group update, deletion, default promotion/demotion, or metadata reassignment. A selection-specific revision creates a new group; it does not mutate the group used by people outside the selection.

Existing exact-template managed groups are adopted transactionally on first discovery. The migration and adoption path must not change their effective policies or assignments.

### Bulk command extension

Extend the existing `admin_people_bulk_jobs` model rather than creating another worker framework. New action kinds are:

- `assign_entitlement_cohort`;
- `apply_entitlement_template`;
- `derive_entitlement_cohort`;
- `restore_default_entitlement`.

Each job stores an immutable, allowlisted action payload containing the target cohort or template revision, canonical policy patch, `include_custom_profiles`, preview digest, and confirmation metadata. Secrets and free-form provider errors are forbidden.

Existing immutable target snapshots remain the account worklist. Snapshot data is extended with account access-group identity, cohort identity/revision, and profile inheritance classification needed to detect drift before mutation.

### Policy patch semantics

Policy patches are explicit and deterministic:

- libraries: `add`, `remove`, `replace`, `all`, or `none`;
- allowed permissions: `add`, `remove`, `replace`, or `unrestricted`;
- booleans: explicit set for playback, transcoding, downloads, transcoded downloads, and requests;
- numeric limits: explicit set for streams, profiles, and transcodes;
- playback quality: explicit set.

The resulting full policy is validated against the same dependencies as a global template. A patch never means “toggle,” and omitted fields remain unchanged.

## Selection and preview

Organization selectors include:

- current cohort or source template;
- membership status;
- account/profile group;
- recent activity;
- text search;
- explicit account selection.

Platform direct-account selection additionally accepts an explicit bounded account-ID list. The Server does not interpret external product concepts.

Selections and previews always resolve current policy from Server access groups and effective-policy evaluation. A caller-supplied template or cohort identity is a requested target or comparison value, never an assertion of current state.

Preview creates the existing immutable selection snapshot and returns:

- total matched and excluded counts;
- current cohort distribution;
- target policy and field-level difference;
- accounts already compliant;
- inherited profiles that will move;
- custom profiles that will remain or move;
- ineligible or stale targets;
- the exact source and target revisions.

A signed, expiring confirmation token binds the selection reference, action payload digest, source/target revisions, actor, organization or direct-account scope, and custom-profile option. Any material change requires a new preview.

## Execution

Confirmation plus an idempotency key enqueues the durable bulk job. The worker:

1. revalidates actor authority and organization policy revision;
2. loads or transactionally creates the exact cohort revision once;
3. processes immutable targets in bounded batches using row locks and savepoints;
4. revalidates each account, membership, access group, and profile classification;
5. moves the account to the target protected group;
6. moves inherited profiles, plus custom profiles only when explicitly requested;
7. bumps account and membership policy/security revisions;
8. records per-account immutable audit evidence and safe result codes;
9. commits progress so a restarted worker resumes without repeating completed records.

Stale records are skipped with a safe reason. One account failure does not roll back successful accounts. Reusing the same idempotency key and identical command returns the original job; payload mismatch conflicts.

For a derived policy, cohort creation and job persistence are serialized by the command digest. Concurrent identical previews converge on one cohort revision and one durable job.

## API surface

### Organization administrator

Extend the existing organization people API with policy-specific preview and job endpoints:

- `POST /api/v2/admin/organization/people/policy-previews`
- `POST /api/v2/admin/organization/people/policy-jobs`
- `GET /api/v2/admin/organization/people/policy-jobs/{job_id}`
- `GET /api/v2/admin/organization/entitlement-cohorts`
- `GET /api/v2/admin/organization/entitlement-cohorts/{cohort_id}`

The existing people selection endpoint remains the canonical filter snapshot mechanism.

### Platform administrator and generic platform clients

Provide equivalent platform-authority operations for an explicit organization or direct-account set:

- organization cohort discovery, preview, enqueue, and job status under `/api/v2/admin/platform/organizations/{organization_id}/...`;
- direct-account preview, enqueue, and job status under `/api/v2/admin/platform/accounts/entitlement-bulk/...`.

Authoritative read operations are also required:

- `GET /api/v2/admin/platform/accounts/{account_id}/entitlement`
- `GET /api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement`
- `POST /api/v2/admin/platform/accounts/entitlement-snapshots`
- `POST /api/v2/admin/platform/organizations/{organization_id}/entitlement-snapshots`

The single-account response includes the current account access group, cohort identity/revision, source template key/revision, complete effective policy, policy revision, and every profile's effective group/policy plus whether it inherits or is a custom exception. The bounded snapshot endpoints return the same safe policy projection for up to 10,000 explicit Server account IDs, with per-account not-found/stale results and one Server observation timestamp.

The response distinguishes `managed`, `custom`, and `legacy_unmanaged` state. Missing cohort provenance must not be synthesized from a caller's expected template. Policies with dynamic inputs are returned as the currently resolved effective policy together with their durable source revision.

Platform APIs accept Server account IDs only, enforce a 10,000-target limit, and verify every account against the requested scope before creating a selection. Responses contain no email addresses unless the caller already has the corresponding people-read authority.

Every endpoint uses strict bounded JSON, exact status contracts, redirect rejection where applicable, stable safe errors, confirmation tokens, and idempotency keys.

## User interface

### Organization People

Add an “Apply policy” action to the existing bulk action bar. A four-step drawer provides:

1. selected people and filters;
2. operation and target policy;
3. impact preview and custom-profile choice;
4. explicit confirmation and durable job creation.

### Cohort management

An organization cohort view shows name, source template/revision, effective policy, member count, revision lineage, creation actor/time, and archive state. Operators may move people to a cohort or derive a new revision. They cannot edit a cohort in place.

### Platform direct accounts

The platform direct-account page receives the same selection, preview, confirmation, progress, and audit workflow.

### Job result

Job pages show progress, succeeded/skipped/failed counts, stable per-account reasons, retry guidance, and audit links. Buttons remain disabled through synchronous submission guards to prevent competing commands.

## Authorization and security

- Organization administrators may affect only active memberships in their current organization.
- Platform operations require platform-admin authority or a scoped platform API key.
- Actor authority and organization policy revision are checked at enqueue and every resumed batch.
- The acting account cannot remove its own required administrative authority through this workflow.
- Cross-organization account, group, cohort, profile, or selection references collapse to scoped not-found responses.
- Managed cohorts and their effective policies cannot be changed through generic group APIs.
- Audit and job metadata contain policy identities and safe reason codes, never credentials, tokens, raw remote errors, or personal search text beyond the immutable canonical filter already authorized for the actor.

## Rollout

The migration is additive. Existing template-managed groups are adopted without changing access. Existing custom groups remain custom. Existing people bulk jobs continue using their current action records and worker behavior.

Rollout sequence:

1. schema and adoption support;
2. cohort store and protected-group enforcement;
3. preview and durable job APIs;
4. organization and platform UIs;
5. generic platform API contract publication;
6. a small manual canary, then one full cohort, then broader retroactive application.

Rollback must refuse while a new policy job is queued/running or a cohort revision cannot be represented by the prior schema. It must never silently convert a managed cohort into an editable custom group.

## Verification

Required tests include:

- migration up/down refusal and adoption without policy drift;
- exact-template cohort reuse and derived-cohort independence;
- policy patch validation for all libraries, permissions, playback, transcode, download, request, quality, and limit fields;
- immutable selection and confirmation-token binding;
- 10,000-target execution, batching, cancellation, restart, and exact idempotent replay;
- concurrent identical and conflicting commands across replicas;
- actor revocation, organization policy-revision change, and cross-organization denial;
- inherited-profile movement and custom-profile preservation/opt-in movement;
- restore-default behavior with no policy-less result;
- playback/session revision invalidation after assignment;
- component and browser coverage for every operation and job-result state;
- API contract tests for organization and direct-account platform routes;
- authoritative current-policy reads, profile exceptions, legacy/custom state, observation timestamps, and expected-versus-observed drift;

## Success criteria

- Administrators can safely apply policy to a reviewed large set of existing people from Server UI.
- Organizations may run multiple policy cohorts simultaneously.
- Selection-specific changes affect only selected people and create immutable cohort history.
- Every account remains attached to an enforceable managed policy.
- Jobs survive restarts, expose actionable progress, and leave complete immutable audit evidence.
- Generic platform clients can perform the same operation without Server knowing client-specific products or business concepts.
