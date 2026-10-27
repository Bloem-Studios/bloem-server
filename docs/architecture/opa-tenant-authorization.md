# OPA tenant authorization operations

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current, with corrections below. The embedded OPA engine, access groups and
> the vendor Rego bundle (`internal/policy`, `internal/policy/vendor/*.rego`) are
> Silo's (Silo #282, `42602b789`, 2026-07-05). OPA is the Open Policy Agent, an
> embedded policy evaluator. Bloem adds the tenant layer: organizations,
> memberships, organization-owned groups, resource ownership and entitlements,
> loaded as facts into Silo's engine through Bloem-owned adapters
> (`internal/policy/bloem_*.go`, `internal/access/bloem_group_store.go`,
> `internal/tenancy`). This page dates from the tenant foundation of 2026-08-13
> (`94f88cc9c`, `fed275c71`, `0e6f7f03d`). Later changes that matter:
> `654301e56` (2026-09-09) made the transactional group read subject-bound, and
> `b14f34552` (2026-10-05) moved that read into the Bloem-owned
> `access.TenantGroupPolicyInTransaction`, which returned Silo's
> `GroupPolicyInTransaction` to its account-level form. `c60b7008e` and `d064644a8`
> (2026-10-26) gave each account exactly one membership and moved its account
> policy back onto `users`, where Silo keeps it. The Silo merge `68070f04b`
> (2026-10-27) binds the administrative context check to the token's account.
> For the switch back to Silo's single-organization behavior, see
> [Silo backend switching](silo-backend-switching.md).

This is the operator boundary for Bloem's OPA-centered tenant authorization.
PostgreSQL supplies authoritative organization, membership,
profile-group, ownership, and entitlement facts. The application resolves a
subject-bound tenant context, loads the bounded facts, and sends typed input to
OPA. SQL then applies the returned scope to catalog and playback reads.

## Shipped boundary

`GET /api/bloem/v1/capabilities` advertises `legacy_silo_v1`,
`organization_memberships`, and `tenant_bounded_media_scope` as `true`. It
advertises direct-profile login when its dependencies are wired. That capability
does not admit direct sessions to the native namespace or the browser's Silo v2 calls.
Shared-device profile pairing and delegated administrative roles remain `false`.
Household credential management has its own capability and account/PIN/reauthentication
boundary; see [the security foundation](bloem-security-foundation.md).
Native administration is additive under
`/api/bloem/v1/admin`: an authenticated account exchanges its session for one
short-lived Platform or Organization context token. Every request that carries
the token re-checks the login session it was exchanged from
(`internal/api/middleware/admin_context.go`). Since `68070f04b` that check passes
the token's account to Silo's `SessionRepository.ActiveSession`, so a session that
belongs to another account, or a revoked or remote-playback session, is refused
with `authorization_state_stale`. Platform routes operate on
the organization directory; Organization routes take their organization only
from that token and expose people, profiles, groups, libraries, entitlements,
invitations, redacted activity pages, and policy-decision explanations. Grant changes
check the grant security revision; invitation mutations check the organization policy
revision. See [multitenant administration](multitenant-administration.md).

`/api/v1` remains the Silo-compatible surface. Existing users and profiles are
backfilled into the default organization without changing profile IDs, PINs,
login payloads, profile switching, or legacy token claims. Legacy account requests
ignore caller-supplied organization headers and use the server's account/profile
tenant projection. Direct-profile sessions revalidate their explicitly bound tenant
and cannot be projected into another organization. Bridge and native
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
account, and group by the server-resolved organization. The account's own
`users.access_group_id` (Silo's column, which again holds the account's group
since `c60b7008e`) is used only for profile-less legacy resolution in the
default organization.

Snapshot-based services use `access.TenantGroupPolicyInTransaction` with the
exact organization, account, and profile subject (`access.GroupSubject`). Silo's
`access.GroupPolicyInTransaction(ctx, tx, userID)` is the account-level read and
does not take a subject. The subject must match the validated
tenant context. The read uses the caller-owned transaction and the same
organization-qualified query as normal policy resolution; it never substitutes
an account group for a missing or foreign profile. Callers remain responsible
for loading and validating the other authority facts in that snapshot.

`auth.UserRepository.GetByID` returns the account's `users` row, Silo's
account-shaped projection. Since `c60b7008e` and `d064644a8` (2026-10-26) an
account has exactly one membership (`organization_memberships` is unique on
`account_id`), so the earlier "default or earliest membership" selection no
longer exists. The row is still not a substitute for the profile's group and
the tenant's revisions when assembling a transactional authorization snapshot.

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

The tenant job in `.github/workflows/bloem-ci.yml` first migrates its empty
disposable PostgreSQL service (`go run ./cmd/silo --migrate-only`), then runs
these gates with the job-level database URL:

```sh
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/database -run 'TestTenantIdentityMigration|TestResourceTenancyMigration|TestOrganizationAccessGroupMigration|TestProfileAccessGroupRequired' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/userstore/pgstore -run 'TestProfileOrganizationAndAccessGroupPersistence|TestProfileAccessGroupRejectsDifferentOrganization' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test -race ./internal/resourcetenancy ./internal/access ./internal/policy -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/api -run 'TestV1TenancyCompatibility|TestOPATenantFoundationWithDisposablePostgres' -count=1 -v -timeout=30m
SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/api ./internal/tenancy ./internal/adminpeople ./internal/database -run 'TestMultitenantAdmin|TestAdminStore|TestService|TestAdminPeopleMigrations|TestOrganizationAdminProjectionMigration' -count=1 -v -timeout=30m
```

The race tests for `./internal/tenancy` run in a separate job, "Tenant stores
race", split across four shards (`scripts/ci/go-test-shard.sh`), each with its own
disposable PostgreSQL.
