# Bloem overlay: Playback protocol v3

Bloem additions and overrides for the upstream Silo document [`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “2.2 `POST /playback/start`”, after the paragraph beginning “A client generates a fresh `playback_attempt_id` per user-initiated playback and…”. **Bloem adds:**

**Accepted feature authority.** Every decision may include
`negotiated_client_features`, the canonical feature set accepted for that
attempt. It is the authority for transport behavior; `server_features` only
describes what the server can offer. Start persists the accepted set, and
replan, idempotent replay, reconstructed responses, and terminal decisions echo
the same attempt-sticky value. Older Silo-compatible clients may omit the new
tokens and ignore this additive response field; they continue to receive signed
legacy media URLs. The field lists `client_features` only. HLS delivery features
in `client_playback_context.deliveries.hls.features`, such as
`hls_variable_substitution_v1` and `hls_copy_mpegts_v1`, follow Silo's rules
(§4.2, §5, §6) and are not echoed in it.

---

**Location:** section “Buffering quality (native v2)”, after the paragraph beginning “An Auto start or replan without `bandwidth_estimate_kbps` plans with the…”. **Bloem adds:**

**Native-storage candidates.** Every file a v3 start or replan checks before
planning, including Silo's version fallbacks and this buffering version search,
passes Bloem's native-storage preflight (`nativeVideoPreflight`). A file in
native storage is checked through its storage source instead of the local
missing-file check; a candidate that fails is skipped like a missing file.
Other files keep Silo's check. Native transport limits on replans that switch
files are in [native video playback](../../../architecture/bloem-native-video-playback.md).
