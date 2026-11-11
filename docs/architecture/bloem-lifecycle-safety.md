# How retiring an organization and deleting a library stay safe

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`; the library deletion parts were updated on 2026-11-10, when
> deleting a library started taking it out of the starting set, and the routes checked
> again on 2026-11-11, after Silo retired its v1 API. Organizations, their retirement and the starting set are
> Bloem additions; library deletion and its job runner are Silo's, and Bloem's guard
> wraps them. This page explains both in plain language; the technical rules are in
> the [For developers](#for-developers) section.

Two things in Bloem remove a lot at once: retiring an organization and deleting a
library. This page walks through how each one decides what to keep, what happens
when part of it fails, and the limits that remain.

## Retiring an organization

![The storefront ends Riverside Club; in one all-or-nothing step Sam's account is deleted, the coach's and Kim's accounts are kept without the membership, and the organization is suspended and kept on record; afterwards the kept accounts' things in the organization are cleaned up](images/lifecycle-safety/retire.svg)

When the storefront ends a reseller organization, Bloem **retires** it:

1. Bloem locks the organization, so no member can be added while it works.
2. It removes each member exactly as it would remove one member on their own:
   - an account with **nothing else to hold it** is deleted;
   - an account that **owns another organization**, owns the platform, or has the
     **admin role** is kept, and only its membership and its devices in this
     organization go;
   - if a member owned the organization, ownership moves before that account goes.
3. It retires the organization: suspended, with its link to the storefront cleared.
   The organization is never deleted outright; its record stays as the owner of
   anything it had.
4. **All or nothing.** If removing any member fails, every change is undone and the
   organization is left as it was.
5. **Afterwards**, Bloem cleans up whatever the kept accounts still had in that
   organization. This finishes even if the request that started it is cancelled.

## Deleting a library

![Five steps to delete a library: ask, switch off and queue, lock, delete the catalog in batches, then in one final step move collections, remove the grants, take the library out of every version of the starting set and remove it](images/lifecycle-safety/library-deletion.svg)

The [starting set](resource-tenancy-foundation.md#the-starting-set) lists the
libraries every new organization receives, and it has versions. Deleting a library
takes it out of all of them:

1. An administrator asks to delete a library.
2. The library is switched off and a deletion job is queued.
3. The worker takes a lock that stops anyone adding the library to the starting set
   while it works.
4. It deletes the catalog in small batches.
5. In a final step it moves shared collections to the other libraries they cover,
   removes the organizations' grants, takes the library out of every version of the
   starting set, removes the library itself, and recounts series that live on in
   other libraries. Records of past grant changes are kept.

Until November 10, 2026, Bloem refused to delete a library that any version of the
starting set named, and nothing could take a library out of it. Such a library could
never be deleted.

What it does not do:

- A large library is not deleted in one transaction. If a deletion fails part way,
  the batches already deleted stay deleted, as in Silo.
- It does not restore anything a failed deletion removed earlier.
- It does not move anyone's progress to another library. Progress on titles that
  exist only in the deleted library goes with them.

## Who can change what

| Who | What they manage |
| --- | --- |
| The storefront (Bloem Garden) | Ending a reseller organization, which retires it. It uses a platform administrator's API key. |
| Platform administrator | Retiring a reseller organization through the same route, and deleting libraries. |
| Organization administrator | Nothing here: retirement and library deletion are platform work. |

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Retiring an organization | `tenancy.MemberService.RetireOrganizationInTransaction` (`internal/tenancy/tenant_retirement.go`) |
| Removing one member | `MemberService.DeleteInTransaction` (`internal/tenancy/members.go`) |
| Retired | `organizations.status = 'suspended'`, `external_service_id` cleared |
| Starting set, its versions | the creation-default entitlement bundle and its revisions (`entitlement_bundle_members`) |
| Library | a `media_folders` row (a root) |
| The lock | the advisory fence `bloem_lock_media_folder_bundle_reference(bigint)` |
| The guard | `internal/catalog/bloem_folder_repo.go`; the job runner wrapper in `internal/adminjob/bloem_runner.go` |
| Taking it out of the starting set | the foreign key `entitlement_bundle_members_media_folder_fkey`, `ON DELETE CASCADE` since `20261110123408_bloem_library_starting_set.sql` |

## For developers

- **API:** retiring is `DELETE /api/bloem/v2/admin/platform/tenants/{id}` on the
  [organizations and administration](../bloem/api/organizations.md#delete-apibloemv2adminplatformtenantsid)
  page. The legacy v1 tenant delete went with Silo's v1 API on 2026-11-11. Library
  deletion is Silo's `DELETE /api/v2/libraries/{id}`, with Bloem's guard described
  in the [libraries API overlay](../bloem/overlays/libraries-api.md).

### Tenant retirement

The tenant-delete route (`DELETE /api/bloem/v2/admin/platform/tenants/{id}`, Bloem's
tenant admin API) uses
`tenancy.MemberService.RetireOrganizationInTransaction`. The transaction locks the
organization before enumerating members, then applies the same deletion rules as an
individual tenant-member removal (`MemberService.DeleteInTransaction`):

- Remove only the retiring organization's membership, and its profiles' devices and
  device settings in that organization, for an account that is kept.
- Preserve accounts that own another organization (`organizations.owner_account_id`),
  own the platform (`platform_security.owner_account_id`), or have the administrator
  role. An account has at most one membership
  (`organization_memberships_one_per_account`), so "another membership" no longer
  occurs.
- Delete an otherwise unowned account through the account provisioner.
- Update ownership before deleting an owner account: the first other active, enabled
  member becomes owner, or none.
- Retire the organization after its memberships have been removed.

Member deletion, ownership changes, command tombstones
(`tenant_member_command_receipts.deleted_at`) and organization retirement commit
together. Any member-deletion failure rolls them all back. The organization row
remains as the historical resource owner; retirement
(`Store.DeleteTenantOrganizationInTransaction`) clears its external service identity,
operator, seats, conversions and owner, appends `-retired-<id prefix>` to its slug,
and suspends it.

After commit, `CompleteOrganizationRetirementAfterCommit` runs the ordinary
member-resource adapter, which purges only the retiring organization's resources for
surviving accounts. It runs with a bounded context that survives client cancellation
(`internal/sessioninvalidation`). Cleanup errors are reported after the durable
mutation has committed.

Regression coverage includes shared identities with a second organization's profile
(`TestTenantOrganizationDeletePreservesOtherOrganizations`) and retirement rollback
after a later member fails
(`TestTenantRetirementFailureRollsBackAllMembersAndOrganization`), in
`internal/api/handlers`. The exclusive-account numeric-ID reuse fixture lived in
`admin_tenant_organization_lifecycle_test.go`, which `b017bfe84` deleted with the
lifecycle receipts; no retirement test has replaced it.

### Library deletion

A library's bundle memberships, in every revision, go with it:
`entitlement_bundle_members_media_folder_fkey` is `ON DELETE CASCADE`
(`20261110123408_bloem_library_starting_set.sql`), so the final transaction's
`DELETE FROM media_folders` removes them. Before that migration the key was
`ON DELETE RESTRICT`. Finding the reference only at the end would have destroyed
media and watch-state records while leaving the library behind, so Bloem refused the
deletion up front (`CheckDeletionAllowed`, `409 conflict` "Library is retained by an
entitlement bundle") and again before the first batch. No code removed a member, so a
library in any revision could never be deleted. A revision therefore no longer keeps
every library it once named; the organizations' grant history is in
`organization_entitlements` and the entitlement audit events.

The worker acquires the canonical `bloem_lock_media_folder_bundle_reference(bigint)`
advisory fence, then locks the library row with `FOR NO KEY UPDATE` until the root is
deleted. A database trigger takes the same fence before inserting or updating a bundle
member's library reference, so a writer adding the library to a bundle during the
deletion waits, then fails on the deleted root, instead of succeeding and being
cascaded away unseen. Removing a member needs no fence. The helper hashes a
namespaced bigint ID; it never narrows folder IDs to 32 bits.

Ordinary scanner FK checks remain compatible with the root lock. Blocking those
checks while a scanner holds an item can create a wait cycle: deletion's separate
batch connection needs that item, while the scanner needs the root that the deletion
guard holds. The guard uses one dedicated connection, and all catalog batches use a
private one-connection pool. A fenced bundle writer occupying the last connection in
the caller's pool therefore cannot prevent deletion from completing and releasing the
fence. Both connections inherit the caller's connection configuration and
`BeforeConnect`/`AfterConnect` hooks; the batch pool also inherits its pool hooks. The
batch connection is opened before any destructive work; initialization failure,
cancellation and normal completion close the owned resources.

Before invoking the deletion executor, its admin-job runner prepares a private
one-connection metadata pool with the original pool configuration and hooks
(`internal/adminjob/bloem_runner.go`). The repository retains the execution's claim
generation. Heartbeats, progress, cleanup-job admission and final job writes use this
pool, so a fenced writer cannot delay heartbeats long enough for another runner to
recover a live claim. Finalization precedes heartbeat cancellation and join, then the
metadata pool closes. Metadata initialization failure does not start catalog
deletion.

The admin-job progress callback remains synchronous. Its progress write and realtime
publication share a one-second deadline. A timed-out callback waits at least thirty
seconds and for available metadata-pool capacity before another attempt, so a
saturated pool does not add a wait to every catalog batch. Intermediate progress is
best-effort during metadata contention; the claim-fenced final job result is recorded
after the deletion guard releases the bundle writer. No background progress callback
can write after execution returns.

A running admin-job deletion owns at most three database connections in addition to
the existing pools. The current admin-job
runner executes one catalog job at a time per runner; replicas may run separate
deletion jobs, so connection budgeting includes three connections per active deletion
worker. After the catalog batches finish, one guarded transaction moves admin
collections that cover other libraries off the root, removes live organization grants
and the root, and recomputes the episode membership denorms of series that survive in
other libraries. Silo retries the collection move in a fresh transaction when
collections change or a deadlock aborts it; under the guard each attempt runs in a
savepoint instead, so a retry never releases the root lock. Entitlement audit events
survive.

Catalog batches retain upstream's resumable, short-transaction behavior. The guard
does not make an entire large-library deletion atomic or restore data from an earlier
failed deletion.

Tests are in `internal/catalog/bloem_folder_deletion_test.go` (for example
`TestBloemFolderDeletionFencesConcurrentBundleReferences`,
`TestBloemFolderDeletionDoesNotBlockScannerForeignKeys`,
`TestBloemFolderDeletionProgressesWithFencedWriterInSingleConnectionPool` and
`TestBloemFolderDeletionFenceSupportsBigintRoots`),
`internal/catalog/bloem_starting_set_test.go`
(`TestDeletingALibraryRemovesItFromEveryStartingSetRevision`,
`TestDeleteLibraryAcceptsAStartingSetLibrary`) and
`internal/adminjob/bloem_library_deletion_runner_test.go`.

### History

The page was first written with two fixes of 2026-10-10: tenant retirement that
preserves shared accounts (`cadc2f312`) and library deletion that preserves
bundle-retained libraries (`5436ed812`, migration
`20261010210634_bloem_media_folder_bundle_deletion_fence.sql`). `b017bfe84`
(2026-10-26) removed Bloem's lifecycle receipts (an `Idempotency-Key` replay layer),
and with them this page's sections on keyed retirement and encrypted invitation
receipts. `c60b7008e` (2026-10-26) limited each account to one membership. The
storefront moved to the native tenant routes on 2026-10-31
([leaving Silo's v1 API](bloem-leaving-silo-v1.md)). On 2026-11-10 production's
original Audio Books and Electronic Books libraries could not be deleted because the
starting set named them; `20261110123408_bloem_library_starting_set.sql` made deletion
take a library out of the starting set instead of refusing.

Technical pages:

- [How organizations work](bloem-organizations.md): the organization's states,
  including retired.
- [How library ownership and grants work](resource-tenancy-foundation.md): the
  starting set and its versions.
- [Multitenant administration](multitenant-administration.md).

The pictures are SVG files under `images/lifecycle-safety/`, drawn by
`scripts/bloem/draw-pictures.py`. Redraw them there, not by hand.
