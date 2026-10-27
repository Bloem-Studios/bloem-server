# Bloem overlay: Executable scenario pairing

**What this overlays.** Silo's
[`docs/architecture/scenario-acceptance.md`](../../../architecture/scenario-acceptance.md).
Bloem adds to its opening section “Executable scenario pairing” and replaces
two packet descriptions: “Frozen API-key scope discovery pairs” and “Avatar
uploads and deletion”. Every other packet description applies to Bloem as Silo
wrote it. An *overlay* is a Bloem-owned page that records Bloem's additions to,
or replacements of, sections of a Silo-owned document. The Silo file stays
exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem runs Silo's frozen scenario catalogs against
Bloem's router, which serves more than Silo's:

- a multi-tenant organization model, so fixtures must recreate the default
  organization;
- extra API-key scopes;
- an explicit 503 when avatar storage is absent, instead of a recovered panic.

Three Bloem mechanisms reconcile the frozen catalogs with that router, without
editing Silo's catalogs:

- **Explicit adjudications.** Each adjudication is a pinned replacement
  expectation for one scenario transport, with a written reason. Bloem added
  them in `23ee4c1ac` (2026-09-19); the design is in
  [contract adjudications](../../../architecture/bloem-contract-adjudications.md).
- **Bloem catalog rows**, kept in Bloem-owned files since `ae1117d8a`
  (2026-10-26).
- **Bloem fixture prerequisites.**

Bloem's text moved out of the Silo file into this overlay in `47ea7c889`
(2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/scenariocatalog`, `internal/scenariocatalog/executor`,
`Makefile.bloem`, `.github/workflows/bloem-ci.yml`). Since this overlay was
written (against Silo `8e5ae2f46`), Silo changed the document five times. The
changes cover API-key creation as the admin's primary profile (`00380a108`,
`fca27206c`), OIDC, LDAP and TV device sign-in (`13601f068`), PIN proofs
(`485f545ad`), and explicit nulls in metadata edits (`2365c5c18`). None of them
changes the sections below. This review also removed two replacements of the
opening section. They described lifecycle-request gating of offline runs, which
went with Bloem's lifecycle receipts in `b017bfe84` (2026-10-26). Silo's
offline text now applies unchanged.

Index: [Bloem documentation](../../README.md).

---

**Location:** section “Executable scenario pairing”, after the opening paragraph
beginning “Scenario catalogs retain the observed v1 exchange…”. **Bloem adds:**

### How Bloem runs the catalogs

The executor loads Silo's embedded catalogs and splices Bloem's own rows onto
the end of the catalog each extends. Those are rows for routes Bloem registers
in a Silo route group (`internal/scenariocatalog/bloem_catalogs/api/` extends
the v1 auth, devices and profiles catalogs), so Silo's catalog files stay
byte-identical. Before running, it applies the 49 transport decisions in
`internal/scenariocatalog/bloem_adjudications.json`. Each replaces one
scenario transport's expectation with a pinned Bloem expectation and a reason.
The file pins the SHA-256 of every catalog it adjudicates, so a changed
catalog fails the gate (“adjudicated baseline changed”) instead of silently
keeping a stale decision. Frozen source catalogs
and Silo's selectors remain unchanged; they are not relabeled as current Bloem
oracles.

The fixture differs from Silo's in a few places:

- reseeding recreates the default organization and platform-security rows that
  the users cascade clears;
- the fixture admin becomes the organization and server Owner, as first-run
  setup does;
- access groups belong to the default organization;
- after the first seed, each reseed restores a snapshot of the seeded household
  instead of re-running bcrypt seeding;
- the offline router gets an already-cancelled application context, so its
  background workers stop at once against the dead pool;
- for a paired `FreshState` scenario the pairing wrapper does not reseed again,
  because the transport already resets before and after its exchange.

### Where Bloem runs it

Bloem CI runs the executor in its own `scenario-executor` job (split out in
`d3da00815`, 2026-09-23; `.github/workflows/bloem-ci.yml` since `dcbd1c525`,
2026-10-26). The job uses `make test-scenarios`, which is
`go test -timeout=45m ./internal/scenariocatalog/executor` from
`Makefile.bloem`, under a 45-minute job limit, against an empty PostgreSQL
database named by `SILO_SCENARIO_DATABASE_URL`. `make test-go` keeps a
20-minute per-package timeout, which Bloem added to the Makefile in
`23ee4c1ac`. A passing job is the scenario gate for one commit; it is not
client or device acceptance.

### Current Bloem prerequisite coverage

Row-scoped Bloem fixtures (`withBloemRowFixtures`) install prerequisites that
the frozen rows assume but Silo's bare fixture lacks. Teardown restores the
prior router.

- **Avatars.** The profile-list row, and the avatar validation and ownership
  scenarios, get a real temporary filesystem avatar store, signer and
  resolver. `avatar_upload_enabled` then describes usable storage and those
  cases reach their intended checks. The explicit absent-store cases stay
  unconfigured (see “Avatar uploads and deletion” below).
- **Devices and sections.** Device-removal packets seed the same canonical
  overrides as their dedicated effect tests, without altering device
  identities or last-seen ordering. Section packets receive their prior
  overrides and an organization-owned fixture library. Its exact identity is
  checked and removed before the ordinary scratch guard runs; cleanup refuses
  foreign libraries or any media rows rather than weakening the guard.
- **Reseeding** also restores the invitation public URL and binds account
  tokens to their actual incarnations.

These are prerequisites, not substitutes for the original transport
assertions. `TestBloemScenarioEnvironmentLifetime` checks that application
cancellation during cleanup does not cancel fixture SQL.
`TestBloemScenarioReseedBoundaries` checks that paired invite-code top-up and
list cases reseed independently for each transport.

### History (September 2026)

These results are kept as dated evidence. They describe earlier revisions and
do not certify the current gate.

- A completed configured executor run took 872.712 seconds, beyond Go's implicit
  ten-minute limit, which is why `make test-go` gained its 20-minute budget.
- [CI run 35467142414](https://github.com/Bloem-Studios/bloem-server/actions/runs/35467142414)
  for `418a18b7d` then timed out after 20 minutes (1200.171 seconds) in this
  package. `TestScenarioCatalogs` had run for 17m22s and was still seeding
  profile-section fixtures; no assertion failure was reported, but unfinished
  cases are not passes. A deployment exception was approved for that exact
  revision and timeout; the
  [September 19 deployment record](../../../operations/2026-09-19-xtream-deployment.md)
  keeps it. It is not permission to skip scenarios, reduce authentication cost,
  weaken assertions or treat a timeout as a pass. The executor moved to its own
  CI job four days later.
- The focused adjudication packet passed all 52 transport leaves of the 30
  decisions that existed then. The embedded-web checkpoint passed 98 scenario
  and transport results with zero skips; a larger combined race run timed out
  during device execution and remained inconclusive. See
  [coverage evidence](../../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits).

---

**Location:** section “Frozen API-key scope discovery pairs”. **Bloem replaces**
the sentence beginning “V2 explicitly adds the availability flag…” with:

V2 adds the availability flag and Problem Details. Bloem's native discovery
(`getPersonalAPIKeyScopes`) lists seven scopes: Silo's four, then Bloem's
`admin:entitlements:bulk`, `admin:entitlements:accounts` and `admin:preview`.
Five Bloem adjudications (`scopes.ok`, `scopes.meaning`,
`scopes.api_key_allowed`, `scopes.shape` and `scopes.sorted`, v2 transport)
pin all seven names and their order. v1 discovery keeps its original
two-scope projection and its frozen oracle. Do not truncate native discovery
to match the historical two-scope v2 oracle.

---

**Location:** section “Avatar uploads and deletion”, the first paragraph.
**Bloem replaces** the sentences after “Original requests and oracles remain
unchanged, including `avatar_upload.typed_nil_panic` and
`avatar_upload.meaning`” with:

Their frozen oracles expect an empty v1 500 and a v2 internal-error Problem
from a recovered typed-nil panic. Bloem refuses a missing avatar store
explicitly instead, so four Bloem adjudications (both cases, both transports)
require `503`: on v1 the envelope `{error: "unavailable", message: "Avatar
upload storage is not configured"}`, on v2 a `dependency_unavailable`
Problem. Neither case is skipped. Do not reconstruct the panic or configure
storage for these explicit absent-store cases.
