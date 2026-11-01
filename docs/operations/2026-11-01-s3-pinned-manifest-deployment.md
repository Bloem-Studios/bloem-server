# Bloem deployment — November 1, 2026: a pinned S3 release on every platform

Production has run `main` at `99a4a1133a5cc1ac356009c6efe1cac62df7ab07` since 19:18:24 UTC.
It replaces `ab4aadc75`. The one code change is in the native storage catalog
(`internal/plugins/bloem_storage_catalog.go`): a release's served `manifest.json` names
one platform's checksum, so a server on another platform refused a pinned S3 release
whose manifest named a different binary. The check now accepts a manifest naming any
binary of the same release; the operator's pin still decides which binary is run. See
[native storage](../architecture/bloem-native-storage.md). Production is linux/amd64,
whose checksum the S3 manifest names, so its behaviour does not change; the owner asked
for production to match `main`.

Silo `main` had seven newer commits (through `1faea5ea0`), not merged in this release.
The image was built locally without waiting for CI.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `ab4aadc75`. Tagged
  `bloem-server:release-99a4a1133` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `b1c32053e93cb532a82a21a93f0ae3c2db3da8571bde77dd894fbd3515b72b09` on both hosts.
- **Before the restart:** container snapshot `pre-main-99a4a1133`. Database restore point
  of the same name at LSN `432/DC2CEDF8`. Segment `0000000300000432000000DC`, which holds
  it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `ab4aadc75`. One
  transcode was running inside the server, and the stop took 37 seconds (exit 0). No
  migrations were pending; 750 are applied. The server answered ready at 19:18:31, 46
  seconds after the stop began, and was healthy with no restarts. `/api/v2/system/info`
  reports `99a4a113`; `organization_domains_v1` is listed, and
  `GET /api/bloem/v1/ambience/public` and `/api/v1/ready` answer `200`. No error lines
  were logged after the start. The viewer's player kept reporting progress and a new
  conversion started on the server.
- **Transcode nodes.** Both nodes were idle and moved at once to `release-99a4a1133`,
  healthy at 19:18:52 (CT135) and 19:18:54 (CT172). The server marked each unhealthy
  while it restarted; at 19:19:25 both were enabled, healthy and not draining. Their
  previous compose files are kept with suffix `.bak-pre-99a4a1133`.

## Rollback

Start `bloem-server:release-ab4aadc75` again and set the transcode nodes back from their
`.bak-pre-99a4a1133` compose files. No schema changed.
