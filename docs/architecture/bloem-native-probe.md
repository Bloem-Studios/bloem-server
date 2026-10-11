# Probing pinned native media

`scanner.ProbeNative` prepares metadata from one already authorized, pinned
`mediasource.File`. A scan consumer supplies the `ScanEntryFile` context,
cancellation and reauthorization methods, a trusted ffprobe binary, a fixed
MOV or Matroska demuxer, and explicit input, time and output bounds. No provider
field selects a binary, filesystem path, demuxer name or remote URL.

The consumer uses the private `nativestorage.ToolInput` bridge for seekable HTTP
reads. Direct Matroska track parsing and HTTP reads share one attempted-byte
budget; the supplement runs before the child starts. Source and authority
failures remain sticky, including a terminal cause joined with EOF. Ordinary
EOF at the known size preserves valid partial reads. A zero child exit cannot
override a read failure, cancellation, output overflow or final authority check.
The probe reader follows the bridge's teardown rule: once cleanup cancels the
source, a read of a range the exited child abandoned still fails, but it does
not fail the probe.

ffprobe is restricted to the selected container and the HTTP/TCP protocols
needed for the literal-loopback bridge. MOV external data references and
absolute paths are disabled. Child-only environment filtering removes proxy
controls and `FFREPORT`, and the command explicitly disables the HTTP proxy.
This prevents an inherited proxy from substituting bytes and prevents ffprobe
reports from persisting the private bridge argument. The parent environment
and ordinary runtime/library environment remain intact.

After admission, every outcome joins the child and output copiers, then joins
the bridge handlers before closing the file and releasing host admission. On
Unix, an independent context hook and final process-group cleanup also stop
descendants of a configured wrapper after the direct child has exited. Other
platforms retain direct-child cancellation and require a trusted ffprobe binary
that does not launch other processes.

Output must be a single bounded JSON document with a format and at least one
stream. The converted result uses the pinned file size and display name as a
container hint, fills missing Matroska subtitle track IDs, and rejects an echo
of the private argument. Error messages exposed by this consumer omit raw tool
output, provider details and command arguments; wrapped causes remain available
for typed internal classification.

This prerequisite supplies no scanner wiring, library-kind admission,
ingestion-rejection classification, production budget defaults or playback
change. Immutable versions or verified snapshots remain required for native
content reads. Synthetic pinned fixtures do not certify a live backend.
