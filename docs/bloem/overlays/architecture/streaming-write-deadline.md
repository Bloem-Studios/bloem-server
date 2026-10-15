# Bloem overlay: Streaming write deadlines and writer-chain conformance

Bloem additions and overrides for the upstream Silo document [`docs/architecture/streaming-write-deadline.md`](../../../architecture/streaming-write-deadline.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

## Current native boundary

Apply Silo's streaming write-deadline and writer-chain conformance rules to the
native routes retained by Bloem. Playback, ranges and cancellation still require
their normal host authority and delivery checks; streaming telemetry must not
prevent response-controller traversal of the writer chain.

The former same-origin Audiobookshelf gateway, private in-process dispatch marker,
ABS media route enrollment and dedicated protocol listener were removed. Earlier
ABS-specific rolling-writer guidance describes retired code and must not be used
to add a new public content path. See
[protocol removal](../../../architecture/bloem-compatibility-removal.md).

For Bloem-owned delivery, consult the
[browse-only policy](../../../architecture/bloem-browse-only-content-policy.md),
[native video](../../../architecture/bloem-native-video-playback.md) and
[native audiobook](../../../architecture/bloem-native-audio-playback.md) contracts.
Do not infer equivalent timeout or permission behavior from a removed protocol test.
