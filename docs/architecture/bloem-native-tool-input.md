# Native storage input for trusted tools

> **Provenance.** Bloem addition, part of [native storage](bloem-native-storage.md).
> `nativestorage.ToolInput` (`internal/nativestorage/tool_input.go`, `tool_bridge.go`)
> and this page arrived in `8dd12345d` (2026-10-10). Later changes that matter:
> `4e73aa7aa` (2026-10-11) stopped an abandoned range from failing a successful probe
> (the cleanup exception below); `93d60257d` (2026-10-12) made a request beyond the
> concurrent-read bound wait for a slot instead of failing; `8dbdb744f` (2026-10-12)
> keeps the cancellation cause as the sticky failure. Its consumers are the native
> probe (`internal/scanner/bloem_native_probe.go`, `d3d2c8f72`) and native video
> (`internal/api/handlers/bloem_native_video.go`). **Status:** current at `e852f92bf`.

`nativestorage.ToolInput` gives a trusted local tool seekable access to one
already opened, host-authorized `mediasource.File`. It does not resolve catalog
identity, grant source authority or admit an unversioned source. Native content
still requires immutable versions or verified snapshots.

The bridge listens on literal `127.0.0.1` and selects the file with a random
256-bit capability. Only the exact host and capability path are accepted, with
no query string. GET and HEAD support one byte range and a fixed content length;
unsupported requests cannot read the source. The listener is not mounted on the
public API. Its ephemeral argument must not enter catalog rows, logs, child
stderr or API responses.

The consumer supplies the file's cancellation function, a fresh authority
callback, and explicit total-byte, chunk-size, concurrent-read and elapsed-time
bounds. Every attempted chunk reserves its bytes against the shared budget.
Each successful chunk is buffered until the pinned read and both authority
checks complete. A terminal read error rejects the chunk even when the read
returned every requested byte. Source, authority, cancellation and budget
failures remain sticky and invalidate the tool result even if the child exits
successfully after accepting partial input.

Cleanup's own teardown is the one exception. A tool may exit while a range it
no longer needs is still being served; ffprobe routinely leaves an open-ended
range in flight. Cleanup cancels the source before it joins handlers, so that
read fails, and a handler failure observed after the tool has joined and
cleanup has begun is not recorded. It cannot affect the result: the failed
chunk reached the tool as no byte, and a handler records any failure before it
answers, so a failed read the tool waited on was recorded before the tool
joined. Failures while the tool still runs, or before cleanup begins, stay
sticky, even on a range the tool had already abandoned.

`Run` accepts one trusted callback. The callback must use its context to
terminate the child and must reap that child before returning; it must not call
`Close` itself. Cleanup stops HTTP admission, cancels source and HTTP work,
joins the callback and active handlers, calls `File.Close`, then releases host
admission. A cleanup timeout cannot release admission underneath a running
consumer. An opened adapter retains its own cancellation semantics: for
example, `mediasource.OpenLocal` may close its raw descriptor when canceled
before the bridge's explicit close.

The adapter itself supplies no scanner integration, probe budget defaults, library
kind admission or playback capability. Each consumer establishes its own authority,
process limits and output validation: see [native probe](bloem-native-probe.md) and
[native video playback](bloem-native-video-playback.md).
