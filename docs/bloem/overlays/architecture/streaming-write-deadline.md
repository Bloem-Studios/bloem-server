# Bloem overlay: Streaming write deadlines and writer-chain conformance

Bloem additions and overrides for the upstream Silo document [`docs/architecture/streaming-write-deadline.md`](../../../architecture/streaming-write-deadline.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Stream telemetry sits inside the same contract”, after the paragraph beginning “`streamtelemetry.observedWriter` is inserted between an enrolled route handler's…”. **Bloem adds:**

### Same-origin Audiobookshelf media

The public compatibility gateway keeps its global `WriteTimeout: 120s`. When it
dispatches `/audiobookshelf` in process, it carries the fixed public mount in the
private request context described by the compatibility-mount contract. At the ABS
boundary, that contract deliberately separates two facts: the trusted mount used to
construct public URLs, and whether the request arrived through private in-process
dispatch. ABS installs a `RollingDeadlineWriter` only when the latter marker is present,
before the media handler can write headers. It does not infer enrollment from the
stripped URL, the mount string, or caller-controlled forwarding headers.

An identity-verified gateway companion may supply the fixed mount header so ABS emits
the same `/audiobookshelf` public URLs. Verification makes that URL metadata trusted;
it does not turn a companion request into in-process dispatch. The header therefore
never sets the rolling-deadline marker. A dedicated listener with no companion mount
continues to emit origin-root URLs. Neither listener is enrolled in the public
server's deadline policy.

This placement is deliberately narrow:

- only routes declared in `absMediaRoutes` pass through the wrapper; ordinary ABS JSON
  endpoints and socket.io do not;
- verified companion and dedicated ABS listeners do not receive the private dispatch
  marker, so their own server timeout policies — including the dedicated listener's
  existing `WriteTimeout: 0` — are unchanged;
- the in-process gateway passes the response writer through unchanged. The mounted
  media chain is handler → telemetry writer (when enabled) → rolling writer → ABS
  access-log writer → the public server writer. Every wrapping layer forwards
  `io.ReaderFrom`, `http.Flusher`, and `Unwrap`, so `http.ResponseController` can still
  reach the connection deadline.

Some direct-play handlers also construct their own rolling writer as part of the
shared playback helper. The route-level enrollment remains the authority for the
public mount: a newly added ABS media handler cannot silently fall back to the public
server's absolute deadline merely because it uses a different file-serving helper.

---

**Location:** section “How conformance is verified”, after the paragraph beginning “The deadline behavior itself is pinned by `internal/httpstream/readfrom_deadline_test.go`,…”. **Bloem adds:**

The mounted ABS regressions use the real compatibility gateway and real TCP servers
with short test bounds. One drives the production `Handler.Mount` registration and
RSS `http.ServeFile` route; its progressing transfer outlives the public server's
`WriteTimeout` while completing each bounded `ReadFrom` slice inside the rolling
window. Lower-level track regressions pin both progress and stalled-client reaping. A
separate boundary test proves that in-process dispatch enrolls while an
identity-verified companion keeps its mounted public URL without enrolling, and a
dedicated listener does neither. The chain test drives the gateway and ABS
access-log/telemetry wrappers and verifies exact byte accounting plus `ReadFrom`,
`Flush`, `Unwrap`, and response-controller deadline traversal end to end.
