# Bloem overlay: Playback protocol v3

Bloem additions and overrides for the upstream Silo document [`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “2.1 `GET /playback/capability`”. **Bloem replaces** the corresponding upstream passage with:

v3 is the server's only playback protocol, so `enabled` is constant `true`.
Most features describe binary support. One token,
`header_authenticated_media_ready_v1`, describes deployment readiness and is
present only while the administrator setting
`playback.header_authenticated_media_mode` is `single_or_affine`:

---

**Location:** section “2.1 `GET /playback/capability`”, after the paragraph beginning “v3 is the server's only playback protocol, so `enabled` is…”. **Bloem adds:**

```json
{
  "enabled": true,
  "protocol_versions": [3],
  "features": ["playback_plan_v3", "neutral_playback_v3_contract_v1", "embedded_subtitles_v1", "layout_aware_passthrough", "playback_route_diagnostics",
               "device_quirks_v1", "seek_reanchor_v1", "output_change_v1", "output_display_evidence_v1", "direct_stream_resume_v1",
               "header_authenticated_media_v1", "authorized_media_origins_v1", "software_video_decode_v1",
               "header_authenticated_media_ready_v1",
               "plan_invalidated_v1", "plan_source_duration_v1"],
  "deliveries": ["original_http", "server_remux_progressive", "server_remux_hls", "server_transcode_hls"],
  "transformations": [{"name": "audio_to_aac", "executor": "server", "recipe_version": "2", "validated_claims": ["audio_decode"]}]
}
```

---

**Location:** section “2.1 `GET /playback/capability`”. **Bloem replaces** the corresponding upstream passage with:

The sixteen feature strings above cover fifteen stable binary-support tokens
plus the dynamic readiness token.

---

**Location:** section “2.1 `GET /playback/capability`”, after the paragraph beginning “The sixteen feature strings above cover fifteen stable binary-support tokens…”. **Bloem adds:**

| Feature | What it promises |
| --- | --- |
| `playback_plan_v3` | The three plan endpoints exist and behave as specified here |
| `neutral_playback_v3_contract_v1` | The server mints opaque `plan_attempt_key` values that clients only echo, and exposes track/quality intent replans distinct from failure recovery |
| `embedded_subtitles_v1` | Exact native embedded subtitle selection on `original_http`, with a sidecar or burn-in fallback after selection failure (§8) |
| `layout_aware_passthrough` | Audio passthrough is decided from channel *layouts*, not just channel counts (§3) |
| `playback_route_diagnostics` | `POST /playback/route-events` is accepted |
| `device_quirks_v1` | Plans may carry `applied_quirks` and `runtime_corrections` (§9) |
| `seek_reanchor_v1` | The `seek_reanchor` replan operation is available (§6) |
| `output_change_v1` | The `output_change` intent replan is available; clients must keep the active route when this feature is absent |
| `output_display_evidence_v1` | The server honors `output.display` and its `hdr_evidence` tier; without it a client must still send `output.hdr_details` so the legacy fallback stays correct |
| `direct_stream_resume_v1` | A direct route may resume mid-file rather than restarting |
| `header_authenticated_media_v1` | An opted-in client receives media URLs without signed credentials in their query or path, and authenticates every media request with its normal Authorization header (§4.1) |
| `authorized_media_origins_v1` | Meaningful only with the token above: the client also honors credential-free absolute media URLs on server-designated proxy origins, which restores distributed egress for a header-authenticated attempt (§4.1) |
| `software_video_decode_v1` | Exact/platform-attested clients may qualify bounded `video_decode[]` entries with `hardware: false` for direct/original delivery; without the opt-in those evidence tiers remain hardware-only (§3) |
| `header_authenticated_media_ready_v1` | This deployment, not merely this binary, is configured to serve header-authenticated attempts safely. Its absence forces legacy media authentication even when both client and server understand the transport (§4.1) |
| `plan_invalidated_v1` | The client can be told mid-session that the plan it is playing was withdrawn, over the realtime `plan_invalidated` command, and replans off it. A session that did not negotiate it is stopped instead (§6.1) |
| `plan_source_duration_v1` | `source.duration_seconds` is populated when known, so its absence means *unknown* rather than *unsupported* (§5) |

---

**Location:** section “2.2 `POST /playback/start`”, after the paragraph beginning “A client generates a fresh `playback_attempt_id` per user-initiated playback and…”. **Bloem adds:**

**Accepted feature authority.** Every decision may include
`negotiated_client_features`, the canonical feature set accepted for that
attempt. It is the authority for transport behavior; `server_features` only
describes what the deployment can offer. Start persists the accepted set, and
replan, idempotent replay, reconstructed responses, and terminal decisions echo
the same attempt-sticky value. Older Silo-compatible clients may omit the new
tokens and ignore this additive response field; they continue to receive signed
legacy media URLs.

---

**Location:** section “4.1 Header-authenticated media URLs”. **Bloem replaces** the corresponding upstream passage with:

`header_authenticated_media_v1` is an engine-neutral client opt-in. A client
uses it only after the server advertises both that binary-support token and
`header_authenticated_media_ready_v1`, then includes it in the top-level
`client_features` on start and replan requests. The server removes it when the
deployment is not ready and reports the exact accepted set in
`negotiated_client_features`. It negotiates *how* media URLs authenticate;
`authorized_media_origins_v1` (below) separately negotiates *which origins* may
serve them and is never accepted without the header-authenticated mode.

---

**Location:** section “4.1 Header-authenticated media URLs”, after the paragraph beginning “**Replica affinity.** Because there is no reconstruction recipe, a…”. **Bloem adds:**

The setting defaults to `disabled`. Enable it only when media routes use one API
replica or verified session affinity. Rollback is immediate for new attempts:
set the mode back to `disabled`, verify the readiness token disappears from the
capability response, and clients will negotiate fresh signed legacy attempts.
Already-started attempts retain their durable accepted feature set until they
end; changing authentication mid-attempt is forbidden.
