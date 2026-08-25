# OPA tenant foundation — final fix report

Date: 2026-08-13  
Starting commit: `ba4a3c76`  
Branch: `feature/opa-tenant-foundation`

## Result

The final whole-branch findings are addressed without adding deferred roles,
authentication, UI, Live TV, plugin, or adult-scene features.

- The Jellyfin compatibility resolver, Audiobookshelf resolver, notification
  interest resolver, and request reconciliation resolver now resolve the exact
  account/profile tenant before invoking the policy viewer. The adapter
  overwrites caller context and hides foreign profiles. Compat playback session
  limits use the same subject resolver and fail closed when it is unavailable.
- `user_profiles.access_group_id` is now `NOT NULL` after a safe migration that
  promotes an existing organization group (or creates a starter default),
  backfills unassigned profiles, validates coverage, and supports down/up.
  Non-default group deletion moves only that organization's affected profiles
  to its default group in the same transaction.
- Every authorization-affecting group mutation (libraries, quality, download,
  download transcode, stream/transcode count, permissions, and requests) bumps
  assigned accounts' `access_policy_revision`; identical values, description,
  and empty updates do not.
- CI again selects resource-tenancy migrations and pgstore profile/FK tests.
  Every PostgreSQL step sets `SILO_REQUIRE_TEST_DATABASE=1`; ordinary CI keeps
  running non-database tests without a database.

V1 administrator compatibility was inspected. Profile creation now fills a
missing canonical assignment from the profile organization's default group;
the existing v1 compatibility and access-group handler tests remain green.
The new migration's down action restores nullability but deliberately retains
safe backfilled assignments/default promotions; the complete documented legacy
rollback still crosses all tenant migrations.

## RED evidence and diagnosis

1. The production call-site trace found four direct `policy.NewViewerResolver`
   compositions and compat session-limit resolution without authoritative
   `tenancy.Context`. The first focused adapter tests failed to compile because
   `tenancy.SubjectResolver` and `policy.TenantViewerResolver` did not exist.
   The regression now proves a forged context is overwritten and a missing or
   foreign subject never reaches the downstream viewer.
2. The latest schema declared `user_profiles.access_group_id` nullable and
   `GroupStore.Delete` explicitly wrote `NULL`. The up/down/up regression and
   deletion assertions characterize the required non-null/default reassignment
   behavior. Static inspection supplied the initial RED characterization; the
   migration was present for the first executable PostgreSQL run.
3. `GroupStore.Update` read and compared only `max_playback_quality` before
   bumping revisions. The table-driven regression covers all eight policy
   categories and repeats each value to prove no-op behavior. Static inspection
   supplied the initial RED characterization before implementation.
4. `.github/workflows/ci.yml` omitted resource-tenancy migration and pgstore
   profile/FK selectors. The structural workflow assertion was extended with
   the missing exact commands, and a database-package subprocess proves the
   explicit required signal fails without a URL. Existing policy subprocesses
   prove ordinary `CI=true` still runs a named non-database test and the
   explicit signal fails before store tests can skip.

During the first restored migration-selector run, the older organization-group
test expected an organization default to remain false after re-up. The new
mandatory-profile migration correctly promotes it on re-up, so that stale
expectation failed. It was corrected to assert the new invariant, and the
focused up/down/up test passed.

## GREEN and database evidence

All PostgreSQL commands used the maintenance URL
`postgres://silo:***@localhost:5432/silo?sslmode=disable`. Test helpers created
uniquely named child databases, applied embedded `migrations.FS`, terminated
child connections, dropped exact names, and verified cleanup where provided by
the package fixture. No broad database was dropped or modified by cleanup.

- `go test ./internal/tenancy ./internal/policy -run 'TestSubjectResolver|TestTenantViewerResolver'`: PASS.
- focused tenancy/policy/API live-context tests and `go test ./cmd/silo -run '^$'`: PASS.
- `SILO_REQUIRE_TEST_DATABASE=1 go test ./internal/database -run 'TestProfileAccessGroupRequired' -count=1 -v`: PASS, including up/down/up and missing-URL subprocess.
- focused real-PostgreSQL access group CRUD, canonical resolution, cross-org,
  delete reassignment, and all revision categories: PASS.
- exact pgstore profile/FK selector: PASS (`TestProfileOrganizationAndAccessGroupPersistence`, `TestProfileAccessGroupRejectsDifferentOrganization`).
- `TestPolicyOrdinaryCIWithoutDatabaseRunsNonDatabaseTest` and
  `TestPolicyRequiredDatabaseSignalFailsWithoutURL`: PASS.
- `TestV2FoundationCIRequiresDisposablePostgres`: PASS.
- named `TestOPATenantFoundationWithDisposablePostgres`: PASS in 6.30s; it
  exercised the exact ten-item contract and its disposable database cleanup.
- exact four-package PostgreSQL race gate:
  `internal/tenancy` PASS 34.917s; `internal/resourcetenancy` PASS 46.635s;
  `internal/access` PASS 10.313s; `internal/policy` PASS 31.579s.
- focused v1 compatibility, compat playback session limits, and access-group
  handler/API tests: PASS.
- `go vet ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api/... ./internal/userstore/pgstore`: PASS, no output.
- `go build ./cmd/silo`: PASS, no output (existing web assets were available).
- `git diff --check`: PASS, no output.

The first full restored migration-selector run passed all tenant-identity and
resource-tenancy cases and failed only the stale organization-default
expectation described above; that exact test then passed after correction. A
redundant second full selector was stopped to conclude promptly after the
focused correction and all other required evidence were green. This is not
reported as a clean second full-selector run.

## Self-review and concerns

- Subject selection always begins with account/profile identity and validates
  current membership; caller tenant context cannot select an organization.
- Group reassignment queries are organization-qualified, use the exact default
  in the same transaction, and leave foreign profiles untouched.
- Revision increments select distinct assigned account IDs and occur before the
  transaction commits the policy mutation.
- The migration never converts an existing configured organization policy to a
  synthetic policy when an existing group can be promoted.
- Deferred capability booleans remain false; no v10 alias or v2 mutation route
  was added.

Concern: the redundant final full migration selector was interrupted as noted;
the corrected failing case, named acceptance, exact race gate, pgstore selector,
vet, build, and diff checks are green.

## Scoped review residual

The single scoped rereview identified one remaining compatibility defect: the
limit provider attached tenant facts only to its local context value, so the
session manager passed the original context to OPA admission. The final local
correction adds a subject-validating `SessionContextProvider` whose returned
context is used by both limit calculation and admission. A regression proves
both callbacks receive the same validated context. Full playback tests,
focused API context/limit tests, compatibility session/stream tests, the server
build, and `git diff --check` pass.
