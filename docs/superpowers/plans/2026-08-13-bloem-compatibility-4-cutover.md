# Bloem Compatibility Cutover and Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove embedded Jellyfin and Audiobookshelf implementations from Bloem after both private external applications are proven, retain rollback data safely for one release, and complete operational/documentation gates.

**Architecture:** Fixed Bloem gateway routes become the only compatibility entry points. Embedded listeners, packages, compatibility-specific storage access, and installers are removed from the binary; canonical Bloem media/identity state remains untouched, while old protocol-only tables are made inert for one release and dropped later.

**Tech Stack:** Go 1.26, PostgreSQL/Goose, React/TypeScript, Docker Compose, GitHub Actions, binary/source/security scans.

**Spec:** `docs/superpowers/specs/2026-08-12-bloem-compatibility-sidecars-design.md`

## Global Constraints

- Begin only after exact private Audiobookshelf and Jellyfin release images pass their complete shared contract and cutover suites.
- Never delete canonical progress, bookmarks, favorites, collections, playlists, downloads, sessions, Live TV rules, or recordings.
- Do not migrate live compatibility tokens; require one fresh login.
- Old protocol-only state is read/write-inert for one rollback release and removed only in a later reviewed migration.
- Native Bloem must start and remain healthy with zero, one, or both applications absent.
- Repositories and artifacts remain private indefinitely; no workflow may change visibility or publish publicly.
- Use RED/GREEN TDD, task commits, independent reviews, and production rollback evidence.

---

### Task 1: Classify Embedded Code and Database State

**Files:**
- Create: `docs/architecture/compatibility-removal-inventory.md`
- Create: `internal/acceptance/compat_removal_inventory_test.go`
- Modify: `docs/plugin-fork-inventory.md`

**Interfaces:**
- Produces an exact ownership ledger for every embedded source path, route, listener, config key, migration/table, scheduled job, background worker, asset, test, and canonical-state dependency.
- Classifies each item as `remove_now`, `retain_canonical`, `retain_inert_one_release`, or `drop_later`.

- [ ] **Step 1: Generate and review an exact inventory**

Inventory at minimum `internal/jellycompat`, `internal/audiobooks/abs`, `internal/audiobooks/abssocket`, ABS/Jellyfin store files, cmd/silo wiring, API routes, config/environment keys, Compose settings, Web controls, migrations, background jobs, protocol assets, and compatibility contract fixtures.

- [ ] **Step 2: Write failing structural assertions**

Tests require every inventory path to exist at baseline, every database table to have a classification, and every `retain_canonical` table to be referenced by native Bloem code independent of compatibility packages. Fail on unclassified `jelly`, `emby`, `audiobookshelf`, or `abs_` production references.

- [ ] **Step 3: Verify and commit**

```bash
GOWORK=off go test ./internal/acceptance -run TestCompatibilityRemovalInventory -count=1
git add docs/architecture/compatibility-removal-inventory.md docs/plugin-fork-inventory.md internal/acceptance/compat_removal_inventory_test.go
git commit -m "docs(compat): inventory embedded compatibility ownership"
```

---

### Task 2: Remove Embedded Audiobookshelf Runtime

**Files:**
- Delete: `internal/audiobooks/abs/**`
- Delete: `internal/audiobooks/abssocket/**`
- Modify/Delete: compatibility-only `internal/audiobooks/abs_*_store.go` files according to Task 1 classification
- Modify: `cmd/silo/main.go`
- Modify: `internal/config/config.go`
- Modify: `internal/api/router.go`
- Modify: `docker-compose.yml`
- Modify: `web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx`
- Modify: `web/src/pages/admin-settings/CompatibilityProxiesSettings.test.tsx`
- Modify: `internal/acceptance/audiobookshelf_external_test.go`

**Interfaces:**
- Preserves `/audiobookshelf/**` exclusively through `compatgateway`.
- Removes embedded ABS listener/runtime and direct protocol-state access from the Bloem binary.

- [ ] **Step 1: Write failing structural/binary tests**

Assert `go list -deps ./cmd/silo` contains no embedded ABS package, no secondary ABS listener can bind, removed config keys are rejected or reported as deprecated, and the binary string scan contains no embedded ABS route/bootstrap marker except gateway-unavailable responses.

- [ ] **Step 2: Remove wiring and implementation**

Delete only paths classified `remove_now`. Keep native audiobook services and all canonical state. Keep shared contract fixtures in Bloem for external regression testing. Make any old protocol-state tables inaccessible to production read/write code.

- [ ] **Step 3: Verify external, native, and absent-app behavior**

```bash
GOWORK=off go test ./internal/acceptance -run 'ExternalAudiobookshelf|CompatibilityRemoval' -count=1
GOWORK=off go test ./internal/audiobooks/... ./internal/compatgateway -count=1
GOWORK=off go build ./cmd/silo
```

With the app absent, only `/audiobookshelf/**` is unavailable; native audiobook browse/play/progress remains green.

- [ ] **Step 4: Commit**

```bash
git add -A internal/audiobooks cmd/silo internal/config internal/api docker-compose.yml web/src internal/acceptance
git commit -m "refactor(compat): remove embedded Audiobookshelf runtime"
```

---

### Task 3: Remove Embedded Jellyfin Runtime

**Files:**
- Delete: `internal/jellycompat/**`
- Modify/Delete: Jellyfin-only stores/assets according to Task 1 classification
- Modify: `cmd/silo/main.go`
- Modify: `internal/config/config.go`
- Modify: `internal/api/router.go`
- Modify: `docker-compose.yml`
- Modify: `web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx`
- Modify: `web/src/pages/admin-settings/CompatibilityProxiesSettings.test.tsx`
- Modify: `internal/acceptance/jellyfin_external_test.go`

**Interfaces:**
- Preserves the exact audited Jellyfin route table through `compatgateway`.
- Preserves Bloem playback, native sessions/events, Prairie Live TV/DVR, and discovery relay.
- Removes embedded Jellyfin listener/runtime and protocol-state access from the Bloem binary.

- [ ] **Step 1: Write failing dependency/binary tests**

Assert `go list -deps ./cmd/silo` contains no `internal/jellycompat`, no embedded Jellyfin listener binds, no implementation marker remains in binary strings, and Live TV packages remain dependencies through native/private API paths rather than Jellyfin implementation paths.

- [ ] **Step 2: Remove embedded wiring and implementation**

Delete only `remove_now` items. Retain shared contract fixtures, gateway route definitions, LAN discovery, compatibility administration, and all canonical media/playback/Live TV state.

- [ ] **Step 3: Verify external/native/absent-app behavior**

```bash
GOWORK=off go test ./internal/acceptance -run 'ExternalJellyfin|CompatibilityRemoval' -count=1
GOWORK=off go test ./internal/compatgateway ./internal/livetv/... ./internal/playback/... -count=1
GOWORK=off go build ./cmd/silo
```

With the app absent, Jellyfin paths and discovery become unavailable/silent while native API/Web, playback, and Live TV remain green.

- [ ] **Step 4: Commit**

```bash
git add -A internal/jellycompat cmd/silo internal/config internal/api docker-compose.yml web/src internal/acceptance
git commit -m "refactor(compat): remove embedded Jellyfin runtime"
```

---

### Task 4: Make Legacy Protocol State Inert for One Release

**Files:**
- Create: `migrations/sql/20260813210000_compat_protocol_state_inert.sql`
- Create: `internal/database/compat_protocol_state_inert_test.go`
- Create: `docs/operations/compatibility-rollback-window.md`
- Modify: `internal/acceptance/compat_removal_inventory_test.go`

**Interfaces:**
- Produces an explicit inert-state marker/version and prevents new production writes to compatibility-only tables.
- Preserves table contents for one release solely for rollback.

- [ ] **Step 1: Write failing up/down/up migration tests**

Seed representative old protocol tokens/preferences/socket state and canonical audiobook/Jellyfin-mapped progress/collections/playlists/Live TV state. Up must preserve both while marking protocol tables inert; down restores only old application write compatibility; second up is deterministic.

- [ ] **Step 2: Add write-path absence tests**

Run Bloem with both external applications, exercise full contracts, and prove row timestamps/counts in inert tables do not change while canonical tables do. A source/query scan must find no production repository referencing inert tables.

- [ ] **Step 3: Implement and verify**

```bash
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/database -run 'CompatProtocolStateInert' -count=1
GOWORK=off go test ./internal/acceptance -run 'CompatibilityRemoval|ExternalAudiobookshelf|ExternalJellyfin' -count=1
```

- [ ] **Step 4: Document rollback and commit**

Document exact application image digests, Bloem rollback version, one-release deadline, required fresh login, no dual writes, and canonical-state invariants.

```bash
git add migrations/sql/20260813210000_compat_protocol_state_inert.sql internal/database/compat_protocol_state_inert_test.go internal/acceptance/compat_removal_inventory_test.go docs/operations/compatibility-rollback-window.md
git commit -m "feat(compat): retain inert rollback protocol state"
```

---

### Task 5: Whole-System Release and Removal Acceptance

**Files:**
- Create: `internal/acceptance/compat_external_release_test.go`
- Create: `scripts/verify-external-compatibility.sh`
- Create: `scripts/verify-external-compatibility_test.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/superpowers/specs/2026-08-12-bloem-compatibility-sidecars-design.md`

**Interfaces:**
- Consumes exact private image digests for both applications.
- Produces the required release gate for zero/one/both-app topologies and uninstall safety.

- [ ] **Step 1: Write the complete failing matrix**

Run native-only, ABS-only, Jellyfin-only, and both. Cover direct/legacy/PIN identity, cross-tenant/revision revocation, adult absence, all media/state/playback/events, ABS Socket.IO/offline, Jellyfin Web/discovery/Live TV/DVR, restart/gap/slow client/dependency failure, credential rotation, disabled/revoked/incompatible state, uninstall/reinstall, and same-origin redirects.

- [ ] **Step 2: Add structural security gates**

Scan source, compiled server, images, layers, Compose, workflows, logs, redirects, and runtime mounts/environment. Fail if embedded packages, Bloem DB/Redis/media/Docker/signing/provider/tuner access, public publication, credentials, signed URLs, or unowned public routes appear.

- [ ] **Step 3: Run release verification**

```bash
bash scripts/verify-external-compatibility_test.sh
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -tags=integration ./internal/acceptance -run TestExternalCompatibilityRelease -count=1 -v
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./internal/compatapp ./internal/compatapi ./internal/compatgateway -count=1
GOWORK=off go vet ./...
GOWORK=off go build ./cmd/silo
git diff --check
```

- [ ] **Step 4: Mark extraction complete and commit**

Record exact Bloem commit/CI, application tag/peeled commit/image digest/CI/private visibility, parity reports, and rollback release deadline. Mark the spec implemented only after every required gate passes.

```bash
git add .github/workflows/ci.yml internal/acceptance/compat_external_release_test.go scripts/verify-external-compatibility* docs/superpowers/specs/2026-08-12-bloem-compatibility-sidecars-design.md
git commit -m "test(compat): lock external compatibility release"
```

---

### Task 6: Operator and User Documentation Set

**Files:**
- Create: `docs/operations/compatibility-applications.md`
- Create: `docs/operations/compatibility-remote-placement.md`
- Create: `docs/guides/profile-login-and-switching.md`
- Create: `docs/guides/jellyfin-clients.md`
- Create: `docs/guides/audiobookshelf-clients.md`
- Create: `docs/troubleshooting/compatibility-applications.md`
- Modify: `README.md`
- Modify: `docs/README.md`

**Interfaces:**
- Produces the complete documentation deliverables named by the design.
- Consumes only commands, URLs, errors, and behaviors proven by Tasks 1–5.

- [ ] **Step 1: Write operator lifecycle guides**

Cover prerequisites, private image authentication, commented Compose activation, enrollment secret, enablement, health/API range, version/digest, update/pin/rollback, disable/revoke/uninstall, volume retention/removal, backups, and zero-Docker-socket administration.

- [ ] **Step 2: Write identity and client guides**

Explain accounts versus profiles, optional direct profile email/password, globally unique email, shared-only profiles, legacy Silo-compatible account login, trusted-device tiles, PIN switching, revocation, Jellyfin canonical root plus discovery/manual entry/Web, and ABS `/audiobookshelf` URL.

- [ ] **Step 3: Write remote and troubleshooting guides**

Document mTLS identities, allowlisted firewall paths, credential rotation, separate companion database ownership, trace correlation/redaction, and actionable matrices for absent, unhealthy, disabled, revoked, incompatible, event-gap, discovery, Socket.IO, playback, and Live TV failures.

- [ ] **Step 4: Verify documentation and commit**

```bash
make verify-local-paths
rg -n 'localhost:[0-9]+|http://bloem-|BLOEM_DATABASE_URL|docker.sock' docs/guides docs/operations docs/troubleshooting
git diff --check
git add README.md docs
git commit -m "docs(compat): publish external application guides"
```

The scan may contain loopback diagnostics only in their explicitly named section; canonical client examples must use `https://bloem.example` and `/audiobookshelf`.

---

### Task 7: Drop Inert Protocol Tables in the Following Release

**Files:**
- Create: `migrations/sql/20260814200000_drop_legacy_compat_protocol_state.sql`
- Create: `internal/database/drop_legacy_compat_protocol_state_test.go`
- Modify: `docs/architecture/compatibility-removal-inventory.md`
- Modify: `docs/operations/compatibility-rollback-window.md`

**Interfaces:**
- Consumes proof that the one-release rollback window has elapsed and no supported rollback binary requires the tables.
- Removes only tables classified `drop_later`; canonical tables remain byte-for-byte outside migration metadata.

- [ ] **Step 1: Rebase and confirm the reserved migration timestamp is still available**

After rebasing onto current `origin/main`, require `20260814200000_drop_legacy_compat_protocol_state.sql` to sort after every existing migration; stop and amend this plan in a reviewed docs commit if that timestamp has been consumed. The reviewed drop set is exactly `jellycompat_sessions`, `jellycompat_playback_sessions`, `jellycompat_displayprefs`, `abs_sessions`, `abs_playback_sessions`, `abs_bookmarks`, and `abs_rss_feeds`. Task 1 must either confirm this set or amend the design and this plan before code removal; prefix-based inference is forbidden.

- [ ] **Step 2: Write failing data-preservation migration tests**

Seed every drop-later table plus canonical progress/bookmarks/favorites/collections/playlists/downloads/Live TV rules/recordings. Up drops only the former; down recreates schema without claiming to restore expired protocol data; canonical rows and constraints remain.

- [ ] **Step 3: Verify references and migration**

```bash
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/database -run 'DropLegacyCompatProtocolState' -count=1
rg -n '\b(jellycompat_sessions|jellycompat_playback_sessions|jellycompat_displayprefs|abs_sessions|abs_playback_sessions|abs_bookmarks|abs_rss_feeds)\b' --glob '*.go' --glob '*.sql' .
git diff --check
```

The reference scan may match only the old creation migrations, this drop migration, its test, and historical documentation.

- [ ] **Step 4: Commit after separate destructive-migration approval**

```bash
git add migrations/sql internal/database/drop_legacy_compat_protocol_state_test.go docs/architecture/compatibility-removal-inventory.md docs/operations/compatibility-rollback-window.md
git commit -m "chore(compat): remove expired protocol rollback state"
```
