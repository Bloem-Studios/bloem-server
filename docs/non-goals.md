# Non-goals

Bloem ships Live TV: HDHomeRun-compatible tuner discovery, guide data,
channel playback, DVR rules and recordings are maintained server features.
See [Live TV tuner discovery](livetv-tuner-discovery.md) for the supported
deployment path.

Current implementation limits are distinct from these permanent non-goals. DVR does
not automatically import recordings into the catalog, enforce stored `keep_last`, or
expose a rule-update API. Completed unlinked recordings require an administrator to
scan the recording folder through a library. See [Live TV client access](architecture/live-tv-client-access.md)
and [web coverage](architecture/bloem-web-feature-coverage.md) before promising those workflows.

The following remain out of scope regardless of implementation quality:

- `.strm` files and equivalent library shortcuts whose contents are arbitrary
  remote media URLs.
- Generic remote-URL fetching, proxying, redirecting, or transcoding outside
  the explicit Live TV tuner and guide integrations.

These limits preserve the product boundary: Bloem supports the reviewed Live
TV integrations it ships, not an open-ended remote-stream ingestion service.
