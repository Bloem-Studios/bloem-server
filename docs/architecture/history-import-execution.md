# History import execution

Admin history imports persist a versioned dispatch intent in `history_import_runs`
before reporting acceptance. The intent references source and mapping revisions,
the external user locator, and the original Silo account/profile target. Tokens
remain in the source's encrypted storage and are decrypted only into the claimed
worker's provider. A queued run does not depend on an in-memory provider or wake
signal: each node polls for work as well as accepting local wake signals.
Construction does not start recovery or dispatch. Production installs the stable
identity resolver and run observers, then calls `StartBackgroundWork`; activation
is idempotent. Configuration must finish before any persisted run can execute.

A node reserves local capacity before atomically claiming a queued row. Claims use
`FOR UPDATE SKIP LOCKED` and an incremented generation. Heartbeat, progress, and
terminal writes require the running status and exact generation. Personal import
execution retains its existing provider path and uses generation zero; its legacy
repository entrypoints cannot modify durable claims or terminal runs. This does
not make personal import acceptance restart-durable.

Source, token, and mapping edits remain available during an import. Workers retain
the original target, validate current configuration revisions before provider
execution and each target operation, and repeat validation on their 15-second
heartbeat. A changed or missing configuration stops the run with a review message;
it never redirects work to the new target. Updating a mapping's import timestamp
does not change its configuration revision. A completed claim updates that timestamp
only if the mapping still matches the captured revision and target.

Queued cancellation is immediately terminal. Running cancellation persists a request
and signals a local worker when present. A remote worker observes the request through
validation or heartbeat, stops, and acknowledges the terminal cancellation. An expired
worker with a pending cancellation is reconciled as cancelled. Cancellation does not
undo completed effects or guarantee that an operation already in flight stops before
committing: PostgreSQL queue state and the per-user store are not one transaction.
Configuration changes have the same in-flight limit.

Running jobs with expired heartbeats become terminal failures, never automatic retries.
Some target writes may already have committed before a crash; an administrator can
review the outcome and explicitly create a new run. Legacy queued admin jobs without
reconstructible dispatch metadata fail with an operator-visible explanation. The
continuous orphan sweep applies only to admin-token runs, not personal import jobs.

Admission serializes on the mapping row and checks all active runs. A database trigger
covers legacy insert paths as well as new admissions; a partial unique index additionally
protects new durable jobs. Existing duplicate active rows remain unchanged and block
new admissions until they reach terminal states. The migration does not delete or select
a winner among historical duplicates. Run targets and dispatch metadata are immutable;
foreign-key deletion may detach a mapping while the retained dispatch locator preserves
source-filtered audit history. Terminal execution state cannot be overwritten.

The lock order for enqueue is source, then mapping. Claims and ordinary progress or
terminal transitions lock the run without acquiring a mapping admission lock. Mapping
configuration edits use the same source-before-mapping order. The admission trigger
performs its active-run lookup after the mapping lock wait; a PostgreSQL regression test
verifies that a waiting READ COMMITTED transaction sees the preceding committed run.

Bulk admission reads at most 201 mapping IDs in stable name/ID order and rejects sources
with more than 200 before creating any runs. Supported batches report every mapping as
accepted, active, or failed. Partial success is explicit; transport failures are not an
invitation to automatically replay the batch.

Canonical source responses redact credentials, query strings, and fragments from
legacy addresses and identify configurations that need review. They do not rewrite
the stored address or redirect a queued worker. An administrator must explicitly
review and save a valid address; new writes reject credential-bearing URLs, queries,
and fragments. The original stored configuration revision still guards that edit.
