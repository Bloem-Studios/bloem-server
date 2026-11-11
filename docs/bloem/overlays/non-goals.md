# What Bloem will not do

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which contains Silo through `63ef34b23`. This is Bloem's
> complete edition of Silo's [`docs/non-goals.md`](../../non-goals.md): read it
> *instead of* Silo's, not beside it. Bloem differs from Silo in one place: Live TV is
> in scope. The technical rules are in the [For developers](#for-developers) section.

Silo keeps a short list of features it will never accept, however well they are built.
This page walks through Bloem's version of that list: what Bloem adds back, the line it
keeps around Live TV, and the rule about media files that Bloem keeps exactly as Silo
wrote it.

![In scope: Live TV from sources an administrator set up, guides and recordings, media files read only. Never: .strm and other web-address shortcuts, other remote streams, deleting or moving media files](../../architecture/images/non-goals/boundary.svg)

## Live TV is in scope

Silo rules out Live TV, antenna tuners, internet TV, programme guides and recording for
good. Bloem ships them, as a maintained feature
([how Live TV works](../../architecture/bloem-live-tv.md)):

- **Tuners on the network.** HDHomeRun-compatible boxes, which Bloem can find by itself,
  and Dispatcharr, which presents internet TV sources as such a tuner.
- **Xtream providers**, for **live channels only**. Bloem signs in to the provider with
  its password stored encrypted, and calls only a fixed set of the provider's addresses.
  It never imports the provider's films or series, and never acts as an Xtream server
  for other apps.
- **A programme guide** from Schedules Direct, a Gracenote listings sync, or the Xtream
  provider's own guide.
- **Recordings and recording rules** (DVR).

### Where Live TV stops

Bloem plays channels only from a source an administrator set up. These stay out of
scope however they are built:

- **`.strm` files** and any other library shortcut whose content is a web address of
  some remote media.
- **Fetching, passing on, redirecting or converting any other remote stream**, outside
  the reviewed tuner and guide sources above. Nobody can paste an address and have
  Bloem play it.

That keeps Bloem a server for sources someone reviewed, not a service that takes in any
stream from anywhere.

### What is missing today, and is not a non-goal

These are current limits, not permanent decisions:

- A recording does not appear in the catalog by itself. An administrator adds the
  recordings folder to a library and scans it.
- A recording rule's "keep the last N" setting is stored but not applied: Bloem deletes
  no old recordings.
- A recording rule cannot be edited; remove it and make a new one.

## Deleting or managing media files

Bloem keeps Silo's rule unchanged: the server **reads** library files and never deletes,
moves, renames or reorganizes them. That holds for every app, admin page and plugin, and
for automatic clean-up such as removing a watched episode. Silo's
[reasons and alternatives](../../non-goals.md#deleting-or-managing-media-files) apply as
written: the shipped Compose file mounts the media folder read-only, Silo's
Jellyfin-compatible server reports items as not deletable, and library managers, file
managers or the shell look after the files, with the next scan showing their changes.

Bloem's own features stay inside that rule:

- **Storage sources** can only describe, list, check and read files. Removing a library
  location deletes nothing at the source.
- **Recordings** are new files in the recordings folder. Bloem removes only the partial
  pieces of its own recordings once it has joined them, and "keep the last N" deletes
  nothing.

## Who can change what

| Who | What they decide |
| --- | --- |
| Silo's maintainers | Silo's list in `docs/non-goals.md`, which Bloem never edits. |
| Bloem's maintainers | This edition: which Silo non-goals Bloem opens (only Live TV) and where Live TV stops. |
| Platform administrator | Which tuners, Xtream providers and guides a server uses, and which libraries it scans. |

## Words used in the code

| On this page | In the code and settings |
| --- | --- |
| Live TV | `internal/livetv`, routes under `/api/bloem/v2/livetv` (`internal/api/router_livetv.go`) |
| Tuner on the network | a tuner of type `hdhomerun` |
| Xtream provider | a tuner of type `xtream`, created with `POST /api/bloem/v2/livetv/tuners/xtream` |
| Programme guide source | `schedules_direct`, `xml_sync` or `xtream` (at most 3) |
| Recording rule | a series rule, `livetv_series_rules`, with `keep_last` |
| Recordings folder | the `livetv.dvr_path` setting (default `/var/lib/bloem/dvr`) |
| Storage source | the storage provider protocol, `proto/bloem/plugin/v1/storage_provider.proto` |
| Media folder | `MEDIA_ROOT`, mounted read-only |

## For developers

- **API:** [Live TV](../api/live-tv.md): every route under `/api/bloem/v2/livetv`.

### Where this edition came from

Bloem's edition moved out of the Silo file in `47ea7c889` (2026-09-23), so Silo's file
stays exactly as Silo wrote it and Silo merges stay clean; see the
[documentation workflow](../documentation-workflow.md). Live TV has been in Bloem since
`ef2d38171` (2026-08-13, an attributed adaptation of Prairie Server's tuner and DVR
code); its routes later moved under `/api/bloem/v2/livetv`. Xtream providers followed on
2026-09-19. This edition took Silo's media-file-management non-goal at Silo `6de6d5e5e`.
It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `e852f92bf`, and on
2026-11-01 against `b6039036d`; Silo has not changed `docs/non-goals.md` from
`3e2d306b2` through `63ef34b23`.

### Live TV boundary

Bloem ships Live TV: HDHomeRun-compatible tuner discovery, authenticated Xtream
live-provider consumption, guide data, channel playback, DVR rules and recordings are
maintained server features. See [Live TV tuner discovery](../../livetv-tuner-discovery.md)
and [Xtream live providers](../../architecture/xtream-live-tv.md) for supported
deployment paths. Xtream is live-only, with fixed reviewed endpoints (`player_api.php`,
`get_live_streams`, `xmltv.php` and live `.ts` requests), direct MPEG-TS and
provider-bound XMLTV; it does not expose an Xtream server API or import VOD/series.
Guide sources are `schedules_direct`, `xml_sync` (a native Zap2XML-style Gracenote
listings sync) and `xtream`; there is no generic XMLTV URL import and no M3U playlist
ingestion (Dispatcharr can present M3U sources as an HDHomeRun tuner).

The following remain out of scope regardless of implementation quality:

- `.strm` files and equivalent library shortcuts whose contents are arbitrary remote
  media URLs.
- Generic remote-URL fetching, proxying, redirecting, or transcoding outside the
  explicit Live TV tuner and guide integrations.

Current implementation limits are distinct from these permanent non-goals. DVR does not
automatically import recordings into the catalog, enforce stored `keep_last`, or expose
a rule-update API (series rules are list, create and delete only). Completed unlinked
recordings require an administrator to scan the recording folder through a library. See
[Live TV client access](../../architecture/live-tv-client-access.md) and
[web coverage](../../architecture/bloem-web-feature-coverage.md) before promising those
workflows.

### Media files

The storage provider protocol only describes, lists, stats and reads (`Describe`,
`List`, `Stat`, `Read`), and removing a library location deletes no provider files. The
DVR writes new recordings under `livetv.dvr_path` and removes only its own partial
segment files after merging them (`mergeSegments` in `internal/livetv/recorder.go`);
stored `keep_last` deletes nothing.

Technical pages:

- [How Live TV works](../../architecture/bloem-live-tv.md).
- [Live TV client access](../../architecture/live-tv-client-access.md).
- [Xtream live TV providers](../../architecture/xtream-live-tv.md).
- [Live TV tuner discovery](../../livetv-tuner-discovery.md).
- [Bloem web feature coverage](../../architecture/bloem-web-feature-coverage.md).
- Silo's [non-goals](../../non-goals.md), and the summary in
  [BLOEM_AGENTS.md](../../../BLOEM_AGENTS.md).
- Index: [Bloem documentation](../README.md).

The picture is an SVG file under `docs/architecture/images/non-goals/`, drawn by
`scripts/bloem/draw-pictures.py` from `scripts/bloem/pictures/non_goals.py`. Redraw it
there, not by hand.
