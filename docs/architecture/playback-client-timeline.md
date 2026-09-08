# Bound client audiobook timelines

The `client_bound` progress mode uses one admitted playback session to carry a
part-local transport clock and persist a book-global resume position. It reuses
the captured source, sink, progress sequence and terminal receipt. It does not
upload a second copy through `/sync/progress`.

This contract requires the trusted catalog resolver and initial/progress/stop
runtime integration. Schema and storage support alone do not enable it. The
`bound_client_timeline` playback capability remains absent until the runtime
explicitly opts in with those dependencies configured. Operational account
admission also requires the intended clients to adopt the contract.

## Discover and pin the manifest

An authenticated, profile-scoped client reads
`GET /api/v2/playback/timelines/{file_id}?installation_id=...`. The anchor file
identifies the chosen edition. The application checks the exact installation,
catalog access, audiobook membership and edition before resolving a complete,
ordered manifest. Responses use `Cache-Control: no-store`.

The response contains `installation_id`, `timeline_id`, `media_item_id`,
`edition_id`, `duration_seconds` and ordered `parts`. Each part contains its
`file_id`, `offset_seconds` and `duration_seconds`. Native v2 IDs are strings;
all timeline positions and durations are seconds. The bounded manifest has at
most 4096 parts, with distinct positive file IDs and finite positive durations.
Unknown or ambiguous catalog membership, ordering or durations must refuse;
client-provided offsets or display heuristics cannot supply authority.

`timeline_id` is a canonical SHA-256 digest of item identity, edition identity
and the complete ordered file/duration list. The same edition manifest has the
same digest for every selected part. Clients retain the returned snapshot to
resolve saved global resume positions, chapters, next-part targets and
cross-part seeks. The existing detail file order is not this authority.

A v2 start requests `progress_persistence="client_bound"`, advertises
`bound_client_timeline` in `client_features`, supplies the discovered
`timeline_id`, and sends explicit part-local `start_position`, including zero.
Before installing the sink, the initial runtime resolves the trusted manifest
and compares that exact digest. A changed manifest returns a conflict; the
server must not reinterpret the target under new ordering or durations.

The playable decision includes `progress_timeline`: `timeline_id`,
`media_item_id`, `file_id`, `part_offset_seconds`, `part_duration_seconds` and
`duration_seconds`. The selected file must belong to the pinned manifest.
The initial activation captures this mapping once, alongside the existing
source/admission, scope/fence, policy, version hints and history identity.
Existing installed bindings are never upgraded or changed in place.

## One sequence, two clocks

Existing v2 progress and stop bodies carry `timeline_id` for a client-bound
session. It is required even for a stop without a final position, and is rejected
for other persistence modes. `position` remains part-local in ordinary progress
and the final stop sample. The server validates the local range and computes:

`item_position = captured part offset + local position`

The sink persists the global sample using the captured item duration, policy
and hints. The live session projection keeps the local clock. In particular,
the global position returned by the sink must not be sent to the transport as
its local position.

The accepted response retains `sequence`, local `position` and `is_paused`, and
adds `timeline_id` plus global `item_position` for this mode. Global zero is
present rather than omitted. A receipt describes the stored accepted sequence,
not the latest attempted value. The existing atomic sink sequence rules apply:
identical replay returns the receipt, a changed payload at the same sequence
conflicts, and a lower sequence cannot regress progress. The sink sample and
digest format do not change.

Clients capture account/profile/installation, attempt/session and timeline
before dispatch. They preserve the exact command through uncertainty, and
apply receipts only to that captured context. A timeline digest is identity,
not a bearer grant. A later account, profile, installation, source or session
cannot authorize replay of the old command.

## Multipart transitions

A next-part transition, including a cross-part seek, first resolves the old
part's terminal stop receipt. A `202` draining response, lost response or error
holds the transition. Local teardown does not establish terminal server state.
The client retains the old stop and target intent; it must not start another
part to escape uncertainty.

After terminal confirmation, the client may start the distinct next-part
playback attempt using the selected file and the same pinned manifest digest.
It captures the new session binding and begins its sequence stream. Late old
part reports cannot write through a terminal sink. If the manifest changed,
the next start refuses; only a new explicit user intent may discover and choose
a new mapping. An unresolved old command is never rebased.

Owner-loss abandonment is a separate terminal result. An exact retained START
or STOP may return `202` with `outcome: "draining"` and a recovery record, then
complete with `recovery.state: "aborted"`. The recovery record binds the original
attempt and session to a stable `recovery_id` and `reason: "owner_lost"`. START
completion is a nonplayable `201` decision with terminal reason
`playback_owner_lost`; STOP completion is a `200` abandonment receipt, not an
ordinary matching StopID receipt.

The web player persists this record before releasing the original journal
blocker. It retains the exact abandoned request bytes. Only the server's optional
accepted Last may refresh bound progress; the client's attempted final position
is not confirmation. A legacy journal without the original attempt ID cannot
accept this recovery union.

Abandonment cancels the captured next-part, chapter-replacement or subtitle
fallback intent. It must not resolve the ordinary STOP-success continuation,
autoplay another part, or leave a retry action that will later run that intent.
The ended audiobook player closes, and a later explicit Play may create a new attempt.
Malformed recovery, changed identities, generic errors and lost replies keep the
original command unresolved.

An ordinary same-part replan preserves the captured timeline. It cannot silently
select a different part or mutate the initial binding. This serialized part
policy does not implement a same-attempt cross-file route replacement.

## Legacy queues and manual writes

Existing `client` sessions and queued `/sync/progress` uploads remain unbound.
Their payloads do not contain captured playback authority and cannot be converted
into client-bound commands after admission. Do not relabel them as imports,
refresh their identities, or create attempts to replay them. Fresh client-bound
playback reports through its own captured session independently of those queues.
Genuine manual edits and trusted imports remain separate operations.

The v1 playback start handler does not accept `client_bound`. There is no v1
fallback for missing capability or an unavailable manifest. See
[first playback admission](playback-first-admission.md) for operational
preconditions; database eligibility alone does not establish client readiness.

## Initial runtime admission and retained refusal

A configured resolver serves discovery and start from the same trusted catalog
snapshot rules. Start reserves the exact attempt and request digest before it
compares the manifest pin. A changed valid manifest produces a retained,
non-executable decision with `outcome: "adaptation_unavailable"`,
`terminal.reason: "client_timeline_changed"`, and `terminal.retryable: false`.
It uses the ordinary start decision response. No session, activation intent,
source installation, or route exists for that rejected attempt. Exact replay
reads this terminal decision before consulting today's catalog. If publishing
that decision has an uncertain outcome, the request fails without claiming a
safe rejection; replay must resolve it.

A client may allow a new explicit playback intent after receiving that retained
terminal decision. It must not automatically rebase the rejected command or
reinterpret a generic conflict or network failure as proof of rejection.

Bound part activation is serialized under the source registration lock for the
captured source, account, profile and media item. Another nonterminal bound part
returns `409 timeline_part_active` before source installation. A draining stop,
expired owner or lost terminal receipt holds this barrier. Only a persisted
terminal receipt releases it. Other profiles and unbound playback retain their
existing behavior. Runtime progress and final stop samples map through the
captured timeline; accepted receipts return local `position` and global
`item_position`, while the session clock remains local.
