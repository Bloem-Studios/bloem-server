# Vondel Compatibility Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the identity, trust, private API, gateway, administration, and deployment foundation required by removable compatibility applications.

**Architecture:** Vondel remains the sole authority and exposes a capability-scoped `/api/internal/compat/v1` contract. A fixed-path gateway on Vondel's existing listener forwards only reviewed protocol routes to enrolled applications; enrollment and application state are durable, revocable, and independent per instance.

**Tech Stack:** Go 1.26, PostgreSQL/pgx, chi, OpenAPI 3.1, React/TypeScript, Docker Compose, mTLS for remote placement.

**Spec:** `docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md`

## Global Constraints

- Repositories and artifacts remain private indefinitely.
- No companion receives Vondel database, Redis, filesystem, Docker socket, signing-key, provider, or tuner access.
- Vondel reauthorizes every subject operation; companion cache state is never authoritative.
- Profile email and password are optional together, never separately; login email is globally unique across accounts and profiles.
- Existing account login and per-profile PIN switching remain unchanged.
- Gateway ownership is compile-time/static configuration, not runtime route registration.
- Credentials, cookies, signed URLs, and enrollment secrets never appear in logs, reports, URLs, or Compose environment values.
- Use RED/GREEN TDD and a separate commit for every task.

---

### Task 1: Optional Direct Profile Credentials

**Files:**
- Create: `migrations/sql/20260813200000_profile_login_credentials.sql`
- Create: `internal/auth/profile_credentials.go`
- Create: `internal/auth/profile_credentials_test.go`
- Modify: `internal/auth/service.go`
- Modify: `internal/userstore/types.go`
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/router.go`

**Interfaces:**
- Produces: `ProfileCredentialService.Set(ctx, accountID, profileID int64, email, password string) error`.
- Produces: `ProfileCredentialService.Clear(ctx, accountID, profileID int64) error`.
- Produces: `ProfileCredentialService.Authenticate(ctx context.Context, email, password string, device DeviceClaim) (SessionSubject, error)`.
- Preserves: legacy account authentication and profile PIN selection.

- [ ] **Step 1: Write failing migration and service tests**

Test an empty pair, a complete pair, rejection of partial pairs, case-insensitive collision with account/profile emails, bcrypt-only storage, direct login binding one profile, disabled/suspended subjects, password reset revocation, and concurrent duplicate email writes.

```go
func TestProfileCredentialsRejectPartialPair(t *testing.T) {
	service := newProfileCredentialService(t)
	err := service.Set(context.Background(), 7, 11, "reader@example.test", "")
	if !errors.Is(err, auth.ErrIncompleteProfileCredentials) { t.Fatal(err) }
}
```

- [ ] **Step 2: Prove RED**

Run: `GOWORK=off go test ./internal/auth ./internal/api/handlers -run 'ProfileCredential|DirectProfileLogin' -count=1`

Expected: FAIL because the migration and service do not exist.

- [ ] **Step 3: Implement the canonical email registry and direct login**

The migration creates one normalized-email registry row per account or direct-login profile, enforces exactly one owner column, and adds nullable profile password hash plus credential revision. `Set` performs the registry update and bcrypt hash replacement in one transaction; `Clear` removes both. Authentication returns a subject already bound to organization, account, profile, device, auth method, and current policy/security revisions.

- [ ] **Step 4: Verify legacy and direct flows**

Run:

```bash
GOWORK=off go test ./internal/auth ./internal/api/handlers -run 'ProfileCredential|DirectProfileLogin|Login|ProfilePIN' -count=1
GOWORK=off go test -race ./internal/auth -run 'ProfileCredential|DirectProfileLogin' -count=1
```

Expected: PASS; a raw password or hash scan outside tests returns no fixture credential.

- [ ] **Step 5: Commit**

```bash
git add migrations/sql/20260813200000_profile_login_credentials.sql internal/auth internal/userstore/types.go internal/api/handlers/auth.go internal/api/router.go
git commit -m "feat(auth): add optional direct profile login"
```

---

### Task 2: Freeze External Protocol and Subject Semantics

**Files:**
- Modify: `internal/compatcontract/types.go`
- Modify: `internal/compatcontract/baselines.go`
- Modify: `internal/compatcontract/runner.go`
- Modify: `internal/compatcontract/jellyfin_test.go`
- Create: `internal/compatcontract/audiobookshelf_test.go`
- Create: `internal/compatcontract/testdata/identity/*.json`

**Interfaces:**
- Produces: `compatcontract.Run(ctx context.Context, target Target, suite Suite) (Report, error)` covering direct-profile, legacy-account, device, PIN, revocation, adult-policy, and timing cases.
- Produces stable suite names `JellyfinBaseline()` and `AudiobookshelfBaseline()`.

- [ ] **Step 1: Add failing identity and policy cases**

```go
func TestUnknownDeviceNeverReceivesProfileDirectory(t *testing.T) {
	report := runCase(t, UnknownJellyfinDeviceProfileList())
	require.Equal(t, 0, report.JSONCount("$.Items"))
}
```

Add direct profile login, legacy remembered/default selection, trusted-device tiles, unprotected and PIN-protected switching, cross-org denial, all revision revocations, and adult absence from bodies/counts/artwork/events/playback. Compare missing-adult and random-missing timing distributions with a documented tolerance and at least 100 samples.

- [ ] **Step 2: Prove RED, implement fixtures, and verify**

Run: `GOWORK=off go test ./internal/compatcontract -count=1`

Expected RED: the new cases are absent. Add deterministic IDs, reserved-domain URLs, semantic JSON comparison, credential redaction, WebSocket messages, binary digests, and named protocol exceptions. Rerun normal and race tests; both must pass.

- [ ] **Step 3: Commit**

```bash
git add internal/compatcontract
git commit -m "test(compat): freeze identity and policy behavior"
```

---

### Task 3: Companion Enrollment and Revocable Service Trust

**Files:**
- Create: `migrations/sql/20260813200100_compat_applications.sql`
- Create: `internal/compatapp/types.go`
- Create: `internal/compatapp/store.go`
- Create: `internal/compatapp/service.go`
- Create: `internal/compatapp/service_test.go`
- Create: `internal/compatapp/store_test.go`

**Interfaces:**
- Produces: `Service.CreateEnrollment(ctx, kind Kind, requested []Capability) (EnrollmentSecret, error)`.
- Produces: `Service.Enroll(ctx, secret string, request EnrollmentRequest) (ServiceCredential, error)`.
- Produces: `Service.Authenticate(ctx, bearer string, peerTLS *tls.ConnectionState) (Identity, error)`.
- Produces: `Service.Rotate`, `Service.Revoke`, `Service.SetEnabled`, and `Service.Heartbeat`.

- [ ] **Step 1: Write failing PostgreSQL tests**

Cover a 15-minute one-use enrollment, hashed-at-rest secrets, immutable instance ID and kind, capability subset enforcement, 15-minute renewable access credentials, independent rotation/revocation, stale API range, mTLS identity binding, disabled application, audit rows, and concurrent enrollment replay.

- [ ] **Step 2: Prove RED**

Run: `SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/compatapp -count=1`

Expected: FAIL because the package and schema do not exist.

- [ ] **Step 3: Implement schema and service**

Persist application, enrollment-digest, credential-digest, granted-capability, API-range, version, image-digest, health, enablement, revocation, and last-contact state. Return every raw secret exactly once; store only digests. Lock enrollment rows before consuming them and fail closed on unknown capabilities or incompatible API ranges.

- [ ] **Step 4: Verify migration, race, and leak gates**

```bash
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/database ./internal/compatapp -run 'CompatApplication|Enrollment' -count=1
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -race ./internal/compatapp -count=1
rg -n 'vce_[A-Za-z0-9_-]{20,}|vcc_[A-Za-z0-9_-]{20,}' --glob '!**/*_test.go' .
```

- [ ] **Step 5: Commit**

```bash
git add migrations/sql/20260813200100_compat_applications.sql internal/compatapp
git commit -m "feat(compat): add companion enrollment and trust"
```

---

### Task 4: Private Compatibility API v1

**Files:**
- Create: `contracts/compat/v1/openapi.yaml`
- Create: `contracts/compat/v1/openapi_test.go`
- Create: `internal/compatapi/types.go`
- Create: `internal/compatapi/handler.go`
- Create: `internal/compatapi/identity.go`
- Create: `internal/compatapi/catalog.go`
- Create: `internal/compatapi/state.go`
- Create: `internal/compatapi/playback.go`
- Create: `internal/compatapi/livetv.go`
- Create: `internal/compatapi/events.go`
- Create: `internal/compatapi/handler_test.go`
- Create: `internal/compatapi/client/client.go`
- Modify: `internal/api/router.go`

**Interfaces:**
- Produces versioned routes below `/api/internal/compat/v1`.
- Produces generated-style `compatapi/client.Client` methods for enrollment/health, credential exchange/device/PIN/profile, catalog/search/detail/artwork, state/collections/playlists/downloads, playback, sessions, Live TV, and resumable events.
- Consumes `compatapp.Service.Authenticate` and existing narrow Vondel domain interfaces.

- [ ] **Step 1: Write the failing OpenAPI structural test**

```go
func TestOperationsDeclareCapabilityIdempotencyAndErrors(t *testing.T) {
	for _, op := range loadOperations(t) {
		require.NotEmpty(t, op.Extension("x-vondel-capability"))
		requireResponses(t, op, 401, 403, 409, 429, 503)
		if op.Mutates() { require.True(t, op.RequiresHeader("Idempotency-Key")) }
	}
}
```

- [ ] **Step 2: Prove RED and author the complete contract**

Run: `GOWORK=off go test ./contracts/compat/v1 ./internal/compatapi -count=1`

Expected: FAIL. Define subject tokens without caller-selected subject IDs, signed cursors, idempotency keys, trace IDs, short-lived audience-bound delivery grants, bounded event cursors, stable error envelopes, and every capability named in the design.

- [ ] **Step 3: Implement handlers as policy-enforcing adapters**

Each handler authenticates the application, validates its operation capability, resolves the token-bound subject, installs authoritative tenant facts, invokes existing Vondel services, and filters the response before encoding. Never expose database identifiers not already part of the public media model, filesystem paths, provider/tuner credentials, or signing material.

- [ ] **Step 4: Verify authorization and contract parity**

```bash
GOWORK=off go test ./contracts/compat/v1 ./internal/compatapi ./internal/compatapi/client -count=1
GOWORK=off go test -race ./internal/compatapi -count=1
GOWORK=off go vet ./internal/compatapi/... ./contracts/compat/v1/...
```

- [ ] **Step 5: Commit**

```bash
git add contracts/compat/v1 internal/compatapi internal/api/router.go
git commit -m "feat(compat): add private compatibility API v1"
```

---

### Task 5: Fixed-Path Edge Gateway and Administration

**Files:**
- Create: `internal/compatgateway/routes.go`
- Create: `internal/compatgateway/proxy.go`
- Create: `internal/compatgateway/proxy_test.go`
- Create: `internal/compatgateway/discovery.go`
- Create: `internal/compatgateway/discovery_test.go`
- Create: `internal/api/handlers/v2_admin_compatibility.go`
- Create: `internal/api/handlers/v2_admin_compatibility_test.go`
- Modify: `internal/api/router.go`
- Create: `web/src/pages/admin-platform/CompatibilityApplicationsPage.tsx`
- Create: `web/src/pages/admin-platform/CompatibilityApplicationsPage.test.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Produces static `compatgateway.RouteTable` with Jellyfin and `/audiobookshelf/**` ownership.
- Produces admin list/enroll/enable/disable/rotate/revoke endpoints and read-only version/digest/API-range/health/session data.
- Consumes only enrolled, enabled, healthy, API-compatible application endpoints.

- [ ] **Step 1: Write failing overlap, isolation, and UI tests**

Test native `/`, `/api/v1/**`, and `/api/v2/**` never proxy; unknown paths never fall through; hop-by-hop headers are stripped; body/deadline limits apply; application failure affects only owned paths; redirects remain same-origin; admin mutation is revision-guarded/audited; UI never invokes Docker.

- [ ] **Step 2: Prove RED and implement the gateway**

Run: `GOWORK=off go test ./internal/compatgateway ./internal/api/handlers -run 'Compat' -count=1`

Expected: FAIL. Implement explicit route entries, signed internal request identity, trace propagation, circuit breaking, protocol-specific unavailable responses, and no runtime route additions.

- [ ] **Step 3: Implement the administration page**

Display state, instance identity, version, resolved image digest, API range, last contact, active sessions, capabilities, canonical client URL, and exact enrollment/update/rollback/removal commands. Provide enable/disable/rotate/revoke actions; never mount a Docker socket or invoke Docker commands.

- [ ] **Step 4: Verify backend and frontend**

```bash
GOWORK=off go test ./internal/compatgateway ./internal/api/handlers ./internal/api -run 'Compat' -count=1
cd web && pnpm exec vitest run src/pages/admin-platform/CompatibilityApplicationsPage.test.tsx --reporter=dot && pnpm exec tsc -b
```

- [ ] **Step 5: Commit**

```bash
git add internal/compatgateway internal/api web/src
git commit -m "feat(compat): add fixed-path gateway administration"
```

---

### Task 6: Private Deployment Contracts

**Files:**
- Modify: `docker-compose.yml`
- Create: `docker-compose.audiobookshelf.yml`
- Create: `docker-compose.jellyfin.yml`
- Create: `docker-compose.compat-diagnostics.yml`
- Create: `scripts/verify-compat-compose.sh`
- Create: `scripts/verify-compat-compose_test.sh`
- Create: `docs/operations/compatibility-applications.md`

**Interfaces:**
- Produces private network aliases `vondel-audiobookshelf` and `vondel-jellyfin` with no host ports.
- Produces Docker-secret enrollment mount `/run/secrets/vondel_compat_enrollment`.
- Produces loopback-only diagnostic ports solely in the diagnostic override.

- [ ] **Step 1: Write failing structural tests**

Assert companions have no `ports`, Vondel DB/Redis environment variables, media/data mounts, Docker socket, privileged mode, shared signing/provider/tuner credentials, or public registry publication actions. Assert only named volumes for disposable SQLite/WAL state and a read-only enrollment secret.

- [ ] **Step 2: Prove RED and implement Compose overlays**

Run: `bash scripts/verify-compat-compose_test.sh`

Expected: FAIL because overlays are absent. Add commented base examples and complete opt-in overlays using private image placeholders `ghcr.io/vondel-media/vondel-audiobookshelf:latest` and `ghcr.io/vondel-media/vondel-jellyfin:latest`; production pulls require private-registry authentication.

- [ ] **Step 3: Document same-host and remote deployment**

Document enrollment secret creation/destruction, readiness API handshake, private network, mTLS remote mode, firewall allowlist, version/digest display, pinning, update, rollback, disable, revoke, and volume removal. Exact activation command:

```bash
docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml up -d
```

- [ ] **Step 4: Verify rendered configurations and commit**

```bash
bash scripts/verify-compat-compose_test.sh
docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml config >/dev/null
docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml config >/dev/null
git add docker-compose.yml docker-compose.*.yml scripts/verify-compat-compose* docs/operations/compatibility-applications.md
git commit -m "docs(compat): add private companion deployment contracts"
```

---

### Task 7: Foundation Acceptance and CI Gate

**Files:**
- Create: `internal/acceptance/compat_foundation_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md`

**Interfaces:**
- Consumes Tasks 1–6.
- Produces one required CI gate proving foundation behavior with zero companions and with deterministic fake companions.

- [ ] **Step 1: Write failing disposable-system acceptance**

Create a clean PostgreSQL database and two fake applications. Exercise enrollment, API negotiation, direct and legacy login, trusted device/PIN switching, fixed routing, application isolation, every revocation revision, adult non-disclosure, idempotency replay, cursor tampering, incompatible version, disabled route, and native health with both companions stopped.

- [ ] **Step 2: Prove RED, wire the real stack, and turn GREEN**

Run: `SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -tags=integration ./internal/acceptance -run TestCompatibilityFoundation -count=1 -v`

Expected initial failure: the CI gate is absent or one required invariant is not wired. Use real handlers/stores/gateway with deterministic HTTP fixtures; do not bypass policy or call stores directly for behavioral assertions.

- [ ] **Step 3: Add required CI execution and structural guards**

The CI job provisions PostgreSQL, sets `SILO_REQUIRE_TEST_DATABASE=1` only on the required step, runs the exact acceptance selector, and scans Compose/workflows/source for prohibited credentials, mounts, host ports, public publishing, and Docker socket access.

- [ ] **Step 4: Run final foundation gates**

```bash
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test -tags=integration ./internal/acceptance -run TestCompatibilityFoundation -count=1 -v
GOWORK=off go test ./internal/auth ./internal/compatapp ./internal/compatapi ./internal/compatgateway -count=1
GOWORK=off go test -race ./internal/compatapp ./internal/compatapi ./internal/compatgateway -count=1
GOWORK=off go vet ./internal/auth ./internal/compatapp ./internal/compatapi ./internal/compatgateway
git diff --check
```

- [ ] **Step 5: Mark the foundation implemented and commit**

Update only the foundation checklist/evidence in the spec; extraction remains incomplete.

```bash
git add .github/workflows/ci.yml internal/acceptance/compat_foundation_test.go docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md
git commit -m "test(compat): lock compatibility foundation"
```
