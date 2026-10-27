# Probing pinned native media

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current. A Bloem addition for [native storage](bloem-native-storage.md), the
> Bloem feature that reads media from provider-backed storage locations (for
> example S3) rather than from local folders. It is built on Silo's
> ffprobe-based scanner (`internal/scanner`, `internal/mediaprobe`): the
> Bloem-owned file `internal/scanner/bloem_native_probe.go` returns Silo's
> `ProbeData`. `ProbeNative` and this page were added in `d3d2c8f72`
> (2026-10-10), and `4e73aa7aa` (2026-10-11) added the teardown rule. The
> scanner consumers came later: native movies in
> `internal/scanner/storage_movie.go` (`b781ac3ff`, 2026-10-11, see
> [native video playback](bloem-native-video-playback.md)) and audiobooks in
> `internal/scanner/storage_audiobook.go` through `ProbeNativeAudio`
> (`17ca70d9b` and `d5d02a00e`, 2026-10-12). The input bridge is described in
> [native tool input](bloem-native-tool-input.md), and the pinned reader in
> [native scan reader](bloem-native-scan-reader.md).

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

`ProbeNative` itself supplies no scanner wiring, library-kind admission,
ingestion-rejection classification, production budget defaults or playback
change. When it was added in `d3d2c8f72` it had no consumer. Its callers now supply
those: the movie and audiobook scanners named above admit container families
and publish items, and `cmd/silo/bloem_native_movie_scanner.go` sets the movie
budgets (128 MiB of input, 60 seconds, 4 MiB of JSON output).
Immutable versions or verified snapshots remain required for native
content reads. Synthetic pinned fixtures do not certify a live backend.

## Native audio

`ProbeNativeAudio` uses an explicit reviewed audio demuxer and the same pinned
reader, byte budget and cleanup boundary. Its exact positive finite duration
and validated chapter endpoints are retained separately from integer catalog
durations. A book-level rejection prevents partial publication; source and I/O
failures abort the scan. See [native audiobook presentations](bloem-native-audio-playback.md).
