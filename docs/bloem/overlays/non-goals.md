# Bloem overlay: Non-goals

**What this is.** Bloem's complete edition of Silo's
[`docs/non-goals.md`](../../non-goals.md). For Bloem Server, read this page
*instead of* Silo's, not beside it. Silo's file stays exactly as Silo wrote it,
so Silo merges stay clean; see the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Silo declares Live TV, OTA tuners, IPTV, guide data and
DVR permanently out of scope. Bloem ships them: HDHomeRun-compatible tuners,
Xtream live providers, XMLTV guides and DVR rules and recordings. Live TV has
been in Bloem since `ef2d38171` (2026-08-13); its routes later moved under
`/api/bloem/v1/livetv`. Xtream providers followed on 2026-09-19.
Silo's other non-goal, not deleting or managing media files, Bloem keeps
unchanged. Bloem's edition moved out of the Silo file in `47ea7c889`
(2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/livetv`, `internal/api/router_livetv.go`). Silo has not
changed `docs/non-goals.md` since the previous review (Silo `6de6d5e5e`), when
this edition took Silo's media-file-management non-goal.

Index: [Bloem documentation](../README.md).

---

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
