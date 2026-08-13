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
```

## Self-review and concerns

- Auditing is fail-closed at the HTTP mutation boundary: if persistence is unavailable the response is `503`. The lifecycle mutation and audit insertion are not one PostgreSQL transaction because the existing store API returns the completed mutation before the handler records its typed event. This meets the requested record content and prevents a success response without audit, but a database failure between those operations can leave a committed mutation whose handler returns `503`. A future store API can accept audit metadata to make this fully atomic.
- Ownership transfer enforces explicit confirmation and an active organization membership. The approved design also calls for account re-authentication; Task 1 exposes validated Platform context but no recent-auth proof, so the endpoint cannot honestly enforce re-authentication yet. This should be added when that proof is available rather than accepting a caller-supplied assertion.
- The added migration is necessary because the current generic request activity table has no fields for typed lifecycle action, target, authority context, revision pairs, outcome, or bounded state.
