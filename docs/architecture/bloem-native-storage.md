# Bloem native storage

Native storage is experimental. Host startup composes the isolated runtime, explicit native library dispatch, EPUB/PDF catalog publisher and authorized reader delivery. Source configuration and lifecycle APIs, verified backend admission and broader acceptance remain gated. Discovery records become ordinary catalog files only after atomic authorized publication. Installing ordinary Silo plugins does not require this private service.

## Ownership and protocol

The private SDK service uses `bloem.plugin.v1.StorageProvider`. The released `silo.plugin.v1` namespace, capability constants and exported SDK configuration structs remain unchanged. The host uses the public runtime's connection and its owned private bindings in `internal/storageproto/bloem/plugin/v1`, avoiding duplicate public protobuf descriptors and an unpublished private SDK module dependency. The SDK-built provider uses its own bindings in a separate process with the same wire descriptor.

The optional service supplies Describe, List, Stat and revision-pinned Read. Host discovery requests at most 512 entries with a 1 MiB receive limit and a 30-second deadline. Native files use at most 8 MiB per range request and validate ordered chunks of at most 128 KiB, exact byte counts and final RPC status. ReaderAt returns only successfully validated ranges, including when a late provider error follows all requested bytes. Cancellation closes active native requests.

Executable fixtures verify transport and process behavior. The owned native registry can install host-approved binary-only artifacts with empty public capabilities through its private validation path. Ordinary installation still requires ordinary Silo capabilities. Neither fixture nor internal registry success exposes source installation/configuration in the application.

## Protocol generation

`proto/bloem/plugin/v1/storage_provider.proto` mirrors the canonical private SDK schema. Its wire namespace, field numbers, service paths and original `go_package` descriptor option remain unchanged. The host generator uses an explicit Go import mapping rather than editing that option.

`scripts/generate-bloem-storage-proto.sh` pins protoc 3.21.12, protoc-gen-go 1.36.11 and protoc-gen-go-grpc 1.6.1. The tools must be on PATH, or supplied through PROTOC, PROTOC_GEN_GO and PROTOC_GEN_GO_GRPC. `--check` regenerates into temporary storage and compares both checked-in generated files without overwriting them. Contract tests pin the schema digest, field numbers, streaming methods and coexistence with the public runtime descriptor. Changes to the protocol require coordinated SDK/host schema updates and regeneration.

Host builds and owned protocol/reader/staging tests use `GOWORK=off`; no private SDK module requirement or replacement is needed. Real executable SDK-provider tests still require `BLOEM_STORAGE_SDK_WORKTREE` for the development fixture build. That test input is separate from the host build dependency and from an immutable provider SDK release.

## Durable identity and references

`internal/storagesource` owns discovery and reference tables. The native installation registry adds a separate explicit marker table. A UUID identifies a configured source independently of its plugin installation. A retained resource-owner UUID survives uninstall; composite owner constraints prevent cross-owner installation associations. Owner consistency is not membership or entitlement authorization. Installation deletion nulls the association without deleting discovered entries or catalog references. Source and binding removal cannot discard retained references implicitly.

Each library binding has a separate UUID. A catalog location is `bloem-storage:` plus SHA-256 of the binding UUID and length-prefixed opaque entry ID. Revisions do not participate in that key. This is an internal catalog identity, never an operating-system filename, mounted path or supplied URL.

Attaching a reference checks the actual file's library and catalog key against the binding, then checks discovered entry identity, revision, logical display path and source configuration revision. A revision refresh preserves the catalog file ID and location. Resolution requires the folder already authorized by host catalog/profile policy, checks the actual file/binding, and classifies disabled, removed or mismatched installations as unavailable. Unavailable sources retain their typed reference and never fall back to opening the catalog key locally.

Accounts, encrypted setting key names, existing catalog files, watch progress and ebook reading progress are not rewritten by the additive schema migration. Down acquires exclusive locks before checking that every owned table is empty, and refuses rollback while retained storage data exists, including a concurrently committed insert.

## Restart-safe staging

The database, rather than a Go-process mutex, serializes scan ownership. All lease operations acquire source then generation locks. A lease includes source/configuration revision, generation UUID, owner and increasing epoch. Expiry uses the database clock after locks are acquired. Page application rechecks the fence before committing.

An expired generation can resume from its committed directory checkpoint under a new epoch. Reacquiring as the same owner also changes the epoch, fencing that owner's older process. A live foreign owner cannot be displaced. Configuration replacement invalidates the old generation and starts at the replacement root.

A bounded transaction applies entry updates, discovered directory queues, cursor history and the next checkpoint together. Exact committed page replay is idempotent; its deterministic protobuf digest must match. Repeated identities within a generation and historical cursor loops are rejected. Cursor indexes use 32-byte SHA-256 values with raw text comparisons rather than indexing potentially 4,096-byte tokens directly.

Completion requires every queued directory to have a successfully committed terminal page. A provider error, cancellation, revision conflict, invalid page, stale lease or late SQL failure preserves the previous checkpoint. Completion records discovery only: it does not mark unseen entries absent, set existing catalog files missing, or authorize deletion.

## Restart-safe catalog claims

Completed discovery has a separate ingestion lease and keyset checkpoint for each binding. An owned source pointer records the current discovery run atomically with scan acquisition; a newer discovery fences earlier ingestion before it can overwrite staged entries. Existing generations without that pointer require a fresh discovery. Claim takeover changes the epoch and pending token; stale workers cannot publish.

One persisted pending entry bounds each claim. Parsing and cover I/O happen outside the catalog transaction. Publication rechecks the current source/configuration/run, binding, lease epoch/expiry, token and exact staged entry, then commits the caller's catalog writes, transaction-aware native reference and checkpoint together. Failed parsing, cover work or late SQL writes leave the pending entry retryable. A callback explicitly acknowledging an unsupported entry publishes no catalog file.

The production consumer uses `PublishAuthorizedIngestion`, which requires a host SQL authorizer before taking source/run/checkpoint locks. The authorizer and catalog callback retain their locks through the same commit. The trusted staging-only publication primitive remains available for internal fixtures; it is not the consumer's authority path. Recurring scan history and absence/retention remain separate acceptance gates.

## Native installation and runtime boundary

`plugins.NativeStorageRegistry` accepts an immutable host-approved artifact map. Installation requests select an approval; they cannot approve their own manifest or checksum. Private packages retain exact SHA-256, host-platform, reserved-identity, archive-member and path validation. A transaction publishes the installation, explicit native marker, archive, encrypted runtime configuration and retained source together. Existing encrypted configuration envelopes and associated-data keys remain unchanged.

Filesystem packages survive an uncertain COMMIT acknowledgement, because PostgreSQL may already have committed. A confirmed transaction rollback permits cleanup. Orphan-package cleanup is a separate policy; uncertainty must not remove an executable referenced by committed records. Detached same-owner sources can be reinstalled while retaining their UUID, bindings and progress.

Before ordinary preload, the owned isolation decorator filters explicitly marked installations and rejects their ordinary Service, Installer, archive and AutoUpdate paths. This protects the decorated instances; it does not replace every original concrete store. Owned application guards also reject native IDs on ordinary raw admin installation/configuration mutation and configuration-test routes. Native lifecycle authority must use its separate owned service.

`storageplugin.Manager` receives an already host-authorized immutable snapshot. It performs no tenant authorization. It validates the actual executable checksum and embedded manifest, negotiates the private service, and sends only supplied source configuration. Its owned process runner supplies null stdin, a small explicit environment plus plugin transport variables, and no general RuntimeHost callbacks. This is process environment/descriptor control, not an OS sandbox.

A durable increasing generation identifies configuration/artifact changes. The process-local manager deduplicates starts, rejects stale or changed same-generation snapshots, retains disabled-generation tombstones, and waits for predecessor reaping before replacement. RPCs have caller/generation cancellation and finite deadlines; caller-triggered restarts have finite attempts and backoff. Shutdown waits for reaping. The host must authorize fresh snapshots, cancel affected sessions after successful configuration/disable/uninstall, and supply cluster lifecycle policy when integrating multiple API instances.

## Reader delivery adapter

The owned handler decorator delegates file/content/library/profile authorization to the ordinary reader resolver. Explicit native keys never fall back to filesystem opening. An injected opener must resolve the authorized file's actual folder/binding/reference and obtain an authorized native runtime; it is not supplied by the adapter itself.

Native EPUB/PDF delivery preserves GET/HEAD, single/multipart ranges, conditional requests, transfer telemetry, rolling write deadlines, trusted MIME types and sanitized inline names. ETags hash the opaque revision rather than exposing provider identities. HEAD does not read the body or trigger conversion. A late binary read failure aborts HTTP delivery instead of returning an error that the caller could append as JSON. Ordinary local reading and conversion delegate unchanged. Application routing supplies this decorator to existing v2 ebook file operations and v1 GET/HEAD reader routes. The owned coordinator reloads the actual catalog file/reference, resolves fresh real membership and resource access, compares registry/source state again after process startup, and ties file lifetime to request and runtime cancellation. Readers retain runtime admission until their actual files close. Revocation of a stream already open on another node remains a separate lifecycle gate.

## Unsupported attachment containment

The entire raw `bloem-storage:` namespace is reserved, including malformed identities. Unsupported original downloads and Jellyfin local-file delivery hide those locations through owned file-resolver decorators installed regardless of native-runtime availability. Download candidate filtering copies repository slices and maps. Explicit native file IDs cannot select an ordinary alternate file, create a Ready original, or resolve an existing row to a local byte target. Physical paths containing the prefix only in a basename retain ordinary filesystem behavior.

The proxy checks fresh source authority before rejecting a reserved token path. Its host authority also rejects the actual current native catalog location after the folder grant, covering retained remote-artifact tokens whose signed path is empty. Hidden and unavailable authority errors retain their existing precedence. These guards provide safe refusal through existing not-found conventions; they do not implement native attachment delivery, conversion or offline integrity. The supported authorized EPUB/PDF reader still uses its native coordinator.

## Native ebook parsing

The owned scanner adapter `ParseNativeEbook` accepts an already opened, revision-pinned `mediasource.File`. It supports EPUB and PDF, uses the existing metadata sanitization and XML/PDF helpers, and leaves file ownership with its caller. Display paths and reserved catalog identities are never opened as filesystem paths. Native sidecars require separate discovered references; the adapter does not probe local sidecars.

EPUB validates bounded EOCD/ZIP64 metadata and every central-directory record before `zip.NewReader`: at most 8 MiB of directory data and 8,192 members. It supports ordinary prepended archives and ZIP64 and rejects multi-disk or ambiguous index encodings. Index construction also has a read-byte/request budget. Container, OPF, image and decompression limits remain in force. An absent optional cover is allowed. A declared cover failure propagates so a provider outage cannot silently replace a retained cover. PDF reads bounded, nonoverlapping head/tail windows and checks cross-reference trailers before trusting metadata. Encrypted metadata is suppressed; invalid trailers and transport failures remain errors. Full-count reads with a failed final status also remain failures.

This adapter parses metadata and embedded cover bytes only. It does not publish catalog files, cache images, run enrichment, or wire reader endpoints. Ordinary local format and conversion wrappers remain unchanged.

## Catalog publication and native groups

`PublishAuthorizedNativeEbook` parses main and individually pinned sidecar bytes and caches bounded covers before taking SQL locks. A mandatory host callback then authorizes the actual source/library in the publication transaction. Nil authorization fails closed. The prepared item, authors, series, ISBN, membership, file, native reference and claim cursor commit together. Curated metadata and artwork are retained; failed SQL cover publication leaves an already tracked orphan for the established artwork policy. Enrichment uses the ordinary durable scanner queue. The native consumer runs its bounded missing-job reconciliation after startup authority, before provider I/O, and after successful ingestion. The database-persisted reconciliation cursor repairs interrupted or failed post-commit enqueueing without rereading committed provider files. Repair uses the existing independent queue transaction; scan authority locks do not extend through that queue commit.

Native cross-format group keys have a retained binding namespace distinct from every ordinary filesystem ebook key. Different native formats may share an item within that binding; another file of the same format remains separate. An older ordinary scan cannot select a native group by ISBN/title. The reserved catalog location resolves existing item/file IDs before regrouping, preserving progress across revision and configuration changes. This grouping separation does not grant filesystem scans or cleanup authority over native-bound folders.

Sidecar lookup reads bounded named candidates and counts at most two ebook siblings from the current completed discovery. Unsupported book formats count against generic-cover eligibility. Parent identity is the exact object-key prefix including its final slash: empty, leading-slash, repeated-slash and dot-segment prefixes remain distinct. Hash indexes bound key sizes while raw equality guards collisions. Candidates are individually pinned and rechecked through publication; successful enumeration does not prove a historical snapshot or authorize disappearance cleanup.

## Host background scan authority

A scan queue job carries the application context, not an HTTP account membership. The owned background policy acts only on the actual durable binding and retained source, enabled installation/native marker and enabled ebook folder. It compares every retained source field after taking locks, rejects filesystem roots and multiple source bindings, and checks typed resource owners. Platform folders require platform sources. Organization folders require their actual active organization and either its own source or an active entitlement to the actual platform installation. This establishes system scan authority, not account membership or end-user access.

Authorization locks installation before source to agree with registry configuration/removal. It locks the folder strongly enough to exclude path insertion through foreign keys, and retains actual owner, organization and entitlement locks through publication. Existing path rows use fail-fast locking so a child-first path replacement cannot deadlock against the folder fence. Contention fails without advancing the claim and requires retry. Callbacks perform SQL only and must not commit or perform provider I/O.

The consumer checks fresh approvals before and after startup, requires matching described source/root and advertised revision-pinned reads, and never overrides a false capability. Completed current discovery resumes its pending ingestion; otherwise bounded directory pages complete before catalog claims begin. Independent lease renewal covers slow parsing/cover work and cancels the job on lease loss. Joining a renewal worker suppresses only cancellation caused by its deliberate stop; deadline failures and genuine lease or policy errors remain failures. Retained ingestion lease and claim identities are checked against the selected binding/source before provider I/O and SQL authorization. Session/job cancellation closes outstanding I/O. Unsupported entries receive an authorized checkpoint acknowledgement; parsing, provider and publication errors remain failures. Only a successful full ingestion permits the existing executor to mark the library scanned. No absence deletion or history pruning is implicit.

## Remaining integration gates

- Immutable private SDK distribution for production provider releases; standalone host builds already use owned generated bindings.
- Verified backend admission for the separate S3 provider. Admission requires both immutable version reads and complete ordered listing, including keys that coexist with descendants. Separate selected-backend fixtures establish partial evidence; no tested candidate has passed the entire contract. A production capability policy remains gated and unknown backends are not admitted.
- Admitted-backend discovery/catalog/reader acceptance across process restart and source lifecycle. Synthetic executable HTTP tests cover the existing authenticated reader surfaces; client/device acceptance remains separate. Generic downloads, proxy delivery and Jellyfin attachments reject native locations and have no native delivery adapter. Native conversion and offline representation integrity remain separate contracts.
- Source lifecycle configuration/removal and cluster cancellation, including streams already open on another node.
- Authenticated source/binding configuration using existing organization, library, profile and resource authority.
- Authoritative absence confirmation before missing-file reconciliation; successful pagination alone is insufficient.
- Owned scan-history retention and measured persisted/catalog throughput and resource bounds before large recurring libraries.

Integration tests require an explicitly configured disposable database whose name starts with `bloem_storage_test_`; they must fail rather than skip when setup is missing. Normal package tests do not require a database. Synthetic two-million-entry protocol traversal and smaller persisted staging tests are separate evidence; neither is a measurement of an actual customer's library or proof of end-to-end catalog readiness.
