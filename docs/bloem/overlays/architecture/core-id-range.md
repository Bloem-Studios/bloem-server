# Bloem overlay: Core database ID ranges

Bloem additions and overrides for the upstream Silo document [`docs/architecture/core-id-range.md`](../../../architecture/core-id-range.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Core database ID ranges”, after the paragraph beginning “This is parent-column standardization. It does not make every application…”. **Bloem adds:**

Bloem's policy-library arrays are an exception to those retained child limits.
Migration `20260919095615_bloem_policy_library_ids_bigint` widens access-group,
invitation, membership, template-revision and cohort-revision library arrays,
including both rollback snapshots and the users compatibility/rollback projection.
Dynamic policies discover existing libraries automatically: encountering a valid
bigint library ID must not prevent an ordinary tenant from being created. NULL
(inheritance), an empty array (explicit denial where supported), values and array
bounds retain their meanings. This is not general wide-ID workflow support.

---

**Location:** section “Core database ID ranges”. **Bloem replaces** the corresponding upstream passage with:

- Account references in profiles, login sessions, settings, progress, downloads,
  notifications, and playback admission state.
- Library references in catalog memberships, scanner state, collection scopes,
  setting values and search documents.
- File references in downloads, subtitle state, matching queues, reader state,
  playback history, and watch progress.
- `page_section_scope_revisions.library_id` and
  `library_collection_order_revisions.library_id`. These revision tables retain
  the integer limit even where the visible section row uses bigint.
- Integer parameters in `refresh_episode_catalog_entry` and
  `refresh_audiobook_item_file_stats`, and integer locals in their trigger paths.
- Account advisory-lock keys in preference/device settings, setting mutations,
  imported progress, diagnostics, requests, and subtitle AI. The two-integer
  lock form and `int32(userID)` conversions need coordinated replacement before
  wide account IDs can be used.

---

**Location:** section “Core database ID ranges”. **Bloem replaces** the corresponding upstream passage with:

Input predicates against widened IDs need bigint parameters, including arrays.
This matters even when a query matches no rows: pgx encodes the parameter before
PostgreSQL executes the query. Output scans from integer children and array
columns remain integer-width until those columns change. Artwork reconciliation
uses a 64-bit numeric cursor for library IDs.

---

**Location:** section “Core database ID ranges”, after the paragraph beginning “The v2 catalog, account, and playback wire contracts retain decimal-string…”. **Bloem adds:**

## Deployment evidence

The policy-array migration was applied in the
[September 19 deployment](../../../operations/2026-09-19-xtream-deployment.md#database-and-recovery-evidence)
of `418a18b7d`, after a fresh full backup and a scoped restored-backup rehearsal.
Production checks preserved selected row hashes, array bounds and policy triggers;
all eight arrays widened and no invalid indexes remained. Writers were quiesced
and application pools recycled. This establishes that deployment's migration
result, not general wide-ID workflow support or a full-catalog recovery-time claim.
The precautions below still apply to other installations.

---

**Location:** section “Apply and rollback”, after the paragraph beginning “The Up and Down each run in one transaction. Down…”. **Bloem adds:**

The Bloem policy-array migration also rewrites tables, including users and
invitations. Quiesce writers, provide copy space and recycle application pools
before resuming traffic. It locks the users projection before selecting its
compatibility or finalized column name. Column-dependent triggers are recreated
inside the same locked transaction with their original definitions, deferral and
enabled modes; policy authority, guards, digests and security revisions do not
change. Down checks every array, including rollback snapshots, before narrowing
any column and refuses either out-of-range bound without losing data. Image
rollback should retain this schema; it does not make older narrow query casts
support wide policies.

No wire format or client capability changes with this repair. Apple, Android and
legacy-number precision limits above remain separate. The shared Audiobookshelf
runtime predicate accepts wide library filters without admitting a different
library or treating an empty allowlist as unrestricted.
