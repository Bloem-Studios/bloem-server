# Bulk policy cohorts

Bulk policy administration changes the effective entitlement policy of a
reviewed, immutable set of existing accounts. It supports up to 10,000 Server
account IDs per job and uses the same protected access-group enforcement as
entitlement templates.

Use this workflow for retroactive assignments and cohort moves. It is not a
dynamic rule: accounts that match the same filter later are not changed.

## Concepts and invariants

- A **template revision** is a reusable global policy definition. A bulk
  operation never edits it.
- A **policy cohort revision** is an immutable organization-scoped effective
  policy backed one-to-one by a protected access group. Exact applications of
  the same template revision converge on the same organization cohort.
- A **derived cohort** is a complete policy produced by applying an explicit
  patch to an existing cohort. It has its own protected group and cannot change
  accounts outside the reviewed selection.
- The **managed default** is the organization's protected default group.
  Restoring default assigns it; there is no remove-policy operation.
- An **authoritative read** resolves current account and profile access groups,
  policy provenance, and complete effective policy from Server state. A caller's
  expected template or provisioning state is never treated as current truth.

Cohort-backed groups cannot be updated, deleted, made default, or demoted
through the generic access-group API. Cohort revisions cannot be updated or
deleted. Archive state affects discovery only and never removes an applied
policy.

## Interfaces

Organization administrators use:

- **Admin → Organization → People** to select people and open **Apply policy**;
- **Admin → Organization → Policy Cohorts** to inspect source revision, policy,
  lineage, member count, creator/time, and archive state; and
- the organization people policy routes under
  `/api/v2/admin/organization/people` for preview, enqueue, status, and cancel.

Platform administrators use **Admin → Platform → Bulk Account Policies** for an
explicit list of directly managed Server account IDs. Equivalent organization
and direct-account APIs are documented in the
[native `/api/v2` reference](../bloem-v2-api-reference.md#platform-entitlement-cohorts-and-bulk-policy).

Authoritative platform reads are:

```text
GET  /api/v2/admin/platform/accounts/{account_id}/entitlement
GET  /api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement
POST /api/v2/admin/platform/accounts/entitlement-snapshots
POST /api/v2/admin/platform/organizations/{organization_id}/entitlement-snapshots
```

The bulk snapshot body is `{ "account_ids": [41, 42] }`. All results share one
`observed_at` timestamp. Each successful item contains current account group,
cohort and source-template provenance, policy revision, complete resolved
policy, and every profile's effective policy plus `inherits_account`. Missing
or foreign accounts return a safe per-account `not_found`; policy provenance is
reported as `managed`, `custom`, or `legacy_unmanaged` without invention.

## Review and apply

1. Select accounts. Organization administrators may use the existing people
   filters; platform direct-account operations require an explicit bounded ID
   list.
2. Choose exactly one operation. No cohort or template is selected by default.
3. Create a preview. Review matched/excluded counts, current cohort
   distribution, target key/revision and policy digest, every field difference,
   already-compliant accounts, and profile movement.
4. Decide whether custom profiles should move. The default is **off**.
5. Confirm before the displayed expiry. Confirmation is bound to the immutable
   selection, exact command, actor, organization policy revision, route scope,
   target revision, and custom-profile choice.
6. Enqueue with one stable idempotency key and poll the returned `job_id` until
   `completed`, `failed`, or `cancelled`.
7. Re-read authoritative account policies and compare them with the reviewed
   target. Do not infer success only from the enqueue response.

Available operations are:

| Operation | Effect |
| --- | --- |
| Assign cohort | Moves selected accounts to an existing active cohort revision. |
| Apply template | Reuses or creates the exact cohort for one enabled template revision. |
| Derive cohort | Applies an explicit deterministic patch and creates/reuses a separate immutable derived cohort. |
| Restore default | Moves selected accounts to the protected managed default; never to no policy. |

Patches use explicit `add`, `remove`, `replace`, `all`, `none`, or
`unrestricted` modes where applicable. They never toggle. Omitted fields stay
unchanged, and invalid policy dependencies or no-op derivations are rejected.

## Profile behavior

A profile is inherited when its access group equals the account's current
access group at preview and execution time. Inherited profiles move with the
account. A profile on another group is custom and remains unchanged unless the
operator explicitly confirms **Include custom profiles**.

Execution rechecks every account, membership, access group, policy revision,
and profile classification. A stale target is skipped with a stable reason
instead of applying a now-unreviewed change. The acting account is also skipped
when the workflow could affect its required administrative authority.

## Monitoring, cancellation, and retry

Jobs commit bounded batches and resume after a process restart without
repeating completed account records. Each result exposes progress plus
successful, skipped, and failed counts; skipped and failed records carry only a
Server account ID and a stable safe reason.

Cancellation is best-effort at a batch boundary. A terminal job cannot be
cancelled. When a request times out or its response is lost:

1. Query the job if a `job_id` was received.
2. If no job ID was received, resubmit the exact same payload with the same
   idempotency key.
3. Treat the returned original job as the outcome of an exact replay.
4. If the server returns `idempotency_conflict`, stop: the key was reused with a
   different selection, command, or route scope.
5. If confirmation or selection is stale/expired, create and review a new
   preview and use a new idempotency key.

Do not automate retries of per-account failures by inventing a new target set.
Review the safe reasons, create a fresh selection containing only intended
targets, and preview again.

## Authorization and safe records

Organization routes require a current organization-admin context and can
affect only active memberships in that organization. Platform routes require a
platform-admin context, or a scoped API key owned by an enabled platform admin
with `admin:entitlements:bulk`. Cross-organization references collapse to safe
not-found responses.

Retain job IDs, organization UUIDs, Server account IDs, cohort/template
identities, policy digests, counts, timestamps, and stable reason codes. Do not
retain bearer credentials, API keys, selection tokens, confirmation tokens,
email addresses, personal search text, raw database errors, or upstream
provider errors.

## Migration and rollback boundary

The migrations adopt existing exact-template groups without changing policy or
assignments. Existing custom groups remain custom, and existing people jobs
continue under their prior action contract.

Schema rollback refuses while a policy job is `queued` or `running`. It also
refuses when a derived or managed-default cohort cannot be represented by the
prior schema. Never bypass these checks or manually clear cohort markers: doing
so could turn an immutable managed policy into an editable custom group. Follow
the [release and canary runbook](bulk-policy-cohorts-runbook.md) for preflight,
rollback assessment, and bounded verification.
