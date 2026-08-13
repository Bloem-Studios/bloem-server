# Vondel Compatibility Sidecars Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the embedded Jellyfin and Audiobookshelf compatibility protocols into two private sidecars that use a scoped, versioned Vondel module API while native Vondel media behavior remains in the server.

**Architecture:** Vondel Server remains authoritative and exposes `/api/internal/compat/v1` through a new `internal/moduleapi` boundary. `vondel-compat-jellyfin` and `vondel-compat-audiobookshelf` translate their external protocols through generated Go clients, keep only ephemeral protocol state, and never access PostgreSQL or Vondel media paths directly. Characterization fixtures and a dual-run development stack gate parity before the embedded listeners and packages are removed.

**Tech Stack:** Go 1.26, chi, PostgreSQL/pgx, OpenAPI 3.1, WebSockets, Docker Compose, existing Vondel playback/event services, GitHub private repositories and Actions.

## Global Constraints

- `vondel-server`, `vondel-compat-jellyfin`, and `vondel-compat-audiobookshelf` remain private indefinitely; no workflow may change visibility or publish public packages/images.
- Vondel Server remains authoritative for identity, profiles, access, catalog, state, playback, streams, downloads, and events.
- Sidecars never receive PostgreSQL credentials, mount Vondel's data directory, or query server tables.
- Sidecars use distinct least-privilege service credentials and Vondel reauthorizes every user-visible operation.
- Unauthorized adult content is absent from results, counts, artwork, events, activity, and timing-distinguishable negative responses.
- Native audiobook, ebook, manga, comic, podcast, movie, series, music, radio, and Live TV implementations remain server-owned.
- Plex import/sync and ARR webhooks stay in Vondel Server during this plan.
- Preserve source license notices and add exact extraction provenance before the first sidecar commit.
- Implement with strict RED/GREEN TDD, task-scoped commits, an independent review after every task, and no long-lived `replace` directives.

---

## File and Repository Map

### Vondel Server

- `contracts/compat/v1/openapi.yaml`: canonical private module API.
- `internal/moduleapi/types.go`: stable request/response types matching the contract.
- `internal/moduleapi/auth.go`: service-token authentication and scope enforcement.
- `internal/moduleapi/handler.go`: route assembly only.
- `internal/moduleapi/identity.go`: external-login exchange and effective profile scope.
- `internal/moduleapi/catalog.go`: libraries, browse, search, detail, and artwork resolution.
- `internal/moduleapi/state.go`: progress, favorites, collections, and activity operations.
- `internal/moduleapi/playback.go`: playback-plan creation, stream handoff, cancellation, and recovery.
- `internal/moduleapi/events.go`: filtered event stream and reconnect cursors.
- `internal/moduleapi/client/`: generated Go client consumed by both sidecars.
- `internal/compatcontract/`: neutral protocol fixtures and runner shared during dual-run verification.
- `migrations/sql/<timestamp>_compat_service_credentials.sql`: hashed sidecar credentials and exact scopes.
- `cmd/vondel/compat_credentials.go`: create, list, rotate, and revoke sidecar credentials.
- `deploy/dev/compat-compose.yml`: development-server and sidecar topology.

### Jellyfin Sidecar

- Repository: `Vondel-Media/vondel-compat-jellyfin`.
- `cmd/vondel-compat-jellyfin/main.go`: configuration, module client, listener, and shutdown.
- `internal/jellyfin/`: extracted Jellyfin protocol mapping and handlers.
- `internal/vondel/adapter.go`: the only Jellyfin-to-module-API adapter.
- `internal/session/`: ephemeral Jellyfin session correlation.
- `NOTICE`: exact Vondel source commit and extracted paths.

### Audiobookshelf Sidecar

- Repository: `Vondel-Media/vondel-compat-audiobookshelf`.
- `cmd/vondel-compat-audiobookshelf/main.go`: configuration, module client, listener, and shutdown.
- `internal/audiobookshelf/`: extracted ABS HTTP and socket protocol implementation.
- `internal/vondel/adapter.go`: the only ABS-to-module-API adapter.
- `internal/session/`: ephemeral ABS token/session correlation.
- `NOTICE`: exact Vondel source commit and extracted paths.

---

### Task 1: Freeze Observable Compatibility Contracts

**Files:**
- Create: `internal/compatcontract/types.go`
- Create: `internal/compatcontract/runner.go`
- Create: `internal/compatcontract/jellyfin_test.go`
- Create: `internal/compatcontract/audiobookshelf_test.go`
- Create: `internal/compatcontract/testdata/jellyfin/*.json`
- Create: `internal/compatcontract/testdata/audiobookshelf/*.json`
- Modify: `internal/jellycompat/router_test.go`
- Modify: `internal/audiobooks/abs/handler.go`

**Interfaces:**
- Produces: `compatcontract.Run(ctx context.Context, target compatcontract.Target, suite compatcontract.Suite) (compatcontract.Report, error)`.
- Produces: `compatcontract.Target{BaseURL string, Client *http.Client, Credentials CredentialProvider}`.
- Consumes: the current embedded Jellyfin listener and Audiobookshelf handler only.

- [ ] **Step 1: Write failing parity-runner tests**

```go
func TestRunRejectsUnredactedSecrets(t *testing.T) {
    report, err := Run(context.Background(), Target{
        BaseURL: "http://127.0.0.1:8096?token=secret",
        Client:  http.DefaultClient,
    }, JellyfinBaseline())
    if err == nil || strings.Contains(report.JSON(), "secret") {
        t.Fatalf("err=%v report=%s", err, report.JSON())
    }
}
```

- [ ] **Step 2: Run the new package and confirm RED**

Run: `GOWORK=off go test ./internal/compatcontract -count=1`

Expected: FAIL because `Run`, `Target`, and the named suites do not exist.

- [ ] **Step 3: Implement the neutral runner and redacted report**

The runner must support request method/path/body, expected status, selected headers, JSON semantic comparison, binary checksum comparison, WebSocket message comparison, and named protocol exceptions. Store invented IDs and reserved-domain URLs only.

```go
type Case struct {
    Name       string
    Method     string
    Path       string
    Body       []byte
    WantStatus int
    WantJSON   json.RawMessage
    WantSHA256 string
}

type Report struct {
    TargetOrigin string       `json:"target_origin"`
    Results      []CaseResult `json:"results"`
}
```

- [ ] **Step 4: Capture protocol fixtures from the embedded implementations**

Cover Jellyfin system info, login, users, views, items, search, images, favorites, progress, playback info, direct stream, transcode start/stop, sessions, WebSocket, logout, and protocol errors. Cover Audiobookshelf login, user, libraries, items, authors, series, collections, playlists, bookmarks, progress, playback sessions, file delivery, socket events, logout, and errors.

- [ ] **Step 5: Add adult-access negative fixtures**

Seed an authorized adult profile and an unauthorized ordinary profile. For the unauthorized profile assert zero adult IDs in bodies, counts, image responses, event frames, and activity. Compare missing adult and random missing IDs through a bounded timing-distribution assertion rather than a single elapsed-time comparison.

- [ ] **Step 6: Run focused and package-wide verification**

Run:

```bash
GOWORK=off go test ./internal/compatcontract ./internal/jellycompat ./internal/audiobooks/abs ./internal/audiobooks/abssocket -count=1
GOWORK=off go test -race ./internal/compatcontract ./internal/jellycompat ./internal/audiobooks/abs ./internal/audiobooks/abssocket -count=1
```

Expected: PASS with both embedded suites executing every named case.

- [ ] **Step 7: Commit and push**

```bash
git add internal/compatcontract internal/jellycompat/router_test.go internal/audiobooks/abs/handler.go
git commit -m "test(compat): freeze embedded protocol behavior"
git push origin main
```

---

### Task 2: Add Scoped Compatibility Service Identities

**Files:**
- Create: `migrations/sql/20260812190000_compat_service_credentials.sql`
- Create: `internal/moduleapi/auth.go`
- Create: `internal/moduleapi/auth_test.go`
- Create: `cmd/vondel/compat_credentials.go`
- Create: `cmd/vondel/compat_credentials_test.go`
- Modify: `cmd/vondel/main.go`

**Interfaces:**
- Produces: `moduleapi.Authenticator.Authenticate(ctx context.Context, bearer string) (moduleapi.ServiceIdentity, error)`.
- Produces scopes: `compat:jellyfin`, `compat:audiobookshelf`, `catalog:read`, `state:write`, `playback:write`, and `events:read`.
- Produces CLI: `vondel compat-credential create|list|rotate|revoke`.

- [ ] **Step 1: Write failing migration and authentication tests**

```go
func TestAuthenticateReturnsOnlyStoredScopes(t *testing.T) {
    raw := "vcs_" + strings.Repeat("a", 64)
    store := newCredentialStore(t, raw, []string{"compat:jellyfin", "catalog:read"})
    got, err := NewAuthenticator(store).Authenticate(context.Background(), raw)
    if err != nil { t.Fatal(err) }
    if diff := cmp.Diff([]string{"catalog:read", "compat:jellyfin"}, got.Scopes); diff != "" {
        t.Fatal(diff)
    }
}
```

Test malformed tokens, revoked tokens, expired tokens, cross-sidecar scope use, constant-shape unauthorized errors, last-used throttling, and absence of raw tokens from storage/logs.

- [ ] **Step 2: Run focused tests and confirm RED**

Run: `GOWORK=off go test ./internal/moduleapi ./cmd/vondel -run 'Compat|ServiceIdentity' -count=1`

Expected: FAIL because the credential store and command do not exist.

- [ ] **Step 3: Add the credential migration**

Create `compat_service_credentials` with UUID identity, unique label, adapter kind check (`jellyfin` or `audiobookshelf`), SHA-256 token digest, JSONB scopes, optional expiry, revocation timestamp, last-used timestamp, and audit timestamps. Store only a 256-bit random `vcs_` token's digest. Down migration drops only this table and its indexes.

- [ ] **Step 4: Implement authentication and CLI lifecycle**

The create and rotate commands print the raw token exactly once to stdout and write all diagnostics to stderr. List never displays a token or digest. Revoke is idempotent. Reject unknown or duplicate scopes.

- [ ] **Step 5: Run migration, auth, CLI, and secret scans**

Run:

```bash
GOWORK=off go test ./internal/moduleapi ./cmd/vondel -run 'Compat|ServiceIdentity' -count=1
GOWORK=off go test -race ./internal/moduleapi -count=1
rg -n 'vcs_[0-9A-Fa-f]{32,}|Authorization: Bearer' --glob '!**/*_test.go' .
```

Expected: tests PASS and the scan finds no credential literal.

- [ ] **Step 6: Commit and push**

```bash
git add migrations/sql/20260812190000_compat_service_credentials.sql internal/moduleapi/auth.go internal/moduleapi/auth_test.go cmd/vondel/compat_credentials.go cmd/vondel/compat_credentials_test.go cmd/vondel/main.go
git commit -m "feat(module-api): add scoped compatibility identities"
git push origin main
```

---

### Task 3: Define and Implement Compatibility Module API v1

**Files:**
- Create: `contracts/compat/v1/openapi.yaml`
- Create: `contracts/compat/v1/openapi_test.go`
- Create: `internal/moduleapi/types.go`
- Create: `internal/moduleapi/handler.go`
- Create: `internal/moduleapi/identity.go`
- Create: `internal/moduleapi/catalog.go`
- Create: `internal/moduleapi/state.go`
- Create: `internal/moduleapi/playback.go`
- Create: `internal/moduleapi/events.go`
- Create: `internal/moduleapi/handler_test.go`
- Create: `internal/moduleapi/client/client.go`
- Modify: `internal/api/router.go`

**Interfaces:**
- Produces HTTP routes under `/api/internal/compat/v1`.
- Produces `moduleapi/client.Client` with `ExchangeLogin`, `ListProfiles`, `ListLibraries`, `Search`, `GetItem`, `ResolveArtwork`, `GetState`, `PutState`, `CreatePlayback`, `StopPlayback`, and `SubscribeEvents`.
- Consumes the authenticator from Task 2 and existing Vondel services through narrow interfaces.

- [ ] **Step 1: Write schema and route tests before the contract**

```go
func TestEveryOperationDeclaresScopeAndErrors(t *testing.T) {
    doc := loadOpenAPI(t)
    for path, item := range doc.Paths.Map() {
        for method, op := range operations(item) {
            if len(op.Security) != 1 || op.Extensions["x-vondel-scope"] == nil {
                t.Fatalf("%s %s lacks exact service scope", method, path)
            }
            requireResponses(t, op, 401, 403, 429, 503)
        }
    }
}
```

- [ ] **Step 2: Run contract tests and confirm RED**

Run: `GOWORK=off go test ./contracts/compat/v1 ./internal/moduleapi -count=1`

Expected: FAIL because the contract and handlers do not exist.

- [ ] **Step 3: Write the complete OpenAPI contract**

Define service health/capabilities, credential exchange, profiles/effective scope, libraries, browse/search/detail, artwork resolution, progress/favorites/collections, playback create/recover/stop, and filtered event stream. Every list response carries a stable cursor and every resource uses Vondel content IDs rather than SQL row IDs where available.

- [ ] **Step 4: Implement identity and authorization adapters**

Every request carries `ServiceIdentity` plus an explicit Vondel user/profile context. Resolve effective access through the existing server policy/access services on every operation. Never accept client-supplied library scope without intersecting it with effective scope.

- [ ] **Step 5: Implement catalog, state, artwork, and playback adapters**

Reuse existing domain services; do not copy SQL from `internal/jellycompat` or `internal/audiobooks/abs`. Artwork resolution returns short-lived signed Vondel URLs. Playback creation returns Vondel session identity and authorized direct/transcode handoff information without filesystem paths.

- [ ] **Step 6: Implement filtered events with reconnect cursors**

Filter before serialization. A service may subscribe only to event families in its scopes, and each event is intersected with the mapped user's effective profile access. Cursor expiry returns an explicit resync-required error.

- [ ] **Step 7: Implement the Go client from the canonical types**

The client validates origin, forbids URL userinfo and credential-bearing query strings, adds authorization only to the configured Vondel origin, strips it on redirects, applies caller deadlines, and redacts errors.

- [ ] **Step 8: Run API, authorization, race, and route-leak tests**

Run:

```bash
GOWORK=off go test ./contracts/compat/v1 ./internal/moduleapi ./internal/moduleapi/client -count=1
GOWORK=off go test -race ./internal/moduleapi ./internal/moduleapi/client -count=1
GOWORK=off go vet ./internal/moduleapi/... ./contracts/compat/v1
```

Expected: PASS, including adult negative cases and cross-origin redirect credential stripping.

- [ ] **Step 9: Commit and push**

```bash
git add contracts/compat/v1 internal/moduleapi internal/api/router.go
git commit -m "feat(module-api): expose compatibility contract v1"
git push origin main
```

---

### Task 4: Extract the Jellyfin Sidecar

**Files:**
- Create repository files listed under **Jellyfin Sidecar** in the file map.
- Copy then adapt: `internal/jellycompat/**` into sidecar `internal/jellyfin/**`.
- Create server: `internal/compatcontract/jellyfin_sidecar_test.go`.

**Interfaces:**
- Consumes: `moduleapi/client.Client` from Task 3.
- Produces: Jellyfin-compatible HTTP/WebSocket listener and `/healthz` sidecar health endpoint.
- Produces configuration: `VONDEL_URL`, `VONDEL_COMPAT_TOKEN`, `LISTEN_ADDR`, and optional bounded cache settings.

- [ ] **Step 1: Create the private repository and safety root**

Verify the destination does not exist, create it as GitHub `PRIVATE`, disable Actions before the first push, add AGPL-3.0-or-later `LICENSE`, and add `NOTICE` naming the exact Vondel source commit and extracted path. Configure the Vondel source remote fetch-only with a disabled push URL.

- [ ] **Step 2: Write failing adapter tests**

```go
func TestItemHandlerUsesModuleAPIWithoutDatabase(t *testing.T) {
    api := &fakeModuleAPI{item: fixtureMovie4242()}
    srv := NewServer(Config{Module: api})
    rec := request(t, srv, "GET", "/Items/4242", jellyfinAuth())
    if rec.Code != http.StatusOK { t.Fatalf("status=%d", rec.Code) }
    if api.getItemCalls != 1 { t.Fatalf("calls=%d", api.getItemCalls) }
}
```

Add a repository policy test that rejects PostgreSQL drivers, Vondel internal-package imports, filesystem media reads, visibility mutation, public release actions, and database environment variables.

- [ ] **Step 3: Run focused tests and confirm RED**

Run: `GOWORK=off go test ./internal/vondel ./internal/jellyfin -count=1`

Expected: FAIL because the adapter and server are absent.

- [ ] **Step 4: Import protocol code with exact provenance**

Preserve protocol DTOs, mapping, routing, Jellyfin authentication semantics, ID codec, display preferences, image shape, playback negotiation, WebSocket shape, and observable errors. Replace every repository/database/filesystem dependency with the Task 3 client interface. Keep only bounded ephemeral protocol-session state.

- [ ] **Step 5: Implement startup, shutdown, and health**

Validate the Vondel origin and credential before listening. Use bounded header/read-idle timeouts while preserving streaming endpoints. Propagate cancellation to module API and playback sessions. Health distinguishes sidecar liveness from Vondel readiness.

- [ ] **Step 6: Run the full Jellyfin characterization suite against the sidecar**

Run:

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
```

From Vondel Server run: `GOWORK=off go test ./internal/compatcontract -run TestJellyfinSidecarParity -count=1 -v`.

Expected: every Task 1 Jellyfin case matches, including adult-negative fixtures and playback recovery.

- [ ] **Step 7: Add private CI without publication**

Use a checkout-free private dependency prefetch job, a separate secretless test/build job, pinned action SHAs, `persist-credentials: false`, and no release/registry/visibility mutation. Keep Actions disabled until the workflow has been reviewed; then enable Actions explicitly and require exact-head green.

- [ ] **Step 8: Commit and push**

```bash
git add .
git commit -m "feat: extract Jellyfin compatibility sidecar"
git push origin main
```

---

### Task 5: Extract the Audiobookshelf Sidecar

**Files:**
- Create repository files listed under **Audiobookshelf Sidecar** in the file map.
- Copy then adapt: `internal/audiobooks/abs/**` into sidecar `internal/audiobookshelf/**`.
- Copy then adapt: `internal/audiobooks/abssocket/**` into sidecar `internal/audiobookshelf/socket/**`.
- Create server: `internal/compatcontract/audiobookshelf_sidecar_test.go`.

**Interfaces:**
- Consumes: `moduleapi/client.Client` from Task 3.
- Produces: Audiobookshelf-compatible HTTP/socket listener and `/healthz`.
- Produces configuration: `VONDEL_URL`, `VONDEL_COMPAT_TOKEN`, `LISTEN_ADDR`, and optional bounded session settings.

- [ ] **Step 1: Create the private repository and safety root**

Apply the same private-before-first-push, Actions-disabled, AGPL, NOTICE, fetch-only source remote, and no-publication controls required by Task 4, naming the exact ABS and abssocket source paths.

- [ ] **Step 2: Write failing module-adapter and policy tests**

```go
func TestProgressUpdateUsesScopedModuleState(t *testing.T) {
    api := &fakeModuleAPI{}
    srv := NewServer(Config{Module: api})
    rec := request(t, srv, "PATCH", "/api/me/progress/audio-2718", progressBody(.5))
    if rec.Code != http.StatusOK { t.Fatalf("status=%d", rec.Code) }
    if api.progress.ContentID != "2718" || api.progress.Ratio != .5 {
        t.Fatalf("progress=%+v", api.progress)
    }
}
```

The repository policy test rejects PostgreSQL, internal Vondel imports, direct media filesystem access, public publication, and credential leakage.

- [ ] **Step 3: Run focused tests and confirm RED**

Run: `GOWORK=off go test ./internal/vondel ./internal/audiobookshelf/... -count=1`

Expected: FAIL because the adapter and server are absent.

- [ ] **Step 4: Import HTTP and socket protocol behavior**

Preserve DTOs, login/refresh/logout behavior, libraries, items, authors, series, collections, playlists, bookmarks, progress, listening stats, playback session semantics, file responses, socket event shapes, rate limits, and observable errors. Replace all stores with module API calls or bounded ephemeral correlation.

- [ ] **Step 5: Implement safe file and playback handoff**

Never expose Vondel file paths. Convert ABS file/play requests to signed Vondel download or playback-session URLs. Bind URLs to the mapped user/profile and short expiry; ensure redirects cannot receive the sidecar service token.

- [ ] **Step 6: Run full verification and characterization parity**

Run:

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
```

From Vondel Server run: `GOWORK=off go test ./internal/compatcontract -run TestAudiobookshelfSidecarParity -count=1 -v`.

Expected: every Task 1 Audiobookshelf case matches, including socket reconnect, progress, signed file handoff, and adult-negative fixtures.

- [ ] **Step 7: Add reviewed private CI and commit**

Use the same trust boundary as Task 4. Enable Actions only after workflow review, require exact-head green, then commit and push:

```bash
git add .
git commit -m "feat: extract Audiobookshelf compatibility sidecar"
git push origin main
```

---

### Task 6: Stand Up the Dedicated Vondel Development Stack

**Files:**
- Create: `deploy/dev/compat-compose.yml`
- Create: `deploy/dev/compat.env.example`
- Create: `scripts/dev-compat-up.sh`
- Create: `scripts/dev-compat-down.sh`
- Create: `internal/compatcontract/development_stack_test.go`
- Modify: `docs/development.md`

**Interfaces:**
- Consumes: private sidecar images or locally built binaries from Tasks 4 and 5.
- Produces: loopback/private-network Vondel, PostgreSQL, Jellyfin sidecar, Audiobookshelf sidecar, private plugin catalog, and invented media fixtures.

- [ ] **Step 1: Write a failing topology/security test**

Parse the Compose file and assert PostgreSQL is reachable only by Vondel, sidecars receive no database/data-volume configuration, each gets a distinct secret reference, and only explicit compatibility listener ports are exposed.

- [ ] **Step 2: Run the topology test and confirm RED**

Run: `GOWORK=off go test ./internal/compatcontract -run TestDevelopmentStackTopology -count=1`

Expected: FAIL because the development topology does not exist.

- [ ] **Step 3: Implement deterministic stack startup**

Create credentials through `vondel compat-credential create`, pass them through temporary mode-0600 files or container secrets, install the private plugin catalog from its token-free materialized tree, seed invented commercial media and separately permissioned adult scenes, and wait on bounded health checks.

- [ ] **Step 4: Run dual protocol acceptance**

Run both complete characterization suites, native Vondel 14-case conformance, real playback/download, WebSocket reconnect, sidecar restart, Vondel restart, expired/revoked token, adult isolation, and native-server behavior with both sidecars stopped.

- [ ] **Step 5: Prove cleanup**

`scripts/dev-compat-down.sh` removes only the named development containers, network, generated credentials, and disposable database/volumes. It refuses empty, root, home, or non-prefixed targets. Confirm no sidecar or database process remains.

- [ ] **Step 6: Commit and push**

```bash
git add deploy/dev scripts/dev-compat-up.sh scripts/dev-compat-down.sh internal/compatcontract/development_stack_test.go docs/development.md
git commit -m "test(compat): add dual-sidecar development stack"
git push origin main
```

---

### Task 7: Cut Over and Remove Embedded Compatibility Code

**Files:**
- Delete: `internal/jellycompat/**`
- Delete: `internal/audiobooks/abs/**`
- Delete: `internal/audiobooks/abssocket/**`
- Modify: `cmd/vondel/main.go`
- Modify: `internal/api/router.go`
- Modify: `internal/config/config.go`
- Modify: `web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx`
- Modify: `web/src/pages/admin-settings/AdminSettingsLayout.tsx`
- Modify: `web/src/pages/settings/ConnectAppsSettings.tsx`
- Modify: `web/src/pages/settings/connectApps.ts`
- Modify: `web/src/lib/jellyfinCompat.ts`
- Modify: `web/src/hooks/queries/admin/settings.ts`
- Create: `internal/api/handlers/compat_migration.go`
- Create: `internal/api/handlers/compat_migration_test.go`
- Modify: `docs/configuration.md`

**Interfaces:**
- Consumes: green sidecars and development-stack parity from Tasks 4–6.
- Produces: Vondel binary with no embedded Jellyfin or Audiobookshelf listener.

- [ ] **Step 1: Write failing binary/source absence tests**

```go
func TestServerContainsNoEmbeddedCompatibilityListeners(t *testing.T) {
    forbidden := []string{"internal/jellycompat", "internal/audiobooks/abs", "Jellyfin compat server listening", "Audiobookshelf-compatible API"}
    scanRepositoryAndBinary(t, forbidden)
}
```

Also assert native audiobook packages, scanner, playback, detail payloads, and web player remain present.

- [ ] **Step 2: Run absence tests and confirm RED**

Run: `GOWORK=off go test ./internal/api ./cmd/vondel -run 'EmbeddedCompatibility|CompatMigration' -count=1`

Expected: FAIL while embedded wiring exists.

- [ ] **Step 3: Remove embedded wiring and packages**

Remove listeners, config fields/defaults, Jellyfin web installer commands, compatibility status/settings routes, and extracted source packages. Do not remove native audiobook services, podcast feeds, scanner, metadata, playback, or reader code.

- [ ] **Step 4: Add explicit migration diagnostics**

When legacy compatibility configuration is detected, log one actionable message naming the replacement sidecar and documentation. Do not bind the old port, auto-create credentials, or start a hidden proxy.

- [ ] **Step 5: Run full server and sidecar regression matrix**

Run:

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./internal/moduleapi ./internal/compatcontract ./internal/audiobooks ./internal/ebooks ./internal/manga ./internal/scanner -count=1
GOWORK=off go vet ./...
GOWORK=off go build ./cmd/vondel
```

Then run both sidecar suites and the Task 6 development-stack acceptance with the embedded listeners absent.

- [ ] **Step 6: Commit and push**

```bash
git add -A
git commit -m "refactor(compat): remove embedded compatibility servers"
git push origin main
```

---

### Task 8: Private Release, Deployment, and Final Audit

**Files:**
- Create sidecars: `.github/workflows/release.yml`
- Create sidecars: `scripts/verify-private-release.sh`
- Create server: `docs/operations/compatibility-sidecars.md`
- Modify server: `deploy/dev/compat-compose.yml`

**Interfaces:**
- Produces: private, annotated, immutable sidecar releases and pinned deployment manifests.
- Consumes: exact-head green commits and reviewed release guards.

- [ ] **Step 1: Write release-guard tests before workflows**

Each sidecar guard must require exact repository identity, private visibility, annotated stable SemVer tag, tag peel equal to checked-out SHA, candidate reachable from `origin/main`, exactly reviewed private artifacts, no public registry, and a final privacy recheck.

- [ ] **Step 2: Run guard tests and confirm RED**

Run: `GOWORK=off go test ./... -run 'PrivateRelease|WorkflowSecurity' -count=1`

Expected: FAIL because release workflows do not exist.

- [ ] **Step 3: Implement private release workflows**

Use tag-push only, exact event SHA checkout, isolated private-module prefetch without repository checkout, secretless tests/builds, final-job-only `contents: write`, strict asset checksum bijection, and private GitHub Releases only. Never publish container, npm, Homebrew, Maven, or public package artifacts.

- [ ] **Step 4: Run exact-head CI and independent review**

Require all three repositories clean and equal to `origin/main`, exact-head CI green, private visibility, no candidate tag/release, and no credentials in current or reachable history.

- [ ] **Step 5: Create annotated tags and verify private releases sequentially**

Tag Jellyfin first, verify every artifact and native health response, then tag Audiobookshelf. Do not move or replace a failed tag; fix on a new version.

- [ ] **Step 6: Pin the development deployment and run final acceptance**

Pin both exact private release versions/digests. Run all native client contracts, both protocol suites, adult isolation, playback, events, failure injection, restart recovery, sidecars-absent native operation, and credential leakage scans.

- [ ] **Step 7: Commit operator documentation**

Document topology, credential creation/rotation/revocation, ports, TLS/reverse proxy, upgrades, rollback, health/readiness, logs, backups (Vondel only), and removal. Explicitly state that sidecars have no authoritative database state.

- [ ] **Step 8: Record final evidence**

Record repository, commit, annotated tag, release run, artifact hashes, private visibility, CI runs, compatibility reports, and deployment digests in the ignored SDD task reports. Update tracked architecture inventory only after every gate passes.
