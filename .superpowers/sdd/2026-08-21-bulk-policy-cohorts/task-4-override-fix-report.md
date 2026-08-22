# Task 4 effective-policy override fix

## Finding and remediation

Final review correctly identified that policy-job execution changed only
`users.access_group_id`. Nullable account policy overrides therefore continued
to win over the assigned cohort in the authoritative entitlement resolver.

Policy execution now clears every nullable managed account override atomically
with the group assignment: libraries, playback quality, stream/transcode
limits, video/audio transcoding, download/download-transcode, and requests.
The same-cohort fast path also treats remaining overrides as reconciliation
work instead of reporting `already_applied`.

Before the target savepoint can commit, execution re-reads the account through
the authoritative entitlement projection and compares all managed override
fields with the target access group. A mismatch fails safely as the existing
non-disclosing `mutation_failed` target result. Both assignment audit events
now contain the observed effective policy and its deterministic digest, plus
whether managed account overrides were cleared.

Custom profile group semantics are unchanged: custom profiles remain assigned
to their custom group by default and move only with the confirmed
`include_custom_profiles` option.

## TDD and verification

- RED: the override regression completed successfully while the authoritative
  snapshot still exposed `max_streams=1` and conflicting transcode, download,
  quality, library, and request values instead of the premium cohort.
- GREEN: cross-cohort override reconciliation and same-cohort reconciliation
  pass, including raw NULL inheritance and audit equality with the authoritative
  effective snapshot.
- Focused live PostgreSQL policy/custom-profile gate: PASS (`4.776s`).
- Focused live PostgreSQL race gate, including identical enqueue and overlapping
  jobs: PASS (`6.787s`).
- Existing 10,000-target exactly-once regression after the change: PASS
  (`50.107s`).
- Affected package compile-only test, `go vet`, and `git diff --check`: PASS.

No push or deployment was performed. The unrelated `web/package-lock.json`
remains untouched.
