# Playback attempt reservation authority

`planstore.Postgres` implements `AuthoritativePlanStoreV3`, an extension of the
existing `PlanStoreV3`. It reserves and commits the existing
`playback_v3_attempts` row. The legacy memory store does not claim distributed
fencing. Lifecycle handlers do not yet call this extension.

A reservation binds the attempt ID to its account, profile, requested file, and
existing request digest. The digest is supplied by the protocol boundary; the
store never derives it from reserialized `NormalizedRequest`. The original
normalized request and retention deadline remain unchanged by publication.

Each reservation has a process-boot UUID, row-incarnation UUID, increasing epoch,
database-clock lease, and state. PostgreSQL generates a fresh incarnation on each
insert; it remains stable across takeovers of that row. Every owner/epoch mutation
also compares this incarnation, so retention cleanup and same-boot reuse of an
attempt ID cannot make a delayed old mutation match a new reservation. `preparing` means transport preparation may proceed for the winning
caller. Concurrent callers receive `Owned=false`, even if they supply the same
boot UUID. An expired preparing reservation that has never issued a grant can be reclaimed
with a higher epoch; its uncommitted staged route is cleared. A reservation that
has issued any grant must drain instead.
An active attempt cannot be reclaimed through this operation.

Publication compares the incarnation, owner, epoch, live lease, identity, digest, and preparing
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


## Durable grants and drain

`GrantPlanStoreV3` extends the same store. Grant issuance requires an explicit
`AttemptGrantPolicyV3` maximum duration; the ordinary Postgres constructor keeps
issuance disabled. Test fixtures use five seconds, which is not a protocol or
production tuning promise.

`StageAttemptRoute` freezes the existing plan and executable recipe with their
session, transport, execution-node, and egress-node identities. Repeating the same
binding is allowed; changing it is rejected. Node zero denotes the owning API's
local role. Publication must match the staged binding. Execute grants may use a
preparing or active route; serve grants require the committed active route.
Requests must match the session, plan, transport, purpose and appropriate node.
Every operation also compares the incarnation, boot identity and epoch.

Issuance locks the row before reading database time. The grant expires no later
than the requested duration, configured maximum, owner lease, or retention.
The row's maximum issued deadline is committed before the response is returned.
A lost response still counts; a later shorter grant cannot lower that maximum.
Renewal does not shorten the owner lease beneath already-issued grants.

`BeginAttemptDrain` forbids further issuance and records a stable deadline no
earlier than the maximum issued grant. It can run after owner-lease expiry with
the same fence. `CompleteAttemptDrain` marks the record stopped only once that
database deadline has elapsed. Direct stop cannot bypass an outstanding grant.
These are durable state transitions, not evidence that HTTP responses or FFmpeg
have stopped. They create no successor or active-takeover path.

Runtime recovery must isolate output by incarnation, epoch, and executor
generation, publish immutable recipe locators under authority CAS, and fence
serving and shared-state writes. Obsolete computation can overlap if it cannot
affect the authoritative successor. Positive exit acknowledgement must not be an
indefinite prerequisite for recovery from a dead worker. Those runtime changes
and selected-store progress fencing remain separate from this storage checkpoint.
