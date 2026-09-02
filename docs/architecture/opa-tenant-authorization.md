# OPA tenant authorization operations

This is the operator boundary for the first OPA-centered tenant authorization
increment. PostgreSQL supplies authoritative organization, membership,
profile-group, ownership, and entitlement facts. The application resolves a
subject-bound tenant context, loads the bounded facts, and sends typed input to
OPA. SQL then applies the returned scope to catalog and playback reads.

## Shipped boundary

`GET /api/bloem/v1/capabilities` advertises `legacy_silo_v1`,
`organization_memberships`, and `tenant_bounded_media_scope` as `true`. It
advertises direct-profile login, shared-device pairing, and delegated
administrative roles as `false`. Native administration is additive under
`/api/bloem/v1/admin`: an authenticated account exchanges its session for one
short-lived Platform or Organization context token. Platform routes operate on
the organization directory; Organization routes take their organization only
from that token and expose people, profiles, groups, libraries, entitlements,
invitations, and redacted policy-decision explanations.

`/api/v1` remains the Silo-compatible surface. Existing users and profiles are
backfilled into the default organization without changing profile IDs, PINs,
login payloads, profile switching, or legacy token claims. V1 ignores caller-
supplied organization headers and resolves the default organization. V1 and v2
policy adapters use the same live OPA bundle and expose the same decision
generation in policy decision evidence.

## Group and media scope

An access-group name, including a default-group name, is unique only within its
organization. A selected profile's `organization_id` and `access_group_id` are
the canonical assignment, and `user_profiles.access_group_id` is required.
Deleting a non-default group transactionally reassigns its profiles to that
organization's default group; it never creates an ungrouped, account-only
profile. Authorization-affecting group changes bump every assigned account's
policy revision in the same transaction. Resolution qualifies the profile,
account, and group by the server-resolved organization; the legacy
`users.access_group_id` is only the temporary profile-less
default-organization ceiling.

An organization has media availability for:

- folders owned by that organization; and
- platform-owned folders with an active organization entitlement.

Platform ownership alone is not access. A missing, suspended, or revoked
entitlement excludes the folder. A foreign organization-owned folder is always
excluded. The availability store performs one bounded query, and vendor OPA
policy intersects that upper bound with account, group, profile, disabled-
library, and custom narrowing restrictions. A later rule cannot add a folder
outside the tenant availability set.

## Failure and incident behavior

Missing or subject-mismatched tenant facts, stale membership or organization
revisions, unresolved ownership, unavailable resource-tenancy state, OPA
timeouts, evaluation failures, and malformed or undefined decisions deny the
request. Native stale tenant sessions return `authorization_state_stale`.
Hidden, missing, foreign, and non-entitled resources must not be distinguished
in list results or error details.

Treat repeated `tenant_unavailable`, `policy_evaluation_failed`, or policy
timeout records as an authorization incident. Do not bypass the tenant store,
substitute an unrestricted resolver, or advertise the capability while the
enforcement path is degraded. Stop affected traffic, preserve policy decision
evidence, repair the database or policy bundle, and re-run the release gate.

## Rollback boundary

Rollback remains supported only while all durable state is representable by
the legacy single-organization schema. Before any non-default organization,
organization-specific profile group assignment, resource owner, or entitlement
is relied upon, stop writes, keep a tested backup, and roll the application and
schema back together using the procedure in
[Bloem native API security foundation](bloem-security-foundation.md). After that boundary
is crossed, use restore/recovery planning; schema rollback would discard tenant
meaning even if legacy rows survive.

## Verification

Commands assume the repository root is the current directory and a maintenance
database URL with `CREATE DATABASE` permission is exported as
`SILO_TEST_DATABASE_URL`. Required acceptance tests fail rather than skip when
that variable is absent. Every acceptance run creates a unique database,
terminates its test connections, drops it, and verifies absence after cleanup.

```sh
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test ./internal/database ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api ./internal/api/handlers ./internal/api/middleware -count=1 -v -timeout=45m
SILO_REQUIRE_TEST_DATABASE=1 SILO_TEST_DATABASE_URL="$SILO_TEST_DATABASE_URL" GOWORK=off go test -race ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=45m
GOWORK=off go vet ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api/...
GOWORK=off go build ./cmd/silo
git diff --check
git status --short
```

CI executes these PostgreSQL-specific gates with the job-level database URL:

```sh
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/database -run 'TestTenantIdentityMigration|TestResourceTenancyMigration|TestOrganizationAccessGroupMigration|TestProfileAccessGroupRequired' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/userstore/pgstore -run 'TestProfileOrganizationAndAccessGroupPersistence|TestProfileAccessGroupRejectsDifferentOrganization' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test -race ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/api -run 'TestV1TenancyCompatibility|TestOPATenantFoundationWithDisposablePostgres' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/api ./internal/tenancy ./internal/adminpeople ./internal/database -run 'TestMultitenantAdmin|TestAdminStore|TestService|TestAdminPeopleMigrations|TestOrganizationAdminProjectionMigration' -count=1 -v -timeout=30m
```
