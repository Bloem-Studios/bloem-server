# Vondel Audiobookshelf Application Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the private `vondel-audiobookshelf` application, reach full protocol parity through Vondel's private API, and switch `/audiobookshelf/**` without affecting native audiobook behavior.

**Architecture:** The application owns Audiobookshelf HTTP and Socket.IO translation plus disposable protocol state. Vondel remains authoritative for identity, policy, audiobook catalog, progress, bookmarks, collections, playlists, downloads, playback, and events; the fixed gateway strips `/audiobookshelf` before forwarding.

**Tech Stack:** Go 1.26, SQLite/WAL, optional PostgreSQL, Socket.IO/WebSocket, generated Compatibility API client, Docker, private GitHub Actions/OCI.

**Spec:** `docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md`

## Global Constraints

- Repository and images remain private indefinitely.
- The application receives no Vondel database, Redis, media mounts, Docker socket, signing key, or unrelated secrets.
- Submitted passwords are forwarded transiently and never persisted, hashed, cached, inspected beyond decoding, or logged.
- Canonical progress, bookmarks, collections, playlists, downloads, playback, and activity remain in Vondel.
- Application persistence is disposable protocol state only, defaulting to SQLite/WAL in its own named volume.
- Public access exists only at the canonical Vondel `/audiobookshelf/**` prefix.
- Use RED/GREEN TDD, task commits, independent reviews, and exact private release evidence.

---

### Task 1: Create the Private Application Repository

**Files (new repository `Vondel-Media/vondel-audiobookshelf`):**
- Create: `go.mod`
- Create: `LICENSE`
- Create: `NOTICE`
- Create: `README.md`
- Create: `cmd/vondel-audiobookshelf/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `.github/workflows/ci.yml`
- Create: `scripts/verify-private-source.sh`
- Create: `scripts/verify-private-source_test.sh`

**Interfaces:**
- Consumes the tagged private Compatibility API client module produced by Foundation Task 4.
- Produces one HTTP listener on private port `8080`, `/health/live`, `/health/ready`, and graceful shutdown.

- [ ] **Step 1: Create the private repository and prove visibility before first push**

Initialize a parentless `main`, preserve AGPL-3.0 license obligations, and add `NOTICE` naming the exact Vondel source commit and extracted `internal/audiobooks/abs`, `internal/audiobooks/abssocket`, and related store paths. Configure the upstream/source remote fetch-only and its push URL to `DISABLED`.

- [ ] **Step 2: Write failing configuration and privacy tests**

Test required Vondel endpoint, enrollment secret file, state DSN, API range, TLS/mTLS validation, absence of password-bearing log values, and rejection of enrollment secrets supplied through environment variables or URLs.

- [ ] **Step 3: Implement minimal process lifecycle**

Read the enrollment token only from `/run/secrets/vondel_compat_enrollment`, enroll once, remove its in-memory bytes, renew service credentials, perform API compatibility handshake before readiness, and stop accepting traffic on revocation/incompatibility.

- [ ] **Step 4: Add trusted private CI**

CI may fetch private modules only in a checkout-free credential job that uploads a sanitized module artifact. Repository-controlled jobs are secretless. Workflows may publish only private repository-owned release artifacts/images and must never alter visibility or dispatch to Silo.

- [ ] **Step 5: Verify and commit**

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
bash scripts/verify-private-source_test.sh
git add . && git commit -m "feat: create Vondel Audiobookshelf application"
```

---

### Task 2: Extract Authentication and Protocol Identity

**Files:**
- Create: `internal/audiobookshelf/login.go`
- Create: `internal/audiobookshelf/users.go`
- Create: `internal/audiobookshelf/router.go`
- Create: `internal/vondel/client.go`
- Create: `internal/session/store.go`
- Create: `internal/session/sqlite.go`
- Create: `internal/session/postgres.go`
- Create: `internal/audiobookshelf/login_test.go`
- Create: `internal/session/store_test.go`

**Interfaces:**
- Produces ABS `/login`, `/logout`, `/authorize`, `/me`, ping/status, and token refresh semantics.
- Consumes private API credential exchange, remembered/default profile, device trust, revocation, and subject-scoped tokens.

- [ ] **Step 1: Write failing black-box identity tests**

Run the shared Audiobookshelf identity fixtures against the new application. Assert direct profile email/password enters that profile; legacy account login resolves remembered/default profile; shared-only profiles cannot remote-login; passwords never reach persistence/logs; disabled/revoked subjects fail immediately; logout removes only disposable correlation.

- [ ] **Step 2: Prove RED**

Run: `GOWORK=off go test ./internal/audiobookshelf ./internal/session -run 'Login|Identity|Revocation' -count=1`

Expected: FAIL because handlers and state adapters are absent.

- [ ] **Step 3: Implement protocol translation and disposable state**

Store only opaque Vondel session correlation, ABS token ID, device capabilities, expiry, and event cursor. Enable SQLite WAL/busy timeout and provide the same repository contract for a separate optional PostgreSQL database. Never store submitted credentials or canonical profile data.

- [ ] **Step 4: Run parity and persistence tests**

```bash
GOWORK=off go test ./internal/audiobookshelf ./internal/session -count=1
GOWORK=off go test -race ./internal/audiobookshelf ./internal/session -count=1
```

Expected: PASS for SQLite and disposable-state reset; PostgreSQL contract runs when its dedicated DSN is supplied.

- [ ] **Step 5: Commit**

```bash
git add internal/audiobookshelf internal/vondel internal/session
git commit -m "feat(abs): add Vondel-backed identity"
```

---

### Task 3: Extract Catalog, State, Downloads, and Playback

**Files:**
- Create: `internal/audiobookshelf/libraries.go`
- Create: `internal/audiobookshelf/items.go`
- Create: `internal/audiobookshelf/search.go`
- Create: `internal/audiobookshelf/collections.go`
- Create: `internal/audiobookshelf/playlists.go`
- Create: `internal/audiobookshelf/bookmarks.go`
- Create: `internal/audiobookshelf/progress.go`
- Create: `internal/audiobookshelf/downloads.go`
- Create: `internal/audiobookshelf/playback.go`
- Create: `internal/audiobookshelf/protocol_test.go`

**Interfaces:**
- Consumes the private API catalog/state/playback/download operations.
- Produces the embedded ABS response shapes frozen by `compatcontract.AudiobookshelfBaseline()`.

- [ ] **Step 1: Run the shared suite and record RED cases**

Point the shared runner at the application. Expected failures must be named for libraries, items/authors/series, search, collections, playlists, bookmarks, progress, continue listening, playback sessions, file delivery, offline metadata, and errors.

- [ ] **Step 2: Implement read translation**

Map Vondel subject-filtered resources to ABS shapes without querying storage directly. Preserve signed cursors internally even where ABS exposes page numbers. Resolve images and downloads to same-origin `/audiobookshelf/**` or Vondel delivery URLs according to protocol requirements.

- [ ] **Step 3: Implement idempotent mutations and playback**

Derive stable idempotency keys from external request/session identifiers. Propagate cancellation/deadlines. Prefer direct client-to-Vondel delivery; bound any protocol-required proxy by size, time, cancellation, and slow-client backpressure.

- [ ] **Step 4: Verify full HTTP parity and adult isolation**

```bash
GOWORK=off go test ./internal/audiobookshelf -run 'Protocol|Catalog|State|Playback|Adult' -count=1
GOWORK=off go test -race ./internal/audiobookshelf -count=1
```

Assert adult items are absent from payloads, counts, search, artwork, activity, delivery, and timing-disclosing errors.

- [ ] **Step 5: Commit**

```bash
git add internal/audiobookshelf
git commit -m "feat(abs): translate catalog state and playback"
```

---

### Task 4: Extract Socket.IO Events and Recovery

**Files:**
- Create: `internal/socket/server.go`
- Create: `internal/socket/session.go`
- Create: `internal/socket/events.go`
- Create: `internal/socket/server_test.go`
- Create: `internal/socket/recovery_test.go`

**Interfaces:**
- Consumes subject-filtered private API events and resumable cursors.
- Produces ABS Socket.IO connection/authentication, progress/session/library notifications, ping/pong, reconnect, and protocol closure behavior.

- [ ] **Step 1: Write failing socket parity tests**

Cover authenticated upgrade, unknown/revoked token, filtered events, reconnect from durable cursor, gap-triggered bounded resync, app restart, Vondel restart, slow consumer, queue overflow, and graceful shutdown.

- [ ] **Step 2: Prove RED and implement bounded event handling**

Run: `GOWORK=off go test ./internal/socket -count=1`

Expected: FAIL. Implement per-session bounded queues, subject/audience verification on connect and renewal, redacted trace IDs, durable cursor checkpoints, and protocol-appropriate close reasons.

- [ ] **Step 3: Verify normal/race/leak behavior and commit**

```bash
GOWORK=off go test ./internal/socket ./internal/audiobookshelf -count=1
GOWORK=off go test -race ./internal/socket -count=1
git add internal/socket internal/audiobookshelf
git commit -m "feat(abs): add resumable socket events"
```

---

### Task 5: Dual-Run Parity and Gateway Cutover

**Files (Vondel Server):**
- Create: `internal/acceptance/audiobookshelf_external_test.go`
- Modify: `internal/compatgateway/routes.go`
- Modify: `internal/compatgateway/proxy_test.go`
- Modify: `docker-compose.audiobookshelf.yml`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes an exact private `vondel-audiobookshelf` image digest.
- Produces the `/audiobookshelf/**` route cutover while embedded code remains available only as a test oracle for this task.

- [ ] **Step 1: Add failing dual-run acceptance**

Provision a clean Vondel database, deterministic media, embedded target, and external target. Run every shared case against both and compare normalized reports. Then exercise gateway same-origin URLs, unavailable behavior, disabled/revoked application, incompatible API, restart recovery, and native server health.

- [ ] **Step 2: Prove RED and switch the route**

Run: `SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -tags=integration ./internal/acceptance -run TestExternalAudiobookshelfParity -count=1 -v`

Expected initial failure: `/audiobookshelf/**` does not yet target the external application. Enable only this fixed route after the exact image passes parity.

- [ ] **Step 3: Verify no direct companion exposure or canonical-state loss**

Stop/remove the application container and its volume; native audiobook state must remain. Reinstall/re-enroll, log in fresh, and prove progress/bookmarks/collections/playlists reappear while only protocol preferences reset.

- [ ] **Step 4: Commit the server cutover**

```bash
git add internal/acceptance/audiobookshelf_external_test.go internal/compatgateway docker-compose.audiobookshelf.yml .github/workflows/ci.yml
git commit -m "feat(compat): route Audiobookshelf to external application"
```

---

### Task 6: Private Release and Operator Documentation

**Files (application):**
- Create: `Dockerfile`
- Create: `.github/workflows/release.yml`
- Create: `scripts/verify-private-release.sh`
- Create: `scripts/verify-private-release_test.sh`
- Create: `docs/client-configuration.md`
- Create: `docs/operations.md`

**Interfaces:**
- Produces a private multi-architecture image and provenance/SBOM attached to an annotated stable tag.
- Produces exact client URL `https://vondel.example/audiobookshelf`.

- [ ] **Step 1: Write failing release policy tests**

Require private repository visibility, annotated stable tag at exact green `origin/main`, immutable digest, signed provenance/SBOM, no public registry/package action, no credentials in layers/history/logs, and supported API-range handshake.

- [ ] **Step 2: Implement and verify the private image**

Build as non-root with read-only root filesystem compatibility, separate writable state volume, health checks, no shell when practical, and linux/amd64 plus linux/arm64 images. Scan the image for Vondel secrets, submitted-password fixtures, database URLs, and media paths.

- [ ] **Step 3: Write client and operations guides**

Cover direct profile login, legacy account fallback, shared-only profiles, URL suffix, enable/enroll/update/pin/rollback/disable/revoke/uninstall, SQLite backup, optional dedicated PostgreSQL, mTLS remote placement, and disposable-state expectations.

- [ ] **Step 4: Run exact release gates and commit**

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
bash scripts/verify-private-release_test.sh
git diff --check
git add . && git commit -m "release: prepare private Audiobookshelf application"
```

Tag and release only after independent review and exact-HEAD CI are green; record tag, peeled commit, image digest, CI run, and private visibility in the Vondel inventory.
