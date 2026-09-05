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

## Executor output and recipe namespace foundation

Staged routes and grant requests bind an `ExecutorNamespaceV3`: the row
incarnation, epoch, and a separately supplied executor UUID. A replacement writer
requires a fresh executor binding. This checkpoint does not add the lifecycle
operation that replaces a staged binding.

Bound HLS output resolves beneath the configured transcode root as
`_authority/<incarnation>/<epoch>/<executor UUID>`. Starting a process exclusively
claims that name before creating its output leaf. The claim survives process
failure and leaf cleanup, preventing another process from reusing the name.
Reconstruction can reuse an existing matching runtime or launch an unclaimed
bound generation; it cannot respawn a consumed generation. Legacy restart rejects
bound runtimes before stopping them. Cleanup removes only the bound leaf, and the
legacy orphan sweep excludes the authority subtree. Claim retirement needs a
separate authority-aware retention policy.

Redis stores bound recipes under generation-specific keys with atomic
put-if-absent semantics. Identical replay preserves expiry; conflicting bytes are
rejected. A locator includes the namespace and the digest of the full versioned
recipe. Reads verify both; deletion compares the exact stored bytes. Bound cards
cannot use legacy session-key storage or reconstruction fallback.

`PublishAttemptRecipeLocator` publishes that descriptor in PostgreSQL after the
immutable Redis write. It compares the current live authority, staged executor,
and expected locator in one update. Initial publication expects no locator; a
confirmed locator can be replayed unchanged. A stale publisher can leave unused
Redis bytes but cannot replace the current pointer. Preparing reclamation clears
the old locator. A locator alone grants no execution or serving permission.

Worker reconstruction has an explicit current-recipe resolver seam, unwired by
default. Bound reads check their signed namespace against the runtime. Legacy
stop and progressive remux reject bound runtimes or tokens because they do not
yet carry the required authority operations. Replacement lifecycle activation and production dependency configuration remain
required before these foundations can serve authority-owned playback. Runtime
enforcement and metadata checks are described below.


## Runtime grant enforcement

`RuntimeGrantV3` converts a database grant into a cancellable runtime lifetime.
Its policy explicitly supplies maximum duration, safety margin, renewal lead,
and watchdog interval. The watchdog interval must be shorter than both the
safety margin and renewal lead. Invalid or missing configuration refuses bound work.
The local deadline is acquisition request-start plus `NotAfter - IssuedAt`, less
the safety margin. Charging the entire round trip avoids depending on matching
worker and database wall clocks. Late replies, invalid bindings, excessive
intervals, and deadlines beyond the returned owner lease are rejected.

Elapsed time must include suspend. Linux uses `CLOCK_BOOTTIME`; Darwin uses
`CLOCK_MONOTONIC_RAW`. Other platforms refuse configuration. Every guarded
operation checks that clock; errors, backwards readings, and expiry permanently
cancel the lifetime. Timers only schedule checks. Renewal is serialized and
bounded, with an independent expiry watchdog while a request is blocked. A
failed renewal or canceled lifetime cannot be revived by a delayed response.
Execute grants admit preparing or active routes; serve grants require active.

The configured margin must cover bounded clock-rate and dispatch uncertainty.
This assumes the database clock does not jump forward beyond that bound during
a live grant. Arbitrary clock steps or scheduling stalls cannot yield a strict
real-time stop guarantee in userspace. On resume, reads recheck elapsed time;
obsolete compute can briefly persist until cancellation is scheduled, but its
output remains isolated. Bytes already accepted by the kernel before expiry
cannot be recalled. Drain waits for the durable grant bound, not an indefinite
exit acknowledgement from a dead worker.

Bound `StartTranscode` acquires an execute grant before preflight or output
claims, checks it again before launch, and parents FFmpeg to its context. Request
cancellation controls admission; adopted execution outlives that request only
while the grant remains valid. Process exit and failed startup close the grant.
`Session.Executor` preserves the binding without a runtime. Metadata fastpaths
require an exact reference and a fresh serve check; that check alone does not
authorize a subsequent long response. Bound manager reconstruction requires an
explicit authoritative recipe resolver and uses its returned immutable recipe.

`planstore.ExecutorRuntime` supplies concrete grant and recipe callbacks using
the configured node identity, PostgreSQL authority and locator, and immutable
Redis recipe reads. The incarnation lookup has a unique index. Neither a lookup
nor a recipe grants execution: issuance rechecks the live row under its lock.
Constructing the adapter does not activate handlers or renew owner leases.

Worker manifest, segment, and acknowledgement responses acquire separate serve
grants. The response wrapper checks before headers and every body write, caps
socket write deadlines by remaining validity, flushes under that deadline, and
interrupts blocked writes on cancellation. Unsupported deadline writers refuse
bound delivery. Disconnect closes the response grant independently of execution.

Public v2 lifecycle and active takeover remain disabled. Central response
integration is limited to the guarded paths below; unbound legacy behavior
remains available. Bound worker stop and progressive remux remain disabled.
Production callback wiring, owner-supervisor activation, generation replacement,
selected-store progress fencing, and deployment/suspend acceptance remain
activation requirements.

## Isolated owner supervision

`RuntimeOwnerLeaseV3` supervises one captured attempt ID, row incarnation,
process-boot owner UUID, and epoch. `Postgres.RenewAttemptLease` renews that exact
fence and returns the database time sampled after acquiring the row lock. A
renewal that waits beyond the persisted lease expiry cannot revive the owner.
The database keeps an existing longer lease, including outstanding grant bounds,
and clamps extensions to attempt retention.

The supervisor uses the same explicit timing policy and suspend-inclusive clock
as runtime grants. Its local deadline is request-start elapsed time plus the
smaller of the returned database lease interval and configured maximum, less
the safety margin. This charges the entire database round trip and bounds local
authority even when the persisted lease is longer. Failed or late renewal,
expiry, cancellation, invalid identity, and clock failure permanently cancel
the supervisor. An independent watchdog checks expiry during a blocked renewal.
Reading a newer owner never changes the captured fence.

This foundation has no public lifecycle wiring. Its context and validity check
are inputs for future guarded preparation and publication; they do not write
progress, install a selected-store fence, replace an executor, or authorize
delivery. Those operations still require their own durable CAS or runtime grant.
Production activation must define the relationship between owner lease and
executor grant policies and satisfy the clock assumptions above.

Executor-bound node tracking uses keys qualified by logical session and the
complete executor namespace. Removal uses the retired runtime's captured
namespace; refresh and shutdown cleanup retain those qualified keys. Delayed
cleanup from an old process cannot remove or refresh a successor's record.
Legacy removal addresses only the legacy key. These Redis records are
diagnostic observations, not ownership proof: multiple generations may remain
visible until cleanup or TTL expiry.

## Central response integration

`GuardExecutorResponseV3` owns the response grant and writer for both worker and
central handlers. Callers must defer its cleanup and pass its returned request
and writer through all response work. Nil namespaces preserve legacy behavior;
callers must first compare actual runtime or metadata bindings so a missing
reference cannot select that fallback. The wrapper caps rolling deadlines and
has no `ReaderFrom` or `Unwrap` path around write checks. Provider errors close
any returned grant. Transport cancellation is exercised over HTTP/1 and HTTP/2.

Native direct-file and local encoded-HLS handlers require a signed executor
reference, authoritative recipe resolution, matching live metadata, and the
complete supported route assignment. A direct route uses execution `none` and
API egress; encoded HLS uses API execution and egress. Bound cold reconstruction
runs under the response context and the manager's execute-grant/resolver checks.
Error paths cannot call unfenced legacy progress or stop finalizers. Progressive
remux, video-copy HLS, and remote proxy chains remain refused until their complete
execution, serving, and finalization paths are guarded. Bound subtitle and font
requests remain refused until extraction and delivery are guarded.

Native signed references bind the recipe and metadata profile, while native
bearer delivery preserves the existing same-account authorization rule.
Compatibility delivery additionally requires the authenticated selected profile
to match. Owner supervision does not change either authorization rule.

Compatibility master, playlist, and segment handlers can serve an existing
local encoded-HLS runtime using the authenticated compatibility session's
stored reference and a matching authoritative recipe. They bypass legacy route
selection, startup, and recipe mutation. A stripped recipe reference is rejected
when local metadata or runtime still carries a binding. Cold compatibility
startup, progressive/remux delivery, copy-video HLS, and remote proxy chains
remain refused for bound sessions. Compatibility subtitle extraction is also
refused for bound metadata.

Ordinary local file reads can still block in the operating system. Response
cancellation and write deadlines prevent later authorized body writes; they do
not promise to interrupt every kernel filesystem operation. Deployment testing
must cover the storage and suspend behavior actually used.

A selected-store progress snapshot establishes a read source, not permission to
mutate progress. Before lifecycle activation, the selected sink must commit its
ownership fence, sequence watermark, progress/hints, and terminal receipt within
one transaction. A control-plane lookup followed by an independent selected-store
write is not atomic. Owner renewal and executor replacement must preserve this
boundary and the original logical playback identity. Dead-worker exit
acknowledgement cannot be an indefinite prerequisite for replacement.

The inactive [selected-store sink](playback-progress-sink.md) now supplies atomic
authority installation, sequenced progress and terminal receipts in PostgreSQL
and SQLite. Its local transaction does not activate the cross-store coordinator
or solve selected-source identity and restore handling.

Legacy progress, stop, and finalization paths must refuse bound sessions before
calling personal-state writers. This includes HTTP and shared control helpers,
compatibility playback reports, and expiry/crash callbacks. Resource cleanup may
act on its exact executor object, but it cannot imply a durable stop receipt or
use a legacy progress/history writer. This restriction keeps delivery-only
integration from implicitly activating an unfenced lifecycle.
