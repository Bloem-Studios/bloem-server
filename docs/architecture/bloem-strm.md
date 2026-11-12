# How `.strm` files play

> **Provenance and status.** Bloem page, written on 2026-11-12 and checked against
> `main` at `bf0fde82f` plus the `.strm` changes. `.strm` support is a Bloem addition:
> Silo refuses `.strm` files ([Bloem's non-goals](../bloem/overlays/non-goals.md#strm-files-are-in-scope)).
> It covers scanning from library folders and storage sources, playback in the web
> app and Bloem's apps on the API server and on transcoding workers, subtitle files
> beside a `.strm` file, rescans that skip unchanged files, and the private-address
> switch in the admin settings. Jellyfin apps play `.strm` items, with their subtitles,
> through the Jellyfin media host for stored movies (checked on 2026-11-12). The
> technical rules are in the [For developers](#for-developers) section.

A **`.strm` file** is a small text file in a library whose content is the web address
of a video somewhere else. This page covers how Bloem finds such a file, what it keeps,
how the video plays, the one security default, and what does not work yet.

## What a `.strm` file is to Bloem

![Top row, scanning: a .strm file, the scan that checks the address and lets ffprobe read the video, and Bloem's record of the address. Bottom row, playing: the app asks Bloem, Bloem passes on, rewraps or converts the bytes, every connection goes through the address check to the remote server, and addresses on the server's own network are refused unless allowed](images/bloem-strm/flow.svg)

1. **It is a video.** Bloem reads the first line that is not empty and treats the
   file as the movie or episode its name says, like any other video. A `.strm` file
   can sit in a movie or series library folder, or in a movie or series library's
   storage source (an S3 bucket, for example).
2. **The scan looks at the video.** Bloem checks the address, then lets ffprobe read
   the video through Bloem to learn its length, picture, sound and subtitles.
3. **Bloem keeps the address to itself.** It stores the address in its own record. The
   catalog shows the file's own name and the video's details, never the address.
4. **A changed address is a new version.** When the file changes, the next scan reads
   it again. A new address replaces the old one, and a playback started with the old
   address asks the app to start again.
5. **An unchanged file is not read again.** A rescan that finds the same address, with
   the remote server reporting the same size and version, keeps what the last scan
   learned and asks the remote server for one byte only.
6. **Subtitle files beside it are its subtitles.** `Harbour Lights (2019).en.srt` next to
   `Harbour Lights (2019).strm` is offered and served like a subtitle file next to any
   video, in a library folder or a storage source. Jellyfin apps get those beside a
   `.strm` file in a library folder.

Bloem never filters what a `.strm` file points at.

## How it plays

The app gets an ordinary playback plan with Bloem's own addresses. Bloem then:

- **passes the bytes on** (direct play): each range the player asks for becomes a range
  request to the remote server;
- **rewraps** the video as MP4 without converting it; or
- **converts** it into short HLS segments.

Jellyfin apps get the same three ways through Bloem's Jellyfin media host for
stored movies, under the same checks.

Converting runs on the API server or on a transcoding worker, as for any stored movie.
A worker reads the remote address itself, from Bloem's record, and applies the same
security default with its own check. ffmpeg and ffprobe never see the remote address:
they read from a private address inside Bloem (or the worker) that fetches the bytes
for them.

**Each time the video is opened**, and about once a second while it plays, Bloem checks
again that the viewer may still watch it and that the recorded address is still the
one this playback started with.

## The security default

Content is never filtered, but the server's own network is protected:

- Only addresses that start with `http://` or `https://`.
- By default, Bloem refuses to connect to an address on the server's own network: the
  server itself, link-local addresses (including cloud metadata services), private and
  CGNAT ranges, and multicast.
- Bloem checks **every connection**, after looking the name up, and **every redirect**.
  A name that answers differently the second time, or a redirect into the private
  network, is refused too.
- A **platform administrator** can allow private addresses, for videos that really
  live on the private network, in the admin settings under **Libraries → .strm files**.

A refused address does not stop a scan: a storage source skips that file, a library
folder keeps listing it, and in both cases it does not play.

This example is made up. A movie library folder holds
`Harbour Lights (2019)/Harbour Lights (2019).strm`, whose one line is
`https://media.example.net/films/harbour-lights.mkv`. After a scan, "Harbour Lights
(2019)" appears with its length and tracks, and plays in the web app. Someone edits the
file to `http://192.168.1.20/harbour-lights.mkv`; the next scan refuses that address
and the film stops playing, until a platform administrator allows private addresses
and the library is scanned again.

## What does not work yet

- **Subtitle files next to a `.strm` object in a storage source for Jellyfin apps**,
  as for every stored video.
- **Downloads** of `.strm` items, as for every stored video.
- **Attached fonts and bitmap subtitles** of `.strm` items in Jellyfin apps, as for
  stored movies.
- The remote server must answer range requests for anything larger than 1 MiB.

## Who can change what

| Who | What they decide |
| --- | --- |
| Anyone who can write to a library folder or bucket | What a `.strm` file points at. |
| Platform administrator | Which libraries are scanned, and whether `.strm` addresses may reach private addresses (Settings → Libraries → .strm files). |
| Viewers | Nothing about the address: they never see it. |

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| `.strm` file | `remotesource.IsStrmPath`; a catalog file whose path ends `.strm`, or a storage entry named `*.strm` |
| The address, Bloem's record | `bloem_remote_sources` (`url`, `revision`, `source_revision`, `remote_size`), `remotesource.Store` |
| The address check | `remotesource.Guard`, `remotesource.BlockedAddr`, `remotesource.BlockedName` |
| Allow private addresses | `bloem.strm.allow_private_addresses` (`config.StrmAllowPrivateAddressesSettingKey`) |
| A new version | `remotesource.Revision` (`url-sha256:` of the URL) |
| Reading the video | `remotesource.File`, `scanner.(*Scanner).ProbeRemote` |
| The size and version check | `remotesource.Stat`, `remotesource.Validators`, `scanner.ReuseRemoteProbe` |
| Playing through Bloem | `internal/api/bloem_strm.go` behind `handlers.NativeVideoService` |
| A subtitle file beside it | `scanner.StrmSidecarSubtitle`, `handlers.bloemStrmSidecar` |

## For developers

- **API:** `.strm` items use Silo's playback routes and
  [playback protocol v3](playback-protocol-v3.md); there is no `.strm` route. The
  setting is written with Silo's admin settings routes
  (`PUT /api/v2/admin/settings/{key}`), which need the account `admin` role, the
  platform administrator. The web app's page is `BloemStrmSettings`
  (`/admin/settings/strm`, listed under Libraries after Silo's library settings).

### Parsing and storage

- `remotesource.ParseStrm`: UTF-8, at most 8 KiB (`MaxStrmBytes`), a leading BOM
  dropped, the first non-empty line trimmed. `ValidateURL` requires an absolute
  `http`/`https` URL with a host and no control characters. Userinfo and query strings
  are kept.
- `bloem_remote_sources` (migrations `20261112042022_bloem_remote_sources.sql` and
  `20261112051322_bloem_remote_source_validators.sql`) is keyed by `file_path`, the
  catalog file's `media_files.file_path`, so the scanner can write it during the probe
  before Silo's upsert gives the file an id. `etag`, `last_modified` and `probe` (the
  scanner's probe as JSON) are what the last probe saw and found. `url` is checked to start
  with `http://` or `https://` and to be at most 8 KiB. `revision` is
  `remotesource.Revision(url)`; `source_revision` is the storage entry revision for a
  storage object and empty for a local file; `remote_size` is the remote size at scan.
  Backups classify the table as server state (`bloemServerStateTables`): the next scan
  rebuilds it, and URLs can carry tokens.
- The URL never enters a catalog path, a stream URL or token, a playback recipe, a
  worker grant, an API response or a log line. Errors from `remotesource` name the
  HTTP status or the cause, never the URL (`scrubURL` strips `*url.Error`).

### The guard

- `remotesource.Guard` owns its `http.Transport`: `Proxy: nil` (environment proxies
  would hide the address reached), `DisableCompression`, dial 10 s, TLS 10 s, response
  headers 20 s, no overall timeout (bodies are bounded by the request context).
- `DialContext` resolves the host itself (an IP literal is used as is, IPv4-mapped
  addresses unmapped, zones dropped), drops every blocked answer, and dials only the
  remaining IP literals. `net.Dialer.ControlContext` checks the socket address again.
  A name whose answers change between `Check` and the dial (DNS rebinding) is refused
  at the dial.
- `CheckRedirect` allows `http`/`https` only and at most 10 hops (`MaxRedirects`);
  each hop dials through the same checks.
- `BlockedAddr` refuses unspecified, loopback, private (`10/8`, `172.16/12`,
  `192.168/16`, `fc00::/7`), link-local unicast and multicast (`169.254/16`,
  `fe80::/10`), interface-local and other multicast, `0.0.0.0/8`, `100.64.0.0/10`,
  `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`, `fec0::/10`, `100::/64`, and the IPv4
  address embedded in NAT64 (`64:ff9b::/96`) and 6to4 (`2002::/16`) addresses.
  `BlockedName` refuses `localhost`, `*.localhost`, `metadata`,
  `metadata.google.internal`, `instance-data` and `instance-data.ec2.internal`.
- `bloem.strm.allow_private_addresses` (default `false`, a boolean admin setting,
  restored at the stricter value) turns the address and name checks off; the scheme
  check stays. `remotesource.ForPool` shares one `Store` and `Guard` per database pool
  and caches the setting for 5 seconds. A switch from allowed to refused closes idle
  connections.

### Reading

- `remotesource.Open` fetches `Range: bytes=0-1048575`. A `206` must carry a
  `Content-Range` with a known total. A `200` is accepted only for a body of at most one
  block; a larger body without ranges fails with `ErrRangeUnsupported`.
- `File.ReadAt` serves from one cached block or fetches at least one block from the
  offset; every range must keep the total size and the ETag seen at open
  (`ErrChanged`, sticky). It implements `mediasource.File` plus `Err`, `Cancel` and
  `Context`.

### Scanning

- Local folders: two Bloem lines in Silo's `internal/scanner/scanner.go` (a declared
  seam). `walkMode.acceptsPath` takes `.strm` in video walks (`bloemStrmFile`), and
  `probeFile` hands `.strm` paths to `probeBloemStrm` (`internal/scanner/bloem_strm.go`).
  Change detection is Silo's (size and modification time). A failure deletes the
  source row. Content that is not a URL or media that is not video counts as a probe
  rejection; a refused address or a network failure stays retryable. `ScanFile` (the
  single-file trigger) still refuses `.strm`; the next library scan picks the file up.
- Storage sources: `StorageScanner.ConfigureRemoteSources` (startup, in
  `cmd/silo/bloem_native_movie_scanner.go`) makes `prepareMovies` and
  `prepareSeries` take `.strm` entries of movie and TV libraries (same extras,
  sample and episode-number rules as other videos), through `prepareStrms`. A
  `.strm` episode counts against the series probe budget and pacing like any
  episode. `prepareStrmVideo` opens
  the pinned entry with the scan reader, reads it, and probes while the entry stays
  open, rechecking its authority on every probe read. An unusable URL skips the entry
  as excluded (an older publication goes missing) and deletes its row; a failure of
  the scan's own authority fails the page. The row's `source_revision` is the entry
  revision.
- Rescans: `scanner.ReuseRemoteProbe` reuses the stored probe when the URL (and, for a
  storage object, the object revision in `source_revision`) is the recorded one and
  `remotesource.Stat` (a `Range: bytes=0-0` GET through the guard) reports the same
  size and the same ETag, or the same Last-Modified when no ETag was recorded. With
  neither, or on any error, the file is probed again. Local folders reach this only
  when Silo's size and time check already sees a change. Storage sources read a page's
  rows with one `GetMany` query, run the checks eight at a time (`strmReuseWorkers`) and
  publish reused entries without opening them; 4,999 unchanged entries take well under
  a second against a local server (`TestStrmReuseScalesToThousandsOfEntriesDB`).
- `Scanner.ProbeRemote` sniffs the container (EBML, an MOV atom, `RIFF…AVI `, MPEG-TS
  sync bytes at 188 or 192, then the URL's extension) and runs `ProbeNative` with the
  native movie limits (128 MiB of reads, 128 KiB chunks, two concurrent reads, 60 s,
  4 MiB of JSON, 64 KiB of stderr). `ProbeNative` gained `NativeProbeAVI` and
  `NativeProbeMPEGTS`. The probe must find video. Every storage `.strm` is probed again
  on each scan, as stored movies are.

### Playback

- `storagesource.IsNativeLocation` is true for a path with a `.strm` extension, so
  every guard that keeps native files from Silo's OS-path code also keeps `.strm` text
  away from clients: stream file resolution, downloads, the Jellyfin file guard, the
  proxy and worker input authorization.
- `NativeVideoService.Open` and `OpenDelegated` (`internal/api/bloem_native_video.go`)
  call `strmPlayback.open`, which looks up `bloem_remote_sources` by catalog path:
  - a local `.strm` with a row opens the URL under `nativeVideoAuthority` /
    `nativeVideoDelegatedAuthority`; its identity carries `Reference.Revision =
    revision` and `Reference.LogicalPath` = the catalog path, and no storage location;
  - a storage `.strm` with a row opens the object through
    `Coordinator.OpenAuthorizedPlayback` (every storage check), requires
    `source_revision` to equal the object's revision, and takes the size from the
    remote;
  - a local `.strm` without a row is not found; any other file opens as before.
- `strmVideoFile.Reauthorize` rechecks the viewer and catalog file (and the storage
  source for an object) and that the row's `revision` and `source_revision` are the
  bound ones; otherwise `ErrReferenceConflict`, which the delivery answers as
  `409 source_revision_changed`. Direct play, progressive remux and local HLS are
  tested with `.strm` items; seek anchors and embedded subtitle extraction use the same
  opened file through the unchanged native pipeline.
- An open the guard refuses is `strmRefusedError`: still
  `remotesource.ErrBlockedAddress`, and a 403 `source_refused` `handlers.APIError` to
  `errors.As`, so the Jellyfin media host answers 403.
- Subtitle files in storage: a storage `.strm` object's text subtitles are its
  sidecars in `bloem_storage_episode_sidecars`, paired by the movie and series lanes
  like any stored video's ([native storage](bloem-native-storage.md)), and are listed
  and served through the provider like other native sidecars (`serveNativeSidecar`).
- Subtitle files beside a local `.strm` file: Silo's scanner already lists files named
  for it (beside it or in its `Subs/` folder) as its `external_subtitles`, which
  `withNativeSidecars` leaves in place for a local `.strm` path. The subtitle route
  in `stream.go` (a declared seam, one Bloem condition) serves such a track only when
  `scanner.StrmSidecarSubtitle` finds it among the `.strm` file's sidecars by Silo's own
  rules at request time, after the native source preflight; it is then served by
  Silo's local sidecar code. Every other external subtitle of a native file stays 404.

### Jellyfin apps

- `.strm` items play through the Jellyfin media host for stored movies
  ([Jellyfin clients](bloem-native-video-playback.md#jellyfin-clients)):
  `NativeVideoCompat.Handles` takes every native location, `.strm` included, so
  direct play, remux, local HLS, copy seek anchors, worker HLS and embedded text
  subtitles read the host's pinned source, which `strmPlayback.open` opens as for
  Bloem's apps. A refused address answers 403.
- `bloemNativeStorageFiles` (`cmd/silo/bloem_compat.go`) hides a `.strm` file from
  every Jellyfin handler except a stream request that carries the host (video,
  HLS, subtitles, attachments).
- Subtitle files: PlaybackInfo lists the `external_subtitles` Silo's scanner found
  beside the local `.strm` file as external streams, and Silo's subtitle route
  serves them from disk as for any local file. That read is not a source open, so
  it does not run the native recheck the API route runs; the compatibility session
  and the playback session authorize it, as for a local file.
- Embedded text subtitles: Silo's subtitle cache keys a track by the `.strm` file's
  size and modification time, which change with its URL. On a miss its batch demux
  hands ffmpeg the `.strm` text file, which ffmpeg refuses as invalid data (a
  warning in the log); the single-track extract then reads the host's source. The
  text is cached under the `.strm` file like a local file's.

### Workers

- The worker grant (`NativeVideoWorkerGrantStore.prepare`) binds a storage object's
  catalog location, or, for an identity with no storage location whose catalog path is
  a local `.strm` file, that path (`nativeVideoGrantLocation`). The grant, the start
  request, the recipe and the stream token carry only the catalog path and the URL's
  revision.
- The worker composes `NewNativeVideoDelegateService` as for stored movies; its
  `strm.open` reads the URL from `bloem_remote_sources` itself and opens it through its
  own `remotesource.ForPool` guard, which reads the private-address setting from the
  database and checks every connection and redirect. A worker without approved native
  storage does not answer `GET /bloem/native-execution` and is skipped.

### Tests

- `internal/remotesource`: `TestBlockedAddr`, `TestBlockedName`,
  `TestGuardRefusesLiteralPrivateAddressesWithoutConnecting`,
  `TestGuardAllowsPublicAndHonoursTheSetting`, `TestGuardRefusesDNSRebindingAtConnect`,
  `TestGuardSkipsPrivateAnswersOfAMixedName`, `TestGuardRefusesRedirectsToPrivateAddresses`,
  `TestGuardBoundsRedirectChains`, `TestGuardIgnoresEnvironmentProxy`, the `TestFile*`
  range tests, `TestParseStrm`, `TestAllowPrivateSettingKeyMatchesConfig` and
  `TestRemoteSourceStoreDB`.
- `internal/scanner`: `TestLibraryWalkTakesStrmFiles`, `TestProbeRemoteReadsRemoteVideo`,
  `TestProbeRemoteRefusesPrivateAddressesByDefault`, `TestSniffRemoteDemuxer`,
  `TestLocalStrmScanRecordsRemoteSourceDB`.
- `internal/libraryingest` (integration tag): `TestNativeStrmStorageScanPublishesRemoteMovieDB`,
  `TestNativeStrmStorageUnreadableURLSkipsOnlyThatEntryDB`,
  `TestNativeStrmStorageSeriesEpisodesDB` (a storage `.strm` episode, its sidecar, an
  unreadable one skipped, the one-byte rescan, and the object revision playback needs),
  `TestNativeStrmStorageMovieSidecarDB`.
- `internal/api`: `TestStrmDirectPlayProxiesRanges`, `TestStrmURLChangeIsANewRevision`,
  `TestStrmReauthorizeChecksTheViewer`, `TestStrmOpenRefusesPrivateAddressesAndUnrecordedFiles`,
  `TestStrmStoredObjectNeedsTheScannedRevision`, `TestStrmRemuxRunsFFmpegThroughTheBridge`,
  `TestStrmHLSTranscodeReadsThroughTheBridge`, `TestStrmRefusedAddressReadsAs403`, and
  `TestStrmWorkerHLSRunsOnTheWorkerThroughItsOwnGuard` (Redis: an API host seals the
  grant, a separate worker host with its own guard resolves it and runs a real ffmpeg
  HLS encode, for a local and a storage `.strm`).
- Jellyfin: `TestStrmJellyfinDirectPlay`, `TestStrmJellyfinRefusalsAnswer403`,
  `TestStrmJellyfinRemuxAndTranscode`, `TestStrmJellyfinWorkerStartSealsTheGrant`
  (`internal/api`) and `TestStrmSubtitlesServeForJellyfinApps`
  (`internal/jellycompat`: a sidecar and an embedded track through Silo's subtitle
  route, both 404 without the host).
- `internal/api/handlers`: `TestNativeVideoWorkerGrantAdmitsLocalStrmItems`,
  `TestNativeVideoWorkerGrantRefusesLocationFreeStorageIdentities`,
  `TestStrmSidecarSubtitlesServeLikeLocalSidecars`, `TestStrmSidecarSubtitlesStayBesideTheirStrmFile`.
- Rescans: `TestStatReadsValidatorsWithOneByte`, `TestValidatorsUnchanged`,
  `TestRemoteSourceValidatorsDB`, `TestLocalStrmRescanSkipsUnchangedRemoteDB`,
  `TestNativeStrmStorageRescanSkipsUnchangedRemoteDB`, `TestStrmReuseScalesToThousandsOfEntriesDB`.
- Web: `BloemStrmSettings.test.tsx`.
- The DB tests are listed in `scripts/ci/bloem-library-locations.txt`.

### Known limits

- Jellyfin: attached fonts are not listed for a `.strm` item: Silo's font probe reads
  the `.strm` text file. Bitmap subtitles cannot be extracted, as for stored movies.
- `media_files.file_size` of a local `.strm` is the text file's size; the remote size
  is in `remote_size`. Silo's OSHash logs that the file is too small.
- Silo switching: Silo, serving the same database, treats a local `.strm` row as a plain
  file, and its own scans drop the row.
- A local `.strm` item's external subtitles are served only for files beside it by
  Silo's sidecar rules; a storage object's only from its recorded sidecars.

The picture is an SVG file under `docs/architecture/images/bloem-strm/`, drawn by
`scripts/bloem/draw-pictures.py` from `scripts/bloem/pictures/bloem_strm.py`.

Related pages:

- [Bloem's non-goals](../bloem/overlays/non-goals.md): why `.strm` is in scope.
- [How stored movies play](bloem-native-video-playback.md): the native pipeline.
- [Native probe](bloem-native-probe.md): `ProbeNative` and its limits.
- Index: [Bloem documentation](../bloem/README.md).
