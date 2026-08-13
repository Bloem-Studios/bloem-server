# Vondel Prairie Live TV and DVR Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Selectively port Prairie Server's complete Live TV subsystem into Vondel Server and expose versioned contracts for original Vondel clients on every target.

**Architecture:** The exact Prairie Live TV slice at commit `095ecd22fbea3384a905eb9049386015db3ff4d8` is inventoried and adapted unit-by-unit to Vondel's current stores, routes, policy, playback and web systems. HDHomeRun hardware and Dispatcharr's HDHomeRun interface feed one normalized tuner model; Vondel owns guide, live sessions, timeshift and DVR. Native clients consume invented Vondel contracts and never use Prairie/Silo client code.

**Tech Stack:** Go 1.26, PostgreSQL, Redis, UDP HDHomeRun discovery, HTTP/HLS/MPEG-TS, FFmpeg, React/TypeScript, Docker Compose, JSON Schema/OpenAPI.

## Global Constraints

- Source baseline is exactly `Prairie-Server/prairie-server@095ecd22fbea3384a905eb9049386015db3ff4d8` under AGPL-3.0.
- Port only the Live TV slice and minimum Vondel integration points; never merge Prairie's unrelated 412 commits.
- Preserve Prairie attribution, imported blob hashes and per-file imported/adapted/omitted classification.
- Support native HDHomeRun and Dispatcharr HDHomeRun emulation; do not store direct M3U/Xtream provider credentials.
- All outbound tuner/guide/artwork/stream requests use one SSRF-safe client with redirect and DNS-rebinding checks.
- Guide publication, recording finalization and session leases are transactional/fenced; late work cannot overwrite current state.
- Live TV is capability-gated; clients hide it on official Silo servers that do not advertise it.
- No Prairie or Silo native-client code, assets, layouts or tests enter `vondel-apple` or `vondel-android`.
- Existing non-Live-TV Vondel behavior and public defaults remain unchanged.

---

### Task 1: Pin, Inventory and Attribute the Prairie Slice

**Files:**
- Create: `docs/livetv/prairie-source-manifest.tsv`, `docs/livetv/port-classification.tsv`
- Modify: `NOTICE`, `LICENSES.md`
- Test: `internal/livetv/provenance_test.go`

**Interfaces:**
- Produces an immutable path/blob/mode manifest and classification consumed by every port task.

- [ ] Write a failing test that fetches the pinned Prairie tree metadata and requires every classified path to have the exact source blob SHA, one classification (`imported`, `adapted`, `omitted`, `vondel-created`) and a nonempty rationale for omissions/adaptations.
- [ ] Run `GOWORK=off go test ./internal/livetv -run TestPrairieSourceManifest -count=1` and record RED because the manifest is absent.
- [ ] Build the manifest from Prairie paths matching `internal/livetv/**`, Live TV handlers/tests, guide task, migrations, web Live TV paths, Compose override and discovery documentation; separately list minimum router/config/playback integration hunks.
- [ ] Add Prairie name, URL, exact commit, AGPL basis and material adaptation statement to `NOTICE`; do not alter the existing license.
- [ ] Configure an audit-only `prairie-livetv` remote with fetch URL and `DISABLED` push URL; tests must reject any enabled push target.
- [ ] Commit/push provenance before importing production code; require docs/diff/provenance tests green.

### Task 2: Port Migrations, Types and PostgreSQL Store

**Files:**
- Create/adapt: `migrations/sql/*_livetv_*.sql`, `internal/livetv/types.go`, `internal/livetv/store.go`, store tests
- Modify: `migrations/embed.go`, migration ordering tests

**Interfaces:**
- Produces `livetv.Store` operations for tuners, channels, guide snapshots, sessions, rules, schedules, conflicts and recording executions.

- [ ] Add RED migration/store tests for a fresh database and upgrade from current Vondel HEAD, including unique tuner identity, encrypted guide secret envelope, fenced session heartbeat, profile-owned DVR rules and atomic last-good guide swap.

```go
func TestSessionHeartbeatRejectsStaleFence(t *testing.T) {
    first := createSession(t, "tuner-1")
    replacement := reclaimSession(t, first.ID)
    require.ErrorIs(t, store.Heartbeat(ctx, first.ID, first.Fence), livetv.ErrStaleLease)
    require.NoError(t, store.Heartbeat(ctx, replacement.ID, replacement.Fence))
}
```

- [ ] Run the focused package tests and record RED before creating migrations.
- [ ] Adapt Prairie migrations into Vondel's sequence without renumbering existing files; add forward-only preservation rules for recordings and last-good guide data.
- [ ] Port/adapt types and store queries to Vondel database conventions, secret envelopes and profile identity.
- [ ] Run migration up tests, store tests, race, vet and `git diff --check`; commit/push.

### Task 3: Port Secure Fetch, HDHomeRun and Dispatcharr Discovery

**Files:**
- Create/adapt: `internal/livetv/fetch_url.go`, `http_client.go`, `hdhomerun/{packet,lan_discover,probe,client}.go`
- Test: corresponding `*_test.go`, `internal/livetv/security_fetch_test.go`
- Create: `docker-compose.livetv.yml`, `docs/livetv-tuner-discovery.md`

**Interfaces:**
- Produces `DiscoveryService.DiscoverLAN(ctx)`, `DiscoveryService.ProbeURL(ctx, rawURL)` and a guarded `HTTPClient` used by all later Live TV fetches.

- [ ] Add RED tests for valid HDHR/Dispatcharr candidates, malformed UDP packets, duplicates, userinfo, redirect-to-metadata, DNS rebinding, oversized/decompression responses, timeout and sanitized errors.
- [ ] Implement one URL policy and dialer that validate scheme, hostname, resolved IP, redirect target and the actual dialed address; allow LAN ranges only under explicit Live TV policy.
- [ ] Port packet/discovery/probe behavior from the pinned Prairie files, adapting logging/config only; clients submit a URL, never a device ID.
- [ ] Add Linux host-network Compose override and Docker Desktop/manual-probe guidance matching the approved design.
- [ ] Run focused tests/race/vet plus a deterministic fake UDP+HTTP tuner integration; commit/push.

### Task 4: Port Lineup, Guide Providers, Matching and Artwork

**Files:**
- Create/adapt: `internal/livetv/{service,guide_match,xml_sync_match,artwork_cache,artwork_index_pg}.go`
- Create/adapt: `internal/livetv/{schedulesdirect,gracenote}/**`, `internal/taskmanager/tasks/sync_livetv_guide.go`
- Test: provider/matching/transaction/security tests

**Interfaces:**
- Produces `GuideService.Sync(ctx, sourceID) (GuideSnapshot, error)` and stable channel/program/artwork models.

- [ ] Add RED tests with invented stations/programs for lineup normalization, ambiguous matching, admin overrides, encrypted credentials, pagination/rate limits, artwork bounds and failed-refresh last-good retention.
- [ ] Port provider clients and matching logic, routing every request through Task 3's guarded client and every secret through Vondel's encrypted store.
- [ ] Stage, validate and atomically publish guide snapshots; stale status must be queryable without deleting the prior snapshot.
- [ ] Register a bounded/idempotent guide-sync task with observable progress and cancellation.
- [ ] Run provider/matching/store/task tests, race/vet and fake-provider integration; commit/push.

### Task 5: Port Tuner Leases, Live Routes and Timeshift

**Files:**
- Create/adapt: `internal/livetv/{stream_plan,hls_bridge,transcode_settings,session_reclaim,timeshift}.go`
- Modify: Vondel playback/transcode dependency wiring
- Test: stream/lease/HLS/transcode/timeshift tests

**Interfaces:**
- Produces `SessionService.Start`, `Heartbeat`, `Stop`, `Seek` and typed direct/bridge/transcode playback plans.

- [ ] Add RED tests for deterministic tuner choice, recording priority, stale fencing, heartbeat reclaim, HLS origin confinement, signed-URL redaction, direct/bridge/transcode selection and bounded timeshift pause/seek/live-edge behavior.
- [ ] Port/adapt Prairie stream planning, HLS bridge, codec configuration and session reclamation to Vondel playback infrastructure.
- [ ] Implement rolling timeshift storage with per-session byte/time limits, global disk budget, atomic segment index and expiry cleanup.
- [ ] Ensure playlist redirects and segments use Task 3's guarded origins and never expose tuner/provider credentials.
- [ ] Run focused/race/vet plus fake-tuner MPEG-TS/HLS and FFmpeg integration; commit/push.

### Task 6: Port DVR Rules, Scheduling, Conflicts and Recording

**Files:**
- Create/adapt: `internal/livetv/{recorder,scheduler,conflicts,recording_library}.go`
- Modify: library ingestion wiring and task startup/recovery
- Test: DVR/restart/disk/path/profile tests

**Interfaces:**
- Produces one-off/series rule CRUD, expanded schedules/conflicts and fenced recording execution/finalization.

- [ ] Add RED tests for one-off/series rules, padding, overlapping programs, deterministic conflicts, cancellation, recording-over-live priority, restart recovery, stale workers, disk pressure, traversal/symlink rejection and cross-profile access.
- [ ] Port/adapt Prairie recorder and add deterministic rule expansion/conflict arbitration against Task 4 guide snapshots.
- [ ] Write recordings to validated staging paths, verify/finalize atomically and ingest only successful files into a dedicated recordings library.
- [ ] Reconcile active/staging executions at startup without duplication or overwriting complete files.
- [ ] Run DVR/store/race/vet and process-restart integration; commit/push.

### Task 7: Port REST, Jellyfin Compatibility and Capabilities

**Files:**
- Create/adapt: `internal/api/handlers/livetv*.go`, `internal/jellycompat/handlers_livetv.go`
- Modify: `internal/api/router.go`, capability registration, dependency wiring
- Test: handler/auth/capability/Jellyfin tests

**Interfaces:**
- Produces `/api/v1/livetv/**`, Prairie-compatible admin discovery/add behavior and capability-gated Jellyfin Live TV endpoints.

- [ ] Add RED HTTP tests for admin tuner operations; channel/guide windows; live start/heartbeat/stop/timeshift; DVR rules/conflicts/recordings; typed redacted errors; and absent capability when disabled.
- [ ] Port/adapt handlers with Vondel auth/profile/policy middleware and strict request bounds.
- [ ] Register a versioned Live TV capability with supported subfeatures; official servers without it must remain valid.
- [ ] Port only Prairie Jellyfin endpoints supported by the new store/service and keep feature-negative behavior explicit.
- [ ] Run API/Jellyfin/system tests, race/vet and generated route inventory; commit/push.

### Task 8: Port Web Administration, Guide and Player

**Files:**
- Create/adapt: `web/src/components/livetv/**`, `web/src/pages/livetv/**`, `web/src/pages/AdminLiveTV.tsx`, `web/src/pages/LiveTV.tsx`, `web/src/hooks/queries/useLiveTV.ts`, `web/src/lib/liveTV*.ts`
- Modify: routes/navigation/settings only where capability-gated
- Test: Vitest/React Testing Library/Playwright Live TV tests

**Interfaces:**
- Produces Vondel web tuner/guide/DVR administration and user guide/player/recordings surfaces.

- [ ] Add RED tests for capability-hidden navigation, tuner discovery/probe/add, stale guide, guide keyboard/accessibility, tune/timeshift, record/cancel/series/conflict and recordings playback.
- [ ] Port Prairie web behavior while adapting visual components and API hooks to current Vondel conventions; preserve Prairie attribution for imported/adapted files.
- [ ] Ensure non-admin users never receive tuner/provider endpoints/secrets and all controls expose accessible names/focus order.
- [ ] Run unit/UI/browser tests, typecheck, lint and production build; commit/push.

### Task 9: Publish Live TV Contracts and Invented Fixtures

**Files:**
- Modify contracts: `schema/openapi.yaml`
- Create contracts: `schema/livetv/*.schema.json`, `fixtures/livetv/*.json`, `internal/conformance/livetv_test.go`
- Modify server: `internal/clientcontract/conformance_test.go`

**Interfaces:**
- Produces versioned client schemas for channels, guide, plans, sessions, timeshift, DVR rules/schedules/conflicts/recordings and typed errors.

- [ ] Add RED schema/conformance tests using invented tuner `antenna-7`, channels `KVDL-7`/`KVDL-12`, programs and recording IDs; include capability-negative official Silo baseline.
- [ ] Derive schemas from Task 7 Vondel handlers, not Prairie/Silo client models; specify bounds, optional fields and unknown-field behavior.
- [ ] Add deterministic fake-tuner conformance cases covering tune, heartbeat, timeshift, record/cancel, series conflict and completed recording.
- [ ] Run contracts tests, disposable Vondel conformance, official capability-negative baseline and originality audits; commit/push contracts/server.

### Task 10: Full Provenance, Security and End-to-End Acceptance

**Files:**
- Modify: `docs/livetv/port-classification.tsv`, `docs/non-goals.md`, `docs/client-release-inventory.md`
- Create: `internal/acceptance/livetv_test.go`, `docs/livetv/operations.md`

**Interfaces:**
- Produces final auditable server/web/contracts evidence and a stable handoff to clean-room clients.

- [ ] Run provenance verification against exact Prairie blobs and require every production/test/web/migration change classified with no unrelated Prairie files.
- [ ] Run fresh/upgrade migrations and end-to-end fake HDHR/Dispatcharr guide→tune→timeshift→record→library flow with cleanup proof.
- [ ] Run SSRF/rebinding/redirect/secret/session/lease/disk/path/profile security suites and scan current/reachable history plus artifacts for credentials/provider URLs.
- [ ] Run full Go race/vet/build, web tests/typecheck/build, Docker configuration validation and contract conformance.
- [ ] Replace the old blanket Live TV non-goal with an accurate status/architecture boundary; document host networking, Dispatcharr and recovery operations.
- [ ] Update inventory with Prairie commit, Vondel commits, CI/test evidence and explicit no-native-client-code statement; commit/push and require exact-head CI green.
