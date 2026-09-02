# Entitlement templates

Entitlement templates are immutable, revisioned policy definitions managed by
a **platform administrator**. They let an operator apply the same access policy
to an organization or to one directly managed account without manually editing
access groups and profiles.

For retroactive changes to a reviewed set of existing accounts, templates are
materialized as immutable organization policy cohorts and applied through a
durable bulk job. See [Bulk policy cohorts](bulk-policy-cohorts.md). For release,
migration, and bounded-canary procedures, see the
[bulk policy release runbook](bulk-policy-cohorts-runbook.md).

Open **Admin → Platform → Entitlement templates** at
`/admin/platform/entitlement-templates`. Direct-account assignment is at
`/admin/platform/direct-accounts`; organization assignment is on the Platform
organization detail page under **Tenant entitlement**.

## Do not confuse the two entitlement systems

Bloem Server uses the word “entitlement” for two related but different
controls:

- A **policy entitlement template** defines what members may do: playback,
  streams, profiles, transcodes, downloads, requests, permissions, quality,
  and the libraries permitted by the managed access group.
- An **organization library entitlement** grants an organization access to a
  platform-owned library. It is managed under the organization library API and
  has its own `security_revision`.

A policy template cannot grant an organization a platform library it does not
otherwise own or hold a live library entitlement for. Its library selection is
an additional ceiling over libraries already available to the organization.

## Built-in starting points

Fresh and upgraded installations provide these enabled revision-1 templates.
All use `library_ids: null`, so they follow all currently available and future
libraries unless revised.

| Key               | Playback | Streams | Profiles | Transcodes | Downloads | Transcoded downloads | Quality | Requests |
| ----------------- | -------- | ------: | -------: | ---------: | --------- | -------------------- | ------- | -------- |
| `browse-only`     | No       |       0 |        0 |          0 | No        | No                   | —       | No       |
| `viewer`          | Yes      |       1 |        5 |          0 | Yes       | No                   | 1080p   | Yes      |
| `standard`        | Yes      |       3 |        5 |          1 | Yes       | Yes                  | 1080p   | Yes      |
| `premium`         | Yes      |       4 |        5 |          2 | Yes       | Yes                  | 2160p   | Yes      |
| `reseller-member` | Yes      |       3 |        5 |          1 | Yes       | Yes                  | 1080p   | Yes      |

The built-ins are starting points, not hard-coded commercial plans. Revise
them or clone them under a new stable key to match the service being offered.
`browse-only` is protected: a revision under that key cannot enable playback.

## Policy fields

| Field                                  | Meaning                                                                                                                                                                                 |
| -------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Libraries                              | **All libraries** stores `library_ids: null` and dynamically includes libraries made available later. Selecting individual libraries stores their positive numeric IDs as an allowlist. |
| Playback allowed                       | Master gate for starting playback. Turning it off also requires streams, transcodes, and both download controls to remain off.                                                          |
| Maximum streams                        | Concurrent playback ceiling.                                                                                                                                                            |
| Maximum profiles                       | Profile ceiling materialized on the managed group. For managed templates, `0` means primary-profile-only rather than unlimited.                                                         |
| Transcode allowed / maximum transcodes | Whether transcoding may be used and its concurrent ceiling.                                                                                                                             |
| Original downloads                     | Allows downloading the original media file.                                                                                                                                             |
| Transcoded downloads                   | Separately allows preparing/downloading a converted copy; it does not imply original-download access.                                                                                   |
| Maximum playback quality               | Quality ceiling such as `1080p` or `2160p`.                                                                                                                                             |
| Requests allowed                       | Whether members may submit media requests.                                                                                                                                              |
| Allowed permissions                    | `null` permits every access-group permission; an explicit array is an allowlist.                                                                                                        |

Enforcement is server-side. Playback and compatibility entry points fail
closed when playback is disabled, download routes check the relevant download
gate, profile creation observes the managed profile ceiling, and policy changes
invalidate the affected authorization state.

## Create, revise, clone, and archive

1. Open **Entitlement templates** and select **Create template**.
2. Choose a stable lowercase key (`a-z`, digits, and hyphens), display name,
   enabled state, and policy.
3. Save. Creation produces revision 1.
4. To change a template, create a **new revision**. Existing revisions remain
   immutable and visible in History.
5. **Clone** when the new policy represents a different commercial or
   operational concept. The clone starts disabled so it can be reviewed before
   use.
6. **Archive** only when the key must no longer be selected. Applied policy is
   not silently removed from existing targets.

Rollback is also immutable: choose an older revision in History and create a
new latest revision from it. Bloem never edits or deletes historical policy.

## Apply to an organization

1. Open **Platform → Organizations** and select the organization.
2. In **Tenant entitlement**, select an enabled template and exact revision.
3. Run the dry-run preview. Review the previous and proposed key/revision,
   policy, profile movement, library selection, and tenant-wide slot/transcode
   summary.
4. Confirm within ten minutes. The signed confirmation is bound to the actor,
   target, exact template revision, and previewed state.

Apply updates only Bloem's managed default group and its assigned profiles.
Custom groups are preserved. Repeating the same idempotency key returns the
stored result; reusing it for another template or revision is rejected.

## Apply to a direct account

Open **Platform → Direct accounts**, load the numeric account ID, and use the
same preview-then-confirm flow. The server creates or updates the account's
managed template group inside the default organization. Other accounts and
custom groups are not changed.

The canonical API uses `/platform/accounts/{account_id}`. The equivalent
`/platform/users/{user_id}` routes remain available as compatibility aliases.

## Apply to a reviewed cohort of accounts

Do not repeat the single-account apply endpoint in a loop. Use the bulk policy
workflow so one immutable selection, one reviewed preview, and one durable job
cover the entire operation.

- Organization administrators select people under **Admin → Organization →
  People**, choose **Apply policy**, and may inspect immutable history under
  **Policy Cohorts**.
- Platform administrators use **Admin → Platform → Bulk Account Policies** for
  an explicit list of directly managed Server account IDs.
- Applying an exact template revision reuses the corresponding organization
  cohort. A selection-specific patch creates a distinct derived cohort; it does
  not revise the source template or change unselected accounts.
- “Restore default” assigns the protected managed default. It never removes an
  account's policy or leaves the account without an enforceable access group.

The bulk preview reports current cohort distribution, exact target provenance,
field-level differences, already-compliant accounts, inherited profiles that
will move, and custom profiles that will remain or move. The signed confirmation
expires and is invalidated by any material selection, command, actor, route
scope, or policy-state change.

## Audit and reconciliation

Template creation, revision, rollback-as-new-revision, clone, archive, and
successful application produce durable audit events. Organization entitlement
history is available from its detail panel and from:

```text
GET /api/bloem/v1/admin/platform/organizations/{id}/entitlement/audit
```

If an apply reports stale confirmation or changed target state, do not resend
the old token. Reload the target, run a new dry-run, review it, and confirm the
new preview. If an expected built-in template is missing after an upgrade,
verify that migration `20260821210000_enable_builtin_entitlement_templates`
was applied and that the platform-admin context is active.

For a bulk job, poll its policy-specific status route to a terminal state.
Retry a transport failure by reading the job first, then resubmitting the exact
same command with the same idempotency key only when no job was returned. A
different selection, command, or operation scope requires a new key and a new
preview. Per-account skipped/failed reason codes are safe to retain in an
operations record; confirmation and selection tokens are not.

## API clients

API automation must use a platform-scoped admin-context token, perform a
dry-run, and submit the returned `confirmation_token` plus a stable
`idempotency_key` to apply. See the
[native `/api/bloem/v1` reference](../bloem-api-reference.md#entitlement-template-administration)
for exact routes and wire shapes.
