# Bloem overlay: Playback protocol v3

**What this overlays.** Silo's
[`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md).
Bloem adds a paragraph to section “2.2 `POST /playback/start`” and one to
section “Buffering quality (native v2)”. It replaces nothing. An *overlay* is a
Bloem-owned page that records Bloem's additions to, or replacements of,
sections of a Silo-owned document. The Silo file stays exactly as Silo wrote
it. See the [documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem's v3 responses carry an extra field,
`negotiated_client_features`, which Bloem added to Silo's
`internal/playback/protocol_v3.go` as a seam in `0a0d16094` (2026-08-24). A
*seam* is a Bloem edit inside a Silo-owned file, declared in
`contracts/seams.txt`. Bloem also lets v3 play video from *native storage*,
Bloem's storage-plugin libraries, so every candidate file passes a
native-storage check (`b781ac3ff`, 2026-10-11). Bloem first documented these in
the Silo file in August 2026. The text moved here in `47ea7c889` (2026-09-23).
The header-authenticated media deployment gate this page once described was
removed in `545ed3a33` (2026-10-09).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/bloem_playback_v3_ext.go`,
`internal/playback/bloem_protocol_v3.go`,
`internal/api/handlers/bloem_native_video.go`). Since the previous review (Silo
`6de6d5e5e`), Silo changed the document three times: `8b84639ed` (#3109,
2026-10-26) adds the software-only `pq_full_range` tone-map kind; `e7605afe2`
(#3115, 2026-10-26) starts playback on an audio track the client plays without
conversion; and `4a1a2200c` (#3044, 2026-10-27) corrects the protocol text,
including `400` for a missing `X-Profile-Id`, viewer resolution before the body
is read, and the replan lease and capacity rules. All of them apply to Bloem;
the two anchor paragraphs below are unchanged.

Index: [Bloem documentation](../../README.md).

---

**Location:** section “2.2 `POST /playback/start`”, after the paragraph beginning “A client generates a fresh `playback_attempt_id` per user-initiated playback and…”. **Bloem adds:**

**Accepted feature authority.** Every decision may include
`negotiated_client_features`, the canonical feature set accepted for that
attempt. It is the authority for transport behavior; `server_features` only
describes what the server can offer. Start persists the accepted set, and
replan, idempotent replay, reconstructed responses, and terminal decisions echo
the same attempt-sticky value. Bloem narrows the request's `client_features`
to canonical tokens the server knows, deduplicated in request order, and drops
`authorized_media_origins_v1` unless `header_authenticated_media_v1` was also
accepted. A client that ignores this additive field, such as an older Silo
client that never sends `header_authenticated_media_v1`, keeps receiving signed
media URLs as Silo's §4.1 describes. The field lists `client_features` only. HLS delivery features
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
