# Bloem overlay: Playback protocol v3

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to Silo's
> [`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md),
> which stays exactly as Silo wrote it. Bloem replaces nothing there. The additions
> are in the [For developers](#for-developers) section.

Silo's page defines how an app asks to play a title and how the server answers:
which file, which way to deliver it, and what to do when conditions change. Bloem
plays titles the same way. It adds two things: it tells the app exactly which of
its declared abilities the server accepted, and it can play files kept in Bloem's
**native storage** (files read through a storage plugin rather than from the
server's own folders).

## What Bloem changes

- **The answer names the accepted abilities.** An app lists what it can do when it
  starts playback. Bloem's answer repeats the abilities it accepted for that
  attempt, and keeps repeating the same list for the rest of the attempt. An app
  that ignores the list behaves as it does with Silo.
- **Files in native storage can play.** Before choosing a file, Bloem checks a
  native-storage file through its storage source. A file that fails the check is
  skipped, like a missing file.

## Words used in the code

| On this page | In the code |
| --- | --- |
| The accepted abilities | `negotiated_client_features` (`NegotiateClientFeaturesV3` in `internal/playback/bloem_protocol_v3.go`) |
| The app's declared abilities | `client_features` |
| The native-storage check | `nativeVideoPreflight` (`internal/api/handlers/bloem_native_video.go`) |

## For developers

### Where this overlay came from

Bloem's v3 responses carry an extra field, `negotiated_client_features`, which
Bloem added to Silo's `internal/playback/protocol_v3.go` as a seam in `0a0d16094`
(2026-08-24). A *seam* is a Bloem edit inside a Silo-owned file, declared in
`contracts/seams.txt`. Bloem also lets v3 play video from native storage, so every
candidate file passes a native-storage check (`b781ac3ff`, 2026-10-11). Bloem
first documented these in the Silo file in August 2026; the text moved here in
`47ea7c889` (2026-09-23). The header-authenticated media deployment gate this page
once described was removed in `545ed3a33` (2026-10-09).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/bloem_playback_v3_ext.go`,
`internal/playback/bloem_protocol_v3.go`,
`internal/api/handlers/bloem_native_video.go`), after Silo's `8b84639ed` (#3109,
the software-only `pq_full_range` tone-map kind), `e7605afe2` (#3115, starting on
an audio track the client plays without conversion) and `4a1a2200c` (#3044,
protocol corrections, including `400` for a missing `X-Profile-Id`, viewer
resolution before the body is read, and the replan lease and capacity rules).
From `3e2d306b2` through `63ef34b23` Silo changed the document four more times:
transcoding H.264 High 10 for browsers that cannot decode it (`539f9b90e`), noting
that downloads still refuse declared `video_decode` entries (`23aec3e11`), keeping
a session alive while the client waits out its startup timeout (#3208), and direct
play of a 1080p50 source on a decoder rated 4K30 (#3267). All of them apply to
Bloem; the two paragraphs this overlay follows are unchanged.

### Section by section

---

**Location:** section “2.2 `POST /playback/start`”, after the paragraph beginning
“A client generates a fresh `playback_attempt_id` per user-initiated playback
and…”. **Bloem adds:**

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

**Location:** section “Buffering quality (native v2)”, after the paragraph
beginning “An Auto start or replan without `bandwidth_estimate_kbps` plans with
the…”. **Bloem adds:**

**Native-storage candidates.** Every file a v3 start or replan checks before
planning, including Silo's version fallbacks and this buffering version search,
passes Bloem's native-storage preflight (`nativeVideoPreflight`). A file in
native storage is checked through its storage source instead of the local
missing-file check; a candidate that fails is skipped like a missing file.
Other files keep Silo's check. Native transport limits on replans that switch
files are in [native video playback](../../../architecture/bloem-native-video-playback.md).

### Related pages

- [Native video playback](../../../architecture/bloem-native-video-playback.md).
- [Native storage](../../../architecture/bloem-native-storage.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
