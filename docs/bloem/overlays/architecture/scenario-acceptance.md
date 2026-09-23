# Bloem overlay: Executable scenario pairing

Bloem additions and overrides for the upstream Silo document [`docs/architecture/scenario-acceptance.md`](../../../architecture/scenario-acceptance.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Executable scenario pairing”, after the paragraph beginning “# Executable scenario pairing…”. **Bloem adds:**

Bloem's ordinary scenario gate applies the explicit
[downstream contract adjudications](../../../architecture/bloem-contract-adjudications.md) to 30 transport
expectations. Frozen source catalogs and the historical Silo selectors described
below remain unchanged; they are not silently relabeled as current Bloem oracles.

---

**Location:** section “Executable scenario pairing”. **Bloem replaces** the corresponding upstream passage with:

Non-lifecycle offline unit cases retain optional database execution. Their skipped
cases are not acceptance evidence. Lifecycle readiness failures are explicit, as described below. The catalog coverage gate still checks all
existing scenarios, but the other unpaired rows remain outside this bounded
acceptance pilot.

---

**Location:** section “Executable scenario pairing”. **Bloem replaces** the corresponding upstream passage with:

Offline execution is not a complete acceptance gate. Lifecycle requests require a
reachable phase store before input validation, even when a frozen row's static
requirements originally allowed offline execution. The executor derives this from
production's `api.MatchLifecycleRoute` registry, including each transport's own
follow-up requests. Without a database it reports an explicit missing-prerequisite
failure before HTTP, naming `SILO_SCENARIO_DATABASE_URL`; it does not skip the request
or rewrite its oracle. Explicit `database_unavailable` scenarios retain the dead pool.
Ready-store validation (`400` for the affected v1 cases) and dead-store refusal (`503`)
remain separate checked behavior.

The offline router is built without a user store, policy system,
viewer-access or acting-admin gate, so v2 operations whose declared class
(`x-silo-class`) is anything other than `public` or `authenticated` fail closed
with 503 `dependency_unavailable` before authentication. The executor therefore
treats those v2 exchanges, and any v2 follow-up step on such an operation, as
database-gated and skips them offline, exactly as it already skips v1 rows the
offline wiring does not register. Their 401 oracles are proven only on the live
router by the required targeted packets.

---

**Location:** section “Executable scenario pairing”, after the paragraph beginning “The offline router is built without a user store, policy…”. **Bloem adds:**

### Runtime budget and fixture lifetime

`make test-go` uses an explicit **20-minute per-package timeout**. A completed
configured executor run took 872.712 seconds, beyond Go's implicit ten-minute
limit. This remains a bounded gate, not a timeout exemption or a claim that all
scenario assertions pass. Race-instrumented runs have a different cost and must
not be presented as equivalent CI timing evidence.

Router maintenance uses the owning test's application context, canceled before
server and pool teardown. Fixture SQL retains a separate context for cleanup hooks
that restore owned rows. `FreshState` transports reset once before and once after
the exchange; their pairing wrapper no longer repeats the pre-reset. Paired reads
still reset before each transport. No password cost, database guard, frozen
assertion, pairing or exclusion changed.

`TestBloemScenarioEnvironmentLifetime` checks application cancellation during
cleanup without canceling fixture SQL. `TestBloemScenarioReseedBoundaries` runs
the unchanged paired invite-code top-up and list cases: top-up observes
`5 → 7`, resets to `5`, and repeats independently for the other transport. The
focused regression passed ten hierarchical events with no skips; no further full
local executor run followed those changes. The later
[contract adjudication](../../../architecture/bloem-contract-adjudications.md) resolves the 30 inspected
transport mismatches without editing frozen catalogs or weakening production.
Its focused packet passed all 52 transport leaves, including unchanged partners.
For example, invalid v2 invitation email still requires `422 validation_failed`
and now explicitly asserts the safe `body.email` diagnostic required by
[Problem Details](../../../architecture/api-contract.md#problem-details); v1 remains `400 invalid_email`.

Remote [CI run 35467142414](https://github.com/Bloem-Studios/bloem-server/actions/runs/35467142414)
subsequently ran the ordinary gate for `418a18b7d` and **timed out after 20 minutes**
in this package (1200.171 seconds). `TestScenarioCatalogs` had run for 17m22s and
was progressing through profile-section fixtures; the stack included bcrypt
seeding, not a proven deadlock. No assertion-failure markers were reported, but
unfinished cases are not passes. The other 189 Go packages passed. The runtime
budget remains unresolved: neither the historical 872.712-second completion nor
the focused packets certify this hosted run.

The maintainer approved a deployment exception for that exact revision and timeout;
[deployment completed](../../../operations/2026-09-19-xtream-deployment.md), but CI remains
failed. This is not permission to skip scenarios, reduce authentication cost,
weaken assertions or treat future timeouts as successful runs.

### Current Bloem prerequisite coverage

The profile-list row supplies the supported temporary filesystem avatar store plus
the real signer and resolver. This makes `avatar_upload_enabled` describe usable
storage. The fixture is scoped to that row and its paired v2 execution; teardown
restores the prior router and closes its server/background context. Avatar validation
and ownership scenarios also receive this real store so they reach their intended
checks. The explicit `avatar_upload.typed_nil_panic` and `avatar_upload.meaning`
cases remain unconfigured. Their historical 500 oracles remain frozen, while the
explicit Bloem adjudication requires the corrected 503 refusal and safe envelope
on both transports. Neither case is skipped by the current Bloem gate.

Generic device-removal packets seed the same canonical overrides as their dedicated
effect tests without altering device identities or last-seen ordering. Section packets
receive their prior overrides and an organization-owned fixture library. Its exact
identity is checked and removed before the ordinary scratch guard runs; cleanup refuses
foreign libraries or any media rows rather than weakening the guard. Reseeding also
restores the invitation public URL and binds account tokens to actual incarnations.
These are prerequisites, not substitutes for the original transport assertions.

Focused real-router checks upload through v1/v2, verify persisted ownership and
decoded 256px WebP bytes, private cache headers, unsigned/wrong-key refusal,
unauthenticated/cross-account rejection, deletion and absent-storage teardown.
This is separate from the historical [avatar packet](../../../architecture/scenario-acceptance.md#avatar-uploads-and-deletion).

The embedded-web checkpoint passed 98 scenario/transport results with zero skips:
46 additions covering five catalog gaps, 20 profile-list, 26 device-list and six
lifecycle-validation results. All 337 older scenarios and row metadata in the three
edited catalogs remained unchanged. Tenant/bootstrap ownership is restored on reseed;
policy projections use membership authority. Household command fixtures assert
`rejected_unsupported`, not socket delivery. Ten focused prerequisite tests and a later
17-event focused race selection passed. A larger combined race run timed out during
device execution and remains inconclusive. These bounded results do not certify every
executor packet; see [coverage evidence](../../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits).

---

**Location:** section “Frozen API-key scope discovery pairs”. **Bloem replaces** the corresponding upstream passage with:

`make test-scenario-api-key-scopes` requires six original cases:
`scopes.ok`, `scopes.meaning`, `scopes.shape`, `scopes.sorted`,
`scopes.no_token` and `scopes.error_shape`. The original oracle remains unchanged.
The historical v2 oracle adds the availability flag and Problem Details while
requiring two scope names, descriptions and fixed order. Current v1 discovery keeps
the two legacy scopes, but v2 advertises five supported scopes; those exact-count/order
assertions remain unresolved. Do not truncate supported native discovery to make this
historical packet pass. The real router/provider runs
twelve transport requests with reseeding before and after each transport; all
24 full API-key-table snapshots must prove unchanged rows without exemptions.
Required DSN, the pre-setup occupancy guard and the fixed selector fail closed.
API-key-auth usage metadata remains outside this cohort. These six frozen pairs
are separate from NEW scenarios; no provisioning or scope-enforcement mutation
is exercised.

---

**Location:** section “Avatar uploads and deletion”. **Bloem replaces** the corresponding upstream passage with:

`make test-scenario-avatar` pairs eleven original avatar-upload scenarios and
eight avatar-delete scenarios. Original requests and oracles remain unchanged,
including `avatar_upload.typed_nil_panic` and `avatar_upload.meaning`. Their historical
oracles require an empty v1 500 and a v2 internal-error Problem. The corrected
missing-store configuration now returns 503 on both transports, so these assertions
remain visible failures pending contract-owner resolution. Do not reconstruct the
panic or configure storage for these explicit absent-store cases.
