# SDD ledger — plan: docs/superpowers/plans/2026-08-13-vondel-opa-tenant-foundation.md

Workspace: `/Users/jimcole/projects/vondel-server/.worktrees/opa-tenant-foundation`
Branch: `feature/opa-tenant-foundation`
Merge base: `0d4747da1b186ca801ac79f71a6a59bf3a596f17`

Baseline: `GOWORK=off go test ./internal/tenancy ./internal/resourcetenancy ./internal/access ./internal/policy ./internal/api ./internal/api/handlers ./internal/api/middleware -count=1` passed. `SILO_TEST_DATABASE_URL` was absent, so database-backed cases may have skipped; every task that changes persistence must provision a disposable PostgreSQL target or explicitly record the missing gate.

## Preflight dependency and consistency scan

| Tasks | Producer → consumer or internal check | Finding |
|---|---|---|
| 1 | Renames v10 routes, handlers, middleware, tests, CI selectors, and operator doc to v2 | Internally consistent; implementation must not leave a v10 alias. |
| 2 | Produces per-organization default-group invariant consumed by Task 3 | Consistent; current global partial index is the expected RED defect. |
| 3 | Produces tenant-scoped group APIs and `GroupPolicyProvider.ResolvePolicy`, consumed by Tasks 4–6 | Consistent; account policy revision must be bumped via organization-scoped profile membership, not a nonexistent profile revision. |
| 4 | Produces additive `TenantFacts` in policy inputs, consumed by Task 5 and final acceptance | Consistent; existing policy JSON fields remain untouched. |
| 5 | Produces `AvailableMediaFolderIDs` and OPA tenant-library intersection, consumed by Task 6 | Consistent; availability query is a hard upper bound and custom Rego remains narrowing-only. |
| 6 | Consumes all earlier interfaces for router wiring, acceptance, CI, and docs | Consistent; capability advertisement stays false for all deferred features. |
| 1 ↔ 6 | Both modify `internal/api/v1_tenancy_compat_test.go`, `.github/workflows/ci.yml`, `FORK.md`, and v2 operator docs | Ordered safely: Task 1 renames the surface; Task 6 strengthens final assertions. |
| 2 ↔ 3 | Task 2 establishes schema invariant; Task 3 changes store/query semantics | Ordered safely; Task 3 must use the new per-organization default index and existing composite profile/group FK. |
| 3 ↔ 4 | Both modify `internal/policy/viewer_resolver.go` and tests | Ordered safely; Task 4 must preserve Task 3's `GroupSubject` resolution while adding tenant facts. |
| 3 ↔ 5 | Both modify viewer resolution | Ordered safely; Task 5 injects resource scope after Task 3 selects the canonical profile group. |
| 4 ↔ 5 | Both modify `internal/policy/input.go`, viewer resolver, scope policy, and parity tests | Ordered safely; Task 5 extends Task 4's additive tenant document and may not rename its fields. |
| 5 ↔ 6 | Task 5 produces policy/store interfaces; Task 6 wires them into production and acceptance | Ordered safely; final capability is advertised only after acceptance passes. |

Preflight result: no unresolved contradictions. The plan is intentionally limited to the first secure foundation increment; administrative roles, v2 authentication, management mutations/UI, Live TV, plugins, and adult-scene enforcement remain later plans under the approved spec.

Task 1: complete (commits 0d4747d..941bc1b, review clean)
- Implementation: complete v10-to-v2 rename with no alias; active source, tests, CI selectors, fork documentation, resource-tenancy spec, and operator document aligned.
- Verification: focused RED/GREEN and complete relevant Go suite passed; local `SILO_TEST_DATABASE_URL` was absent, so DB-backed cases skipped. Task review confirmed Task 1 itself did not introduce a persistence change and CI still requires its PostgreSQL configuration.
- Review: spec PASS; no Critical, Important, or Minor findings.

Task 2: minor (deferred): `organization_access_group_migration_test.go` hard-codes the rollback target without the immediate-predecessor guard used by adjacent resource-tenancy migration tests; final whole-branch review should decide whether consistency warrants adding that guard.
Task 2: fix round 1/5 (1 addressed, 0 open — post-re-up organization-scoped uniqueness now executed; commits b380f17..7ab8465)
Task 2: complete (commits 941bc1b..7ab8465, review clean; 1 deferred minor)
- Implementation: replaced the global default-group index with a per-organization partial unique index, retained the composite profile/group tenant FK, and preserved group rows through rollback normalization.
- Verification: real PostgreSQL up/down/up, cross-organization FK rejection, pre- and post-re-up scoped uniqueness, tenant-identity migration, and CI missing-database fail-closed guard passed.

Task 3: fix round 1/5 (3 addressed, 0 open — playback profile propagation, download profile propagation, and default-organization user assignment; commits 604d929..587e329)
Task 3: complete (commits 7ab8465..587e329, review clean)
- Implementation: organization-scoped group CRUD/defaults/counts; canonical profile group resolution with legacy account ceiling; tenant-derived handler context; profile identity propagated through playback limits and every applicable download policy path; legacy user creation selects only the default organization's default group.
- Verification: focused TDD, real PostgreSQL two-organization regression, race, complete relevant access/policy/playback/API/handler/auth suites, and repository compile-only passed.
- Concern recorded for final triage: broader downloads suite has a pre-existing unstable `TestRemoteCleanupBudgetBoundsUnreachableOrigins`; changed download policy tests pass.

Task 4: Ruling: the plan-prescribed `TenantFactsFromContext(context.Context)` signature cannot enforce the binding constraint that foreign subjects fail closed. Replace it with subject-aware extraction accepting the expected account ID, and carry resolver-validated default-organization provenance in `tenancy.Context` so the legacy-initializing exception cannot rely on `Legacy` alone. This is the smallest change consistent with the approved spec. Cost if wrong: additional internal signature propagation and one additive context field; no external API or policy JSON change.
Task 4: fix round 1/5 (1 addressed, 0 open — policy subject binding and default-organization legacy provenance; commits d0f8a09..f671916)
Task 4: complete (commits 587e329..f671916, review clean)
- Implementation: additive tenant facts on all three OPA inputs; authoritative adapters overwrite/extract facts from validated context bound to the exact account; default-organization provenance closes the legacy-initializing exception; simulation is marked non-authoritative; base grants unchanged.
- Verification: focused TDD, forged-facts regression, tenancy/policy/middleware/download/playback suites, tenancy+policy race, real PDP benchmark 97,158 ns/op (<25 ms), and diff check passed.

Task 5: fix round 1/5 (1 addressed, 0 open — real default-organization materialization and Store→ViewerResolver parity proof; commits a32a1e9..a6b5607)
Task 5: complete (commits f671916..a6b5607, review clean)
- Implementation: one bounded SQL query exposes matching organization-owned folders plus actively entitled platform folders; mandatory TenantLibraryIDs upper bound intersects account/group/profile/custom restrictions in vendor Rego; all production viewer resolvers require the resource resolver.
- Verification: real PostgreSQL materialization RED/GREEN, missing/failed materialization fail-closed, pre-existing platform folder parity, foreign/post-materialization-unentitled exclusion, catalog/search/detail/playback predicates, full relevant/race/compile gates, and cleanup passed.

Task 6: Ruling: the specified multi-package PostgreSQL gate exposed that `internal/access` tests use the maintenance database directly and race migration packages that create/drop schema. Isolate the access fixture in its own uniquely named disposable database, applying `migrations.FS` with Goose directly rather than importing `internal/database` (which creates a test import cycle). Cost if wrong: a focused test-harness change in this task; production behavior is unchanged, while CI becomes deterministic under package parallelism.
Task 6: Ruling: policy-store tests have the same maintenance-database skip/mutation defect as access tests. Give the policy package its own uniquely named migrated database with fatal CI behavior and exact cleanup. Cost if wrong: broader test-harness changes, but no production/API behavior; leaving it would allow a required security gate to pass while six tests skip.
Task 6: Ruling: database-required failure must be controlled by an explicit `SILO_REQUIRE_TEST_DATABASE=1` gate set only on PostgreSQL CI steps, not generic `CI=true`; ordinary non-DB unit jobs must still run policy unit tests without a database while DB store tests skip. Cost if wrong: one additional CI/test environment contract; generic CI gating would make ordinary `go test ./...` unusable.
Task 6: fix round 1/5 (2 addressed, 0 open — isolated policy-store database and exact v1 profile/settings contract; commits 94f88cc..69cb0a3)
Task 6: fix round 2/5 (2 addressed, 0 open — explicit required-DB signal and foreign-profile non-disclosure; commits 69cb0a3..ba4a3c7)
Task 6: complete (commits a6b5607..ba4a3c7, review clean)
- Implementation: full disposable-PostgreSQL acceptance, exact v2 capability document, production composition, CI selectors, isolated access/policy test databases, no-skip explicit database gate, v1 profile/switch/settings parity, stale revision checks, docs, vet/build evidence.
- Verification: named acceptance, exact migration/API selectors, exact four-package race, policy DB modes, vet, server build, and diff checks passed. Latest eight-package concurrent gate had seven packages pass and one saturation-sensitive existing 25 ms handler test fail; its immediate focused unchanged rerun passed. Evidence is reported without claiming the full concurrent gate green.

Final whole-branch fix wave: complete (starting at ba4a3c76; final commit recorded in final-fix-report.md)
- Production out-of-request policy surfaces now attach subject-validated tenant context and reject forged/missing context; compat playback session limits use the same validation.
- Profiles have exactly one canonical group after a safe NOT NULL backfill; non-default deletion reassigns within the organization; all authorization-affecting group mutations bump member revisions with tested no-op behavior.
- Resource-tenancy migration and pgstore profile/FK CI gates are restored with per-step explicit database requirements; ordinary non-database CI remains usable.
- Evidence: disposable migration and store tests, named ten-item acceptance, exact four-package PostgreSQL race, focused v1/admin compatibility, vet, server build, and diff checks passed. See `final-fix-report.md` for the one interrupted redundant selector and complete evidence.

Final scoped review residual: resolved locally after `fed275c7`.
- Review proved that tenancy attached inside `SessionLimitProvider` could not reach the later admission decider because Go contexts are immutable values.
- Added a `SessionContextProvider` boundary to `playback.SessionManager`; its validated context now feeds both the dynamic limit lookup and OPA admission for native, Jellyfin, and Audiobookshelf session starts.
- RED: the regression failed to compile because the provider did not exist. GREEN: full playback tests, focused API context/limit tests, compatibility session/stream tests, server build, and diff check passed.
