# Upstream reconciliation — 2026-09-08

This reconciliation adopts applicable Silo main changes while preserving Bloem's
native client contracts and fork behavior. It does not authorize deployment.

## Anchors and method

- Bloem base: `7eae6ce1f4344bfc4f14a71ed5880de0bd161f22`.
- Verified fetch remote: `https://github.com/Silo-Server/silo-server.git`;
  `git ls-remote --symref upstream HEAD` confirms `refs/heads/main`.
- Upstream target: `aeb82e1c935336eba7a4a7b22233134dbee13c4c`.
- Common ancestor: `e7677886fe850f545bf19cc198cac267b87d0c90`.
- Reconciled source candidate before generated artifacts and validation fixes:
  `e06e013046f7209b305367d484d4075e12b5dade`.
- Final integration: the final reconciliation merge commit has the tested
  Bloem candidate and the upstream target as parents. Its immutable full hash
  is recorded in the task's completion report; use `git log --merges` to locate it.

Reviewed the September 1–8 lookback and all 38 commits outside Bloem ancestry,
regardless of timestamp. `git cherry` found no patch-identical copies of those
38 commits at the base. That does not exclude partial prior fixes: the discovery
lookback query already used safe numeric interval multiplication; it now uses
upstream's equivalent `make_interval` expression.

The September 3 merge `6f863fd21` incorporates all upstream ancestry through
`e7677886` and records the prior 52-commit reconciliation from `8592f439`.
The August 30 merge `49db10f53` records the preceding 52 commits from `f8556dd0`
through `8592f439`; the August 29 and earlier merges extend that ancestry further.
No older upstream-main commits are missing from the ancestry. Reviewed historical
conflict decisions for profile transcoding, node-routing migration, password
administration, notifications and gateway behavior; these remain preserved.
Ancestry establishes incorporation, not proof that every older line remains
unchanged after subsequent intentional Bloem work.

Changes were applied and reconciled in bounded groups. The final merge records
upstream ancestry over the reviewed tree, including the explicit exclusions below;
it must not be interpreted as byte-for-byte adoption of every upstream patch.
Future syncs must revisit the deferred subtitle work explicitly.

## Commit inventory

| Upstream commit | Date | Disposition | Change |
|---|---|---|---|
| `7c1cb2d3f34e7a37d2864b63735386300e81ef31` | 2026-09-03 | Included | fix(sections): deduplicate items within home rows (#928) |
| `a4c8f4b2f5a85e7db351dfb5407c587fa536d834` | 2026-09-04 | Included | feat(notifications): mint a long-lived display token for Apple push enrichment (#929) |
| `009675b40442074f5228d1d37585b8746c8eab42` | 2026-09-04 | Included | fix(sections): encode day lookbacks as intervals, not text (#916) |
| `eb83e2944873f47fb944ea6d1e6aeaa6f90fc8fc` | 2026-09-04 | Included | fix(web): add webkit video fullscreen fallback for iPhone Safari (#868) |
| `049a86ee6c03456e9310efc1979ab685ae268e5d` | 2026-09-04 | Included | fix(watchsync): match episodes through duplicate series (#651) |
| `fbbc8c262ca2e6ccfb83d547d17eaf6575511365` | 2026-09-04 | Included | fix(historyimport): match localized Plex admin history (#913) |
| `6ce01276a271c20c6bb7e5e5ec672a0854e4bc98` | 2026-09-04 | Included | fix(scanner): prune stale troubleshooting roots on full library scan (#872) |
| `310fcbd2cc007547e617f30a6457027a8e1bd609` | 2026-09-04 | Included | perf(metadata): avoid no-op artwork GC healing (#735) |
| `d4a083e3618f81b9360e7a2f2308dda4d10be144` | 2026-09-04 | Included | fix(jellycompat): harden negotiated session advisory locks (#852) |
| `83b6bdee581a11db6a7fc7a62f3402513afc8852` | 2026-09-04 | Included | fix(web): restore playback auth, history sorting, and AI subtitles (#939) |
| `658be10eb03615f104790fba0431a4d19fd02d15` | 2026-09-04 | Partial; cache and Jellyfin FFmpeg fixes; native contract deferred | fix(playback): make subtitle delivery reliable across playback routes (#938) |
| `3131494e52e069fc1ac10e1a31503c797d09f26e` | 2026-09-05 | Included | fix(userstore): keep repeated watch-progress lookups fast (#946) |
| `94c94772054377ab8e51f0734e27b7fdf49f190c` | 2026-09-05 | Excluded; preserve Bloem contributor workflow and instructions | docs: simplify contribution rules and protect private evidence |
| `1b5833886b78ea525a0cc8e261751a268c7f5e5e` | 2026-09-05 | Included | perf(imageutil): reuse equivalent artwork encodes (#966) |
| `195b801ace6eb4e5b175943840ad7c9f6f57efc5` | 2026-09-05 | Included | perf(scanner): cache classified subtitle candidates (#967) |
| `377ac4c5931b1ec319689517209cdb9bba7fa0e3` | 2026-09-05 | Included | perf(catalog): skip unnecessary playback progress reads (#969) |
| `06fd351b4c03c262bcb515438a60d378a9953fd0` | 2026-09-05 | Included | perf(web): defer playback settings code until navigation (#975) |
| `188ba32c5fad52259ed54ed025be3c469f86bd89` | 2026-09-05 | Included | perf(catalog): infer exact totals for terminal pages (#970) |
| `b236f8f52689aeb2941e5a13e56297f184e99f4b` | 2026-09-05 | Included | perf(catalog): reuse single-item localization reads (#971) |
| `ecde15e121b6efcf41a722d50aa18d4d85046532` | 2026-09-05 | Included | perf(search): remove redundant PostgreSQL ranking window (#972) |
| `db5786d99524cc6f4c1e7b1144fa5830002d9b3c` | 2026-09-05 | Included | perf(search): bound initial Meilisearch candidate hydration (#973) |
| `3be01165b948a6f8c9667bd0c236373bfa6cf0a4` | 2026-09-05 | Included | perf(metadata): reuse wanted-title normalization across matches (#978) |
| `e955532d4905728d09c59417dcb9e9100e8ab887` | 2026-09-05 | Included | perf(web): load hero backdrops near the active slide (#974) |
| `861844402b89bf502c3ca332c8331de72de77123` | 2026-09-05 | Included | perf(web): defer the audiobook player until playback (#976) |
| `1825a93bf77af4dbeb7105d8833b30f16968e03f` | 2026-09-05 | Included | perf(catalog): batch credit photo URL resolution (#977) |
| `aa284b98ea0d57f8c20f31e0be1946c50f4fa7a5` | 2026-09-05 | Included | perf(api): fetch versions without full detail hydration (#979) |
| `3b5d1718a7a3ee1bd5121e4c658b29aa9c4a2393` | 2026-09-05 | Included | perf(metadata): batch completed episode debt deletion (#968) |
| `c71502fdd82e8ee655f34b0175bbe1cd3747f519` | 2026-09-05 | Included | fix(web): prevent player controls overlapping in narrow windows (#980) |
| `142fb5451d3fe6af687fd0dc1849ade0dd876ede` | 2026-09-05 | Included | perf(catalog): cover recently added TV parent lookups (#983) |
| `3b0ba7313e639a98586e26079092a9a6bd86addd` | 2026-09-05 | Included | perf(catalog): avoid hydrating episodes for completion status (#981) |
| `0ed5c359ff1b99b82770d930aadddd3df4892b47` | 2026-09-05 | Included | perf(catalog): hydrate only the requested episode page (#982) |
| `21cf0d81e31edeaaafcb9fe2239ded8ffa2badda` | 2026-09-05 | Included | perf(catalog): use stored parent IDs for episode totals (#984) |
| `723d5a791402c867ef5773a421cd6770b6075b26` | 2026-09-05 | Included | fix(jellycompat): deliver text subtitles to web clients (#863) |
| `2f996f2bc088f280270573677ed7b84cfd34a699` | 2026-09-05 | Included | fix(jellycompat): bound remux cache lifecycle (#866) |
| `dc3f7fa5b955b6351fe6e0089ed78ec0edb110ac` | 2026-09-05 | Included | perf(sections): make Continue Watching stop re-reading watch history (#947) |
| `ae572e11df6cf0ea7c028dd379fdabf8d5025d2f` | 2026-09-06 | Included | docs: sync trademark policy with siloserver.org (#986) |
| `25e460ffd0610ca4e4f1a5e2509c33604a3bf310` | 2026-09-06 | Included | docs: allow wordplay names in trademark policy (#987) |
| `aeb82e1c935336eba7a4a7b22233134dbee13c4c` | 2026-09-07 | Included | fix(artwork): remove availability probes from catalog reads (#992) |

The first trademark update is included historically and superseded by the second
update's wordplay clarification. Bloem's branded README remains intact; the Apple
push contract link is added to its existing documentation section.

## Deferred work and client compatibility

`658be10eb` changes native subtitle artifact format and timing-origin semantics,
adds native embedded-track negotiation, and states that Apple clock mapping needs
coordination. This task explicitly excludes client-repository changes. Its cache
implementation and cache tests are included as prerequisites for `723d5a791`;
Jellyfin configured-FFmpeg forwarding is also included. Its native planner,
protocol, schema, fixtures, native extraction-route and web subtitle
changes are deferred. Existing native artifact semantics remain unchanged. Revisit
this commit in the separate Apple/Android v3 audit before importing the remainder;
do not infer that its upstream ancestry makes that follow-up complete.

Apple push registration gains optional `display_token` and
`display_token_expires_at`; the capability gains optional `display_token`.
Existing access-token display fetches still work. The server-owned Kotlin and
Swift DTO copies and digest are regenerated. Apple adoption of the long-lived
credential is optional follow-up; Android receives no display token and needs no
change. No native client repository is edited. Jellyfin text subtitles have their
own authenticated negotiated route and format tests.

Remote transcode throttling now requires node attestation when enabled. A future
operator rollout must update transcode nodes before relying on that policy; no
running service or production configuration is changed by this source sync.

## Conflict resolutions

- Section deduplication coexists with Bloem promotion-card projection.
- Notification handler retains dismiss, ambience and promotion behavior.
- Viewer access retains direct-profile binding and audience-ticket/direct-profile
  PIN handling while allowing the profile-bound display credential.
- The existing websocket-ticket route boundary and optional tenant/demo guards
  on Apple display fetches are preserved.
- API/player refresh sharing retains Bloem retry policy and captured profile
  identity; old-profile mutations are not replayed under a new profile.
- Conditional episode completion wrappers retain profile lifecycle transaction
  forwarding and its regression tests.
- Jellyfin subtitle extraction uses the configured FFmpeg and format-isolated
  cache; native subtitle decision/timing behavior is unchanged.
- Shutdown cleanup retains the public-port gateway, Live TV cleanup, shared-worker
  ownership tests, fleet reservations and auth telemetry. Live TV encoder presets
  coexist with durable throttle settings.
- All four upstream migrations retain their timestamped filenames. No existing
  migration is renumbered or changed.

## Validation-driven adjustments

Upstream version-query fixtures now remove Bloem-generated organization
entitlements before deleting folders. The existing catalog membership seed uses
the writer appropriate to the disposable database's compatibility/finalized phase.
The episode-completion fixture also seeds an exact organization membership and
profile access group, as required by Bloem. These changes affect test fixtures
only. Existing ambience and promotion assertions now include the already-shipped
UTC timezone and in-playback surface. A stale OPA acceptance assertion now
includes the already-shipped `watch_live_tv` admin permission. The settings
overview now includes the already-shipped Announcements page, matching navigation;
its existing assertions cover the correction. No test exclusion was added.

## Verification

- `GOFLAGS='-p=2' make test-go`: passed across the complete Go suite.
- Configured web suite with the Makefile's existing exclusions and
  `--maxWorkers=2`: 394 test files and 3,217 tests passed. No exclusions were added.
- `make build`: passed, including TypeScript, Vite and Go compilation.
- `go vet ./...`: passed. `go tool govulncheck ./...`: no reachable vulnerable
  symbols; it also reports three imported-package and seven module advisories
  whose affected symbols are not called.
- `golangci-lint run --new-from-rev=7eae6ce1f`: zero issues. Web lint: zero errors,
  180 existing warnings. Changed web files pass Prettier; `git diff --check` passes.
- Settings bindings (all), playback fixtures, server-owned client DTOs, digest,
  client coverage and local-path checks: passed; sync verification has 22 passing
  checks.
- Fresh disposable PostgreSQL with pgvector: all migrations applied. Catalog,
  metadata, history import, scanner, PostgreSQL user store, Jellyfin and API
  and handler database suites passed.
- Independent read-only review of the reconciliation and final integration fixes:
  no confirmed actionable issues.

Repository-wide Go lint still reports 275 existing issues; it is not a clean
baseline. Repository-wide Prettier identifies the unchanged
`web/src/api/adminV2Client.ts`; changed files are clean. Initial resource-heavy
parallel runs exceeded short media-probe/admin-test timeouts; bounded concurrency
runs passed without changing timeouts or adding test exclusions.

The test PostgreSQL instance is disposable and separate from existing services.
No production migration, service restart, deployment or native client repository
change is part of this reconciliation. Scratch logs and generated build output
are not repository deliverables.
