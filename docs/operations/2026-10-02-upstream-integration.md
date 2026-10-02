# Upstream integration verification — October 2, 2026

This is a dated source-integration checkpoint, not a live deployment-health claim.
The normal merge imports Silo `bbf12add23a9af9c25ca055f4b76604810052543`
onto Bloem `e2e55f15e5983a6935491b3e50faa532f775846e`, preserving both
parents and **396 declared upstream-file seams**.

## Changes

The imported source includes external OIDC and LDAP sign-in, device-login
cancellation and provider chains, link invitations, branded email, trickplay,
stored subtitle timing, prepared download improvements, and dependency fixes.
Bloem retains tenant memberships, direct-profile restrictions, Live TV and DVR,
and its own branding. See the [adapter invariants](../architecture/bloem-upstream-adapters.md).

Integration regressions covered the membership projection after an account lock,
provider-created profiles, owner recovery flags, organization-bound invitations,
and password-policy admission before a lifecycle transaction pins its snapshot.
The generated native contracts and settings bindings are current. Native-client
adoption and provider-specific browser acceptance remain separate verification.

## Passing checks

- Go 1.26.8 builds, final `go vet ./...`, and changed-line lint (zero issues).
- All 6,991 enabled web tests in 793 files, with eight workers; web lint,
  formatting, production build, and bundle budget also pass.
- Full database-backed authentication and invitation packages, plus handler
  integration tests and focused lifecycle admission/rollback tests.
- Download unit tests, compatibility-gateway route coverage, native DTO round trips,
  nullable download-episode projection, and Bloem email-brand rendering.
- Fresh database migration through all 575 migrations and the finalized-policy
  upgrade regression. Production migration commands use `migrations.BloemFS`.
- Settings and client generation, client coverage and digest, playback fixtures,
  seam ledger, route inventory, migration ledger, OpenAPI generation and fixtures,
  scenario catalog generation, offline routes, and local-path checks.

## Remaining validation gaps

The complete Go/database/contract matrix is **not green**. These failures remain
visible; no tests, scenario approvals, or contract approvals were disabled to
make the update pass:

- Upstream `TestGetDeviceLogin`, `TestBrandLoaderDefaults`, and
  `TestRenderLayoutEscapesAndPlacesContent` hard-code the default name `Silo`.
  Bloem returns `Bloem`; the generated device fixture and owned mail-brand test
  verify that intended output.
- Five download artifact-recovery database tests still construct accounts by
  inserting the retired `users.download_allowed` column. Bloem stores that policy
  on organization memberships, so those fixtures fail before exercising recovery.
- `TestBloemAdjudicationsPreserveFrozenCatalogs` reports a changed frozen
  `api/api-v1-auth.json` baseline. Regenerating a catalog does not revalidate an
  old acceptance adjudication.
- Comparing API v2 with the previous Bloem base flags the new `not_requested`
  invitation-delivery enum value on create and resend. Those changes come from
  upstream invitation links (Silo #1788); the imported approval file does not
  approve them. A separate semantic comparison with the imported Silo OpenAPI
  reports 13 existing additive Bloem differences and no additional breaking
  changes.

The unbounded web run reproduced a search-test timing failure; the full run with
eight workers passed. The initial Go run also lacked the required database for
four owned account regressions; the final database-backed auth package passed.
