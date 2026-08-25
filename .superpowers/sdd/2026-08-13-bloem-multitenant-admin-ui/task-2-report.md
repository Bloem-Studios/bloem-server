# Task 2 report: Platform organization lifecycle API

## Outcome

Implemented the Platform organization directory and lifecycle API behind the Task 1 validated administrative context boundary. No `/api/v1` behavior was changed, and no organization hard-delete path was added.

## Delivered behavior

- Cursor-paginated organization directory ordered by `(lower(name), id)`, with search, status filtering, bounded limits, opaque cursors, and exact membership/profile/owned-library/live-entitlement counts.
- Organization detail, creation with transactional active owner-admin membership, revision-guarded name/slug updates, reversible suspension/reactivation, and ownership transfer.
- Organization-bounded membership listing, creation, role/status update, protected current-owner state, security-revision increments, and non-disclosing foreign membership identifiers.
- Platform-only HTTP routes under `/api/v2/admin/platform/organizations`; handlers re-check the server-validated administrative scope attached by middleware before touching storage.
- Field-addressable `422` validation, `409 authorization_state_changed` with current revision, non-disclosing `404`, and `503 tenant_unavailable` mapping.
- Typed append-only `admin_audit_events` storage and repository integration. Successful mutations record actor, Platform role/context, action, target organization/subject, before/after revisions and bounded state, outcome, and request ID. Request bodies, credentials, invitation secrets, and tokens are excluded.
- Existing tenancy types now have explicit snake_case JSON tags for v2 response stability.

## TDD evidence

Initial PostgreSQL contract run failed at compile time exactly because lifecycle types and methods were absent. Store tests then passed against a disposable PostgreSQL database. Handler tests similarly failed on the missing handler/audit/context interfaces before implementation. Two self-review gaps—organization-bounded membership reads and detail counts—were each introduced as failing tests and then made green.

## Verification

Using `postgres://silo:silo@127.0.0.1:5432/silo?sslmode=disable` as the local maintenance DSN (the inherited environment did not set `SILO_TEST_DATABASE_URL`):

```text
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy ./internal/api/handlers \
  -run 'TestAdminStore|TestV2AdminPlatform' -count=1
ok github.com/Silo-Server/silo-server/internal/tenancy
ok github.com/Silo-Server/silo-server/internal/api/handlers

GOWORK=off go vet ./internal/tenancy ./internal/api/handlers
exit 0

SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/api ./internal/api/middleware ./internal/activitylog -count=1
ok github.com/Silo-Server/silo-server/internal/api
ok github.com/Silo-Server/silo-server/internal/api/middleware
ok github.com/Silo-Server/silo-server/internal/activitylog

git diff --check
exit 0

SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test -race ./internal/tenancy ./internal/api/handlers \
  -run 'TestAdminStore|TestV2AdminPlatform' -count=1
ok github.com/Silo-Server/silo-server/internal/tenancy
ok github.com/Silo-Server/silo-server/internal/api/handlers
```

## Fix round 2

An organization update whose normalized name and slug already match the locked row is now a true no-op. The store still validates and locks the expected revision first, then returns the current organization without incrementing `policy_revision` or writing an audit event. A stale unchanged request remains `ErrAuthorizationStateChanged`.

### RED/GREEN evidence

```text
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy \
  -run 'TestAdminStoreUnchangedOrganizationUpdateIsNoOpWithoutAudit' -count=1
--- FAIL: TestAdminStoreUnchangedOrganizationUpdateIsNoOpWithoutAudit
policy revision = 2, want unchanged 1

SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy \
  -run 'TestAdminStoreUnchangedOrganizationUpdateIsNoOpWithoutAudit' -count=1
ok github.com/Silo-Server/silo-server/internal/tenancy

SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy ./internal/api/handlers \
  -run 'TestAdminStore|TestV2AdminPlatform' -count=1
ok github.com/Silo-Server/silo-server/internal/tenancy
ok github.com/Silo-Server/silo-server/internal/api/handlers

GOWORK=off go vet ./internal/tenancy ./internal/api/handlers
exit 0

git diff --check
exit 0
```

## Self-review

- The added migration is necessary because the current generic request activity table has no fields for typed lifecycle action, target, authority context, revision pairs, outcome, or bounded state.

## Fix round 1

Resolved all review findings rather than retaining the earlier concerns:

- Every authority-changing organization and membership store method now requires validated Platform actor metadata in context, locks the accurate before-state, performs the mutation, inserts its typed audit event through the same `pgx.Tx`, and commits once. A forced audit trigger failure rolls the lifecycle change back.
- Stale mutations are rejected after locking and produce no audit event; successful audit records contain the locked before/after revisions and bounded state.
- Ownership transfer requires explicit confirmation and actual password re-authentication. A narrow verifier loads the authenticated account through the existing user repository and uses the existing bcrypt password check. The password is not passed to tenancy storage or audit state.
- Ownership transfer locks the target account and rejects disabled targets.
- Failed current-revision lookup after a stale mutation returns `503 tenant_unavailable`, never a `409` with revision zero.
- Missing membership accounts use the distinct `ErrAccountNotFound` and map to a field-addressable `account_id` validation error.

### RED evidence

```text
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy -run 'TestAdminStore' -count=1
# failed to compile: undefined ErrAccountNotFound, WithAdminMutationActor, AdminMutationActor

GOWORK=off go test ./internal/auth -run 'TestAccountCredentialVerifier' -count=1
# failed to compile: undefined NewAccountCredentialVerifier

GOWORK=off go test ./internal/api/handlers -run 'TestV2AdminPlatform' -count=1
# failed to compile: re-auth verifier did not match the old audit-recorder constructor
```

The first store implementation also exposed and reproduced an import cycle when tenancy attempted to import the broad activity-log package. The final implementation keeps the typed audit insert in the tenancy-owned transaction and avoids that dependency inversion.

### GREEN evidence

```text
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL=... GOWORK=off \
  go test ./internal/tenancy ./internal/api/handlers \
  -run 'TestAdminStore|TestV2AdminPlatform' -count=1
ok github.com/Silo-Server/silo-server/internal/tenancy
ok github.com/Silo-Server/silo-server/internal/api/handlers

GOWORK=off go test ./internal/auth -run 'TestAccountCredentialVerifier' -count=1
ok github.com/Silo-Server/silo-server/internal/auth

SILO_TEST_DATABASE_URL=... GOWORK=off go test ./internal/api -run 'TestV2Admin' -count=1
ok github.com/Silo-Server/silo-server/internal/api

GOWORK=off go vet ./internal/tenancy ./internal/api/handlers ./internal/auth
exit 0

git diff --check
exit 0
```
