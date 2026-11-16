# Bloem overlay: Transcode nodes

> **Provenance and status.** Bloem page, written on 2026-11-16 and checked against
> the merge `fb187cab2`, which brings Silo through `699f84ecf` (Silo #4337, #4338). It
> lists what Bloem adds to Silo's
> [`docs/architecture/transcode-nodes.md`](../../../architecture/transcode-nodes.md),
> which stays exactly as Silo wrote it. Bloem replaces nothing; Silo's design applies
> to Bloem as written. The details are in the [For developers](#for-developers)
> section.

A transcoding node converts video for the API servers. Since Silo's change of
November 2026 a node keeps no database and no keys: it reads each movie by asking
the API server for one file by its path. Bloem runs those nodes exactly as Silo
ships them, with no Bloem code on them.

## What Bloem changes

- **Movies Bloem reads itself stay on the API servers.** A movie in a storage
  source, or a `.strm` item whose video lives at a web address, is not a file the
  API server can hand a node by its path. Bloem never sends one to a node; it plays
  and converts them on the API servers, as the delivery settings allow.
- **Every other movie goes to the nodes as Silo decides.**
- **Bloem's private source addresses never reach a log.** Silo logs the FFmpeg
  command it runs; on the API servers Bloem makes that log name a native input as
  `native source`.

## Words used in the code

| On this page | In the code |
| --- | --- |
| A movie Bloem reads itself | a native input, `storagesource.IsNativeLocation` |
| Asking for one file by its path | a source grant, `nodesource.Grant` |
| Bloem's private source address | a `nativestorage.ToolInput` argument |

## For developers

- Placement: `nativeVideoWorkerAdmissionV3` refuses every node for a native input
  on the native API, `playback.RefuseContextRemoteStart` refuses Jellyfin and
  download-preparation remote starts whose input the request's media host handles,
  and `ArtifactManager.prepareNativeHere` prepares native downloads on the API
  server. [Where native inputs run](../../../architecture/bloem-native-video-playback.md#where-native-inputs-run)
  explains why Silo's core source access cannot serve them.
- Redaction: `nativeredact.Register` wraps the registered `fspath.RelayedSources`
  (`fspath.RegisteredRelayedSources`, a Bloem file in Silo's package) and adds the
  tool input address to its `Redact`, so `fspath.RedactRelayed`, which Silo's FFmpeg
  command logs and log sink call, writes `native source`. Its other methods are the
  wrapped relay's, so what counts as a relayed input does not change. The API
  servers register it; a transcode node never does.
- Launch guard: Silo's `TestEveryMediaLaunchGoesThroughCommandContext` gets Bloem's
  launches, with their reasons, from `internal/fspath/bloem_launch_guard_test.go`.
- Removed on 2026-11-16: Bloem's node-side native execution
  (`internal/transcodenode/bloem_native_*.go`), its readiness probe
  (`GET /bloem/native-execution`), the API's Redis worker grants and the standalone
  worker composition in `cmd/silo`. They needed the database, Redis and data
  cipher on the node, which Silo's nodes no longer have.
