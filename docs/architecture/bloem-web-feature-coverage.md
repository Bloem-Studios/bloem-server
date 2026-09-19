# Bloem Server web feature coverage

## Scope and interpretation

This inventory covers the web application embedded in **Bloem Server** (`web/`).
Garden, native-client implementation, billing and a replacement frontend are outside
scope. Retain working Silo screens and APIs; native DTO availability alone does not
justify replacing them.

Baseline: Bloem `95403cedd`, incorporating Silo `0362b6dae`. The tables include the
subsequent embedded-web completion and Xtream work. Deployment of `418a18b7d` completed
on September 19, 2026; it is not exhaustive feature, browser or security certification.
Mounted routes and observed behavior outrank older docs.

The [completion handoff](bloem-web-completion-handoff.md) records the finished milestone,
remaining acceptance and exact continuation steps. [README](../../README.md) and the
[operator guide](../wiki/admin-guide.md) provide the product and operational entry points.

- **Present:** routed controls and contracts exist; not every browser/role is certified.
- **Partial:** implemented controls coexist with the named missing behavior.
- **Boundary:** backend/product support is absent; a button cannot supply it.
- **Shared:** retain Silo's implementation and verify changed Bloem semantics.
- **UI / SEAM / API / CORE:** feature-owned frontend, narrow shared integration,
  Bloem-owned contract/adapter, or underlying Silo behavior respectively.

Administrative context, account login and viewing profile are separate authorities.
A primary household profile is not platform administration. Paths below are relative
to `web/src/` unless prefixed with `internal/`.

## Current deployment and validation

The [September 19 deployment record](../operations/2026-09-19-xtream-deployment.md)
identifies the exact deployed image, both applied migrations, restored-backup rehearsal,
preserved-data checks and image-first rollback. Health/readiness and Chromium login
smoke passed with zero restarts. No provider was configured, so V04/V05 playback and
authenticated Xtream browser acceptance remain open.

Prepublication Xtream checks passed 625 web files / 4,622 tests, then 20 focused
UI tests and TypeScript after the final guide-reconciliation change. Media race
checks passed 2,685 hierarchical events; the explicit contract packet passed
52 transport leaves / 99 events, and the route packet passed 9 events. These are
separate runs, not one aggregate count. The [handoff](bloem-web-completion-handoff.md)
records their order and limits.

Remote CI for `418a18b7d` passed 189 Go packages, other CI jobs and static/artifact
gates, but the executor timed out at 20 minutes. Deployment used an explicitly
approved exception for that exact failure; **CI remains failed**. Historical
acceptance below is not new-provider, Safari or replica-loss evidence.

Current artifacts contain **1,235 routes**, **555 offline routes**, **1,145 migration
ledger entries** (not SQL migrations), **29 client coverage files / 425 types**, and
**515 declared seams** at implementation publication. Older counts below describe
the original web batch. Frozen scenario catalogs, route snapshots and exclusions
were not regenerated to obtain passing checks.

## Administration and access

| ID | Feature / role | Reachable surface and coverage | Boundaries / evidence | Ownership |
| --- | --- | --- | --- | --- |
| A01 | Administrative context / administrators | **Present.** `AdminContextProvider`, context switcher, guarded routes and `adminV2Client`. | Browser switched platform → organization; stale/recreated-context and pending-intent fencing have regression coverage. Full expiry/suspension journey matrix remains broader acceptance work. | Existing UI/SEAM. |
| A02 | Organization lifecycle / platform admin | **Present.** `/admin/platform/organizations` and `/:id`: create, lifecycle, ownership and memberships. | Retained existing screens. Numeric account selection remains a usability limitation, not a missing management API. | Existing UI. |
| A03 | People and profile assignments / organization admin | **Present.** `/admin/organization/people`: filter, page, inspect, group assignment and durable bulk work. | Retained scoped directory; does not imply unrestricted account administration. | Existing UI. |
| A04 | Access groups / organization admin | **Present.** `/admin/organization/access-groups`, shared-route dispatch and scoped hooks. | Default protection and reassignment remain server-owned. Not delegated-role management. | Existing UI/SEAM. |
| A05 | Invitations / organization admin | **Present.** `/admin/organization/invitations`: create, regenerate link, revoke, authoritative readback and one-time handoff. | Rebuilt browser create/regenerate/revoke passed (201/201/204), including confirmation before regeneration, context switching and logout/login without restoring the handoff. Cache exclusion is covered by unit tests; creation/renewal handoffs stay out of query and mutation caches and clear with context changes. Native lifecycle is restricted to user invitations, with captured context and organization revision. No email is sent. | UI + scoped API. |
| A06 | Library grants / organization admin | **Present.** `/admin/organization/libraries`: distinguish owned/granted, confirm suspend/restore/withdraw. | Live all three transitions/readback passed. Mutations use the grant's security revision; withdrawal is not reversible here. Does not create new platform grants. | UI, existing API. |
| A07 | Activity/audit / organization admin | **Present.** `/admin/organization/activity`: redacted lifecycle/entitlement summaries and cursor pagination. | Browser read a seeded audit event. Database tests cover tenant isolation, redaction and pagination ties. Only events already recorded by these ledgers appear; not a claim that every operation emits audit data. | UI + scoped API + route/nav SEAM. |
| A08 | Entitlement templates / platform admin | **Present.** `/admin/platform/entitlement-templates`: editor, revisions and lifecycle. | Retained existing structured workflow. | Existing UI. |
| A09 | Effective entitlements / platform admin | **Present.** Direct-account and organization entitlement panels: inspect, dry-run, apply. | Retained revision/preview binding; a successful write is not proof of effective media access. | Existing UI. |
| A10 | Bulk policy/cohorts / administrators | **Present.** Direct-account bulk page, organization People drawer and policy cohorts. | Retained role-bounded jobs, progress and cancellation; backend owns per-target outcomes. | Existing UI. |
| A11 | Authorization explanations / administrators | **Present.** Organization Policy Decisions; platform policy editing, simulation and history. | Organization audit is a separate surface. Organization authority never implies Rego authoring. | Existing UI/SEAM. |

## Viewer identity and Live TV

| ID | Feature / role | Reachable surface and coverage | Boundaries / evidence | Ownership |
| --- | --- | --- | --- | --- |
| V01 | Account/profile/PIN/device sessions / households and viewers | **Shared.** Login, profile picker, settings, activation and device/session pages. | Fresh real-server setup and account login passed after restoring two transactional adapter interfaces. Existing profile/PIN boundaries remain. | Existing UI; additive setup adapters. |
| V02 | Direct-profile credentials/sign-in / household manager and viewer | **Partial / Boundary.** Profiles settings now expose credential status, set/rotate and disable with current account password and explicit confirmation. | Rebuilt two-tab acceptance passed: set/rotate 204, stale disable 409, current disable 204; the newer direct session stayed valid after conflict and was revoked after current disable. Writes require the reviewed positive credential revision under the row lock. Direct-profile **browser login is not supported**: its default-deny allowlist does not admit Silo v2. SSO-only reauthentication is absent. | UI + account-scoped API; no allowlist expansion. |
| V03 | Shared-device pairing/delegated admin roles | **Boundary.** Capability booleans remain false. | Account-device activation is not shared-device profile pairing; access groups are not delegated roles. | Backend/product decision. |
| V04 | Live TV guide/watch / permitted viewer | **Present.** `/livetv`, `/watch/live/:channelId`, capability gate, tune/renew/release and HLS player. Operational requests use `/api/bloem/v1/livetv`. | Native-HLS fallback requires browser support and server-issued `st`; account credentials are never placed in stream URLs or forwarded to foreign origins. Actual tuner, Safari and owner-loss playback remain unverified. | Bloem UI + small request-policy SEAM. |
| V05 | Tuner/guide/transcoding administration / acting server administrator | **Present.** `/admin/livetv`: discovery, lineup, sources, recordings and settings, plus encrypted Xtream live-provider setup and provider-bound XMLTV. | Xtream credentials bypass mutation caches; unconfirmed creation requires explicit reload, and removal confirms channel/DVR consequences. Protocol, PostgreSQL, component and fixture-encoder tests do not prove real-provider playback. See [Xtream limits](xtream-live-tv.md). | Existing Bloem UI + owned Xtream adapters. |
| V06 | One-off DVR/recorded playback / permitted viewer | **Partial / Boundary.** Guide and manual channel/time scheduling, status/errors, cancellation, and links when `library_item_id` exists. | Rebuilt browser acceptance passed real scheduling/cancellation and readback, including committed writes with lost responses, failed reads, blocking through explicit reload and a required fresh draft. Both created rows were cancelled; authority stayed unchanged. The recorder does not populate the catalog link: automatic import/playback remains a backend boundary. Completed unlinked rows explain manual library scanning rather than claiming playback readiness. | UI; catalog-import integration remains backend work. |
| V07 | Recurring DVR / permitted viewer | **Present with backend limits.** `/livetv?tab=series-rules`: title/channel/new-only rules, exact guide-series selection, list and confirmed removal; available with zero channels. | Live create/remove and exact-series flow passed. No rule-update API; `keep_last` is stored but not enforced, so no retention control is offered. Removing a rule does not cancel its scheduled recordings or delete files. | Bloem UI, existing native API. |

## Engagement, compatibility and operations

| ID | Feature / role | Reachable surface and coverage | Boundaries / evidence | Ownership |
| --- | --- | --- | --- | --- |
| E01 | Announcements / platform admin | **Present.** Existing announcement targeting, preview, publish and withdraw settings. | ID-based targets retained; no organization-admin publication authority invented. | Existing UI. |
| E02 | Alerts/announcements / viewer | **Present.** Existing Notifications page and realtime consumer retained. | The earlier upgrade 401 came from contextless viewer revalidation losing tenant authority. The wired Bloem adapter now captures and revalidates the original principal and tenant through the shared ticket store; PostgreSQL/race regressions pass. The rebuilt browser received a real v2 socket hello and remained healthy for 18 seconds, beyond periodic authority revalidation, with no relevant HTTP/runtime errors. | Owned adapter + optional shared ticket identity field. |
| E03 | In-playback campaigns / viewer | **Present.** Existing `PlaybackEngagement` in the Silo player. | Retained cadence/control suppression and existing player lifecycle. | Existing UI/player SEAM. |
| E04 | Home/detail/pre-playback campaigns / viewer | **Present.** `BloemHome`, `CampaignPlacements`, item wrapper and pre-playback chrome integration. | Live publication → opted-in home delivery passed. Home defaults off; children suppressed; dismissals nonreplayed; immediate Continue remains available and admitted playback stays mounted. Failure/profile-switch behavior has component coverage. | UI + narrow home/detail/player SEAMs. |
| E05 | Campaign authoring / platform admin | **Present.** `/admin/platform/engagement`: typed targets/placements, UTC dates, review, publish/edit/delete and artwork upload. | Rebuilt create/edit/delete and readback passed (201/200/204). Configured public S3 acceptance passed: PNG upload 201, exact bytes/MIME/dimensions, anonymous 200/ETag 304 and decoded Home artwork. Deployment and explicit-profile inclusion/exclusion passed. Native adapter reuses the registry; no revision-locking contract exists and the editor says so. | UI + native authority API + route/nav SEAM. |
| E06 | Seasonal presentation / viewer | **Present.** `SeasonalLayer`: snow, banner/sprite artwork, timing, opt-out, reduced motion and playback suppression. | Rebuilt current-organization delivery returned 200 and public branding excluded the targeted pack; earlier opt-out passed. `seasonal_viewer_v1` identifies mounted authenticated native `/ambience`, which requires a verified current-tenant profile. Single-snapshot filtering excludes other memberships, including concurrent retargeting. Login/fallback stays public-only; requests and caches bind session/profile/PIN generation. | Bloem UI, existing shell SEAM. |
| E07 | Seasonal authoring / platform admin | **Present.** Engagement page pack editor, schedules, annual repeat when supported and asset upload. | Rebuilt create/edit/delete and readback passed (201/200/204). Configured public S3 banner/sprite uploads, public delivery/cache validation, decoded Home images and organization/public filters passed. MIME, size and declared-dimension rejection passed. Public S3 remains required; registry concurrency remains last-write-wins. | UI + native authority API + route/nav SEAM. |
| E08 | Compatibility applications / platform admin | **Present.** `/admin/platform/compatibility`: enroll, enable/disable, rotate/revoke, command display. | Existing commands are displayed, never executed by the browser. Distinct from built-in Jellyfin/ABS settings. | Existing UI. |
| E09 | Branding/LAN/playback deployment controls / platform admin | **Present.** Build-time identity, LAN toggle and existing Playback controls. | Header-authenticated media is not a general multi-replica switch. Deployment prerequisites remain explicit. | Existing settings/branding SEAMs. |
| E10 | Fleet and shared media / viewers/admins | **Shared; acceptance incomplete.** Retain Silo catalog, player, requests, downloads, readers, music, nodes and plugins. | No replacement frontend. A real database/policy regression covers active/suspended/restored/withdrawn grants, campaign delivery and denied-library 404. Earlier simultaneous home 500s coincide with browser teardown; cancellation is a likely explanation, not proven from those logs. No production home fix was made. Fresh rebuilt reads completed all five v2 home sections with 200, including one promoted item; the real v1 promoted consumer payload and visible card also passed. No active home blocker was observed in that fixture. Actual media/encoder/fleet acceptance needs appropriate fixtures. | Existing Silo consumers; investigate concrete integration failures separately. |

## Implementation and authority invariants

- Admin drafts/remount keys and mutation snapshots include context generation. A
  stale intent cannot silently acquire replacement or another organization's credentials.
  Confirmations capture applicable revisions. Reload after attempted writes; a lost
  response can conceal a committed mutation. No optimistic destructive update.
- Profile-scoped work binds session/profile/PIN generation. Writes are not queued
  offline or automatically replayed after transport/authentication failure. Late tune
  responses still release their original viewer's tuner reservation.
- Native invitation renewal/revocation is filtered by organization and `role='user'`.
  Renewal invalidates the old claim link; neither creation nor renewal sends email.
- Audit reads union only the selected organization's `admin_audit_events` and
  `entitlement_audit_events`. No before/after JSON, credential documents or unrestricted
  platform activity is exposed. The fixed page size is 50.
- Direct-profile management reuses household/PIN checks and existing credential
  services. It requires a non-impersonated account login and current local password
  for writes. Positive `expected_revision` is compared under the profile row lock; a
  conflict preserves newer credentials and sessions. Secrets stay out of query/mutation
  caches and clear after attempts.
- Home promotions are off by default, scoped to browser origin/account/profile.
  Blocked storage permits tab-local choice. Pre-playback is always skippable immediately;
  once admitted, campaigns cannot unmount the player. Artwork/link validation rejects
  credential-bearing or unsafe URLs; local artwork is limited to the asset route.
- Events tickets carry a server-owned binding to the original principal and tenant.
  Upgrade and periodic checks revalidate that authority without selecting a new tenant
  or weakening shared expiry, PIN, origin, fingerprint or revocation checks.
- Manual, guide and cancel DVR actions share captured-authority reconciliation. Failed
  readback keeps actions blocked until explicit reload succeeds; background refresh
  does not clear that state. The in-memory QueryClient guard does not promise
  exactly-once execution across reloads, tabs or clients.
- Native Live TV uses an account-owned profile lookup for primary-admin overrides.
  Secondary, child and unknown profiles cannot manage another viewer's recordings.
  Native returned media URLs and stream-token-only HLS delivery have mounted real
  PostgreSQL/fixture-encoder coverage; raw tuner proxy delivery retains normal auth.
- Campaign/season adapters reuse existing registries under native platform context,
  preserving the actor. They do not invent revision locks or organization publishing.
- Setup remains atomic. `cmd/silo/bloem_setup_transaction.go` forwards transactional
  ownership activation; `internal/notifications/bloem_profile_transaction.go` preserves
  transactional profile creation through decoration, failing closed if unavailable.
  Both account transaction entrypoints reject unsupported default-profile providers
  before inserting accounts, provisioning memberships or opening a user store. The
  returned store must still support transactional profile creation. PostgreSQL and
  its notification decorator preserve that support; SQLite creates no files on early
  rejection. Requests that do not create a profile retain their existing behavior.

## Backend and merge evidence

Bloem-owned implementations:

- `internal/api/handlers/bloem_organization_workflows.go`,
  `internal/invitations/bloem_lifecycle.go`, `internal/tenancy/bloem_audit_read.go`.
- `internal/api/handlers/bloem_profile_credentials.go`,
  `internal/auth/bloem_profile_credential_status.go`,
  `internal/auth/bloem_profile_credential_revision.go`.
- `internal/api/router_bloem_engagement.go`,
  `internal/api/handlers/bloem_engagement_context.go`.
- `internal/api/handlers/bloem_events_socket_v2.go`,
  `internal/api/handlers/bloem_seasonal_viewer.go`, `internal/ambience/bloem_viewer.go`.
- Existing native Live TV mounting: `internal/api/router_bloem_client.go` and
  `internal/api/router_livetv.go`. Do not restore unmounted v1 routes to fix a consumer.

Shared seams are recorded in `contracts/seams.txt`: route/navigation registration,
request policy, optional home supplemental rows, pre-playback placement, and the existing
promotion/ambience actor extraction, locked credential revision comparison and optional
server-owned socket identity binding. The two setup fixes are additive adapters;
the shared account provisioner adds the early provider check above. Shared test seams
cover migrated invitation fixtures, platform capability expectations, tenant reseeding
and catalog execution prerequisites. Additive executor helpers, including lifecycle
and local-avatar fixtures, do not modify files present in upstream; their shared runner
hooks are ledgered. The original web batch added no SQL migration; later policy-array
and Xtream migrations are now applied in the recorded deployment. No module-path
rename, replacement player or new Silo v1 business route was introduced.

Native Apple/Android implementation is outside this task. Additive management tokens
and native routes are documented for future clients; existing clients ignore unknown
capabilities. Jellyfin playback/DVR service semantics are unchanged. Do not advertise
browser direct-profile sign-in, shared-device pairing or enforced DVR retention.

## Acceptance evidence and limits

This section preserves the original September 18 web batch's evidence and counts.
Use the current deployment section above for the later Xtream publication and CI
result; do not treat these older browser runs as authenticated Xtream acceptance.

Earlier acceptance used a disposable database and loopback server with synthetic
library/channel/guide/audit fixtures. Those Chromium runs exercised:

- Fresh setup/account login and profile-credential save/readback/disable.
- Grant suspension, restoration and withdrawal with confirmation/readback.
- Invitation creation, one-time handoff, link rotation and confirmed revocation.
- Scoped audit delivery, administrative-context switching and narrow audit layout.
- DVR rule create/remove with no channels, guide-to-exact-series creation, manual
  scheduling/readback and narrow DVR layout.
- Campaign and seasonal review/publication/readback, opted-in home delivery and
  seasonal opt-out.

Automated checks cover denied/unavailable states, tenant/role fences, stale authority,
nonreplay, conflicts, URL safety, pagination/redaction and player admission stability.
They are **not** substitutes for real tuner/media, Safari, prolonged playback, fleet
loss, full keyboard usability or every pending-operation identity-switch journey.

The final integrated web gate passed **623 files / 4,606 tests**, retaining the existing
exclusion list unchanged. TypeScript and touched-file ESLint/Prettier passed. The first
final run exposed a reproducible admin search focus race: deferred selection
overwrote the first typed character. Immediate selection of an already-open input,
with existing autofocus for initial mounting, passed a deterministic regression and
the full rerun. No exclusion was added. The frontend production build and disposable
loopback backend build both passed; the backend packages those assets.
The final backend build includes the provider preflight and uses the existing disposable
bootstrap overlay. Its artifact metadata was verified against the current worktree
revision and dirty state. It was not started: the browser and configured-S3 evidence
below predates that final preflight build.

Earlier focused Go race/database checks passed **284 tests and subtests across 11
packages**; the Redis cross-node ticket test skipped because no test Redis URL was
configured. The full tenancy package passed. The later auth/API/fixture continuation
passed **177 test events**, with zero failures or skips, using actual source under
race detection and **no Go overlay**. This includes the full invitations package:
**30 top-level tests / 60 passing events**. These overlapping runs are separate
evidence sets, not an aggregate test count or an all-Go-suite claim.

The old invitation fixture inserted isolated users while membership writes reached
public tables, causing `organization_memberships_account_id_fkey` errors. An archive
verified against all **8,112 tracked HEAD files** independently reproduced the baseline
failures. Fully migrated disposable databases now preserve real foreign keys, triggers
and schema-qualified functions, with cleanup confined to the generated database.
Repaired fixtures exposed a separate baseline auth defect: unsupported providers were
checked after account work, and SQLite had already created files. The early provider
check above passes a **24-case** PostgreSQL matrix across both transaction entrypoints,
including unchanged row/sequence counts, no membership/store calls or SQLite files on
rejection, supported commits and retained store-level validation. No source public
tables or existing data were repaired to make tests pass.

Corrected API fixtures create a real enabled tenant-owned library with explicit profile
access and use a valid absent playback UUID. Strict 200/404 and authority assertions
remain. At that original checkpoint, malformed playback IDs still produced the
durable-store cast failure and 503. The later deployed security/setup follow-through
corrected malformed playback-stop IDs to 404 before storage lookup. Resource
capability tests assert Linux support versus non-Linux unavailability while preserving authorization and cache
checks. The setup rollback stub now implements the transactional membership interface
needed to reach its intended injected failure; that earlier failure was independently
reproduced at HEAD. No production allowlist, authorization guard, exclusion or assertion
was relaxed.

The route-inventory classifier recognizes the native Live TV profile and stream-token
guards, platform context, current-tenant resolver and credential rate limiter. Its
full package race test and subsequent focused classification regressions passed.
The final documentation-inclusive seams gate passed with **479** entries. Local paths, native/v2 OpenAPI, v2
contract/fixtures/web types, committed v2 router artifact, playback fixtures and web
settings bindings passed.
The final local-path gate completed under shell tracing with no leak diagnostics;
plain invocations hit an unresolved host-shell `Bus error`, sometimes despite a zero
exit status. The scanner itself was not changed and those plain runs are not counted
as successful checks.

Documented generators refreshed the route inventory to **1,234 routes**, offline
inventory to **555 routes**, and migration ledger to **1,144 entries**. All three
artifact gates pass. The 17 added routes and 16 new web call sites were reviewed;
existing migration decisions and sibling consumer evidence were preserved. There are
no removed routes or unclassified middleware. Server-owned Go/web/Kotlin/Swift settings
bindings are current. Kotlin and Swift each add 27 generated lines for nine existing
language aliases across audio, subtitle and metadata suggestions; no manifest change
or sibling-repository edit was needed.

The five tier-1 catalog gaps are covered by **46 passing real-router scenarios**:
profile lookup, both profile-login registrations, household playback commands and
device remote-control capabilities. All **337 existing scenarios and their row
metadata** in the three edited catalogs remain unchanged. The catalog gate passes.
Executor reseeding restores tenant/bootstrap ownership and uses membership-backed
policy projections; synthetic playback sessions refresh tenant IDs after each reseed.
The command fixture asserts `rejected_unsupported`, not socket delivery. Earlier
executor runs separately failed three frozen v1 lifecycle validation cases (400
expected versus unavailable-store 503) and both transports of the avatar-enabled
profile-list assertion. Real fixture prerequisites now close those five failures:
**98 scenario/transport results pass with zero failures or skips**, comprising the
46 additions, 20 profile-list results, 26 device-list results and six lifecycle
validation results. Focused fixture/runner regressions passed 57 test events. These
are bounded packets; the full executor and other avatar-upload packets were not run.
Final integration independently passed 17 prerequisite test events under race detection.
A combined race run also completed profile-list acceptance, but reached its 120-second
package timeout during device-list execution; that combined run remains inconclusive.

Lifecycle selection uses production's route registry to require a reachable store
before validation, including transport-specific follow-up requests. Ready-store 400
and unavailable-store 503 behavior remain separately checked. Missing prerequisites
produce an explicit execution failure before HTTP, not a skip or a rewritten oracle.
The profile-list fixture supplies the supported temporary filesystem avatar store
and real signer/resolver only for that row. Real v1/v2 uploads, persisted ownership,
decoded 256px WebP delivery, private cache headers, unauthenticated/cross-account
refusals, signature scope, deletion and absent-storage teardown all pass. No handler,
phase policy, route allowlist or frozen expectation changed. Scratch cleanup verified
no direct credentials/sessions, remote capabilities, synthetic commands or uploaded
avatar references remained. These fixture failures were reproduced in the worktree
and attributed through unchanged relevant HEAD source and frozen objects; unlike the
invitation failure, they were not reproduced in a separate whole-HEAD checkout.

The coordinator completed these checks against the rebuilt normal loopback server:

- All five v2 home sections returned `200`, including one promoted item. The actual
  v1 promoted consumer payload and visible campaign card both passed. Native seasonal
  delivery returned `200`; a real v2 events socket received `hello` and stayed healthy
  for 18 seconds past initial upgrade, beyond the 15-second authority check. No relevant
  HTTP or browser runtime errors occurred.
- Credential writes returned `204`, `204`, stale `409`, then current `204`, with
  revisions 4/5/6. The newer direct session returned `200` after stale rejection and
  `401` after current disable.
- Invitation create/regenerate/revoke returned `201`/`201`/`204`. Confirmation preceded
  regeneration. Handoffs cleared after closing and did not return after administrative
  context switching or actual logout/login; persistent/session storage held no claim
  secret. Query/mutation-cache exclusion is separately proven by unit tests.
- Campaign and seasonal create/edit/delete returned `201`/`200`/`204`, with readback.
  Authenticated seasonal delivery included the current organization's pack and public
  branding excluded it. In that earlier unconfigured run, multipart upload returned
  `503`: public S3 was absent,
  and local catalog/branding artwork storage is not connected to the ambience service.
  That historical result demonstrated the configuration requirement; the later
  configured-S3 run below supplies successful upload acceptance.

Configured-S3 acceptance subsequently passed against a disposable local MinIO service
and the real loopback backend. Campaign and seasonal banner PNGs (320×180), and a
seasonal sprite PNG (32×32), each uploaded with `201`; exact bytes, MIME and dimensions
matched on anonymous `200` reads, with `304` for matching ETags. The Home page decoded
the campaign, banner and sprite images. Invalid MIME returned `415`, the UI blocked
an over-8-MiB upload before POST, the server independently returned `413` for oversize
content, and mismatched declared aspect ratio returned `400`. Deployment and explicit
campaign audiences, current-organization seasonal inclusion/public exclusion and
deployment-wide public seasonal inclusion all passed. Captured authority stayed stable
and there were no browser runtime errors.

Cleanup removed the fixture registries, asset rows and S3 objects; subsequent public
reads returned `404`. All ten temporary settings keys were restored exactly, including
encrypted ciphertext and the original absence of the artwork-backend row. MinIO was
stopped and removed, and the loopback server returned healthy. This proves configured
S3 upload/delivery with synthetic PNGs, not physical media or tuner acceptance. The
normal SSRF guard blocks a loopback tuner fixture; it remains unchanged. A permitted
isolated network and actual media/tuner/Safari checks remain separate acceptance work.

Initial browser failures were corrected in the acceptance scripts: fresh taste
onboarding needed its Skip action, v2 section output uses the direct `items` body,
raw campaign content belongs to the actual v1 consumer payload, and switching from
organization to platform context needed navigation to the engagement page. No
production home or engagement patch was made for those script failures.

The coordinator then restarted the latest build and passed DVR reconciliation
acceptance against the real disposable backend. Scheduling returned `201` with
readback. A second POST committed with `201` before its browser response was dropped;
injected `503` reads and a failed explicit reload kept actions blocked. They remained
blocked until a successful explicit reload completed, then required a fresh draft.
Cancellation returned `200` with readback; a second committed DELETE with a lost
response also reconciled successfully. The run observed two POSTs and two DELETEs,
with nine deliberately injected GET `503`s. Both created rows were cancelled, cleanup
passed, captured authority stayed unchanged and no browser runtime errors occurred.
These were real backend writes with injected transport/readback faults. This check
does not establish exactly-once scheduling, actual media recording, tuner or Safari
playback, automatic catalog import or retention enforcement.

Commands assume the repository root:

```sh
make test-web
(cd web && pnpm exec tsc --noEmit && pnpm run build)
GOMAXPROCS=2 go test -race -p 2 ./internal/invitations ./internal/tenancy
GOMAXPROCS=2 go test -race -p 2 ./cmd/silo ./internal/notifications -run 'TestBloem.*(Setup|Notification)'
bash scripts/verify-seams.sh
git diff --check
```

Database tests require the documented disposable test database environment. Keep
credentials, generated browser states, screenshots and local fixture scripts out of git.
The deployment record above documents the completed rollout; these historical
acceptance commands do not imply full release or playback certification.

## Related authorities

- [Fork provenance and merge policy](../../FORK.md)
- [Native API reference](../bloem-api-reference.md)
- [Security foundation](bloem-security-foundation.md)
- [Multitenant administration](multitenant-administration.md)
- [Live TV client access](live-tv-client-access.md)
- [Cluster media ownership](bloem-cluster-media.md)
- [Playback overlays](playback-overlays.md)
- [Seasonal scheduling](seasonal-scheduling.md)
- [Compatibility applications](../operations/compatibility-applications.md)
