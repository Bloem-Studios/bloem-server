# Progress bootstrap storage

`internal/progresssync` stores immutable full-replacement progress snapshots in
PostgreSQL. This foundation does not register HTTP routes, advertise a capability,
start cleanup, change a selected backend, or change native sync behavior. SQLite
and incremental delta delivery are outside this implementation.

A snapshot belongs to an account, resolved household profile, installation,
generation, effective-access digest, and fixed page size. Its entries project the
existing progress values in seconds, including explicit false completion state.
History-hidden entries and entries outside the supplied visibility policy are
excluded. Empty results are valid full replacements. A future client must stage
all pages, replace only the matching account/profile's server-derived cache, and
preserve its offline event queue. Merging completion with logical OR cannot apply
a full replacement correctly.

## Transactions and authority

Admission runs in repeatable read. It writes the account's generation-state row
before reading progress, serializing admission and import transitions across
nodes. An older repeatable-read waiter retries serialization failures, ensuring
that concurrent admissions cannot exceed the account quota. Ordinary progress
writers can continue; they do not alter an already captured snapshot.

`RotateGeneration` requires the importer's existing `pgx.Tx`. The importer must
commit imported state, the generation transition, and its durable receipt in that
one transaction. Replaying an acknowledged receipt must bypass all three writes.
Generation rotation expires old snapshot payloads in the same transaction while
retaining replay metadata, freeing the new generation's admission quota.
Generation creation is lazy for accounts that have not imported. Page reads hold
a shared generation-row lock until commit, so a concurrent import transition
cannot commit midway through a successful page read. Subsequent reads of an old
generation require a reset.

Construction requires a visibility callback; there is no permissive default. It
must authorize the account/profile and calculate a nonempty digest of effective
access using the supplied transaction, including any policy that changes which
progress entries are allowed. It checks empty snapshots too. Each page rechecks
the digest and every returned item. A changed digest or lost item visibility
requires replacement admission; it never silently edits a saved page.

Production service wiring remains required. It must use the owning access policy,
resolve the profile, and recheck current request and PIN authority before and after
the storage call. A repeatable-read database view alone does not prove that a
request's session or PIN authority remained valid outside that transaction.
`Position` is a domain continuation structure, not an authenticated public token.
A future transport must sign its entire identity, installation, generation,
access digest, snapshot, page size and position with operation/mode binding.
A legacy numeric `since` value is not accepted as a snapshot continuation.

The installation accessor reuses `diagnostics.server_instance_id` and requires
atomic initialization support. It does not rename a setting or reuse the
Jellyfin compatibility identifier.

## Admission and retention

A UUID request ID identifies one admission intent per account/profile. Repeating
it returns the same first page without extending expiry; changing its page size
is a conflict. A new request ID starts a new snapshot, subject to the two-active-
snapshots-per-account limit. The retained expired metadata prevents an old retry
from silently creating a different snapshot during the replay-retention window.
After that window, the intent can be admitted anew.

Admission has a 30-second context deadline and rolls back completely on failure.
The limits are 100,000 visible entries, 64 MiB of normalized retained JSON payload,
and a page size of 1–200. Byte accounting measures PostgreSQL's actual JSONB text
serialization after insertion, including JSON field names and normalized values;
it is not a Go structure estimate or a claim about compressed disk allocation.
Batches contain at most 200 candidate entries. These limits are safety bounds,
not a latency or throughput promise.

Snapshots expire after 15 minutes. Reads enforce expiry independently of cleanup.
`Cleanup` removes expired payloads and retains admission metadata for another
24 hours; it is idempotent across nodes. A future application lifecycle must call
it regularly. The storage package does not start background workers itself.

## Validation and remaining integration

The PostgreSQL suite applies the real new migration to isolated synthetic schemas
and uses two independent pools. It exercises same-intent replay, competing
admissions, immutable update/delete behavior, account/profile and installation
binding, access changes, generation locks and rollback, expiration cleanup,
cancellation rollback, and actual row/serialized-byte cap crossings.

Remaining gates are the importer transaction/receipt integration, production
visibility and current-request authority wiring, durable cleanup lifecycle,
signed transport and capability contract, and coordinated Apple and Android
replacement staging with offline-queue preservation. None is advertised as
implemented by the storage checkpoint.
