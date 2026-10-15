# Bloem overlay: Core database ID ranges

Bloem additions and overrides for the upstream Silo document [`docs/architecture/core-id-range.md`](../../../architecture/core-id-range.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

## Current policy-library representation

The October 14 integration follows Silo: policy-library arrays use PostgreSQL
32-bit `integer[]`, including membership authority, entitlement/cohort revisions,
rollback snapshots and the users projection. Parent identifiers retain exactly
the types Silo defines; a bigint parent does not imply wide-ID support in every
policy or client workflow.

Migration `20261014233826_bloem_silo_policy_alignment.sql` aligns the
arrays after validating the integer range. Any out-of-range value or bound
refuses the transaction before a partial conversion. NULL inheritance, empty
arrays, selected values and array bounds retain their existing meanings.
Membership and entitlement writers must use the aligned types rather than
reintroducing a fork-only bigint array assumption. See
[upstream adapters](../../../architecture/bloem-upstream-adapters.md).

Historical SQL stays byte-identical. `migrations.BloemFS` adapts ordering and
schema differences, including Silo-first databases and finalized/mirrored
membership projections. Switching acceptance must use the pinned Silo source or
image that the Bloem source actually includes; a wider historical schema alone
does not prove either server can write it.

## Historical widening evidence

The September 19 deployment of `418a18b7d` applied
`20260919095615_bloem_policy_library_ids_bigint`, widening eight policy arrays.
Its [deployment record](../../../operations/2026-09-19-xtream-deployment.md#database-and-recovery-evidence)
records the backup, restored-backup rehearsal, row hashes, bounds and trigger
checks. Those facts remain historical evidence. They do not describe the current
aligned array width or authorize rerunning the old migration as an upgrade step.

## Client and rollout boundary

Keep upstream decimal-string IDs, numeric precision limits and nullable field
semantics. A successful schema conversion is not device acceptance. Review
[contract transition requirements](../../../architecture/bloem-contract-adjudications.md)
and [Silo switching](../../../architecture/silo-backend-switching.md) before
rollout. Writer quiescence, copy space, pool recycling and recovery planning are
operator responsibilities for table-rewriting migrations. The retired
Audiobookshelf runtime is not a policy-array consumer in this fork.
