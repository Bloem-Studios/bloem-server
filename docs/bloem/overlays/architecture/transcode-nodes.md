# Bloem overlay: Transcode nodes

This overlay covers Silo's
[`docs/architecture/transcode-nodes.md`](../../../architecture/transcode-nodes.md),
which stays exactly as Silo wrote it. Bloem replaces nothing; Silo's design applies
to Bloem as written.

A transcoding node converts video for the API servers. Since Silo's change of
November 2026 a node keeps no database and no keys: it reads each movie by asking
the API server for one file by its path. Bloem runs those nodes exactly as Silo
ships them, with no Bloem code on them.

## What Bloem changes

- **Movies Bloem reads itself go only to nodes with core source access.** A movie
  in a storage source, or a `.strm` item whose video lives at a web address, is not
  a file in a library folder. Bloem sends its HLS and download conversion only to a
  node whose source access is `core`, and the API servers send that node the bytes
  under a Bloem source grant. A node that reads its own library folders never gets
  one. For these movies to convert on the GPUs, set at least one node's source
  access to `core`.
- **Every other movie goes to the nodes as Silo decides.**
- **Bloem's private source addresses never reach a log.** Silo logs the FFmpeg
  command it runs; on the API servers Bloem makes that log name a native input as
  `native source`.

## Words used in the code

| On this page | In the code |
| --- | --- |
| A movie Bloem reads itself | a native input, `storagesource.IsNativeLocation` |
| Asking for one file by its path | a source grant, `nodesource.Grant`; Bloem's own for a native input (`handlers.NativeNodeSources`) |
| Bloem's private source address | a `nativestorage.ToolInput` argument |

## For developers

- Placement and grants: `nativeVideoWorkerAdmissionV3` admits a native input only
  to a node with `source_access=core` and no subtitle burn-in;
  `NativeNodeSources` mints the Bloem grant and answers `sources/open` and the
  source bytes route for it in front of Silo's handlers; Jellyfin remote starts of
  a native input stay refused (`playback.RefuseContextRemoteStart`).
  [Where native inputs run](../../../architecture/bloem-native-video-playback.md#where-native-inputs-run)
  has the design.
- Redaction: `nativeredact.Register` wraps the registered `fspath.RelayedSources`
  (`fspath.RegisteredRelayedSources`, a Bloem file in Silo's package) and adds the
  tool input address to its `Redact`, so `fspath.RedactRelayed`, which Silo's FFmpeg
  command logs and log sink call, writes `native source`. Its other methods are the
  wrapped relay's, so what counts as a relayed input does not change. The API
  servers register it; a transcode node never does.
- Launch guard: Silo's `TestEveryMediaLaunchGoesThroughCommandContext` gets Bloem's
  launches, with their reasons, from `internal/fspath/bloem_launch_guard_test.go`.
- Removed with Silo's thin nodes (Silo #4337): Bloem's node-side native execution
  (`internal/transcodenode/bloem_native_*.go`), its readiness probe
  (`GET /bloem/native-execution`), the API's Redis worker grants and the standalone
  worker composition in `cmd/silo`. They needed the database, Redis and data
  cipher on the node, which Silo's nodes no longer have.
