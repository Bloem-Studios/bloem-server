# Silo playback merge impact on Vondel clients

Status: implementation handoff  
Audited: 2026-08-24

## Revisions

- Silo server `upstream/main`: `820eef7792d24e2b5af789e448906481bd560296`
- Vondel server baseline: `781c651366dfdc2c7f5f35aaf67b772a1018620f`
- Vondel Android baseline: `52436d3271a0c656f9b77b78aeb84f9116cd6a6c`
- Vondel Apple baseline: `dcbfc080c7b272961763d4632cfddf9970fb73f5`
- Vondel client contracts baseline: `25ee952aa51acdea6c06546d8c3bad668bf7f566`

The relevant upstream merges are Silo PR #734 (`6f8db023`, optimistic
H.264 remux copy-safety) and PR #737 (`820eef77`, client-managed original
HDR and selected-audio delivery claims).

## Compatibility conclusion

Both changes are additive. Existing Vondel clients omit the new feature and
claims, so they remain correct. The main user-visible risk is Android: when an
optimistic remux is invalidated, Media3 can report a non-recoverable source
failure and require manual Retry. Apple already maps a failed load to one
bounded stream-rejection replan, but AVFoundation error classification means
that recovery is not deterministic for every stopped transport.

The new HDR and selected-audio claims are optimizations, not rollout
requirements. They must remain absent until the actual engine can honor them.

## `plan_invalidated_v1`

An unresolved H.264 multiple-PPS verdict can now plan an optimistic remux. A
positive unsafe scan is persisted and the issued plan is withdrawn. A capable
client advertises `plan_invalidated_v1`, holds the session control WebSocket,
immediately acknowledges `plan_invalidated`, verifies `payload.plan_id`, runs a
`failure_recovery` replan excluding the invalidated attempt key, installs the
replacement through the existing playback owner, and reports completion.

Without the feature, connection, or completed result, the server stops the
session. The next attempt sees the persisted unsafe verdict and selects a safe
route. This is the required legacy compatibility path.

Client implementation rules:

1. Advertising is conditional on the complete socket and command handler.
2. Generic HTTP 404 is not an invalidation signal.
3. Stale or duplicate commands cannot replace the active plan.
4. Position, selected tracks, authentication generation, output generation,
   and Watch Together ownership survive replacement.
5. The room coordinator remains capability-blind and reuses the activation's
   playback source/controller.

## Original-delivery validated claims

Silo adds two optional values under
`client_playback_context.deliveries.original_http.validated_claims`:

- `client_managed_dynamic_range_v1` promises that the original-file executor
  accepts the declared HDR/Dolby Vision range and resolves presentation against
  the live output.
- `client_selected_audio_track_v1` promises that the executor maps
  `selected_tracks.audio.index` to the probed source inventory and activates
  that exact stream.

Without the claims, historical output gating and alternate-audio remux behavior
remain unchanged. Progressive and HLS output remains display-gated.

Current clients cannot truthfully claim either feature. Android retains
`selected_tracks` as raw JSON and does not carry the audio ordinal into Media3.
Apple does not decode `selected_tracks` in its playback plan. Neither delivery
wire model serializes `validated_claims`.

Required order:

1. Decode and validate selected-track identity in both clients.
2. Apply the audio ordinal in the real engine.
3. Preserve it through replan, Next Up, downloads, and Watch Together.
4. Add delivery-scoped claim serialization.
5. Mint each claim only from activation-frozen runtime executor evidence.
6. Never use a handwritten platform, device, or model list as evidence.

## Server integration invariants

Upstream playback changes overlap Vondel's header-authenticated media and
proxy-policy work. Integration must preserve:

- deployment-gated `header_authenticated_media_ready_v1`;
- `authorized_media_origins_v1` and the configured proxy policy;
- sticky negotiated attempt features and credential-free media URLs;
- Vondel fleet admission/reservation release during session invalidation; and
- the PR #734 database migration that persists the multiple-PPS verdict.

Roll out in dependency order: server persistence and legacy stop, shared
contracts, Android invalidation recovery, Apple invalidation recovery, all four
Watch Together paths, selected audio, then managed HDR.
