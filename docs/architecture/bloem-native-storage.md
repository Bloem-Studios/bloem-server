# Bloem native storage

Native storage lets ebook, movie and audiobook libraries read files from storage plugins,
such as Bookwarehouse, alongside filesystem paths. A library can retain multiple
plugin locations, each with independently selected directory roots. It remains
experimental.

Movie admission is startup-only. The host configures the scan reader and bounded
probe before starting queues; the API then admits movie attachment only after
composing the native playback service. An incomplete composition retains the
publication hold. This describes code readiness, not production source certification.

Audiobook scanning also requires the composed direct-play service through a shared
startup latch. See [native audiobook presentations](bloem-native-audio-playback.md)
for publication, retained authority and fixed-file delivery.

## Model

- **Storage plugin.** A host-approved executable that serves the private
  `bloem.plugin.v1.StorageProvider` service (`Describe`, `List`, `Stat`,
  revision-checked `Read`). Installations are recorded in `plugin_installations`
  and marked native in `bloem_storage_installations`.
- **Storage source.** `bloem_storage_sources`: one configured source of an
  installation, with a retained UUID key, owner, encrypted configuration and a
  configuration revision. A source outlives its installation's removal.
- **Library storage location.** `library_storage_locations`: binds one source to
  one library. Libraries can combine local paths and multiple source selections;
  one source can back multiple libraries. Selected opaque roots are retained in
  `bloem_storage_selected_roots`. Enabled `movies` libraries also require the
  scanner and playback readiness boundary. A retained location's source identity
  does not change when its selected roots change.
- **File reference.** `bloem_storage_file_refs`: a published catalog file's
  location, entry ID, revision and display path, plus the cover the provider
  offers for its book. The file's `file_path` is the catalog location
  `bloem-storage:` + SHA-256 of the location UUID and the length-prefixed entry
  ID. It is an internal identity, never a filename, mounted path or URL.

The whole `bloem-storage:` namespace is reserved, including malformed identities.
Path-only consumers refuse it before filesystem probing, missing-file marking
or tool invocation. The native ebook reader and video service use separate
authorized source resolvers. Alternate editions and multipart start may select
supported native video only when that service is installed. Downloads and
Jellyfin delivery still refuse the namespace. Nothing opens it as a local path.
The [video execution contract](bloem-native-video-playback.md) describes native
API serving, worker HLS and the remaining surface limits.

## Creating a library

The native library-locations API can submit multiple filesystem paths and
multiple selected provider roots together in one library mutation when the
server advertises location mixing. Source browsing returns `current` separately
from `breadcrumbs`: the
breadcrumb list contains ancestors from the source root through the immediate
parent, and is empty at the source root. The web picker uses its final breadcrumb
for Up, displays the current directory separately, and retains selections while
navigating. Pending selections use the multi-folder action rather than replacing
them with the current folder. Directory references remain opaque; display names
and logical paths do not become provider identities. S3 prefixes and Bookwarehouse
virtual directories follow the same browser contract.

An administrator creates an ebook or admitted movie library through the ordinary v2
`createLibrary` with `storage_source` instead of `paths`. The creation
transaction retains the installation's `FOR SHARE` lock before inserting the
location, whose immediate foreign key acquires a source lock. This agrees with
native management's installation-before-source order. The full tenancy check
that gates every scan (`resourcetenancy.RequireStorageScanTx`) then validates
the new location and reloads retained source fields after any lock wait:

- a platform library may use a platform source;
- an organization library may use its own organization's source, or a platform
  source its active organization is entitled to.

The source must be enabled, with an enabled, native-marked installation. A
refused attach rolls the whole library creation back. The library is then
scanned like any other.

Sources are installed, upgraded, configured, disabled and uninstalled through the
protected `/api/bloem/v1/admin/{platform,organization}/native-storage` routes
(artifacts, sources, installations, configuration). Installation selects a
publisher-verified artifact; an upload cannot approve its own checksum. Bookwarehouse releases from the fixed Bloem catalog are admitted automatically, with no operator approval or restart. Catalog installation downloads the server-platform binary and checks its SHA-256 before the existing transactional source installation. The UI then stores URL and API key through the encrypted configuration boundary. Upgrade
swaps an installation's executable for a newer approved artifact of the same
plugin in one transaction and bumps its runtime generation, so the running
process is replaced; the source, its encrypted configuration and its library
stay as they are. Ordinary plugin handlers hide native installations.

Configuration replacement, upgrade and removal fence the installation's runtime
while they run. A refused or failed action that commits nothing lifts its fence:
the unchanged generation restarts on the next read or scan. After a commit, or a
COMMIT whose outcome is unknown, the previous generation stays fenced and only
a committed higher generation runs.

## S3 process state

The manually approved `bloem.storage.s3` provider needs a durable private
identity and cursor database. The registry derives its `connection.state_path`
from the installation ID under `state/s3/installation-<id>/index.db` within the
host installation root. Executable replacement, runtime generation changes,
credential rotation and host restart preserve this path. Separate installations
have separate state; reinstalling a detached source creates a new installation
and does not migrate the previous installation's identity database.

The path exists only in configuration prepared for the host runtime. It is
never persisted in `plugin_runtime_configs` or added to configuration API
views. S3 install and configuration replacement require one native `connection`
object and reject any supplied `state_path`, including empty or null values.
Runtime snapshots also refuse historical supplied paths and legacy envelopes.
An incomplete connection object may be installed; the plugin then stays
unconfigured until its required connection settings are supplied.

On Unix hosts, descriptor-relative no-follow traversal checks every ancestor.
Ancestors must belong to the host UID or root and cannot be group- or
world-writable, including sticky temporary directories. The installation root
and its state directories must belong to the host UID with mode 0700. An
existing database must be a regular file owned by that UID with mode 0600 and
one hard link. Symlinks and unsafe permissions fail closed; the registry does
not repair them. Hosts without these ownership checks refuse S3 state admission.

Install and upgrade prepare state and runtime configuration before committing.
A disabled installation may receive a package-only upgrade and a non-runnable
return snapshot; its enabled flags stay false and ordinary runtime `Snapshot`
still refuses it. This avoids reporting a committed disabled upgrade as a
failure. The S3 provider's checks do not certify a backend. Strict admission
requires genuine immutable versions or a separately verified snapshot;
explicit checked-ETag admission is described below. Other library kinds remain outside
native movie ingestion and playback admission.

## Initial S3 root discovery

The manual-upload `POST /installations/discover` operation in each native-storage
admin scope installs a new approved `bloem.storage.s3` source without requiring a
caller-selected root. Its separate command requires `artifact_key`, the exact
opaque `provider_source_id`, `enabled` and complete `config`. Only the platform
scope accepts optional `organization_id`. Caller root, source key and revision
fields are rejected, including null. The existing explicit-root install operation
keeps its request and supports disabled/unconfigured package installation.

The registry reserves the final installation sequence ID and prepares its
approved executable, frozen user configuration and private S3 state at that ID.
No installation or source row is published during discovery. Source management
retains host admission and rechecks current account/session, administrative scope,
owner and organization authority after preparing the execution snapshot,
immediately before starting its private one-shot manager. That short transaction
ends before process startup; no database transaction or connection spans provider
I/O. A fresh, bounded Describe must advertise the requested source and its exact
opaque root, and satisfy the retained read policy below. Discovery performs no
List, Stat or Read.

After Describe, the registry seals the private state file's identity and content
hash. The private process must be reaped before publication, so a committed
consumer cannot open the same S3 identity database concurrently. Publication
verifies the original directory and executable identities and sealed state again,
including after database row waits. Missing, replaced or modified files refuse
publication; cleanup preserves replacements it does not own.

The publication transaction retains current account/session, administrative
scope, owner and organization authority and inserts the installation, marker,
archive, encrypted user configuration and new source atomically. It rechecks
authority after final file hashing and snapshot construction, immediately before
COMMIT, because retained row locks do not prevent clock-based expiry. The
committed source retains the root byte for byte. Desired `enabled: false` still
requires successful configured discovery, then leaves both resources disabled.

Cancellation or established rollback cleans only the unpublished preparation
after reaping. A bounded reaping timeout retains process, preparation and host
admission ownership until the reaper finishes; it never publishes. Unknown
COMMIT outcomes preserve the executable and state and use the existing
`operation: install` reconciliation response. They do not trigger an automatic
retry or compensating deletion.

`s3_initial_root_discovery` is true only when the router's normal storage
composition is ready and source management has the matching host/registry and
runtime manager. It is not backend certification: `backend_verified` remains
false. The retained source read policy still applies to every consumer.
This operation does not expand library-kind support, catalog admission or serving.

The Kotlin and Swift contract artifacts include the discovery command and
capability. This administrative upload operation adds no viewer workflow to
Bloem Apple or Android, and changes no Jellyfin library or playback behavior.
An admin client that adopts it must check `s3_initial_root_discovery` and
reconcile an unknown installation outcome before retrying.

## Scanning

The executor holds one whole-library claim while it runs the existing
filesystem workflow, followed by the ordered plugin location selections. A
failed lane retains its error and counts while independent locations continue;
parent cancellation stops the remaining work. Aggregate catalog events publish
once, and the last-scanned timestamp advances only when every lane succeeds.
File and subtree scans remain local and never enumerate plugin selections.
Early filesystem failures join their match drainers before returning committed
scan and match counts to the aggregate. Native completion reports membership
and item removals after its transaction commits, even when no file becomes
newly missing and trash removes no file.

Startup enables the location domain's mixed-location save guard and capability
only when the executor has both filesystem dependencies and its configured
native storage scanner. This witness does not replace movie playback admission
or enable native audio.

The optional `MaintenanceFileScope` limits filesystem candidates. With no scope,
the original candidate SQL and reconciliation behavior remain unchanged. Native
composition installs a scope excluding every `bloem_storage_file_refs` row,
including files already missing. It guards marking, outside-root deletion,
trash, scoped reconciliation, and the shared ebook/audio cleanup. Retained
native versions anywhere in the catalog also prevent cascading orphan deletion.

For each selection, `libraryingest.StorageScanner`:

1. Checks tenancy (`RequireStorageScan`), loads the approved snapshot, starts
   or reuses the plugin process and obtains a fresh, bounded `Describe`.
   The advertised source ID and root must match the retained source exactly,
   under its retained read policy. After discovery, it reloads the retained source,
   rechecks tenancy and checks the executable generation and checksum again
   before beginning a scan lease.
2. `BeginLocationAuthorized` begins or resumes discovery in a transaction that checks
   and retains current installation, source, library, owner and entitlement
   authority before the journal's source and run locks. A prior preflight
   check grants no later journal write. Refused authority leaves the run,
   checkpoints and discovery pointer unchanged. The database clock decides
   lease expiry; each takeover bumps the epoch. Standalone discovery may use
   `Begin` without library authority. A renewal worker extends the lease and
   rechecks tenancy. Each committed page and the completion transaction also
   renew it, because they make the same checks under the same locks. A renewal
   waits for the library lock a page holds; an attempt that runs out of its
   10-second bound is retried while the lease was renewed within its duration.
   Any lease or authority fence still stops the scan.
3. Lists each directory page by page. `ApplyPageAuthorized` checks current
   installation, source, library, owner and entitlement authority inside the
   page transaction, before journal source/run locks. It retains those
   resource locks through the commit of the page's journal entries
   (`bloem_storage_entries`), directory checkpoint and catalog publication.
   Replay also requires current authority. Provider I/O stays outside this
   transaction. Refused authority or failed publication rolls the page back,
   so a retry lists it again. The unfenced `ApplyPage` entry point remains
   available for standalone journal discovery. A directory the filesystem
   scanner would never walk is recorded but not queued for listing
   (`scanner.StorageDirectorySkipped`): Silo's ignored names (`.recyclebin`,
   `@eaDir`, `.downloads`, ...) for every kind, plus sample, subtitle and
   title-owned extras folders for movies. `.ignore`, `.siloignore` and
   `.nomedia` are not read: honoring them would need a pinned read of each
   ignore file and directory state across listing pages.
   A page extends the run's root reach (`bloem_storage_scan_directory_roots`)
   and publishes root memberships (`bloem_storage_file_roots`) only through
   its own entries and the directories they newly reach, so a page costs its
   own entries and those directories. A directory's first page also counts
   its retained edges, and its completing page publishes files staged by
   earlier pages; both visit that directory's children. A page that removes,
   demotes or skips a directory edge, or a listing restarted in full,
   rebuilds the whole reach and republishes its memberships, at a cost that
   grows with the location.
4. Stops lease renewal, then `CompleteLocationAuthorized` checks current resource
   authority before journal source/run locks and renews the lease for its own
   work. It rebuilds root memberships from the completed graph, computing the
   reachable set once, and writes only memberships and absence stamps that
   change. It then checks and renews the lease again, so reconciliation and
   the finalizer each have the lease's full duration. Its finalizer runs
   `SweepStorageLocationTx` to reconcile memberships and orphans and sweep
   expired missing files with the existing grace and empty-trash settings.
   Authority stays locked through the single catalog and journal commit;
   the lease is checked again after cleanup waits. Refused authority, failed
   cleanup or an expired lease rolls back both cleanup and completion.
   Orphaned artwork is removed after commit. Standalone discovery may still
   use `Complete` without catalog finalization.
5. A scan that stops without completing, failed or cancelled, stops renewal
   and `Release`s its own owner and epoch. The run and its journal stay, so a
   retry resumes at once instead of failing as a stale lease until the claim
   expires.

The scan queue reports any error containing `context.Canceled` as an operator
cancellation, with no message. Provider streams, the tool bridge and the
scan's job context report their own teardown that way too, so the executor
reports a storage scan as cancelled only when the scan's own context was
cancelled. Any other failure is reported as failed and logged, with a message
that names the movie entry and a reason chosen from typed causes; provider
details and tool output stay out of it.

Publication (`scanner.PublishStorageEbooksTx`) is set-based: a page of up to
512 entries costs a fixed number of statements. Entries carrying provider
`EbookMetadata` publish without the host reading the file. Entries without it
are opened and parsed (`ParseNativeEbook`). Files group into items by ISBN, or
title and authors, as file scans do. Items publish with status `matched`: the
source supplies their metadata. Provider metadata and credits are reapplied only
when the item has no present local version and no locked fields. A native edition
joining a curated local item preserves its title, credits, sort title and artwork.

## Movies

Movie publication (`scanner.PublishStorageMoviesTx`) reads identity from the
entry's logical path, not only its file name. The path is placed below a
parse-only root (never persisted or opened) and run through Silo's own naming
rules: root inference, content-group identity and edition hints. So
`13 Eerie (2013)/xyz.mkv` is "13 Eerie" (2013), and a folder such as
`3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}` supplies its title, year and
provider IDs. A source rooted at a single title folder exposes only file
names, because the provider's logical path is relative to its root.

- Files are probed and published only when Silo's walk would collect them:
  sidecars (`.srt`, `.nfo`, images) are never opened, and samples, files in
  sample/subtitle folders and local extras (convention folders owned by a
  title folder, or `-trailer`-style suffixes) are skipped without a read. A
  file an older scan published that is now skipped is marked missing.
  Extras are not published; a native listing cannot prove that a title folder
  holds media, so an extras folder counts as title-owned when its owner is
  named like a title (`Title (Year)` or provider tags).
- A new file joins the movie item of its content group in the library
  (`media_files.content_group_key`): other editions and copies of the title
  become versions of one item. Edition keys and page-local multipart totals
  are recorded. A new group gets a `pending` item, or `ambiguous` when
  folder and file titles conflict without provider tags.
- After each page commits, the scan matches its pending items through the
  metadata pipeline (`MetadataService.Process`), behind the next page's
  listing and probing and joined before the next publication and before
  completion. A folder or file TMDB tag (else IMDb) applies as
  `POST /api/v2/admin/items/{id}/match/apply` does (`ModeIdentify`). Other
  items search with their parsed title, year and Silo's alternate identities
  (`ModeInitialMatch`); no result marks them `unmatched`. A failed request
  leaves the item `pending` for the next scan, and an `unmatched` item with
  provider tags is retried. Each item is attempted at most once per scan.
- Native files are never enqueued in the path-based movie match queue,
  whose matcher derives identity from `media_files.file_path`. The existing
  startup queue seeding still selects `pending` and `unmatched` native files
  in movie libraries; it can mark them `unmatched`, which the next scan
  repairs for tagged items.

## Incremental listing

A directory's final page may carry a `change_token`, stored in
`bloem_storage_change_tokens`. The next scan sends it as `changes_since`, and
the provider lists only books added or changed since, plus `removed_entry_ids`.
Removed entries change retained directory edges. Successful location completion
reconciles that location's root evidence and marks newly absent files missing.
Its sweep applies the existing removal grace and trash setting only to files
owned by that location; other locations and local versions remain retained.
A book listed again before deletion is restored. A token the provider refuses
(`FailedPrecondition`), from another configuration revision, or older than
seven days falls back to a full listing, which also reconciles anything a change
feed missed. Each directory retains its effective full or incremental mode
before provider I/O, including token-refusal fallback and resumed cursors.

`complete` certifies traversal termination, not a point-in-time inventory.
Bookwarehouse fixes an upper bound when a listing starts and can defer a
present book updated before the cursor reaches it. Implicit absence therefore
takes two completed runs. A nonempty full listing suspects each retained
location/parent-directory edge it omits. A later completed full or incremental
listing of that parent confirms the omission, unless an empty full listing
makes the result suspect. Positive observations clear edge suspicion; failed
and partial runs never raise or confirm it. A changed configuration or selection
invalidates prior negative evidence. Explicit removed IDs delete their
parent edge immediately, and successful authorized completion updates missing
flags using the resulting selected-root graph.

A suspect directory remains available through its retained subtree without
being traversed again unless positively listed. Other selected roots and other
locations retain independent edges and memberships. Implicit deletion needs
one extra scan; explicit deletion has no extra delay. No current protocol field
lets the host safely distinguish a snapshot inventory from a traversal, so this
rule applies uniformly rather than relying on a plugin-ID exception.

A listed book whose entry is unchanged is not republished. Its file reference
records a digest of the entry it was published from (`entry_sha256`: revision,
provider metadata and cover), and a present file whose entry is listed again
with the same digest, configuration revision and library membership writes
only its journal entry. A forced full listing of an unchanged library rewrites
`bloem_storage_entries`, not its catalog rows. A metadata-only change counts as
updated and republishes that book.

## Covers

Covers stay with their source; the host never copies them into artwork
storage. A scan records each book's cover entry and revision, and the item's
poster path names it: `bloem-storage://storage-covers/<content_id>/original.<revision>.img`
(`artworkkey.StorageCoverPath`). The revision hashes the provider's cover entry
and revision, so a changed cover gets a new URL. Provider-matched or manually
chosen artwork is kept.

The image resolver always resolves these paths to the signed
`/api/v2/artwork/storage-covers/...` route, whichever backend stores other
artwork. A cover URL is signed in week-long windows: it stays the same for the
week and is valid for at least a week, so clients keep the cover instead of
downloading it again under a new URL. Responses carry the revision as their
ETag and are cached as immutable for the URL's lifetime, so a revalidation
never reaches the source.

The route, on every listener, reads covers through `nativestorage.Host`:

1. The optional cover cache, a dedicated Redis named by
   `BLOEM_STORAGE_COVER_CACHE_URL`, is checked first. Keys hold the content ID
   and cover revision, so a changed cover is a different key and never served
   stale. Entries expire 30 days after they are written.
2. On a miss, concurrent requests for one cover share a single read through
   `nativestorage.Coordinator.OpenCover`. The source must still be enabled
   and allowed to serve the library (`RequireStorageScan`), checked again after
   the plugin starts; the signed URL is the request's authority, as for any
   artwork. Each node reads at most 16 covers at once from sources.
3. The cover is written to the cache.

The cover cache must be its own Redis with a memory limit and an LRU policy
(`maxmemory`, `maxmemory-policy allkeys-lru`). The server's main Redis runs
without eviction and holds sessions and grants; it must never hold covers. A
cache that is unset or unreachable only sends reads to the source. A cached
cover stays servable to holders of its signed URL after its source is
disabled, as stored artwork does.

A changed or removed cover, or an unavailable source, answers 404 and clients
show the provider's thumbhash placeholder.

## Reading

The ebook reader authorizes the file through the ordinary reader resolver, then
`nativestorage.Coordinator` resolves its file reference, checks the source and
installation are still enabled and opens it through the plugin, pinned to the
reference's revision. Every runtime borrow obtains a fresh `Describe`; the
coordinator validates the retained source/root pair before `Stat` or `Open`,
then reloads the reference, authorization and executable snapshot. Cover reads
use the same admission checks. A canceled request or runtime lease cannot
start content I/O. File lifetime is tied to request and runtime
cancellation. GET/HEAD, ranges and conditional requests behave as for local
files; ETags hash the opaque revision. A provider failure at open or before
the first byte is sent is an ordinary HTTP error, such as 404 for a missing book
or 503 for an unavailable provider; after bytes were sent the response is
aborted without an error document. Downloads, conversion and offline delivery
of storage files are not supported.

## Protocol

`proto/bloem/plugin/v1/storage_provider.proto` mirrors the SDK schema; the host
uses its own generated bindings in `internal/storageproto`.
`scripts/generate-bloem-storage-proto.sh` pins protoc 3.21.12, protoc-gen-go
1.36.11 and protoc-gen-go-grpc 1.6.1; `--check` verifies the generated files.

Hosts request at most 512 entries and 1 MiB per page with a 30-second deadline.
`Read` delivers exactly the requested range (at most 8 MiB) in ordered chunks
of at most 128 KiB and must end with status OK; a partial read is discarded.
Cursors must advance and a change token appears only on a final page.

`Describe` has a ten-second deadline, a 1 MiB response bound and at most 128
sources. The protocol revision must be 1. Every advertised source has a unique
source ID and a nonempty root ID; both are opaque UTF-8 strings of at most
1024 bytes without NUL. Root IDs may repeat in different source namespaces.
An empty source list is valid for an unconfigured process, but cannot admit a
scan or read. Startup and health checks validate the entire response, including
unselected sources. An RPC error or cancellation invalidates its response.
Runtime RPCs bind both the caller's context and the executable generation's
lifetime; admitted descriptions are copied for their consumer.

Strict read admission requires an advertised revision-pinned guarantee and
genuine immutable backend versions or a separately verified snapshot. The
advertisement is not evidence that a backend preserves bytes.

The host also supports explicit `connection.revision_mode: "checked-etag"`
for the approved `bloem.storage.s3` adapter. Admission requires the exact
retained S3 installation and provider source, a single `connection` object,
and a matching configured `source_id`. This exception applies consistently to
initial discovery, listing, scan probes, playback, ebooks and covers; it never
rewrites the retained root or bypasses source/configuration/generation fences.
The omitted/default mode remains strict. `s3_checked_etag_reads` reports host
support, without enabling any source or certifying an installed adapter.

Checked mode advertises `revision_pinned_reads: false`. Genuine version IDs
remain preferred. Unversioned reads check the expected ETag and size before
GET and after the complete exact body, before successful final EOF; HEAD and
GET also send `If-Match`. A gateway can ignore that condition. These checks
detect some drift but cannot prove immutable bytes or continuity between
ranges. Failed ranges are discarded by the host's bounded reader. Switching
the mode is a configuration change governed by the existing empty-namespace
replacement rule; it is not an in-place toggle for attached libraries.

## Operations

- Bookwarehouse catalog artifacts are verified automatically and cached privately under the native installation root. The cache retains verified releases across restarts; runtime still checks installed archive integrity.
- Catalog installs of S3 sources (`POST .../catalog/installations/discover`) are platform-only. The host downloads the release itself and admits it only when its SHA-256 digest matches a pre-approved pin; unpinned S3 releases are never listed or installed. Root discovery then runs as for manual installs.
- Credential rotation (`POST .../sources/{source_key}/credentials`) overlays only the access key, secret key and session token onto the stored configuration. It works on a source that backs a library, requires an enabled source and its current `expected_revision`, and never returns credentials. The rotation requires the stored `connection` configuration (there is no fallback to another config). An empty or omitted `session_token` clears a stored token. A non-S3 or unknown source returns `not_found`; a disabled source (no attached installation) returns `native_storage_unavailable`.
- Source documents carry `connection_summary`, a non-secret allowlisted projection (never null) for display. `GET /artifacts` adds `catalog` and `config_schema` so a client can render an install form. Capabilities report `s3_catalog_discovery` (platform scope only, because the route is platform-only; always false in organization scope) and `credential_rotation_in_use`.
- `BLOEM_NATIVE_STORAGE_APPROVALS` remains optional for manually supplied additional artifacts, outside the normal catalog flow.
- Migration `20261007181737_library_storage_locations` replaced the native
  onboarding schema and resets an unpublished native catalog; it refuses to run
  if any file references exist.
- Migration `20261007220212_storage_covers_on_demand` pointed existing storage
  books at their source covers; the artwork GC trigger deletes covers the host
  had copied before.
- Gates still open: verified admission of other backends, cluster-wide
  cancellation of streams open on other nodes, and recurring scan history
  retention.
