# Task 4 complete effective-policy digest fix

## Finding and remediation

Follow-up review correctly identified that assignment auditing converted the
observed `EffectivePolicySnapshot` through the durable cohort `Policy` before
hashing it. That conversion omits `AudioTranscodeAllowed`, allowing effective
policies that differ only in the audio-transcode gate to share a digest.

Added `entitlements.EffectivePolicyDigest`, a distinct deterministic digest for
the complete authoritative projection. It includes every effective snapshot
field, preserves nil-versus-empty set meaning, and sorts/deduplicates explicit
library and permission sets before hashing. The existing durable
`entitlements.PolicyDigest` representation and stored cohort digests are
unchanged.

Policy assignment audits now hash their observed effective snapshot directly.
The live audit regression computes its expected digest through the same
complete projection rather than the lossy cohort conversion.

## TDD and verification

- RED: an audio-only digest regression failed to compile because no complete
  effective-policy digest API existed.
- GREEN: otherwise-identical snapshots with different audio-transcode gates
  produce different digests.
- Focused live PostgreSQL policy/custom-profile gate: PASS (`4.950s`).
- Focused live PostgreSQL race gate: PASS (`4.403s`).
- Focused effective/durable policy digest unit gate: PASS (`0.262s`).
- Affected-package `go vet` and `git diff --check`: PASS.

No push or deployment was performed. The unrelated `web/package-lock.json`
remains untouched.
