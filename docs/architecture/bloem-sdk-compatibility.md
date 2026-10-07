# Bloem SDK compatibility and generated contracts

The server, plugin-author SDK and native-client bindings have different release
identities. A healthy server or a freshly copied generated file does not establish
that every client feature or storage provider has shipped.

## Server and public plugin runtime

The [October 7 deployment](../operations/2026-10-07-native-storage-deployment.md)
runs Bloem `c87b44545228c93f909275c0b5c4fc1d6a289e1b`, including Silo
`74158b4a8a799192312c13253552b8030af8575c`. Its `go.mod` pins the public
`github.com/Silo-Server/silo-plugin-sdk` module at `v0.23.0`. Host builds use
`GOWORK=off`; no private SDK module replacement is required.

Native storage uses the separate `bloem.plugin.v1.StorageProvider` protocol.
The host owns generated bindings under `internal/storageproto/bloem/plugin/v1`
from `proto/bloem/plugin/v1/storage_provider.proto`. This preserves the public
`silo.plugin.v1` descriptors and runtime compatibility. Coordinate private schema
field numbers, method paths, original `go_package` descriptor and host bindings
with the provider SDK; changing a module import is not a protocol migration.

The server's promotions and ambience workers use the owned local JSON process
bridge. They are bundled binaries, not catalog-installable SDK capabilities.
The SDK must not gain host database, tenant, profile or quota authority merely
to package presentation code.

## Plugin-author SDK

The public `Bloem-Studios/bloem-plugin-sdk` repository has an independent
module/release history; its private storage namespace does not imply repository
visibility restrictions. Compare
its public-protocol descriptors and optional service assembly against the host's
pinned public SDK, while retaining private storage registration and Bloem plugin
identity restrictions. Public-runtime currency alone does not publish a storage
provider or approve an executable artifact on a server.

The current plugin-author release is
[`v0.24.0`](https://github.com/Bloem-Studios/bloem-plugin-sdk/releases/tag/v0.24.0),
from commit `6a5324fc65319a8ae204193ccdd13c7d44766360`. It synchronizes the
public protocols and optional service assembly with Silo SDK `v0.23.0`, including
auth/account/network, Configure, request-router season/progress and watch-sync
support, and retains Bloem's separate storage provider service. The release
workflow passed, and an ordinary `go mod download` resolves the public tagged
module. Module dependencies and the server's public SDK pin are unchanged.

```sh
go get github.com/Bloem-Studios/bloem-plugin-sdk@v0.24.0
```

The tag and `codex/sdk-currency-20261007` branch are pushed. This does not imply
that the SDK's `main` branch, existing plugin release pins, or installed provider
executables have changed.

Use an immutable, pushed SDK release when publishing providers. A development
`BLOEM_STORAGE_SDK_WORKTREE` fixture build is test input, not proof that a production
provider can resolve an SDK version. The dated
[plugin inventory](../plugin-fork-inventory.md) preserves its August release audit;
its old SDK pin and six-plugin count are not a current catalog inventory.

### Creating S3 and Bookwarehouse plugins

SDK `v0.24.0` exposes a backend-neutral, read-only storage service: implement
`Describe`, `List`, `Stat` and revision-pinned `Read`, then register it with
`runtime.WithStorageProvider`. Backend clients and credentials belong in the
plugin, with configuration delivered through `WithConfigure`. An S3 adapter can
use its own S3 client; a Bookwarehouse adapter can use the book API. Neither
requires inventing a public storage capability or using `ebook_backend.v1`, which
has no SDK service implementation. See the SDK's
[storage-provider authoring guide](https://github.com/Bloem-Studios/bloem-plugin-sdk/blob/codex/sdk-currency-20261007/docs/storage-provider.md).

The current host admits only EPUB/PDF ebook libraries through this path. A
storage plugin exposing video or music bytes does not add the required host
scanner, playback and delivery integrations. The separate S3 provider has not
passed production backend admission and currently advertises
`revision_pinned_reads: false`.

Bookwarehouse provides authenticated ebook listing, metadata and downloads with
single byte-range support. Check the API version exposed by the target deployment.
Its current download path does not pin reads to a requested immutable revision;
byte ranges and a metadata `file_hash` alone do not establish that guarantee.
A plugin must supply and validate an immutable-read strategy before advertising
it. This documentation does not claim that a Bookwarehouse provider is
implemented or admitted.

## Kotlin and Swift client contracts

`contracts/client/v1/registry.json` selects actual server wire types. The Go graph
generator produces Kotlin and Swift DTOs and a normalized digest; settings bindings
and native/v2 OpenAPI have their own generators. Anonymous HTTP wrappers must be
represented by exact wire types before they can be registered. Preserve explicit
nulls, omitted fields, JSON spelling, opaque identifiers, int64 revisions and
scope-specific requests. Do not expose domain fields that the HTTP handler omits.

The October 7 contract refresh covers native-storage capabilities, sanitized source
and library states, binding/scan receipts, revision commands and recovery errors,
alongside upstream shuffle, episode-series-poster and network-password-retention
additions. These are descriptions of the existing wire contract, not authorization
changes or new runtime routes. Native storage remains capability-gated and
`backend_verified` remains false.

The generated source revision for this refresh is
`9a427dc19ce7f25fe03ae97bbdcebe792d0784e2`, with normalized graph digest
`sha256:c3a4e8a065ab55958a5e46e0a383abbeae431fed6a57ab0a00be1b81a7055d7d`.
Each client records the committed artifact revision separately from that source
stamp. Its checked-in bundle manifest and server pin are the authority for the
adopted bytes; a later documentation-only server commit does not change the graph.

### Reproduce and verify

Use the Go toolchain declared by the server (`1.26.8` for this integration).
Go `1.27.1` changes standard-library JSON alias representation and is not the
validated generation environment. Commands assume the server repository root:

```sh
GOWORK=off make client-digest
GOWORK=off make client-dtos
GOWORK=off make settings-bindings-native
GOWORK=off make bloem-openapi
GOWORK=off make apiv2-openapi
GOWORK=off make verify-client-digest verify-client-dtos verify-client-coverage
GOWORK=off make verify-settings-bindings verify-bloem-openapi verify-apiv2-openapi
```

The native-storage OpenAPI path/method check walks the actual production route
registrar and covers all 32 operations. The older shared route inventory does not
include that group. Its generator currently stops at the existing `NewRouter`
seal invariant; that snapshot and generator are preserved, not treated as fresh
native-storage evidence.

Inspect generated changes rather than replacing a digest alone. The contract
stamp identifies the source used for generation; it is not necessarily the latest
documentation commit or running binary. `verify-client-dtos` preserves the stored
stamp during regeneration and compares every other generated byte. Commit the
source declarations and generated artifacts before asking a client to adopt them.

Consumer synchronization is explicit. Android consumes generated Kotlin in
`core/src/commonMain/kotlin-generated`; Apple consumes Swift in
`packages/BloemCore/Sources/BloemCore/Generated`. Their contract-bundle tools must
verify source revision and digest before adopting, including when the server is
a Git worktree whose `.git` is a file. A server generation must not silently dirty
a sibling checkout. The client-contracts repository stores fixture/schema evidence;
it is not the source from which server DTOs are generated.

## API surfaces and adoption limits

`/api/bloem/v1` is Bloem's native extension surface. `/api/v2` remains the supported
Silo-compatible native API; it has not been retired by this integration. `/api/v1`
business-route retirement is a separate policy decision, while its health/readiness
probe paths remain operational. Check the relevant committed OpenAPI document
before implementing a route.

Native-storage administration requires a signed platform or organization
administrative-context token. Ordinary account tokens, profile headers and v2
mutation conventions are not substitutes. Creation and initialization return
recovery identifiers; ambiguous outcomes require reconciliation, not blind
replay or allocation of another library. Use the
[onboarding contract](bloem-native-storage-onboarding.md) and
[native API reference](../bloem-api-reference.md#native-storage-administration).

Generated types provide encoding/decoding support. They do not implement Android
or Apple administration screens, network sign-in, shuffle controls, provider
installation or native offline delivery. Current native reader support is EPUB/PDF;
repair, conversion, generic downloads, deletion and populated-namespace reinstall
remain explicitly unsupported. Keep provider/device acceptance separate from
source, schema, generator and fixture checks.
