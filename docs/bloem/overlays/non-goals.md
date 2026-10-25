# Bloem overlay: Non-goals

This is Bloem's complete edition of the upstream Silo document [`docs/non-goals.md`](../../non-goals.md),
which is kept verbatim so Silo merges stay clean. For Bloem Server, read this
edition instead. Index: [Bloem documentation](../README.md).

Bloem ships Live TV: HDHomeRun-compatible tuner discovery, authenticated Xtream
live-provider consumption, guide data, channel playback, DVR rules and recordings
are maintained server features. See [Live TV tuner discovery](../../livetv-tuner-discovery.md)
and [Xtream live providers](../../architecture/xtream-live-tv.md) for supported deployment
paths. Xtream is live-only, with fixed reviewed endpoints, direct MPEG-TS and
provider-bound XMLTV; it does not expose an Xtream server API or import VOD/series.

Current implementation limits are distinct from these permanent non-goals. DVR does
not automatically import recordings into the catalog, enforce stored `keep_last`, or
expose a rule-update API. Completed unlinked recordings require an administrator to
scan the recording folder through a library. See [Live TV client access](../../architecture/live-tv-client-access.md)
and [web coverage](../../architecture/bloem-web-feature-coverage.md) before promising those workflows.

The following remain out of scope regardless of implementation quality:

- `.strm` files and equivalent library shortcuts whose contents are arbitrary
  remote media URLs.
- Generic remote-URL fetching, proxying, redirecting, or transcoding outside
  the explicit Live TV tuner and guide integrations.

These limits preserve the product boundary: Bloem supports the reviewed Live
TV integrations it ships, not an open-ended remote-stream ingestion service.

## Deleting or managing media files

Bloem keeps Silo's permanent non-goal unchanged: the server reads library files
and does not delete, move, rename or reorganize them. That holds for every API,
admin option, client action and plugin, and for automatic cleanup such as removing
a watched episode. Silo's [reasons and alternatives](../../non-goals.md#deleting-or-managing-media-files)
apply as written: the shipped Compose file mounts `MEDIA_ROOT` read-only, Silo's
Jellyfin compatibility server reports items as not deletable, and library
managers, file managers or the shell own file layout, with the next scan
reflecting their changes.

Bloem's own features stay inside that boundary. The storage provider protocol
only describes, lists, stats and reads, and removing a library location deletes
no provider files. The DVR writes new recordings under `livetv.dvr_path` and
removes only its own partial segment files after merging them; stored
`keep_last` deletes nothing.
