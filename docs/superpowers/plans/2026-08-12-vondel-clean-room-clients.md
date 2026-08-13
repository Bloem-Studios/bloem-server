# Vondel Clean-Room Native Clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build original, native Vondel clients for all Apple and Android targets with complete Watch, Listen, Read and Live TV/DVR support, while retaining documented compatibility with Vondel and official Silo servers.

**Architecture:** A platform-neutral contracts repository defines server schemas, invented fixtures and conformance behavior. Empty Apple and Android repositories independently implement those contracts using SwiftUI/native Apple media frameworks and Kotlin/Compose/Media3; they share no UI or application runtime. TV is the first production-complete vertical slice, but submission is blocked until the full media/platform matrix passes.

**Tech Stack:** JSON Schema/OpenAPI, Go conformance runner, Swift 6/SwiftUI/AVFoundation/Core Media/XCTest/XcodeGen, Kotlin 2.1/Coroutines/Compose/Media3/Room/WorkManager/JUnit/Gradle, GitHub Actions.

## Global Constraints

- Create new private zero-parent repositories `Vondel-Media/vondel-client-contracts`, `Vondel-Media/vondel-apple`, and `Vondel-Media/vondel-android`.
- Never add either `silo-*-reference` repository as a remote, dependency, submodule, build input or source generator.
- No reference source, assets, layouts, internal symbols, tests, filenames or package structure may be copied.
- The only shared boundary is documented server contracts plus deterministic invented fixtures.
- Support movies, episodic television, music, audiobooks, ebooks and manga on iPhone, iPad, macOS, Apple TV, Android phone, Android tablet and Android TV before store submission.
- Support Live TV guide, tune, timeshift, one-off/series DVR, conflicts and recordings on all seven targets before store submission.
- Implement both Vondel and capability-compatible official Silo server connections.
- Use `media.vondel.app` application identities and `media.vondel` source namespaces.
- Use AGPL-3.0-or-later and record every dependency/asset license.
- Keep repositories private and publication workflows disabled until the final acceptance task explicitly enables signed store delivery.
- Never claim store approval is guaranteed; build auditable evidence of original implementation and value.
- Client implementers do not inspect reference source. A separate audit role produces fingerprint/similarity reports after implementation and cannot contribute application code.

## Execution lanes

- Tasks 1–2 establish the mandatory provenance and contract boundary.
- Tasks 3 and 4 run in parallel after Task 2.
- Task 5 runs in parallel per platform after both foundations expose the same route semantics.
- Tasks 6–10 are vertical feature slices; Apple and Android implementations run in parallel within each slice, while Watch, Listen, Read and Live TV coordinators remain isolated.
- Task 10 depends on the Vondel Live TV server/contracts plan reaching its contract handoff; Task 11 begins only after Tasks 6–10 are green on every target.

---

### Task 1: Create Empty Repositories and Originality Gates

**Files:**
- Create in each repository: `LICENSE`, `NOTICE`, `README.md`, `ORIGINALITY.md`, `.github/workflows/ci.yml`
- Create in contracts: `internal/originality/guard.go`, `internal/originality/guard_test.go`
- Create in clients: `scripts/verify-clean-room.sh`, `scripts/verify-clean-room_test.sh`

**Interfaces:**
- Produces `originality.Scan(root string, forbiddenRoot string) error` and the clean-room ledger format consumed by every later task.

- [ ] Write a failing Go test using an invented forbidden file named `SiloPlaybackCoordinator.swift` and copied canary text; require the guard to reject distinctive filenames, source fragments, binary-identical assets and forbidden remotes while accepting required protocol terms such as `/api/v1/`.

```go
func TestScanRejectsReferenceCanaries(t *testing.T) {
    err := originality.Scan(t.TempDir(), "/tmp/reference-fixture")
    if err == nil || !strings.Contains(err.Error(), "reference similarity") {
        t.Fatalf("Scan error = %v", err)
    }
}
```

- [ ] Run `go test ./internal/originality -run TestScanRejectsReferenceCanaries -count=1` and record the expected RED result before implementation.
- [ ] In an audit-only process, implement SHA-256 asset comparison, normalized nontrivial source-fragment comparison, distinctive-path comparison, Git remote rejection and a narrow standardized-protocol allowlist; emit reports without making reference source available to client implementers.
- [ ] Create the three Git repositories locally with only legal/provenance/guard files, verify GitHub targets are PRIVATE before first push, disable Actions publication, commit zero-parent roots and push.
- [ ] Run the originality guard against both reference repositories and require zero unexplained findings.

```bash
go test ./internal/originality -count=1
./scripts/verify-clean-room.sh ../silo-apple-reference
./scripts/verify-clean-room.sh ../silo-android-reference
```

### Task 2: Define the Server Contract and Disposable Conformance Harness

**Files:**
- Create contracts: `schema/openapi.yaml`, `schema/events/*.schema.json`, `schema/playback/*.schema.json`, `schema/offline/*.schema.json`
- Create contracts: `fixtures/{identity,auth,catalog,watch,listen,read,playback,offline,errors}/*.json`
- Create contracts: `cmd/vondel-client-conformance/main.go`, `internal/conformance/{client.go,suite.go,report.go}`
- Create server: `internal/clientcontract/export_test.go`, `internal/clientcontract/conformance_test.go`

**Interfaces:**
- Produces versioned schemas and `conformance.Run(ctx context.Context, baseURL string, fixtureSet fs.FS) (Report, error)`.

- [ ] Write failing schema tests for identity/capabilities, login/refresh/profile, catalog/search, six media types, progress, playback plan, WebSocket events and offline bundles using invented IDs such as movie `4242`, series `8080`, album `313`, audiobook `2718`, ebook `1618` and manga `5772`.
- [ ] Run `go test ./internal/conformance ./schema -count=1` and record RED failures for absent schemas.
- [ ] Write the schemas from Vondel Server route and API documentation, not client reference models; include required/optional fields, enum values and explicit forward-compatible unknown-field behavior.
- [ ] Implement a disposable-server runner that creates a unique database, migrates it, seeds invented media, starts Vondel on a loopback port, runs all contract cases, then validates and drops only the generated database.
- [ ] Add an official-Silo compatibility job that runs the same baseline contract subset against pinned Silo Server commit `1dcdd4b27ab5fcd697a32fc20f20c2400ca24688`.
- [ ] Commit/push contracts and server harness; require exact-head CI green in both repositories.

### Task 3: Establish Apple Targets, Identity and Connection Foundation

**Files:**
- Create Apple: `project.yml`, `Config/*.xcconfig`, `Sources/VondelCore/{Contract,Connection,Identity,Storage}/**`
- Create Apple: `Apps/{iOS,tvOS,macOS}/**`, `Tests/VondelCoreTests/**`, `UITests/**`

**Interfaces:**
- Produces `ServerGateway`, `CapabilitySet`, `AccountScope`, `SecureAccountVault` and scoped local-store protocols.

- [ ] Write XCTest RED cases proving server/account/profile changes yield different `AccountScope` values, stale responses cannot enter a new scope, token refresh is single-flight and credentials never enter diagnostics.

```swift
func testProfileSwitchRejectsPreviousScopeResult() async throws {
    let old = AccountScope(serverID: "one", accountID: "a", profileID: "p1")
    let new = AccountScope(serverID: "one", accountID: "a", profileID: "p2")
    XCTAssertFalse(new.accepts(ResponseEnvelope(scope: old, value: 42)))
}
```

- [ ] Create XcodeGen targets for iOS/iPadOS, tvOS and macOS using bundle IDs beneath `media.vondel.app`; generate the project and verify target/product identity without signing.
- [ ] Implement URL validation, capability probe, authentication/refresh, Keychain vault and scoped SQLite/SwiftData store from contracts fixtures.
- [ ] Create original platform-native connection/profile UI and accessibility identifiers; do not inspect reference screen structure.
- [ ] Run unit/UI tests and unsigned target builds, originality scan and dependency license audit; commit/push and require exact-head CI green.

### Task 4: Establish Android Targets, Identity and Connection Foundation

**Files:**
- Create Android: `settings.gradle.kts`, `build.gradle.kts`, `gradle/libs.versions.toml`
- Create modules: `:contract`, `:core-model`, `:core-network`, `:core-identity`, `:core-storage`, `:app-mobile`, `:app-tv`
- Create tests under each module's `src/test` and UI tests under `src/androidTest`

**Interfaces:**
- Produces Kotlin `ServerGateway`, `CapabilitySet`, `AccountScope`, `SecureAccountVault` and scoped Room database interfaces matching Task 3 semantics but not source layout.

- [ ] Write JUnit RED cases for scope isolation, stale-response rejection, single-flight refresh, encrypted credential persistence and diagnostic redaction.

```kotlin
@Test fun profileSwitchRejectsPreviousScopeResult() {
    val old = AccountScope("one", "a", "p1")
    val current = AccountScope("one", "a", "p2")
    assertFalse(current.accepts(ResponseEnvelope(old, 42)))
}
```

- [ ] Configure mobile/tablet and Android TV apps with `media.vondel.app` identity and distinct module namespaces beneath `media.vondel`.
- [ ] Implement validated URL connection, capability probe, authentication/refresh, Android Keystore vault and scoped Room storage from contract fixtures.
- [ ] Create original Compose connection/profile surfaces separately optimized for touch and TV focus.
- [ ] Run unit/instrumented/focus tests, lint and unsigned debug builds, originality scan and dependency license audit; commit/push and require exact-head CI green.

### Task 5: Build Original Design Systems and Navigation on Every Target

**Files:**
- Create Apple: `Sources/VondelDesign/**`, `Sources/VondelNavigation/**`, snapshot and accessibility tests
- Create Android: `:design-system`, `:navigation`, screenshot and focus tests
- Create dossier: `docs/originality/visual-system.md`

**Interfaces:**
- Produces original tokens/components and route IDs `now`, `watch`, `listen`, `read`, `library`, `search`, `settings`.

- [ ] Write RED snapshot/focus tests for phone bottom navigation, tablet/macOS adaptive columns, TV spatial navigation and the seven route IDs.
- [ ] Implement Vondel-owned typography, color, spacing, motion, focus, artwork and empty/error/loading primitives without reference assets or component layouts.
- [ ] Implement adaptive navigation shells on all seven form factors; TV focus order must be explicitly modeled rather than derived from phone ordering.
- [ ] Generate visual evidence from invented demo media and run perceptual/reference similarity checks; resolve every unexplained high-similarity result.
- [ ] Commit/push Apple and Android design/navigation slices with exact-head CI green.

### Task 6: Complete TV-First Watch Across All Targets

**Files:**
- Create Apple: `Sources/Watch/{Catalog,Detail,Playback,Progress}/**`, tvOS/iOS/macOS tests
- Create Android: `:feature-watch`, `:playback-video`, TV/mobile tests
- Create contracts: Watch and playback-plan fixture expansions

**Interfaces:**
- Produces movie/series browsing, details, direct play/remux/transcode selection, subtitles/audio/chapters/HDR, progress and next-up behavior.

- [ ] Add RED contract and platform tests for invented movie `4242`, series `8080`, season 1/episode 1, three playback modes, subtitle/audio selection, progress/resume, timeout and recovery.
- [ ] Implement TV catalog/detail/focus flows first using original editorial composition, then implement phone/tablet/macOS variants from the Vondel design system.
- [ ] Implement independent Apple and Android playback coordinators that validate server plans and map them to AVFoundation/Core Media or Media3 routes.
- [ ] Run codec/container/subtitle/HDR/network-transition matrices against controlled media and assert no credentials or media URLs appear in logs.
- [ ] Commit/push both Watch slices and require exact-head CI green.

### Task 7: Complete Listen for Music and Audiobooks

**Files:**
- Create Apple: `Sources/Listen/{Music,Audiobooks,Queue,NowPlaying}/**`
- Create Android: `:feature-listen`, `:playback-audio`
- Create contracts: music/audiobook/queue/progress fixtures

**Interfaces:**
- Produces artist/album/track and audiobook/chapter browsing, queues, background playback, system controls, speed, sleep timer, progress and downloads.

- [ ] Write RED tests for album `313`, audiobook `2718`, deterministic queues, chapter resume, speed, sleep timer, lock-screen/system controls, background transition and profile switch.
- [ ] Implement original TV Listen navigation and now-playing experience, then adaptive touch/macOS versions.
- [ ] Implement platform-native audio sessions/services and durable scoped queue/progress state.
- [ ] Run interruption, route-change, Bluetooth/cast, background/foreground, offline and recovery suites.
- [ ] Commit/push both Listen slices and require exact-head CI green.

### Task 8: Complete Read for Ebooks and Manga

**Files:**
- Create Apple: `Sources/Read/{Ebook,Manga,Reader,Progress}/**`
- Create Android: `:feature-read`, `:reader-ebook`, `:reader-manga`
- Create contracts: ebook/manga/chapter/progress fixtures

**Interfaces:**
- Produces ebook `1618` and manga `5772` browsing/reading, typography/themes, pagination, manga direction/panel controls, progress, bookmarks and offline reading.

- [ ] Write RED tests for pagination stability, font/theme changes, left/right manga direction, bookmarks, progress, invalid archive rejection and account/profile isolation.
- [ ] Implement original remote-friendly TV reading controls, then touch/keyboard/mouse readers for mobile/tablet/macOS.
- [ ] Stage and verify reader bundles before atomic availability; prevent traversal, executable content and cross-profile access.
- [ ] Run reader accessibility, large-text, focus/gesture, right-to-left, offline/corrupt-bundle and recovery tests.
- [ ] Commit/push both Read slices and require exact-head CI green.

### Task 9: Complete Library, Search, Now, Sync and Downloads

**Files:**
- Create Apple: `Sources/{Library,Search,Now,Sync,Downloads}/**`
- Create Android: `:feature-library`, `:feature-search`, `:feature-now`, `:sync`, `:downloads`
- Extend contracts for unified search, mutations, notifications and offline lifecycle

**Interfaces:**
- Produces cross-media discovery/activity and durable idempotent synchronization across all content types.

- [ ] Write RED tests grouping six media types in search; combining watch/listen/read progress in Now; idempotent favorites/watchlists/playlists/bookmarks; download resume/revocation; stale-scope cancellation.
- [ ] Implement original Library, Search and Now surfaces independently on Apple and Android, preserving platform-native TV/touch navigation.
- [ ] Implement bounded exponential retry, network-aware durable queues, idempotency keys and atomic offline promotion.
- [ ] Run airplane-mode, process-death, token-expiry, server-switch, profile-switch, disk-pressure and corrupted-bundle suites.
- [ ] Commit/push both cross-media slices and require exact-head CI green.

### Task 10: Complete Live TV and DVR on Every Target

**Files:**
- Create Apple: `Sources/LiveTV/{Guide,Player,Timeshift,DVR,Recordings}/**`
- Create Android: `:feature-livetv`, `:livetv-player`, `:feature-recordings`
- Consume contracts: `schema/livetv/*.schema.json`, `fixtures/livetv/*.json`

**Interfaces:**
- Consumes the capability-gated Vondel Live TV API produced by `2026-08-12-vondel-prairie-livetv.md` Task 9.
- Produces original guide, live player, timeshift, DVR and recordings experiences on all seven targets.

- [ ] Write RED contract/platform tests using invented channels `KVDL-7`/`KVDL-12` for capability-hidden behavior, guide windows, tune, heartbeat, pause/resume/seek-live, record/cancel, series rules, conflicts and completed/failed recordings.
- [ ] Implement original TV-first spatial guide grids and channel/player overlays independently in SwiftUI and Compose; do not inspect Prairie/Silo client source or reuse Vondel web layouts.
- [ ] Implement touch/keyboard adaptations for iPhone, iPad, macOS, Android phone and Android tablet from each platform's design system.
- [ ] Integrate live playback plans with the independent Apple/Android playback coordinators; protect short-lived tokens, recover sessions and surface typed tuner/guide/disk/conflict errors.
- [ ] Implement profile-scoped DVR rule/conflict/recordings flows and capability-negative behavior for official Silo servers without Live TV.
- [ ] Run TV focus, accessibility, guide timezone/DST, tuner exhaustion, timeshift expiry, process/network recovery and recordings suites; run originality source/asset/visual checks; commit/push both clients with exact-head CI green.

### Task 11: Full Matrix, Privacy, Accessibility and Store Dossier

**Files:**
- Create contracts: `cmd/full-client-matrix/**`, `docs/conformance-report.md`
- Create clients: privacy manifests/data-safety config, localization catalogs, store metadata, review notes and demo-mode configuration
- Create server: `docs/client-release-inventory.md`

**Interfaces:**
- Produces signed evidence that gates—but does not guarantee—store submission.

- [ ] Run the disposable-server conformance suite for all six library media types plus Live TV/DVR against Vondel and the capability-supported baseline subset against official Silo commit `1dcdd4b27ab5fcd697a32fc20f20c2400ca24688`.
- [ ] Run the complete device/form-factor matrix for iPhone, iPad, macOS, Apple TV, Android phone, Android tablet and Android TV, including Watch, Listen, Read, Live TV/DVR, offline and identity switching.
- [ ] Run accessibility, localization, privacy, security, dependency-license, credential, originality source/asset and visual-composition gates.
- [ ] Create Vondel-owned icons/screenshots/previews with invented demo media, accurate privacy/support URLs and stable reviewer credentials/demo server.
- [ ] Generate the originality dossier from Git history, specifications, provenance ledgers, similarity reports, dependencies and build/test results; require zero unexplained findings.
- [ ] Add store signing/submission only in isolated protected workflows after all gates pass; keep credentials environment-scoped and never available to pull-request code.
- [ ] Commit/push final evidence and record exact commits/runs in `docs/client-release-inventory.md` without claiming approval before the stores decide.
