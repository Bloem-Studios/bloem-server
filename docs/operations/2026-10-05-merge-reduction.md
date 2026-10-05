# October 5 upstream merge reduction

## Result and boundary

This pass restores nine complete files byte-identically to Silo
`1555e5214177453d89e698c2b362fa92c5bd96f8` (build 1116). The declared seam count
falls from 396 to 387, with no additions. The shared Makefile diff falls from 278
to 61 changed lines through an additive owned include with the same commands,
default goal and override order.

The larger tenant/startup slice reduces shared production Go changed lines from
4,930 to 4,450, a net reduction of 480 (9.74%). That measurement includes added
constructor, route availability and inventory-hook costs, rather than counting
only the three restored production files. It compares pinned upstream with the
source before and after that slice. It does not estimate total repository size
or promise future conflict counts.

Tenant group storage and canonical mutations live in owned host modules. All
production and background constructors select the organization-qualified store;
the restored historical upstream store is not a finalized-schema fallback.
Account effective policy retains extended playback/profile limits while existing
explicit group DTOs remain unchanged. Five legacy routes retain authentication
and an explicit missing-tenant availability guard. Startup helpers retain direct
gateway construction, listener ownership and worker shutdown order.

The earlier presentation workers remain bundled local processes. This pass does
not make multi-tenant enforcement optional or publish catalog-installable SDK
plugins. Mandatory tenant/profile/PIN, library and quota authority stays with
the host. IPTV/Xtream extraction remains shelved. No migrations, encrypted
settings, dependencies or client implementation files changed.

## Preserved compatibility decisions

Group updates invalidate accounts with assigned profiles, retaining the prior
revision for legacy membership-only accounts. Deletion moves and invalidates both
memberships and profiles. Empty updates preserve configuration revision;
nonempty same-value writes advance it through the existing timestamp trigger
while unchanged authorization values retain their revision. These verified
existing semantics take precedence over broader wording in the initial design.

Legacy null/empty group arrays both encode null; v2 retains its existing null
versus empty-array distinction. Typed conflicts, entity tags, managed/default
group protections, deletion rollback and active-session behavior are preserved.

## Fresh verification

Commands assume the repository root and the Go toolchain declared in `go.mod`.
DB checks use a disposable finalized database with
`SILO_REQUIRE_TEST_DATABASE=1`; never point fixtures at a deployed catalog.

- Tenant storage/guard/snapshot and adapter regressions pass with real DB
  fixtures, including two-organization reuse, foreign-ID denial, canonical
  mutation, same-value/empty writes, stale conflicts and callback rollback.
- Provider, transport, HTTP and DTO regressions pass, retaining assertions for
  profileless compatibility, quota/profile limits, all five availability guards
  and visible account effective-policy fields.
- The full affected `internal/access`, `internal/accesspolicy`,
  `internal/progresssync` and `internal/policy` packages pass against the
  finalized test database, serializing packages to avoid fixture interference.
- Whole-package graph compilation passes. Startup composition, nil-worker,
  ordering/error and listener guards pass. Independent task reviews have no
  open findings. A nested-parentheses inventory edge was reproduced, fixed and
  re-reviewed; all 19 focused wrapper cases pass.
- Combined seam/case/path, OpenAPI, migration-ledger, offline route, v2 semantic
  contract and API/playback fixture gates pass.
- Fresh native DTO/digest/coverage and settings binding checks pass. Generated
  inventory changes exactly five handler-expression provenance fields, with
  leaf identity, authentication, coverage and all other fields unchanged.
  Migration-ledger refresh reports no changes.
- The enabled embedded-web suite passes: 6,068 tests in 785 files. A fresh
  production frontend build passes. Web source is unchanged by the larger
  tenant/startup slice, so that evidence is reused rather than repeated.

The first web attempt used a borrowed dependency symlink and failed font imports
plus several UI tests. A real local offline frozen-lockfile installation and a
four-worker rerun passed unchanged tests, timeouts and exclusions. No additional
known-failure exclusions were introduced.

## Verification limits

The bundle budget remains an inherited failure: both the original validated
baseline output and this fresh output measure 329,854 Brotli bytes against a
327,874-byte budget, exceeding it by 1,980 bytes. No threshold was raised. This
record does not claim the entire Go suite passes; inherited limitations remain
in the earlier upstream integration record. Database and HTTP fixtures do not
establish native runtime acceptance or a live authenticated administrator
mutation journey.

Final combined contract/lint, image and deployment evidence will be appended
once those gates complete.
