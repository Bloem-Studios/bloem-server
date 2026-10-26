# Bloem lifecycle safety

## Tenant retirement

The tenant-delete route uses
`tenancy.MemberService.RetireOrganizationInTransaction`. The transaction locks
the organization before enumerating members, then applies the same deletion
rules as an individual tenant-member removal:

- Remove only the retiring organization's membership for a shared account.
- Preserve accounts with another membership, another organization ownership,
  platform ownership, or an administrator role.
- Delete an otherwise unowned account through the account provisioner.
- Update ownership before deleting an owner account.
- Retire the organization after its memberships have been removed.

Member deletion, ownership changes, command tombstones and organization
retirement commit together. Any member-deletion failure rolls them all back.
The organization row remains as the historical resource owner; retirement
clears its external service identity and suspends it.

After commit, the ordinary member-resource adapter purges only the retiring
organization's resources for surviving accounts. It runs with a bounded context
that survives client cancellation. Cleanup errors are reported after the durable
mutation has committed.

Regression coverage includes shared identities with a second organization's
profile, exclusive-account numeric-ID reuse, retirement rollback after a later
member fails.

## Library deletion

Every bundle revision that references a library retains that root, including
inactive and historical revisions. Deleting catalog rows before discovering
that reference would destroy media and watch-state records while leaving the
library itself behind. Bloem rejects such deletion before the API disables the
library or creates a job, and the worker checks again before its first batch.
Neither check removes or rewrites bundle history.

The worker acquires the canonical
`bloem_lock_media_folder_bundle_reference(bigint)` advisory fence, then locks
the library row with `FOR NO KEY UPDATE` until the root is deleted. A database
trigger takes the same fence before inserting or updating a bundle member's
new library reference, so a bundle cannot retain a library between preflight
and final deletion. Removing a member only reduces retention and needs no
fence. The helper hashes a namespaced bigint ID; it never narrows folder IDs to
32 bits.

Ordinary scanner FK checks remain compatible with the root lock. Blocking
those checks while a scanner holds an item can create a wait cycle: deletion's
separate batch connection needs that item, while the scanner needs the root
that the deletion guard holds. The guard uses one dedicated connection, and
all catalog batches use a private one-connection pool. A fenced bundle writer
occupying the last connection in the caller's pool therefore cannot prevent
deletion from completing and releasing the fence. Both connections inherit
the caller's connection configuration and `BeforeConnect`/`AfterConnect`
hooks; the batch pool also inherits its pool hooks. The batch connection is
opened before any destructive work; initialization failure, cancellation and
normal completion close the owned resources.

Before invoking the deletion executor, its admin-job runner prepares a private
one-connection metadata pool with the original pool configuration and hooks.
The repository retains the execution's claim generation. Heartbeats, progress,
cleanup-job admission and final job writes use this pool, so a fenced writer
cannot delay heartbeats long enough for another runner to recover a live claim.
Finalization precedes heartbeat cancellation and join, then the metadata pool
closes. Metadata initialization failure does not start catalog deletion.

The admin-job progress callback remains synchronous. Its progress write and
realtime publication share a one-second deadline. A timed-out callback waits
at least thirty seconds and for available metadata-pool capacity before another
attempt, so a saturated pool does not add a wait to every catalog batch.
Intermediate progress is best-effort during metadata contention; the claim-fenced
final job result is recorded after the deletion guard releases the bundle writer.
No background progress callback can write after execution returns.

A running admin-job deletion owns at most three database connections in addition
to the existing pools. Preflight opens only the guard connection. The current
admin-job runner executes one catalog job at a time per runner; replicas may
run separate deletion jobs, so connection budgeting includes three connections
per active deletion worker. After the catalog batches finish, one guarded
transaction moves admin collections that cover other libraries off the root,
removes live organization grants and the root, and recomputes the episode
membership denorms of series that survive in other libraries. Silo retries the
collection move in a fresh transaction when collections change or a deadlock
aborts it; under the guard each attempt runs in a savepoint instead, so a retry
never releases the root lock. Entitlement audit events survive.

Catalog batches retain upstream's resumable, short-transaction behavior. This
guard prevents the known permanent bundle-reference failure; it does not make
an entire large-library deletion atomic or restore data from an earlier failed
deletion. A root retained by bundle history needs a separately reviewed root
retirement model before physical deletion can be supported.
