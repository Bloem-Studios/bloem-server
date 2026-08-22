# Bulk policy cohorts release and canary runbook

This runbook releases the Server bulk policy cohort capability and validates it
with one manually selected test account. Commands assume the repository root is
the current working directory. Replace all angle-bracket placeholders locally;
never paste credentials or confirmation tokens into an evidence record.

Production push, deployment, migration, and canary mutations require an
authorized operator. Completing the source review does not authorize those
actions.

The Task 8 source review passed focused feature gates, but it did **not** pass
the full required repository suite: known compile and handler/access failures
remained. That is not a release-green result. Do not deploy this feature until
those failures are resolved (or the owning maintainers update the requirements)
and the complete required suite passes on the exact release artifact.

## 1. Freeze the reviewed artifact

- Record `git rev-parse HEAD` as the intended release SHA.
- Require a clean tracked worktree and review `git diff --check`.
- Fetch the default branch and require the reviewed commit to contain it:

  ```sh
  git fetch origin main
  git merge-base --is-ancestor origin/main HEAD
  ```

- Run the complete repository release gates from `CONTRIBUTING.md`, then the
  focused bulk policy suites, migration refusal tests, and Playwright canary
  journey. Focused feature passes do not substitute for the complete suite.
- Confirm the reviewed artifact includes the post-Task 4 corrections that hash
  the complete authoritative effective policy in assignment audits (including
  the audio-transcode gate) and permit `admin:entitlements:bulk` API keys to use
  the authoritative single-account and snapshot read routes only while their
  owner remains an enabled platform administrator. Run their focused
  regressions against the exact artifact.
- Back up PostgreSQL and verify that the backup is readable before changing the
  deployed image.
- Choose an immutable container tag or digest built from the exact reviewed
  SHA. Do not deploy `latest` for a canary that must be attributable.

Stop if the branch cannot fast-forward the default branch, any required or
focused gate fails, the database backup is unavailable, or the image
provenance does not match the reviewed SHA. A failure documented as a known
baseline is still a failed release gate until it is fixed or the requirement is
explicitly changed by its owner.

## 2. Assess migration and rollback readiness

Before deployment:

1. Read both cohort migrations and run `make migrate-status` against the target
   environment.
2. Confirm no policy job is currently `queued` or `running` if a schema
   rollback might be needed during the change window.
3. Confirm whether any derived or managed-default cohort already exists. Such a
   cohort intentionally makes rollback through the cohort migration refuse.
4. Keep the pre-deployment database backup until the canary and observation
   window are complete.

The application applies pending migrations before opening its listener. Follow
startup logs and do not restart it during migration. A previous image alone is
not a database rollback.

Rollback rules are fail-safe:

- `20260822100000_admin_people_policy_jobs` refuses down while a new policy job
  is queued/running.
- `20260822090000_entitlement_policy_cohorts` refuses down when a derived or
  managed-default cohort cannot be represented by the old schema.
- Never delete job/cohort rows or remove managed markers to force a down
  migration. If refusal is expected, roll forward or restore the reviewed
  backup under the incident plan.

## 3. Deploy and verify the exact build

Deploy the immutable image using the normal deployment procedure, then verify:

```sh
curl -fsS <server-origin>/api/v1/health
curl -fsS <server-origin>/api/v1/ready
```

With a platform-admin session, read
`GET /api/v1/admin/system/build` and require its `revision` to equal the full
reviewed SHA. Record the immutable image digest as a second provenance check.
Do not begin mutations when liveness/readiness fails, the revision differs, or
the database reports an unexpected migration state.

Also confirm that the organization **People** and **Policy Cohorts** pages (or
the platform **Bulk Account Policies** page for direct accounts) load without
console/network errors.

## 4. Select the bounded canary

Choose one non-administrator test account that may safely receive a temporary
policy change. Record only its numeric Server account ID and organization UUID
or the word `direct`; do not record its email or profile names.

Before mutation:

- ensure no unrelated bulk job targets the account;
- identify the account's exact current group/cohort and a valid restoration
  target;
- inventory inherited and custom profiles;
- choose one enabled exact template revision whose temporary behavior is known;
- choose a known media item and an allowed/denied behavior for playback
  validation;
- record that item's `content_id` and optional `file_id`, plus one download
  quality whose expected result exercises either the original-download or
  transcoded-download gate; and
- keep `include_custom_profiles=false` for the first canary unless movement of
  custom profiles is itself the behavior under test.

Use the appropriate authoritative read before preview:

```text
GET /api/v2/admin/platform/accounts/{account_id}/entitlement
GET /api/v2/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement
```

Save the safe response locally for comparison. For an evidence-only digest of
the observed effective policy projection:

```sh
jq -Sc '.policy' < before.json | shasum -a 256
```

This local digest identifies the observed projection; it is not a replacement
for the Server-returned target/cohort policy digest.

## 5. Preview and apply

In the UI, select only the canary account, choose **Apply template**, and select
the exact key/revision. In the preview require:

- `matched = 1` and `excluded = 0`;
- the current cohort/group agrees with the authoritative read;
- the target template key/revision and policy digest are exact;
- every field difference is expected;
- inherited/custom profile counts match the inventory; and
- custom profiles remain unless explicitly opted in.

Capture safe preview evidence: counts, expiry, current/target identities,
target policy digest, and field differences. Do not capture either token.

Confirm once. Record the returned `job_id`, then poll the policy-specific status
route until terminal. Require `progress_current = progress_total = 1`, no
unexpected failed record, and only an expected `already_applied` skip if the
canary intentionally chose an already-compliant target.

If enqueue times out, follow the safe-retry procedure in
[Bulk policy cohorts](bulk-policy-cohorts.md#monitoring-cancellation-and-retry).
Do not create a second idempotency key merely because the first response was
lost.

## 6. Verify behavior and audit evidence

Read the account authoritatively again and require:

- target group and cohort/template provenance match the reviewed preview;
- account policy revision advanced for a successful assignment;
- the complete effective policy matches the target;
- inherited profiles moved; and
- custom profiles stayed on their prior groups unless movement was explicitly
  confirmed.

Authenticate as the test account and verify one ordinary profile read plus a
playback start against the chosen media item. Confirm the result matches the
new playback, library, quality, and stream-transcode gates. Playback does not
prove either download gate.

Exercise the download API separately with the same test account and profile:

1. Read `GET /api/v1/downloads/capability` and require
   `download_allowed`, `quality_presets`, and `transcode_user_allowed` to agree
   with the authoritative effective policy and server-level feature state.
2. Submit exactly one `POST /api/v1/downloads` for the recorded `content_id`
   (and `file_id` when needed) using the chosen `quality`. Use `original` to
   exercise the original-download gate or a listed bitrate preset to exercise
   the transcoded-download gate.
3. For an allowed case, require `202`, record the safe download ID, requested
   and effective quality, then delete the canary row with
   `DELETE /api/v1/downloads/{id}` and require `204`. For an expected denial,
   require `403` with the appropriate stable `forbidden` or
   `transcode_disabled` code and require that no download row was created.

Check the administrative audit view for the job and assignment events,
expected revisions, safe result counts, and absence of credentials or raw
infrastructure errors. Require the assignment audit's
`effective_policy_digest` to describe the complete post-assignment effective
policy, including the audio-transcode gate, rather than only the durable cohort
projection.

Record the post-apply observed-policy digest using the same local command as
the before state.

## 7. Restore and re-verify

Create a fresh one-account selection and preview. Restore the exact prior
cohort when one existed; otherwise use **Restore default** only when the before
state was the managed default. Never approximate a custom or legacy state with
a template.

Confirm the second reviewed job with a new idempotency key, poll it to terminal,
and repeat the authoritative policy, profile, playback, download capability,
actual download-route, and audit checks. The restored policy digest and profile
assignments must equal the captured before state, except for expected monotonic
revision/timestamp changes. Remove any successful restore-check download row.

If the original state cannot be represented by a safe reviewed command, stop
before the first mutation and choose another canary account.

## 8. Evidence record and release decision

Record this checklist without credentials or personal data:

| Evidence | Value |
| --- | --- |
| Reviewed/deployed full SHA | |
| Immutable image digest | |
| Complete required repository suite | pass/fail; no baseline waiver implied |
| Focused bulk/migration/browser suites | pass/fail |
| Complete effective-policy audit digest fix present | yes/no; audio-transcode regression result |
| Scoped API-key authoritative read fix present | yes/no; single/snapshot regression result |
| Scoped API-key canary (when API-key automation is used) | owner currently enabled platform admin; single/snapshot reads pass/fail |
| Build endpoint revision match | yes/no |
| Health and readiness timestamps | |
| Migration versions/status | |
| Scope (`organization UUID` or `direct`) | |
| Test Server account ID | |
| Before group/cohort/template and observed-policy digest | |
| Apply preview counts, target digest, and expiry | |
| Apply job ID and succeeded/skipped/failed counts | |
| Post-apply group/cohort/template and observed-policy digest | |
| Profile and playback checks | pass/fail + safe notes |
| Download capability and actual route check | quality; expected/actual status; safe download ID removed |
| Restore job ID and succeeded/skipped/failed counts | |
| Restored group/cohort/template and observed-policy digest | |
| Audit evidence checked | yes/no |
| Rollback eligibility/refusal condition | |
| Operator and observation-window end | |

Proceed beyond the bounded canary only when the complete required repository
suite and all focused gates passed on the exact artifact, the build is healthy,
both jobs are terminal with expected counts, the actual download-route checks
match policy, authoritative state was restored, and audit evidence is complete.
Otherwise stop broader application, leave no new jobs queued, and follow the
pre-agreed roll-forward or backup-restore path.
