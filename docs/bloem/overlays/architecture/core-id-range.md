# Bloem overlay: Core database ID ranges

This overlay covers Silo's [`docs/architecture/core-id-range.md`](../../../architecture/core-id-range.md), which stays exactly as Silo wrote it.

Every row in the database has a number that identifies it, and each kind of number
has a size: a smaller one (32-bit) or a larger one (64-bit). Silo's page explains
the sizes of its core tables. Bloem adds notes on **policy-library lists**: the
lists of library numbers that say which libraries an account, a group, an
invitation or a plan may use.

## What Bloem changes

- **The lists use Silo's size.** Bloem once widened these lists to the larger size,
  then moved them back to Silo's smaller size so that Silo and Bloem can share one
  database. Code that writes them must use Silo's size.
- **Bloem's plans have lists too.** Plan revisions and policy cohort revisions keep
  library lists of the same size.
- **An account's limits live on the account.** Since October 26, 2026, an
  organization membership holds no limits of its own; it only links an account to
  its one organization.

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Policy-library list | an `integer[]` column such as `users.library_ids` |
| Plan revision | `entitlement_template_revisions` |
| Policy cohort revision | `entitlement_policy_cohort_revisions` |
| Sharing one database with Silo | Silo switching, `public.bloem_silo_switching` |

## For developers

### Section by section

---

**Location:** end of the document. **Bloem adds:**

#### Current policy-library representation

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

Existing migration SQL stays byte-identical. `migrations.BloemFS` adapts ordering and
schema differences, including databases that Silo created first. Silo
switching, where Silo and Bloem take turns on one database, is the single flag
`public.bloem_silo_switching`; see
[Silo switching](../../../architecture/silo-backend-switching.md). Switching
acceptance must use the pinned Silo source or image that the Bloem source
actually includes. A wider schema alone does not show that either
server can write it.

#### Client and rollout boundary

Keep Silo's decimal-string IDs, numeric precision limits and nullable field
semantics. A successful schema conversion is not device acceptance. Review
[Silo switching](../../../architecture/silo-backend-switching.md) before
rollout. For table-rewriting migrations, the operator is responsible for
writer quiescence, copy space, pool recycling and recovery planning. Silo's
Audiobookshelf-compatible server resolves library access through Silo's access filter like Silo's
other surfaces.

### Related pages

- [How plans work](../../../architecture/bloem-plans.md): plan revisions and
  policy cohorts.
- [Silo backend switching](../../../architecture/silo-backend-switching.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
