# Bloem overlay: Streaming write deadlines and writer-chain conformance

This overlay covers Silo's [`docs/architecture/streaming-write-deadline.md`](../../../architecture/streaming-write-deadline.md), which stays exactly as Silo wrote it.

When the server sends a large file, such as a video, it gives each piece a time
limit, so a stalled connection is dropped without cutting off a slow but working
one. Every layer between the handler and the network must pass that limit through.
Silo's page sets those rules. Bloem follows them for the media it adds, and
records one layer it removed.

## What Bloem changes

- **Bloem's media follows Silo's rules.** Files kept in Bloem's native storage
  (ebooks, video and audiobooks) are served through Silo's own media routes and
  must keep the same time limits and checks.
- **Bloem's own Audiobookshelf gateway is gone.** Bloem runs Silo's
  Audiobookshelf-compatible server, on its own port, exactly as Silo ships it.

## Words used in the code

| On this page | In the code |
| --- | --- |
| The time limit on each piece | Silo's `RollingDeadlineWriter` |
| The layers between handler and network | the writer chain |
| Silo's native media routes | `nativeMediaRoutes` in `internal/api/media_routes.go` |
| Routes the compressor leaves alone | `skipNativeMediaCompression` in `internal/api/router.go` |

## For developers

### Section by section

---

**Location:** end of the document. **Bloem adds:**

#### Current native boundary

Apply Silo's streaming write-deadline and writer-chain conformance rules to the
native routes and to Silo's Audiobookshelf-compatible listener. Playback, ranges and cancellation still require
their normal host authority and delivery checks; streaming telemetry must not
prevent response-controller traversal of the writer chain.

The Audiobookshelf server runs only on its own port, with no same-origin gateway or in-process dispatch marker. See
[protocol compatibility](../../../architecture/bloem-protocol-compatibility.md).

Silo's exact-route compressor bypass (`skipNativeMediaCompression`, mounted with
`httpstream.CompressWithExclusions` in `internal/api/router.go`) names only
Silo's `/api/v2` routes: the stream, transcode segment, download,
direct-download, theme audio and ebook read routes. Native-storage media served
through those routes keeps its `ReadFrom` path. A Bloem route under
`/api/bloem/v2`, such as Live TV's
`/api/bloem/v2/livetv/sessions/{sessionId}/stream`, is not on that list, so it is
served through the compressor's writer, which forwards `Unwrap` but not
`ReadFrom`.

For Bloem-owned delivery, consult the
[browse-only policy](../../../architecture/bloem-browse-only-content-policy.md),
[native video](../../../architecture/bloem-native-video-playback.md) and
[native audiobook](../../../architecture/bloem-native-audio-playback.md) contracts.
Do not infer equivalent timeout or permission behavior from a compatibility-protocol test.

### Related pages

- [Native storage](../../../architecture/bloem-native-storage.md).
- [How Live TV works](../../../architecture/bloem-live-tv.md).
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
