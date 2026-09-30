# Bloem adapters over Silo

Bloem retains Silo's module path, Git ancestry, environment names, and protocol
identifiers. New fork behavior belongs in owned files, with small hooks in the
upstream files listed by `contracts/seams.txt`. The ledger currently contains
396 paths; `make verify-seams` checks that it stays accurate. See
[fork policy](../../FORK.md) and the [documentation index](../bloem/README.md).

## Migration filesystem

[`migrations.BloemFS`](../../migrations/bloem.go) wraps Silo's embedded migration
filesystem. The status, up, down, and normal startup paths in
[`cmd/silo/main.go`](../../cmd/silo/main.go) all use that wrapper. Files pass
through unchanged except the explicitly adapted request-group migration.

Bloem's finalized membership authority has moved live policy out of `users`.
When upstream migration `20260926181741` converts legacy blocked request limits,
the adapter updates `organization_memberships.requests_allowed` instead. The
legacy request limit applies to the account across its memberships; the migration
preserves that scope, increments affected policy revisions, and uses the
transaction-local policy-writer marker. Before finalization, the original account
update and existing authority fences remain in effect. The adapter does not
bypass those fences or finalize an installation itself.

The replacement requires an exact match for the expected upstream statement.
An upstream edit therefore requires review. Do not renumber migrations, mutate
the version ledger to skip a failure, or run the unadapted SQL on a finalized
Bloem database.

Fresh empty-database migrations do not exercise the finalized-schema upgrade.
The regression builds that state, verifies both memberships of a blocked account,
and applies the remaining migrations:

```sh
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/database \
  -run '^TestBloemRequestGroupMigrationAfterPolicyFinalization$' -count=1
```

Set `SILO_TEST_DATABASE_URL` to a disposable test database first. The fixture
creates and drops its own child database; never point it at a deployed catalog.

## Profile-aware extensions

- [`SetProfileLimitProvider`](../../internal/playback/bloem_profile_limits.go)
  supplies profile-aware playback limits while the upstream `SetLimitProvider`
  signature stays available to ordinary Silo callers.
- [`downloads.CapabilityForProfile`](../../internal/downloads/bloem_capability.go)
  selects the profile-aware extension when a service implements it and falls
  back to the upstream account-only capability interface otherwise.
- Membership and profile policy continue to be evaluated in the acting
  organization. Server ownership, organization ownership, and a household's
  primary profile are different authority boundaries.

These adapters preserve upstream call shapes; they do not grant access when a
profile or organization policy denies it. Prefer the same pattern for later
upstream changes rather than widening every shared interface and test double.

## Embedded web

[`web/bloem-brand-plugin.ts`](../../web/bloem-brand-plugin.ts) owns product-copy
adaptation. [`web/product-brand-regex.ts`](../../web/product-brand-regex.ts)
handles the corresponding test regular expressions without renaming protocol
identifiers. Bloem administration and Live TV pages load through lazy routes so
their code is not all part of the initial application load.

The bundle-budget runner in
[`web/scripts/bloem-bundle-budget.mjs`](../../web/scripts/bloem-bundle-budget.mjs)
uses the upstream validation functions with Bloem's separate
[`web/bloem-perf-budget.json`](../../web/bloem-perf-budget.json). Keep that
allowance explicit; do not change upstream thresholds merely to hide fork costs.
Seasonal branding reads are cached by server, while authenticated profile
ambience retains its own authority and scheduling rules.

## Updating documentation and contracts

The GitHub front page and contributor guide are `.github/README.md` and
`.github/CONTRIBUTING.md`. Detailed differences live in owned architecture guides
and `docs/bloem/overlays/`. Root Silo documents can then remain identical to
upstream. When Silo removes a documentation page, fix Bloem's incoming links and
identify the replacement instead of describing a missing file as preserved.

Moving a fork note out of an upstream document may affect generated contract
digests. Regenerate and review the applicable artifact; do not treat a link fix,
successful build, or deployment probe as evidence that a contract gate passed.
