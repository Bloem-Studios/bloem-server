# Bloem native storage foundation

Native storage is experimental and is not yet wired into production library ingestion or reader delivery. Discovery records are staging data, not indexed media items. Installing ordinary Silo plugins does not require this private capability.

## Ownership and protocol

The private SDK service uses `bloem.plugin.v1.StorageProvider`. The released `silo.plugin.v1` namespace, capability constants and exported SDK configuration structs remain unchanged. The host uses the public runtime's connection and its owned private bindings in `internal/storageproto/bloem/plugin/v1`, avoiding duplicate public protobuf descriptors and an unpublished private SDK module dependency. The SDK-built provider uses its own bindings in a separate process with the same wire descriptor.

The optional service supplies Describe, List, Stat and revision-pinned Read. Host discovery requests at most 512 entries with a 1 MiB receive limit and a 30-second deadline. Native files use at most 8 MiB per range request and validate ordered chunks of at most 128 KiB, exact byte counts and final RPC status. ReaderAt returns only successfully validated ranges, including when a late provider error follows all requested bytes. Cancellation closes active native requests.

The executable fixture verifies transport and process behavior. It is not an installed production provider. In particular, its empty public capability list is insufficient for the ordinary host installer; production installation and launch policy remain integration work.

## Protocol generation

`proto/bloem/plugin/v1/storage_provider.proto` mirrors the canonical private SDK schema. Its wire namespace, field numbers, service paths and original `go_package` descriptor option remain unchanged. The host generator uses an explicit Go import mapping rather than editing that option.

`scripts/generate-bloem-storage-proto.sh` pins protoc 3.21.12, protoc-gen-go 1.36.11 and protoc-gen-go-grpc 1.6.1. The tools must be on PATH, or supplied through PROTOC, PROTOC_GEN_GO and PROTOC_GEN_GO_GRPC. `--check` regenerates into temporary storage and compares both checked-in generated files without overwriting them. Contract tests pin the schema digest, field numbers, streaming methods and coexistence with the public runtime descriptor. Changes to the protocol require coordinated SDK/host schema updates and regeneration.

Host builds and owned protocol/reader/staging tests use `GOWORK=off`; no private SDK module requirement or replacement is needed. Real executable SDK-provider tests still require `BLOEM_STORAGE_SDK_WORKTREE` for the development fixture build. That test input is separate from the host build dependency and from an immutable provider SDK release.

## Durable identity and references

`internal/storagesource` owns seven `bloem_storage_*` tables. A UUID identifies a configured source independently of its plugin installation. Installation deletion nulls the association without deleting discovered entries or catalog references. Source and binding removal cannot discard retained references implicitly.

Each library binding has a separate UUID. A catalog location is `bloem-storage:` plus SHA-256 of the binding UUID and length-prefixed opaque entry ID. Revisions do not participate in that key. This is an internal catalog identity, never an operating-system filename, mounted path or supplied URL.

Attaching a reference checks the actual file's library and catalog key against the binding, then checks discovered entry identity, revision, logical display path and source configuration revision. A revision refresh preserves the catalog file ID and location. Resolution requires the folder already authorized by host catalog/profile policy, checks the actual file/binding, and classifies disabled, removed or mismatched installations as unavailable. Unavailable sources retain their typed reference and never fall back to opening the catalog key locally.

Accounts, encrypted setting key names, existing catalog files, watch progress and ebook reading progress are not rewritten by the additive schema migration. Down acquires exclusive locks before checking that every owned table is empty, and refuses rollback while retained storage data exists, including a concurrently committed insert.

## Restart-safe staging

The database, rather than a Go-process mutex, serializes scan ownership. All lease operations acquire source then generation locks. A lease includes source/configuration revision, generation UUID, owner and increasing epoch. Expiry uses the database clock after locks are acquired. Page application rechecks the fence before committing.

An expired generation can resume from its committed directory checkpoint under a new epoch. Reacquiring as the same owner also changes the epoch, fencing that owner's older process. A live foreign owner cannot be displaced. Configuration replacement invalidates the old generation and starts at the replacement root.

A bounded transaction applies entry updates, discovered directory queues, cursor history and the next checkpoint together. Exact committed page replay is idempotent; its deterministic protobuf digest must match. Repeated identities within a generation and historical cursor loops are rejected. Cursor indexes use 32-byte SHA-256 values with raw text comparisons rather than indexing potentially 4,096-byte tokens directly.

Completion requires every queued directory to have a successfully committed terminal page. A provider error, cancellation, revision conflict, invalid page, stale lease or late SQL failure preserves the previous checkpoint. Completion records discovery only: it does not mark unseen entries absent, set existing catalog files missing, or authorize deletion.

## Remaining integration gates

- Immutable private SDK distribution for production provider releases; standalone host builds already use owned generated bindings.
- A real S3 provider, validated production installation and a launch policy that excludes inherited server credentials.
- Metadata and cover parsing, coherent sidecar handling, atomic catalog/reference publication, enrichment and existing retention rules.
- Host-authorized EPUB/PDF delivery with range, HEAD, conditional requests and preserved progress; conversion paths need separate native-source handling.
- Authenticated source/binding configuration using existing organization, library, profile and resource authority.
- Authoritative absence confirmation before missing-file reconciliation; successful pagination alone is insufficient.

Integration tests require an explicitly configured disposable database whose name starts with `bloem_storage_test_`; they must fail rather than skip when setup is missing. Normal package tests do not require a database. Synthetic two-million-entry protocol traversal and smaller persisted staging tests are separate evidence; neither is a measurement of an actual customer's library or proof of end-to-end catalog readiness.
