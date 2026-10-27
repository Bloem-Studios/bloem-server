# Native video playback

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current for movies, with the corrections noted below. Native video plays
> movies stored in [native storage](bloem-native-storage.md) locations (Bloem's
> provider-backed storage, for example S3) through Silo's playback planner and
> delivery handlers. It is a Bloem addition, introduced in `b781ac3ff`
> (2026-10-11: playback, the movie scanner and checked-ETag reads; main code in
> `internal/api/bloem_native_video.go` and
> `internal/api/handlers/bloem_native_video.go`). Read-ahead followed in
> `b0b819fc8` (2026-10-11). Native audiobooks followed on 2026-10-12 and are
> described in [native audio playback](bloem-native-audio-playback.md). The Silo
> merge `68070f04b` (2026-10-27) binds the login-session recheck to the
> delegated account; see [Source admission](#source-admission). The plan
> format is Silo's [playback protocol v3](playback-protocol-v3.md).

Native video uses Silo's ordinary playback plan (protocol v3) and HTTP delivery
contract, on `/api/v1` and `/api/v2` alike.
Its catalog identity is an opaque `bloem-storage:` reference. Provider URLs,
headers, credentials and private tool inputs never become client stream URLs,
catalog paths, durable recipes or worker grants.

## Source admission

Strict admission is the default: the source must supply genuine immutable
version reads or a separately verified immutable snapshot. An advertised
`revision_pinned_reads` guarantee, approved executable, ETag, size, HEAD result
or supplied hash does not prove that the backend preserves bytes. A digest
checked only after serving bytes cannot verify the bytes already exposed.

An approved `bloem.storage.s3` installation can explicitly select
`connection.revision_mode: "checked-etag"`. The host admits this exception only
for its exact retained provider source and configuration; other providers keep
strict admission. The S3 adapter prefers genuine versions where available.
For unversioned objects it checks the expected ETag and size before GET and
again after a complete exact body, before reporting successful final EOF.
It also sends `If-Match`, but some gateways ignore that condition. These checks
can detect drift and discard a failed bounded range; they cannot prove that
bytes remained unchanged between checks or across a playback session. The
checked-mode descriptor advertises `revision_pinned_reads: false`.

Each open rechecks the current account or API key, login/profile credentials,
PIN authority, organization membership, library access, source ownership,
installation generation, encrypted configuration revision and catalog file
reference. Since `68070f04b` (2026-10-27), the login-session part of that
recheck (`verifyNativeVideoActor`) passes the delegated account to Silo's
`SessionRepository.ActiveSession`, which binds a token to its session's
account (Silo #3140). A login session that belongs to another account no
longer counts as active. The host retains a sealed source descriptor for the playback
session. Replanning the same file reauthorizes it without replacing or extending
that descriptor. Every execution, seek probe, subtitle extraction and restart
opens fresh authority for the same byte revision.

## Publication

Startup constructs `ScanReader` and calls `ConfigureNativeMovies` before
installing the storage ingestor or starting scan queues. Explicit probe limits
are 128 MiB of aggregate input reads, 128 KiB chunks, two concurrent reads,
60 seconds, 4 MiB of JSON and 64 KiB of stderr. The configured trusted ffprobe
binary probes admitted MOV/MP4 or Matroska/WebM entries. Suffixes only select
the reviewed demuxer; actual bytes must contain video. Other suffixes are
skipped, including `.strm`.

The scanner seals a listed page against its current lease, checkpoint, source
and resource owners. It probes before the authorized publication transaction,
then commits files, item associations and revision references together.
Probe or authority failures do not publish a partial page or reconcile the
failure as absence. New items retain filename-derived pending metadata;
existing matched metadata survives a byte-revision update. Filesystem-based
movie metadata matching is not attached to these opaque references.

The API releases movie library attachment only when the scanner readiness flag
and native playback composition both exist. Its native-storage capability
document reports this composition. It does not certify any live source.

## Execution and ownership

Direct GET, HEAD and ranges use the authorized revision-bound reader. Progressive
MP4 remux and local HLS use a supervised private input bridge with fixed
reviewed demuxers. Progressive HEAD validates fresh source authority and joins
its admitted file owner without reading movie bytes or starting ffmpeg. Text and
PGS subtitles and attached fonts are extracted with bounded complete output;
failed output is discarded. Text burn-in extracts execution-only ASS and font
files while retaining the catalog subtitle ordinal in the durable recipe.
Software HDR-to-SDR uses the same fresh source and shared tone-map checks.

Direct play and the input bridge read through `nativestorage.ReadAhead`, never
one provider range per consumer read. It fetches 1 MiB blocks on aligned
boundaries, three ahead of a sequential reader, in at most five (direct) or
six (bridge) retained blocks. Every block fetch passes the source's own
per-read authority and pinned-revision checks. Cached bytes are copied out
only while the latest successful fresh authority check began less than one
second ago; a stopped or failed source stops delivery at the next read. So a
revocation that only a fresh check can observe stops delivery within one
second. Direct play hints a single closed request range so fetches stop at its
end. The bridge copies 1 MiB chunks and admits four concurrent ranges.

Session bindings compare their source snapshot and delegation by instant.
Stores normalize their times to UTC without a monotonic reading, because a
Redis JSON copy never keeps the location or monotonic reading of the value it
was made from.

The source admission remains owned until ffmpeg and its input handlers have
joined. Request cancellation, playback stop, source failure and worker grant
expiry cancel that owner. A cleanup goroutine cannot release the provider
while the child still consumes it. Replanning local HLS to direct or progressive
delivery tears down the previous execution and authority on commit; rollback
preserves the previous route.

## Worker HLS and recovery

Before a remote HLS start, the API seals a Redis grant for the exact worker,
transport, playback session, source descriptor and start request. Worker
attestation precedes acceptance of the immutable recipe. Header-authenticated
starts must also persist the node serving recipe before publishing a URL;
the first manifest needs it. Failed preparation revokes the new authority.

Standalone workers compose their own approved storage runtime and continuously
revalidate shared grants and delegated viewer/source authority. A worker must
be able to resolve the retained provider identities against the same selected
revision under its retained read policy. The control plane does not distribute a provider's private
identity database. Providers that cannot retain those identities on the
selected worker cannot use that route. Native direct and progressive remux
currently use integrated API execution/egress; worker-only or proxy-only
policies for those transports fail closed. Remote HLS supports the API relay
route after source authority is established. Native proxy HLS shapes are
refused before worker preparation; planning may fall back to API relay when
the delivery policy permits it. A proxy-only policy remains unavailable.
Generated worker responses retain fresh delegated source
authority until response cleanup joins. They reauthorize before successful
headers and every write, including conditional and range responses. Revocation
after framing aborts the response without appending an error document.
Worker HLS artifact routes retain their existing GET-only contract and answer
HEAD with 405; integrated delivery supports HEAD.

Recovery reauthorizes before registering a reconstructed session. Redis deny
checks are fresh and fail closed for native execution; generic filesystem
cache behavior does not authorize it. A deliberate native stop must persist a
session tombstone for the full stream-token lifetime before removing the live
session, then revoke worker authority where present. A failed write returns a
failed stop and preserves the retryable session. Without Redis, bindings stay
in bounded local memory and cross-process recovery is unavailable.

## Supported surfaces and evidence limits

The supported contract is native API video playback for admitted catalog
files. Initial movie scanning accepts MOV/MP4 and Matroska/WebM containers.
Jellyfin native-file delivery, offline/download conversion, native series
ingestion, and arbitrary remote URL or `.strm` playback remain unsupported.
(Native audiobook ingestion was unsupported when this page was written and was
added on 2026-10-12; see [native audio playback](bloem-native-audio-playback.md).
Since Silo's Jellyfin server was restored on 2026-10-18, `bloemGuardNativeStorageFiles`
in `cmd/silo/bloem_compat.go` hides native-storage files from it.) Sidecars belonging to a different original file after an
alternate-file start need their own host-owned binding; an effective-file
binding grants no access to that other file. File-switch replans that need the
new source during preparation remain denied until the host supplies pending
plan authority; a fresh start is the supported switch boundary. Replacing a
remote native transport also requires stop and a fresh start, including
remote-to-remote or remote-to-local HLS replacement. An active HLS A/V replan
that retains the exact current transport may reuse it without replacement.

Web, Apple and Android consume normal HTTP playback plan URLs. Source schemes
and provider headers are not client contracts. Their URL contracts have been
reviewed; runtime playback on those clients and production cross-replica
source operation are separate acceptance gates.

Synthetic-provider acceptance runs the real coordinator, pinned readers,
planner, handlers and ffmpeg. It verifies direct ranges and decodes remux/HLS output, exercises
seek, restart, recovery, stop/revocation and joined cancellation, and checks
HDR conversion to H.264, 8-bit BT.709. Database scan acceptance separately
checks atomic movie publication. These tests do not certify a live storage
backend, a production cluster or a Stremio/debrid implementation. The SDK's
storage byte interface can consume another admitted provider, but it does not
currently offer on-demand Stremio discovery or verified snapshot lifecycle.
