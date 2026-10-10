# Native storage input for trusted tools

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

`Run` accepts one trusted callback. The callback must use its context to
terminate the child and must reap that child before returning; it must not call
`Close` itself. Cleanup stops HTTP admission, cancels source and HTTP work,
joins the callback and active handlers, calls `File.Close`, then releases host
admission. A cleanup timeout cannot release admission underneath a running
consumer. An opened adapter retains its own cancellation semantics: for
example, `mediasource.OpenLocal` may close its raw descriptor when canceled
before the bridge's explicit close.

This adapter supplies no scanner integration, probe budget defaults, library
kind admission or playback capability. Those consumers must establish their
own authority, process limits and output validation before adopting it.
