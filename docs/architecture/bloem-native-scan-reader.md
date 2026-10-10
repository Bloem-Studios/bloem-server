# Pinned entry reads during native scans

`nativestorage.ScanReader` supplies a host scan with seekable reads from an
entry in its current provider page. It grants no viewer access and accepts no
filesystem path, URL or independently selected entry identity. Existing
ebook-only library admission remains in force.

`AdmitScanPage` clones and validates the page, then seals its source, location,
runtime snapshot, lease and pending directory checkpoint. Only the originating
reader can open its entries, selected by page index. `OpenScanEntry` requires a
fresh provider description with pinned reads and the retained source/root,
then checks Stat against every listed identity, revision, size, timestamp and
display field before accepting bytes.

Authorization compares the approved runtime generation, artifact, manifest and
configuration. A short transaction retains installation, source and scan-run
authority in that order, checks organization and entitlement policy, compares
resource-owner revisions, and verifies the current lease and next pending
checkpoint. No transaction or database connection remains open across provider
I/O. `storagesource.ValidatePendingPageTx` and the journal's `NextDirectory`
share the same lease/checkpoint verifier.

The opened file repeats authorization before and after each pinned range.
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

This prerequisite does not wire a probe or scanner consumer, widen library
kinds, certify a backend's immutability, or enable native video playback.
Immutable versions or verified snapshots remain required for content reads.
