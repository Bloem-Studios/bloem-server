# Bloem overlay: Core database ID ranges

**What this overlays.** Silo's
[`docs/architecture/core-id-range.md`](../../../architecture/core-id-range.md). Bloem
adds three sections after Silo's text and replaces nothing. Silo's “Schema
audit” and “Apply and rollback” apply as written. An *overlay* is a Bloem-owned
page that records Bloem's additions to, or replacements of, sections of a
Silo-owned document. The Silo file stays exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** A policy-library array lists the library IDs a policy
grants. Silo has them on `users`, `access_groups` and `invitations`; Bloem adds
its own on entitlement template and policy cohort revisions. Bloem has changed
their integer width twice:

- the September 19, 2026 deployment widened eight such arrays to `bigint[]`
  (`20260919095615_bloem_policy_library_ids_bigint`);
- the October 14 Silo integration aligned them back to Silo's 32-bit
  `integer[]` (`20261014233826_bloem_silo_policy_alignment`).

Since `c60b7008e` (2026-10-26) an account's policy lives on `users` again, where
Silo keeps it. Bloem's per-membership policy columns, their phase table and
rollback snapshots are gone (`20261026003000_bloem_account_policy_on_users`).
This overlay was created in `47ea7c889` (2026-09-23) from Bloem's earlier edits
to the Silo file.

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`migrations/sql/`). Silo has not changed this document since the
previous review (Silo `ea3c10a7f`). This review updated the page for the
October 26 policy move and for the Audiobookshelf server Bloem runs again.

Index: [Bloem documentation](../../README.md).

---

**Location:** end of the document. **Bloem adds:**

## Current policy-library representation

Policy-library arrays use PostgreSQL 32-bit `integer[]`, as in Silo: Silo's
`users.library_ids`, `access_groups.library_ids` and `invitations.library_ids`,
and Bloem's `entitlement_template_revisions.library_ids` and
`entitlement_policy_cohort_revisions.library_ids`. Account policy is on `users`.
An organization membership carries no policy since
`20261026003000_bloem_account_policy_on_users`: it only links an account to its
one organization. Parent identifiers keep exactly the types Silo defines; a
`bigint` parent does not imply wide-ID support in every policy or client
workflow.

`20261014233826_bloem_silo_policy_alignment` converted the arrays after checking
the integer range. Any out-of-range value or bound refuses the transaction
before a partial conversion. NULL inheritance, empty arrays, selected values
and array bounds keep their meanings. Writers must use the aligned types rather
than reintroduce a fork-only `bigint[]` assumption. See
[upstream adapters](../../../architecture/bloem-upstream-adapters.md).

Historical SQL stays byte-identical. `migrations.BloemFS` adapts ordering and
schema differences, including databases that Silo created first. Silo
switching, where Silo and Bloem take turns on one database, is the single flag
`public.bloem_silo_switching` since the October 26 migration; see
[Silo switching](../../../architecture/silo-backend-switching.md). Switching
acceptance must use the pinned Silo source or image that the Bloem source
actually includes. A wider historical schema alone does not show that either
server can write it.

## Historical widening evidence

The September 19 deployment of `418a18b7d` applied
`20260919095615_bloem_policy_library_ids_bigint`, widening eight policy arrays.
Its [deployment record](../../../operations/2026-09-19-xtream-deployment.md#database-and-recovery-evidence)
records the backup, restored-backup rehearsal, row hashes, bounds and trigger
checks. Those facts remain historical evidence. They do not describe the current
array width or authorize rerunning the old migration as an upgrade step.

## Client and rollout boundary

Keep Silo's decimal-string IDs, numeric precision limits and nullable field
semantics. A successful schema conversion is not device acceptance. Review the
[contract adjudications](../../../architecture/bloem-contract-adjudications.md)
and [Silo switching](../../../architecture/silo-backend-switching.md) before
rollout. For table-rewriting migrations, the operator is responsible for
writer quiescence, copy space, pool recycling and recovery planning. Silo's
Audiobookshelf-compatible server, which Bloem runs again since `2393c41ee`
(2026-10-18), resolves library access through Silo's access filter like Silo's
other surfaces.
