# Bloem native storage

Native storage lets an ebook library read its books from a storage source (a
storage plugin such as Bookwarehouse) instead of filesystem paths. It is
experimental. The source is the library's **location**: a library has either
paths or one storage location, never both.

## Model

- **Storage plugin.** A host-approved executable that serves the private
  `bloem.plugin.v1.StorageProvider` service (`Describe`, `List`, `Stat`,
  revision-pinned `Read`). Installations are recorded in `plugin_installations`
  and marked native in `bloem_storage_installations`.
- **Storage source.** `bloem_storage_sources`: one configured source of an
  installation, with a retained UUID key, owner, encrypted configuration and a
  configuration revision. A source outlives its installation's removal.
- **Library storage location.** `library_storage_locations`: binds one source to
  one library. A library has at most one location, a source backs at most one
  library, and only enabled `ebooks` libraries can have one. A storage library
  never gains paths and cannot change its source.
- **File reference.** `bloem_storage_file_refs`: a published catalog file's
  location, entry ID, revision and display path, plus the cover the provider
  offers for its book. The file's `file_path` is the catalog location
  `bloem-storage:` + SHA-256 of the location UUID and the length-prefixed entry
  ID. It is an internal identity, never a filename, mounted path or URL.

The whole `bloem-storage:` namespace is reserved. Downloads, Jellyfin delivery
and the proxy refuse it; nothing opens it as a local path.

## Creating a library

An administrator creates an ebook library through the ordinary v2
`createLibrary` with `storage_source` instead of `paths`. The location is
inserted inside the library's creation transaction, after the same tenancy
check that gates every scan (`resourcetenancy.RequireStorageScanTx`):

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

## Scanning

A storage library scans only as a whole library. `libraryingest.StorageScanner`:

1. Checks tenancy (`RequireStorageScan`), loads the approved snapshot, starts
   or reuses the plugin process and checks `Describe` reports the source with
   revision-pinned reads.
2. Begins or resumes a discovery run under a database lease (source then run
   locks; the database clock decides expiry; each takeover bumps the epoch).
   A renewal worker extends the lease and rechecks tenancy.
3. Lists each directory page by page. `ApplyPage` commits, in one transaction,
   the page's journal entries (`bloem_storage_entries`), the directory
   checkpoint and the page's catalog publication. A failed publication rolls
   the page back, so a retry lists it again.
4. After completion, sweeps files marked missing past the library's grace
   period, as file scans do.

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
feed missed. Without a change feed, a listing never proves absence.

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
reference's revision. File lifetime is tied to request and runtime
cancellation. GET/HEAD, ranges and conditional requests behave as for local
files; ETags hash the opaque revision. Downloads, conversion and offline
delivery of storage files are not supported.

## Protocol

`proto/bloem/plugin/v1/storage_provider.proto` mirrors the SDK schema; the host
uses its own generated bindings in `internal/storageproto`.
`scripts/generate-bloem-storage-proto.sh` pins protoc 3.21.12, protoc-gen-go
1.36.11 and protoc-gen-go-grpc 1.6.1; `--check` verifies the generated files.

Hosts request at most 512 entries and 1 MiB per page with a 30-second deadline.
`Read` delivers exactly the requested range (at most 8 MiB) in ordered chunks
of at most 128 KiB and must end with status OK; a partial read is discarded.
Cursors must advance and a change token appears only on a final page.

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
