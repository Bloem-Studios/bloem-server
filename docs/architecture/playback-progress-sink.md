# Atomic selected-store playback writes

`userstore.PlaybackProgressSink` is an optional internal capability implemented
by both PostgreSQL and SQLite. It commits playback ownership checks, sequence
receipts, progress, version hints and terminal history in the selected account
database. It has no public playback lifecycle wiring.

The sink has three mutation operations and one state read:

- `InstallPlaybackAuthority` inserts an absent binding or advances an exact
  expected predecessor to a higher epoch in the same attempt incarnation.
- `ApplyPlaybackProgress` accepts a sequenced source sample under an exact
  active fence.
- `StopPlaybackProgress` commits terminal state and eligible history with the
  final accepted sample.
- `ReadPlaybackProgress` returns persisted reconciliation state. Reading it
  does not authorize a later write; mutations recheck their fence under lock.

Unsupported providers fail explicitly. There is no fallback to sequential
legacy setters or to another account database.

## Identity and replay

The selected provider supplies account scope. A sink row additionally binds
profile, logical session and resolved progress target. Its authority identifies
an attempt, opaque row incarnation, process-boot owner and positive epoch.
Epochs from different incarnations are not comparable. Changing target,
incarnation or owner within the same epoch cannot overwrite the row.

Initial installation requires an absent predecessor. Advancement requires an
exact predecessor and preserves the last accepted sample and its original
receipt fence. Replaying the installed binding returns its current state,
including a stop receipt; it does not reopen a stopped session.

Samples contain a positive signed 64-bit sequence, raw source position and
duration, pause state, version hints, thresholds and a persistence-disabled
decision. Admission must resolve policy from trusted server state and retain
the same envelope for retries. Re-resolving mutable policy for an existing
sequence may cause a conflict; a new admitted decision needs a new sequence.

The shared transition code hashes validated canonical payloads. Non-finite or
negative positions/durations and invalid sequence/policy values are refused.
Equivalent zero representations hash identically. Authority is checked before
replay; the sample digest excludes the fence so a valid successor can replay
the same logical sample while retaining its original receipt.

A lower sequence returns the current accepted state without writing. An equal
sequence with the same payload replays; a changed payload conflicts. A higher
sequence wins even when its position is earlier: sequence42 at120 seconds
supersedes sequence41 at600 seconds. Client timestamps and maximum position
do not decide ordering. Counters from distinct logical sessions are unrelated.

## Personal projections and stop

Receipts retain the raw source tuple independently of progress normalization.
Completion can reset the resume projection to zero while the receipt still
records the source position needed for terminal history.

Disabled and zero-position samples advance the watermark without changing
personal progress, hints or history. Positive below-threshold heartbeat samples
preserve the existing behavior of suppressing progress while allowing hints
on an existing row. A below-threshold stop suppresses both progress and history.
Completed state remains latched, strict threshold comparisons are preserved,
and a later rewatch can acquire a resume position without clearing that latch.

Stop accepts an optional final sample with the same sequence rules. An older
final sample cannot replace the latest tuple but may close the session. An
equal conflicting sample aborts the entire stop. No-sample stop uses the last
accepted tuple; with no tuple it creates a terminal receipt without inventing
history. Suppressed samples remain terminal without falling back to an older
projected position.

Eligible progress, hints, visible playback history, history ID and stop receipt
commit together. Exact stop replay returns its receipt with no writes. Deleting
history later does not delete the receipt or cause replay to recreate history.
Neither later heartbeats nor authority advancement reopen stopped state.

## Transaction and observer boundaries

PostgreSQL locks the account source gate, then takes the existing profile
history lock before locking the sink row.
This serializes sink writes with history hiding even when a hidden-watermark
row does not exist yet. Projection statements preserve visibility adjustment,
the watched latch, `hide_from_continue`, history source/stable identity and
offline-sync triggers. `synced_seq` remains the server sync cursor, separate
from the playback client sequence. Replay does not update it.

SQLite pins one connection and obtains `BEGIN IMMEDIATE` before reading any
fence or receipt. Every statement runs on that connection with the request
context. A failure rolls back the whole mutation; failed rollback discards the
connection. No database-pool setter is called from inside the transaction.

Both implementations return projection facts only after successful commit and
return no success result on a commit error. The notification wrapper forwards
the capability explicitly and queues interest recomputation from committed
facts. It does not pre-read or predict the winning state. Replay, stale samples
and rollback emit no new effects. External completion/scrobble delivery is not
activated by this storage capability. Future completion observers must use a
newly inserted completed history result, including completed rewatches.

Observer delivery remains best-effort. A lost commit reply can leave durable
data without a notification. Exactly-once external delivery would require a
separate outbox and consumer deduplication protocol.

## Exact source handles

`PlaybackSourceProvider.OpenPlaybackSink` accepts an exact backend, account,
canonical source UUID and positive selection generation. Its handle retains
that reference for its lifetime. Every operation checks the source marker in
the same transaction as the sink state. Missing, mismatched, quarantined and
sealed markers refuse bound operations. Once a marker exists, ordinary
unbound sink calls fail rather than following the latest selection. Empty
marker tables preserve the earlier unconfigured sink behavior.

Migrations create empty marker tables. Opening a handle neither provisions nor
activates a source. PostgreSQL holds a shared account advisory lock before
locking its marker row; future first-marker provisioning must take the
exclusive advisory lock using the same account key. This also orders writes
that observed an absent marker. Marker updates serialize with bound writers
through the marker row lock.

SQLite opens only an existing account file, with no schema migration or journal
mode conversion. It validates the marker and verifies WAL. Each actual pinned
writing connection must use and verify `synchronous=FULL` before
`BEGIN IMMEDIATE`; the marker and receipt checks then execute in that
transaction. Durability depends on the operating system, filesystem and
storage honoring synchronization. Transaction and recovery tests do not
establish physical power-loss durability.

Each SQLite handle owns its connection pool independently of the legacy user
database cache. PostgreSQL handles share the provider pool. Closing a handle
waits for its operations, rejects later calls and does not close another
handle or the provider. Notification wrappers preserve this ownership and the
exact source reference.

SQLite schema 24 adds source markers after schema 23 added sink receipts.
The personal-data bridge still accepts only its explicit schema 22 contract.
Frozen schema 23 and current schema 24 sources remain unsupported; neither
authority receipts nor source markers are implicitly imported as personal data.

## Activation requirements

Local sink atomicity is not a transaction spanning control PostgreSQL and
SQLite. The opt-in initial handler reserves pending authority, installs its fence in
the selected sink, then CAS-activates the same control intent. Recovery
after installation moves forward; it must never roll the sink fence back.
An old write that commits before sink advancement is ordered before that
advancement. Afterward its fence is stale. Control-plane lease expiry alone
does not atomically revoke a selected SQLite writer.

Operational source registration, restore/copy cutover, coordinated receipt retirement,
production lifecycle enablement and executor replacement remain inactive. The
explicitly configured initial handler covers staged start, sequenced progress
and normal stop against an already admitted source. Exact source handles detect a different marker, but a
persisted UUID cannot prove that a restored copy contains current fences.
Those storage-topology and recovery rules must be implemented before activation.
