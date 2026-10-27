# Bloem overlay: Streaming write deadlines and writer-chain conformance

**What this overlays.** Silo's
[`docs/architecture/streaming-write-deadline.md`](../../../architecture/streaming-write-deadline.md).
Bloem adds one closing section and replaces nothing; Silo's contract and
conformance rules apply to Bloem as written. An *overlay* is a Bloem-owned page
that records Bloem's additions to, or replacements of, sections of a Silo-owned
document. The Silo file stays exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem serves media from its own native routes (native
storage ebooks, video and audiobooks), and those routes must follow Silo's
rules too. Bloem once ran an Audiobookshelf gateway on the main port that
needed its own writer-chain marker. That gateway was removed, and since
`2393c41ee` (2026-10-18) Bloem runs Silo's Audiobookshelf-compatible server, as
Silo ships it, on its own port. Bloem first documented this in the Silo file
in August 2026 and moved the text here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`. Silo has not changed this document since the previous review
(Silo `b430099ff`).

Index: [Bloem documentation](../../README.md).

---

**Location:** end of the document. **Bloem adds:**

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
