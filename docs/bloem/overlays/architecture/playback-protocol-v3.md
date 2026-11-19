# Bloem overlay: Playback protocol v3

This overlay covers Silo's [`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md), which stays exactly as Silo wrote it.

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
- **Surround audio for AirPlay receivers.** While the app reports an AirPlay
  receiver as its output, a title whose surround audio HLS cannot carry as it is
  gets AC-3 or E-AC-3 5.1 instead of AAC 5.1. Other outputs are unchanged.

## Words used in the code

| On this page | In the code |
| --- | --- |
| The accepted abilities | `negotiated_client_features` (`NegotiateClientFeaturesV3` in `internal/playback/bloem_protocol_v3.go`) |
| The app's declared abilities | `client_features` |
| The native-storage check | `nativeVideoPreflight` (`internal/api/handlers/bloem_native_video.go`) |

## For developers

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

---

**Location:** section “11. Transformations”, after the table of transformations.
**Bloem adds:**

**Dolby surround for AirPlay receivers.** Silo's `audio_to_ac3` and
`audio_to_eac3` transformations (48 kHz 5.1 at 640 kbps, recipe version `1`)
also serve AirPlay. A plan lists one in place of `audio_to_aac` when the
client's `output.sink_type` is `AirPlay`, the planner would convert the selected
audio to AAC 5.1 for HLS, and the source is a surround codec HLS cannot carry as
it is (such as TrueHD or DTS). AC-3 is planned first; E-AC-3 takes its place
when the client lists only that codec or only that encoder is offered. A client that does not list the codec in its HLS delivery's
`audio_decode_codecs`, or limits the delivery to fewer than six channels,
keeps the AAC plan. When a client reports an AC-3 plan as failed, the replan
moves on to E-AC-3 and then to the AAC plan, because each plan's key is
distinct. Every other output type plans exactly as Silo's page describes.

### Related pages

- [Native video playback](../../../architecture/bloem-native-video-playback.md).
- [Native storage](../../../architecture/bloem-native-storage.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
