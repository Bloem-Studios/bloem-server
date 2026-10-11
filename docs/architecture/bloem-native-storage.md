# Bloem native storage

Native storage lets an ebook or movie library read its files from a storage source (a
storage plugin such as Bookwarehouse) instead of filesystem paths. It is
experimental. The source is the library's **location**: a library has either
paths or one storage location, never both.

Movie admission is startup-only. The host configures the scan reader and bounded
probe before starting queues; the API then admits movie attachment only after
composing the native playback service. An incomplete composition retains the
publication hold. This describes code readiness, not production source certification.

## Model

- **Storage plugin.** A host-approved executable that serves the private
  `bloem.plugin.v1.StorageProvider` service (`Describe`, `List`, `Stat`,
  revision-checked `Read`). Installations are recorded in `plugin_installations`
  and marked native in `bloem_storage_installations`.
- **Storage source.** `bloem_storage_sources`: one configured source of an
  installation, with a retained UUID key, owner, encrypted configuration and a
  configuration revision. A source outlives its installation's removal.
- **Library storage location.** `library_storage_locations`: binds one source to
  one library. A library has at most one location, a source backs at most one
  library. Enabled `ebooks` libraries can have one; enabled `movies` libraries
  also require the scanner and playback readiness boundary. A storage library
  never gains paths and cannot change its source.
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

A storage library scans only as a whole library. `libraryingest.StorageScanner`:

1. Checks tenancy (`RequireStorageScan`), loads the approved snapshot, starts
   or reuses the plugin process and obtains a fresh, bounded `Describe`.
   The advertised source ID and root must match the retained source exactly,
   under its retained read policy. After discovery, it reloads the retained source,
   rechecks tenancy and checks the executable generation and checksum again
   before beginning a scan lease.
2. `BeginAuthorized` begins or resumes discovery in a transaction that checks
   and retains current installation, source, library, owner and entitlement
   authority before the journal's source and run locks. A prior preflight
   check grants no later journal write. Refused authority leaves the run,
   checkpoints and discovery pointer unchanged. The database clock decides
   lease expiry; each takeover bumps the epoch. Standalone discovery may use
   `Begin` without library authority. A renewal worker extends the lease and
   rechecks tenancy.
3. Lists each directory page by page. `ApplyPageAuthorized` checks current
   installation, source, library, owner and entitlement authority inside the
   page transaction, before journal source/run locks. It retains those
   resource locks through the commit of the page's journal entries
   (`bloem_storage_entries`), directory checkpoint and catalog publication.
   Replay also requires current authority. Provider I/O stays outside this
   transaction. Refused authority or failed publication rolls the page back,
   so a retry lists it again. The unfenced `ApplyPage` entry point remains
   available for standalone journal discovery.
4. Stops lease renewal, then `CompleteAuthorized` checks current resource
   authority before journal source/run locks. Its finalizer runs
   `SweepStorageLibraryTx` to reconcile memberships and orphans and sweep
   expired missing files with the existing grace and empty-trash settings.
   Authority stays locked through the single catalog and journal commit;
   the lease is checked again after cleanup waits. Refused authority, failed
   cleanup or an expired lease rolls back both cleanup and completion.
   Orphaned artwork is removed after commit. Standalone discovery may still
   use `Complete` without catalog finalization.

Publication (`scanner.PublishStorageEbooksTx`) is set-based: a page of up to
512 entries costs a fixed number of statements. Entries carrying provider
`EbookMetadata` publish without the host reading the file. Entries without it
are opened and parsed (`ParseNativeEbook`). Files group into items by ISBN, or
title and authors, as file scans do. Items publish with status `matched`: the
source is the authority for its books, so enrichment skips them and each scan
reapplies the provider's metadata over the stored item, keeping artwork.

## Incremental listing

A directory's final page may carry a `change_token`, stored in
`bloem_storage_change_tokens`. The next scan sends it as `changes_since`, and
the provider lists only books added or changed since, plus `removed_entry_ids`.
Removed entries mark their files missing; the post-scan sweep removes them, and
a book listed again before then is restored. A token the provider refuses
(`FailedPrecondition`), from another configuration revision, or older than
seven days falls back to a full listing, which also reconciles anything a change
feed missed: each run records whether it requested a directory's first page
without a token (`bloem_storage_scan_directories.full_listing`) and which
directory last listed each journal entry (`bloem_storage_entries.directory_id`).
A full listing is not a point-in-time snapshot. The Bookwarehouse provider
fixes an upper bound when a directory listing starts and leaves books updated
after it to the next listing, so a present book the cursor had not yet reached
can be missing from a full listing; the change token that listing issues lists
it next time. Absence therefore takes two completed runs, both decided in the
completion transaction (`MarkUnlistedStorageFilesTx`). A run's full listings
make an entry suspect (`bloem_storage_entries.absence_suspected_at`) when a
directory the run listed in full no longer lists it, or, when the run listed
every directory in full, when the run did not list it at all (entries of a
directory no longer listed, or journaled before directories were recorded).
Listing an entry clears the suspicion. A later completed run that has not
listed a suspect entry marks its file missing. A full listing that returned no
entries is suspect itself, as an empty filesystem root is: it raises no
suspicion, and a run with one confirms only entries of the directories it
listed with entries. Incremental, partial, cancelled and failed listings never
raise a suspicion. A book updated during both a full
listing and the following listing, each time before the listing reached it,
can still be marked missing; the listing after that restores it. Marked files
follow the removal grace period and sweep.

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
