# Native audiobook presentations

> **Provenance.** Bloem addition, part of [native storage](bloem-native-storage.md):
> audiobooks kept in a storage source (for example S3) rather than on a local disk,
> scanned through a storage plugin and played without a local path. Publication arrived
> in `d5d02a00e` and ingest and direct playback in `8b8943320` (both 2026-10-12), with
> migration `20261012022129_bloem_native_audio_staging.sql`. It builds on Silo's
> catalog, multi-part audiobook presentations and playback-v3 sessions
> ([playback protocol v3](playback-protocol-v3.md)). Later: Silo's Jellyfin and
> Audiobookshelf servers were restored on 2026-10-18 (`2393c41ee`), which changes the
> compatibility paragraph below. **Status:** current at `e852f92bf`. No production
> storage-source audiobook acceptance is recorded.

Native audiobook tracks use the existing catalog, playback-v3 sessions, stream
tokens and revision-pinned storage reader. The host composes scanning and delivery
before releasing the shared startup latch. A scanner flag alone cannot admit an
audiobook scan or advertise `native_audiobook_presentations_v1`.

## Publication and retained authority

Providers must declare `audiobooks` and directory-only listings. A directory
with tracks is always listed in full, since a book is assembled from all of its
tracks; its change token is dropped. A directory of directories keeps its token,
and an audiobook scan treats the source's change feed as covering its books: when
such a directory is listed from a token, the child directories it does not report
are carried (`carried` listing mode, `carryUnchangedTx`), kept with their retained
entries and memberships without a listing, and only the reported ones are listed.
A full listing, including the one an expired token forces, carries nothing.

An audiobook scan lists and probes up to `nativeAudioScanWorkers` directories
at once (`audioPipeline`): each directory is claimed until its page is applied,
and pages are still applied one at a time. Audio probes read 1 MiB chunks, each
behind the scan reader's per-read authority check.

Every admitted track is probed through `ScanReader` the first time its revision
is seen; provider paths and URLs never become tool inputs. The staged probe is
kept in `bloem_storage_audio_probes` per location and entry with that revision,
and a later scan reuses it instead of opening a track whose revision is
unchanged. Completion drops kept probes for entries the location no longer lists
at that revision. Unsupported audio extensions stage a rejection without a read.
Authority, revision and I/O failures abort the page.

`bloem_storage_audio_staged_tracks` retains bounded candidates across pages and
lease takeover. Nothing publishes before directory completion, including when
the completing page has no new entries. A rejected candidate stays rejected for
its run so replay cannot erase an overflow marker. A new run removes stale
staging only for its location. Run, directory and location deletion cascade.

A book has at most 512 tracks. Complete unique positive disc/track positions
order its parts; otherwise logical paths use natural order with entry IDs as a
tie-breaker. Conflicting book IDs, malformed chapters, unsupported tracks and
tracks shorter than one second reject the whole candidate. File durations are
truncated integers; embedded and synthesized chapter endpoints retain exact
track-local seconds. Single tracks retain Silo's empty presentation kind and
zero part fields.

The reserved group key is `bloem-storage:audiobook:` followed by a scoped SHA-256
of location and directory identity. Native canonical and observed roots stay
empty. Metadata publication respects locked fields, present local files and
present files in another library. Joining native files add membership and
presentation rows without overwriting the existing owner's metadata. ISBN and
ASIN identity locks converge concurrent publications; audiobook provider IDs
remain distinct from ebook IDs.

Completion reconciles retained directory edges before publishing deferred
replacements. Explicit removals retire tracks at completion. The first eligible
implicit omission retains the complete old presentation; the second permits a
reduced replacement. Finalizer publications attach roots through the reconciled
graph, never through paths. If one retained presentation track becomes missing,
the finalizer hides its entire group. A subsequent valid publication restores it.
Counters merge only after the publication or completion transaction commits.

## Delivery

Whole-presentation authority is checked in one database snapshot: every present
track must have current configuration, an enabled root membership and enabled
source/installation, with contiguous and complete part indices. Start then
preflights the requested track through the pinned reader.

Native audio starts persist `allow_alternate_versions=false`. That fixes the
requested file through start and replans, including when the item also contains
local single-file and multipart recordings. Request-scoped pinning rejects other
start candidates too. Native audio never enters the shared version-candidate
list; local resume and fallback retain their existing behavior.

Only `original_http` direct play is admitted. Remux and transcode plans fail
before creating a binding with nonretryable `route_capability_unavailable`.
`InputSource` independently refuses native audio. Mid-stream failure requires a
fresh attempt for the same track; a recovery replan cannot change its source.
Clients select the track and send `start_position` explicitly.

The stream serves ranges, HEAD and conditional requests over the pinned reader.
Its authoritative MIME is `audio/mp4`, `audio/mpeg`, `audio/flac`, `audio/ogg` or
`audio/wav`. Plan MIME can remain `application/octet-stream` because opaque
catalog paths have no extension. Existing revision, credential, root and session
checks remain active on reads, and failure after response bytes aborts the
connection. Cover authority follows the same full-composition gate; cached bytes and conditional replies still require fresh publication and viewer authority.

## Compatibility and limits

Native content remains behind current host publication and playback authority.
Silo's Jellyfin and Audiobookshelf servers run again since `2393c41ee`
(2026-10-18; see [Protocol compatibility](bloem-compatibility-removal.md)). Bloem
hides native-storage files (catalog paths starting `bloem-storage:`) from the Jellyfin
handlers that need an operating-system file name (`bloemGuardNativeStorageFiles`).
No native-file adapter is wired for the Audiobookshelf server, and native audiobook
delivery through either protocol is neither implemented nor tested.
Download/conversion support requires its own admitted path and is not implied by
direct audiobook playback.

Catalog aggregate durations can sum recordings, and duplicate-recording checks
use truncated duration. S3 does not publish embedded covers. Disc directories
are separate book directories. The unchanged Silo enrichment cover fallback
cannot read an opaque native key through ffmpeg. An ebook publisher's ISBN
insert is type-agnostic and can omit a later ebook ID already held by an
audiobook; that is a separate follow-up.
