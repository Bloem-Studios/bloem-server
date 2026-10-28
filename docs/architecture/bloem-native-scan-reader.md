# Pinned entry reads during native scans

> **Provenance.** Bloem addition, part of [native storage](bloem-native-storage.md)
> (Bloem's storage-source libraries read through a storage plugin rather than a local
> path). `nativestorage.ScanReader` and this page arrived together in `ded34edfa`
> (2026-10-10) as a prerequisite with no consumer. Later: native storage movies
> (`b781ac3ff`, 2026-10-11) and audiobooks (`8b8943320`, 2026-10-12) made it the reader
> for the movie and audiobook scan paths in `internal/libraryingest`. **Status:**
> current at `e852f92bf`; the last paragraph below records what the original
> prerequisite left out, and what has since been added.

`nativestorage.ScanReader` supplies a host scan with seekable reads from an
entry in its current provider page. It grants no viewer access and accepts no
filesystem path, URL or independently selected entry identity. Library admission
is `librarykind.SupportsNativeStorageKinds`: ebook libraries always, movie libraries
only when native movie playback is ready and audiobook libraries only when native
audio is ready (`SetNativeMovieReady` and `SetNativeAudioReady`, set at start-up).

`AdmitScanPage` clones and validates the page, then seals its source, location,
runtime snapshot, lease and pending directory checkpoint. Only the originating
reader can open its entries, selected by page index. `OpenScanEntry` requires a
fresh provider description with pinned reads and the retained source/root,
then checks Stat against every listed identity, revision, size, timestamp and
display field before accepting bytes.

Authorization compares the approved runtime generation, artifact, manifest and
configuration. A short transaction retains installation, source and scan-run
authority in that order, checks organization and entitlement policy, compares
resource-owner revisions, and verifies the current lease and that the page's
directory is still pending and reachable at its sealed cursor. A scan may work
on several pending directories at once (audiobook scans use
`nativeAudioScanWorkers`), so the directory need not be the first pending one. No transaction or database connection remains open across provider
I/O. `storagesource.ValidatePendingPageTx` and the journal's `NextDirectory`
share the same lease/checkpoint verifier.

The opened file repeats authorization before and after each pinned range.
`SetAuthorityReuse` lets the reads of a page's open files share one
authorization. One check runs at a time, and a success stands for the reuse
window from when it began, so a revocation stops those reads within the window
instead of at the next read. A failure is never reused. Production uses one
second, because a probe makes hundreds of range reads and each check takes the
authority locks. Publication is unaffected: the scan publishes each page after
authorizing it under the locks in that page's own transaction.
Source, authority and runtime failures stay sticky. A terminal error clears
the entire destination of the enclosing read and returns zero, including when
earlier ranges succeeded or the terminal cause contains EOF. Ordinary EOF at
the known file size preserves valid partial reads and does not poison the file.

Cancellation stops I/O. Close stops admission, cancels the source lifetime,
joins active reads, closes the file, and releases host admission exactly once.
When used with `ToolInput`, the trusted child and HTTP handlers must also join
before file closure. The scan coordinator must retain the publication fence
after preparation; an opened entry is neither a catalog file nor a durable
authorization grant.

As first committed, this prerequisite wired no probe or scanner consumer, kept
admission to ebooks and enabled no native video playback. Since then the movie scan
(`internal/libraryingest/storage_movie.go`) and the audiobook scan
(`storage_audiobook.go`) admit their pages here and probe the opened entries; see
[native probe](bloem-native-probe.md), [native video playback](bloem-native-video-playback.md)
and [native audiobook presentations](bloem-native-audio-playback.md). The reader still
does not certify a backend's immutability: immutable versions or verified snapshots
remain required for content reads.
