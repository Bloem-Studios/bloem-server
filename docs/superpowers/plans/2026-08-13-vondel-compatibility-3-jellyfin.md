# Vondel Jellyfin Application Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the private `vondel-jellyfin` application and replace Vondel's embedded Jellyfin surface, including media, playback, sessions, Live TV, Jellyfin Web, and LAN discovery.

**Architecture:** The application translates Jellyfin protocols through subject-scoped private Vondel APIs and retains only disposable client/session/preferences state. Vondel owns the canonical public address and exact Jellyfin route set, serves delivery URLs, enforces policy, operates Prairie-derived Live TV/DVR, and emits the optional LAN discovery response.

**Tech Stack:** Go 1.26, SQLite/WAL, optional PostgreSQL, WebSockets, Jellyfin Web static assets, UDP discovery, Docker, private GitHub Actions/OCI.

**Spec:** `docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md`

## Global Constraints

- Repository and images remain private indefinitely.
- Vondel remains sole authority for identity, policy, catalog, playback, events, Live TV, DVR, and recording files.
- No public user/profile directory exists; unknown devices receive no user list.
- Direct profile email/password, legacy account login, trusted-device profile tiles, and existing PIN switching all remain supported.
- Adult content is absent from metadata, counts, search, artwork, activity, events, playback, Live TV, and timing hints when unauthorized.
- The application receives no Vondel database, Redis, media mounts, Docker socket, signing/provider/tuner secrets, or unrestricted filesystem paths.
- Public access uses only the canonical Vondel address and a compile-time fixed route set.
- Use RED/GREEN TDD, task commits, independent reviews, and exact private release evidence.

---

### Task 1: Create the Private Jellyfin Application Repository

**Files (new repository `Vondel-Media/vondel-jellyfin`):**
- Create: `go.mod`
- Create: `LICENSE`
- Create: `NOTICE`
- Create: `README.md`
- Create: `cmd/vondel-jellyfin/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `.github/workflows/ci.yml`
- Create: `scripts/verify-private-source.sh`
- Create: `scripts/verify-private-source_test.sh`

**Interfaces:**
- Consumes the tagged private Compatibility API client.
- Produces private HTTP port `8096`, readiness/liveness, API negotiation, enrollment, renewal, revocation, and graceful shutdown.

- [ ] **Step 1: Initialize a verified private parentless repository**

Preserve AGPL-3.0 obligations and add `NOTICE` naming the exact Vondel source commit plus extracted `internal/jellycompat` paths. Configure source/upstream fetch-only with push URL `DISABLED`; confirm private visibility before first push.

- [ ] **Step 2: Write failing lifecycle/privacy tests and implement**

Test secret-file-only enrollment, immutable instance identity, supported API range, state DSN, optional mTLS, renewal, revocation, incompatible readiness, password/log redaction, and native process shutdown. Read enrollment only from `/run/secrets/vondel_compat_enrollment` and erase raw bytes after use.

- [ ] **Step 3: Add trusted private CI and verify**

Use checkout-free private-module prefetch and secretless code execution. Prohibit visibility changes, Silo dispatch, public registries/packages, credential-bearing URLs, Vondel database variables, media mounts, and Docker socket access.

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
bash scripts/verify-private-source_test.sh
git add . && git commit -m "feat: create Vondel Jellyfin application"
```

---

### Task 2: Extract Jellyfin Identity, Devices, and Preferences

**Files:**
- Create: `internal/jellyfin/router.go`
- Create: `internal/jellyfin/system.go`
- Create: `internal/jellyfin/login.go`
- Create: `internal/jellyfin/users.go`
- Create: `internal/jellyfin/devices.go`
- Create: `internal/jellyfin/preferences.go`
- Create: `internal/vondel/client.go`
- Create: `internal/session/store.go`
- Create: `internal/session/sqlite.go`
- Create: `internal/session/postgres.go`
- Create: `internal/jellyfin/identity_test.go`

**Interfaces:**
- Produces the Jellyfin system and users route families, authentication, logout, profile tiles, configuration/preferences, and device/session correlation.
- Consumes private API credential exchange, device trust, profile discovery, PIN verification/switching, revocation, and API-range health.

- [ ] **Step 1: Add failing shared identity cases**

Run direct profile login, legacy account login, remembered/default profile, unknown-device empty public users, trusted-device permitted tiles only, unprotected switch, PIN switch, wrong PIN, sibling/cross-org denial, device revocation, password reset, and policy/membership/org suspension.

- [ ] **Step 2: Prove RED and implement translations**

Run: `GOWORK=off go test ./internal/jellyfin ./internal/session -run 'Identity|Login|Device|Profile' -count=1`

Expected: FAIL. Translate Jellyfin credentials transiently to Vondel; store only opaque token correlation, device capabilities, Jellyfin preferences, expiry, and event cursor. Never persist password or canonical identity/profile policy.

- [ ] **Step 3: Verify SQLite/PostgreSQL contracts, race, and redaction**

```bash
GOWORK=off go test ./internal/jellyfin ./internal/session -count=1
GOWORK=off go test -race ./internal/jellyfin ./internal/session -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/jellyfin internal/vondel internal/session
git commit -m "feat(jellyfin): add Vondel-backed identity"
```

---

### Task 3: Extract Catalog, Images, User State, and Collections

**Files:**
- Create: `internal/jellyfin/views.go`
- Create: `internal/jellyfin/items.go`
- Create: `internal/jellyfin/search.go`
- Create: `internal/jellyfin/images.go`
- Create: `internal/jellyfin/userdata.go`
- Create: `internal/jellyfin/collections.go`
- Create: `internal/jellyfin/playlists.go`
- Create: `internal/jellyfin/downloads.go`
- Create: `internal/jellyfin/catalog_test.go`

**Interfaces:**
- Consumes private API library/catalog/search/detail/artwork/state/collection/playlist/download operations.
- Produces Jellyfin views/items/genres/persons/studios/images/favorites/watched/progress/collections/playlists/download response shapes.

- [ ] **Step 1: Run shared catalog fixtures and record RED**

Cover every commercial/streamed Vondel media type supported by Jellyfin mappings: movies, series, episodes, music artists/albums/tracks, audiobooks, ebooks where the protocol permits, podcasts, radio, Live TV channels/programs/recordings, manga/comics through explicit compatible item mappings, and mixed libraries.

- [ ] **Step 2: Implement subject-filtered translations**

Use signed Vondel cursors behind Jellyfin paging, stable external IDs, same-origin image/delivery URLs, and exact field omissions. No local metadata authority or direct filesystem reads. Use idempotency keys for favorites, watched/progress, collections, playlists, and download mutations.

- [ ] **Step 3: Prove adult and tenant isolation**

Test no unauthorized adult IDs in payloads, counts, suggestions, search, related items, artwork, activity, or cache keys; compare negative timing distributions. Test two organizations with colliding media names/IDs never cross.

- [ ] **Step 4: Verify and commit**

```bash
GOWORK=off go test ./internal/jellyfin -run 'Catalog|Item|Image|UserData|Collection|Playlist|Adult|Tenant' -count=1
GOWORK=off go test -race ./internal/jellyfin -count=1
git add internal/jellyfin
git commit -m "feat(jellyfin): translate catalog and user state"
```

---

### Task 4: Extract Playback, Transcoding, Sessions, and Events

**Files:**
- Create: `internal/jellyfin/playback.go`
- Create: `internal/jellyfin/streams.go`
- Create: `internal/jellyfin/transcoding.go`
- Create: `internal/jellyfin/sessions.go`
- Create: `internal/jellyfin/websocket.go`
- Create: `internal/jellyfin/playback_test.go`
- Create: `internal/jellyfin/websocket_test.go`

**Interfaces:**
- Consumes private playback planning, signed delivery grants, transcode start/cancel/recovery, session reporting, and resumable subject events.
- Produces Jellyfin PlaybackInfo, direct stream, subtitle, transcode, scrobble, session, and WebSocket behavior.

- [ ] **Step 1: Write failing playback/session parity tests**

Cover direct play, direct stream, transcode profiles, range/seeking, subtitles, slow clients, caller cancellation, transcode cancellation, recovery, session start/progress/stop, multiple devices, WebSocket reconnect/gap/restart, queue overflow, and revocation mid-stream/new-request denial.

- [ ] **Step 2: Prove RED and implement playback mapping**

Run: `GOWORK=off go test ./internal/jellyfin -run 'Playback|Stream|Transcode|Session|WebSocket' -count=1`

Prefer short-lived same-origin Vondel URLs so media bytes bypass the application. Where Jellyfin semantics require proxying, enforce byte/time/concurrency bounds, cancellation propagation, and hop-by-hop header stripping.

- [ ] **Step 3: Verify concurrency and recovery**

```bash
GOWORK=off go test ./internal/jellyfin -run 'Playback|Stream|Transcode|Session|WebSocket' -count=1
GOWORK=off go test -race ./internal/jellyfin -run 'Playback|Session|WebSocket' -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/jellyfin
git commit -m "feat(jellyfin): translate playback sessions and events"
```

---

### Task 5: Map Prairie Live TV and DVR into Jellyfin

**Files:**
- Create: `internal/jellyfin/livetv.go`
- Create: `internal/jellyfin/guide.go`
- Create: `internal/jellyfin/dvr.go`
- Create: `internal/jellyfin/livetv_test.go`
- Modify (Vondel): `internal/compatapi/livetv.go`
- Create (Vondel): `internal/compatapi/livetv_test.go`

**Interfaces:**
- Consumes subject-filtered Vondel/Prairie tuner discovery, channels, guide, availability, stream grants, DVR rules, recording status, and recording catalog.
- Produces Jellyfin `/LiveTv/**` channels/programs/guide/timers/series-timers/recordings/playback operations.

- [ ] **Step 1: Add failing deterministic Live TV cases**

Use fake tuner/guide/recording backends. Test channel visibility, adult-channel policy, overlapping tuner allocation, unavailable tuner, guide windows/time zones, start/stop recording, series rules, ownership, recording playback, cancellation, tuner failure, and cross-org/profile denial.

- [ ] **Step 2: Prove RED and extend only the narrow private API**

Run server and application focused tests. Add missing typed Live TV contract fields only when a named Jellyfin fixture requires them; never expose raw tuner credentials, backend URLs, filesystem recording paths, or other organizations' capacity.

- [ ] **Step 3: Implement Jellyfin translations and verify**

```bash
GOWORK=off go test ./internal/compatapi -run 'LiveTV' -count=1
GOWORK=off go test ./internal/jellyfin -run 'LiveTV|Guide|DVR|Recording' -count=1
GOWORK=off go test -race ./internal/jellyfin -run 'LiveTV|DVR' -count=1
```

- [ ] **Step 4: Commit in each repository**

Server: `git commit -m "feat(compat): expose scoped Live TV operations"`.

Application: `git commit -m "feat(jellyfin): translate Live TV and DVR"`.

---

### Task 6: Jellyfin Web, Same-Origin Routing, and LAN Discovery

**Files (application):**
- Create: `internal/web/static.go`
- Create: `internal/web/static_test.go`
- Create: `web/NOTICE`
- Create: `scripts/fetch-jellyfin-web.sh`
- Create: `scripts/verify-jellyfin-web.sh`

**Files (Vondel):**
- Modify: `internal/compatgateway/routes.go`
- Modify: `internal/compatgateway/proxy_test.go`
- Modify: `internal/compatgateway/discovery.go`
- Modify: `internal/compatgateway/discovery_test.go`

**Interfaces:**
- Produces Jellyfin Web at canonical `/web` and the audited protocol route table.
- Produces optional UDP discovery advertising canonical Vondel HTTPS URL plus stable compatibility server ID only while the application is healthy/enabled/compatible.

- [ ] **Step 1: Pin and verify Jellyfin Web assets**

Fetch from an exact reviewed upstream release/tag, verify recorded checksums/license/NOTICE, patch only endpoint/bootstrap configuration needed for same-origin operation, and scan bundles for upstream default servers, source maps with secrets, or unreviewed network origins.

- [ ] **Step 2: Write failing route ownership tests**

Enumerate every Jellyfin-owned root, including `/web`, `/System`, `/Users`, `/Items`, `/Sessions`, `/LiveTv`, images, streams, and WebSocket endpoints. Assert zero overlap with `/api/v1`, `/api/v2`, `/audiobookshelf`, native `/`, and unknown routes.

- [ ] **Step 3: Write failing discovery tests and implement**

Test disabled/unhealthy/incompatible silence, canonical URL/stable ID, selected interfaces, disabled configuration, rate limiting/amplification bounds, malformed datagrams, and absence of users/profiles/orgs/internal topology.

- [ ] **Step 4: Verify browser, gateway, and UDP behavior**

```bash
GOWORK=off go test ./internal/web ./internal/jellyfin -run 'Web|Static' -count=1
GOWORK=off go test ./internal/compatgateway -run 'JellyfinRoutes|Discovery' -count=1
bash scripts/verify-jellyfin-web.sh
```

- [ ] **Step 5: Commit in each repository**

Application: `git commit -m "feat(jellyfin): serve pinned Jellyfin Web"`.

Server: `git commit -m "feat(compat): add Jellyfin routes and discovery"`.

---

### Task 7: Dual-Run Parity, Private Release, and Cutover

**Files (Vondel):**
- Create: `internal/acceptance/jellyfin_external_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docker-compose.jellyfin.yml`

**Files (application):**
- Create: `Dockerfile`
- Create: `.github/workflows/release.yml`
- Create: `scripts/verify-private-release.sh`
- Create: `scripts/verify-private-release_test.sh`
- Create: `docs/client-configuration.md`
- Create: `docs/operations.md`

**Interfaces:**
- Consumes an exact private image digest and all shared protocol fixtures.
- Produces the external Jellyfin route cutover and required CI acceptance gate.

- [ ] **Step 1: Run complete embedded-versus-external parity**

Use clean Vondel state and deterministic fixtures for identity, catalog, every mapped media type, playback/transcode/subtitles, state, sessions/events, adult isolation, Live TV/DVR/recordings, Jellyfin Web, discovery, restart, dependency failure, slow clients, disabled/revoked/incompatible applications, and native health.

- [ ] **Step 2: Switch the fixed route table only after parity**

Run: `SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -tags=integration ./internal/acceptance -run TestExternalJellyfinParity -count=1 -v`

Require a fresh client login. Do not copy embedded access tokens or passwords. Stop/remove the application and volume, prove native/canonical state remains, reinstall, and prove canonical state reappears after login.

- [ ] **Step 3: Build and verify the private image**

Require private visibility, annotated stable exact-main tag, linux/amd64 and linux/arm64 image, immutable digest, SBOM/provenance, non-root/read-only compatibility, no public registry action, API-range readiness, and layer/history/log scans for credentials/database/media paths.

- [ ] **Step 4: Document clients and operations**

Document canonical root URL, LAN discovery/manual entry, direct profile and legacy login, trusted-device tiles/PINs, Web `/web`, Live TV expectations, enable/enroll/update/pin/rollback/disable/revoke/uninstall, state volume, optional separate PostgreSQL, and remote mTLS.

- [ ] **Step 5: Run final gates and commit**

```bash
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1
GOWORK=off go vet ./...
bash scripts/verify-private-release_test.sh
git diff --check
```

Commit application release preparation and server cutover separately. Tag/release only after independent review and exact-HEAD CI; record tag, peeled commit, image digest, CI run, and private visibility in the Vondel inventory.
