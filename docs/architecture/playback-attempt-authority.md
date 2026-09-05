# Playback attempt reservation authority

`planstore.Postgres` implements `AuthoritativePlanStoreV3`, an extension of the
existing `PlanStoreV3`. It reserves and commits the existing
`playback_v3_attempts` row. The legacy memory store does not claim distributed
fencing. Lifecycle handlers do not yet call this extension.

A reservation binds the attempt ID to its account, profile, requested file, and
existing request digest. The digest is supplied by the protocol boundary; the
store never derives it from reserialized `NormalizedRequest`. The original
normalized request and retention deadline remain unchanged by publication.

Each reservation has a process-boot UUID, increasing epoch, database-clock lease,
and state. `preparing` means transport preparation may proceed for the winning
caller. Concurrent callers receive `Owned=false`, even if they supply the same
boot UUID. An expired preparing reservation can be reclaimed with a higher epoch.
An active attempt cannot be reclaimed through this operation.

Publication compares the owner, epoch, live lease, identity, digest, and preparing
state. It stores the existing decision response and recipe atomically and changes
the state to `active` or `terminal`. Stop changes preparing or active state to a
retained `stopped` tombstone. A stopped attempt cannot publish again. Retries read
the same retained record and its state; they must not blindly replay the playable
response of a stopped record.

Renewal, publication, and stop acquire a row lock in a short transaction before
evaluating expiry with the database clock. No transaction spans a transport call.
A blocked operation cannot renew an expired lease using a timestamp captured
before it acquired the lock. Client-supplied authority timestamps are informational.
Leases never extend past the database retention deadline. Cleanup uses the database
clock for authority rows; after retention and cleanup, replay identity is no
longer retained.

Rows created by the legacy path have state `legacy`. Its readers omit preparing
and stopped records. Its replan writer cannot update an authority-owned row. The
existing replan lease and base-revision CAS remain the replan mechanism; there is
no parallel revision journal. Migration rollback refuses to remove authority
columns while reserved attempts or tombstones remain.

These operations fence database publication only. They do not revoke worker
grants, stop an already-authorized response, or make a PostgreSQL check atomic with
a SQLite personal-progress write. Before lifecycle activation, delivery must
fence execution and long responses, and progress/stop must commit their sequence,
winning state, and ownership check in the selected personal-state store. Active
owner takeover, lease-based worker execution, and coordinated deployment behavior
remain separate acceptance requirements.
