# October 5 upstream integration verification

> Historical snapshot: preserve the dated revisions, results and proposals below.
> For current status, see the [maintained reference](2026-10-07-native-storage-deployment.md).

## Imported source

Bloem merges Silo `1555e5214177453d89e698c2b362fa92c5bd96f8` (build 1116)
from the previous Bloem `9645d0ee688140e0b4f312dc39306db3e79311ab`.
This is a normal two-parent merge of 38 upstream commits, preserving ancestry.

The update includes network identity sign-in, subtitle synchronization and
progress, download preparation administration, local discovery, administrator
policy defaults, player reconnect behavior, and catalog/scanner corrections.
Network sign-in remains opt-in. Bloem tenant membership, protected profiles,
public compatibility listeners, Live TV, and download quota fences are retained.

Seven new SQL migrations are imported without renumbering. Existing migration
bytes are unchanged. The Bloem migration filesystem is used for status,
startup, migration-only, and rollback paths.

## Fresh verification

- Enabled web suite: 6,063 tests in 783 files passed; the existing eight
  Makefile exclusions were unchanged.
- Production web build, Go binary build, and Docker rehearsal image passed.
- Go vet, changed Go lint, and changed web lint passed.
- API semantic contract checks, native DTO generation and coverage, settings
  bindings, playback fixtures, Bloem OpenAPI, route inventory, ledger, offline
  routes, and local-path checks passed.
- Seam validation passed with 396 declared and actual paths, no stale entries.
- Whole authentication, downloads, ambience, promotions, Jellyfin compatibility,
  and Audiobookshelf compatibility packages passed.
- Plugin browser authentication/revocation, rollback compatibility, and client
  DTO round-trip regressions passed.
- The complete database package passed against its own freshly prepared
  disposable database. An earlier shared-database run timed out during fixture
  cleanup while two package runs overlapped; the isolated run resolved that
  verification ambiguity without changing or skipping the upstream test.
- A finalized-schema synthetic upgrade from the previous migration set and a
  fresh install both passed. The upgrade preserved exact saved progress,
  completion state, membership, profile, and server settings fixture bytes.

## Limits and inherited failures

The complete Go suite is not green. Focused tests reproduced the existing
failures on the prior Bloem source: device-login/mail branding expectations,
notification deduplication, password-reset fixture projection, scanner cleanup,
frozen scenario adjudication hashes, section statement-budget fixtures, and
storage-transition fixtures. Scenario executor cases also require the separate
scenario database environment, which the all-package invocation did not supply.
No tests were skipped or frozen hashes restamped to conceal these failures.

New integration failures in authentication/plugin fixtures, Dolby Vision fixture
policy, rollback setup, and subtitle-sync DTO round-trip coverage were corrected
and retested. The inherited failures remain follow-up work rather than evidence
of a fully passing release matrix.

Generated Android and Apple DTO additions are not native UI acceptance; feature
adoption is filed for both platforms in the integration adapter guide. No native
client repository was updated by this server merge.

A complete custom-format database backup and configuration/image recovery
material were captured and the transferred archive checksum verified. The
production-data restore rehearsal was explicitly stopped at the owner's
request: this Bloem instance is a development box, and snapshot recovery is
acceptable. The partial restore was isolated and stopped; it did not alter the
running Bloem database. No claim of successful production-data restore or
migration rehearsal is made.

Deployment and post-start checks are recorded separately after the running
revision is verified.
