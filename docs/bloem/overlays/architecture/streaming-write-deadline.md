# Bloem overlay: Streaming write deadlines and writer-chain conformance

Bloem additions and overrides for the upstream Silo document [`docs/architecture/streaming-write-deadline.md`](../../../architecture/streaming-write-deadline.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

## Current native boundary

Apply Silo's streaming write-deadline and writer-chain conformance rules to the
native routes and to Silo's Audiobookshelf-compatible listener. Playback, ranges and cancellation still require
their normal host authority and delivery checks; streaming telemetry must not
prevent response-controller traversal of the writer chain.

Bloem's former same-origin Audiobookshelf gateway and its in-process dispatch
marker were removed; the Audiobookshelf server runs only on its own port. See
[protocol compatibility](../../../architecture/bloem-compatibility-removal.md).

For Bloem-owned delivery, consult the
[browse-only policy](../../../architecture/bloem-browse-only-content-policy.md),
[native video](../../../architecture/bloem-native-video-playback.md) and
[native audiobook](../../../architecture/bloem-native-audio-playback.md) contracts.
Do not infer equivalent timeout or permission behavior from a compatibility-protocol test.
