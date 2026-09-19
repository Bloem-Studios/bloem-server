# Embedded-web completion handoff

> Historical embedded-web checkpoint. Subsequent security/setup follow-through shipped
> as `0366a7b64`, including Go 1.26.8, pre-snapshot setup admission and the malformed
> playback-stop ID correction. Statements below about no production rollout or the
> old malformed-ID failure describe this earlier checkpoint, not current behavior.
> Deployment and recovery results belong in the protected operator records; this
> document does not certify later recovery drills or a fully green Go/scenario suite.
>
> Later schema follow-through includes the [policy-library array migration](core-id-range.md#apply-and-rollback).
> Its writer-quiescence, pool-recycling and lossless rollback requirements are separate
> from this original web batch's no-new-migration statement.

Checkpoint: 2026-09-18. This document accompanies the embedded-web completion
changes. Baseline: Bloem `95403cedd`, incorporating Silo `0362b6dae`. Use Git history
for the commit containing the implementation and this checkpoint.

## Xtream and CI follow-through — 2026-09-19

The follow-through after `5f70bd5d7` adds [Xtream live providers](xtream-live-tv.md):
encrypted account storage, live channel import, provider-bound XMLTV, shared physical
connection admission, protected raw/HLS/DVR inputs and embedded-web setup. The new
creation route is `POST /api/bloem/v1/livetv/tuners/xtream`; support is advertised by
`xtream_supported`. No Silo v1/v2 alias or sibling-client provider administration is
introduced. Server-owned Kotlin/Swift capability bindings and contract artifacts
were updated without editing sibling repositories.

The final review found and corrected missing cipher wiring in the native fallback
service. Cipher publication is atomic because router wiring can repeat after the
task manager starts. Mounted tests cover account/admin, selected primary profile,
PIN-proof binding, membership suspension and delivery/direct-profile refusal. The
web provider and guide forms retain uncertain-write guards across remounts and
require explicit reconciliation, without replaying creation.

The same work repairs persisted timestamp precision in catalog export publication,
download-quota pool starvation through same-connection advisory-lock callbacks, and
a Linux ABS deadline fixture's socket-buffer pacing. The ABS fixture retains its
public-timeout assertion and a failing negative control without rolling deadlines.

Completed validation, in execution order:

- Full media packages (`livetv`, `playback`, `jellycompat`) passed under race
  detection: **2,685 hierarchical pass events**, no skips.
- Full web gate passed **625 files / 4,622 tests**, exclusions unchanged. After the
  final guide-reconciliation change, **20 targeted Xtream UI tests** and TypeScript
  passed; the full web suite was not repeated.
- Full handlers/API/apiv2 race run recorded **4,878 pass / 11 skip events**. Its only
  failing leaves were the two existing frozen v1 route-contract assertions; the API
  package failure adds a third aggregate failure event. Skips include unavailable
  dashboard/Redis and external Watch-schema prerequisites and the inherited
  subtitle-default contract case.
- After cipher-wiring review, mounted Live TV/Xtream race repetition passed **112
  events**; focused Xtream protocol/storage/guide/cipher repetition passed **127**.
  Earlier encoder lifecycle repetition passed **101**, and migration regression **6**.
- Builds, affected vet, artifact/digest/DTO/coverage/seam gates, migration syntax and
  the plain local-path gate passed. The vulnerability scan found no reachable
  vulnerabilities. Scoped Go lint had **158 inherited findings, zero new/changed-line
  findings**; web lint had **zero errors / 187 existing warnings**.

This is **not CI-green or deployment certification**. The expanded scenario race
run timed out at 20 minutes while still progressing; a subsequent non-race run was
interrupted, not completed. Neither proves closure of the published CI timeout.
Frozen route/scenario source snapshots and test exclusions remain unchanged. The
later explicit downstream adjudications below change current Bloem expectations,
not those historical baselines. Further validation is limited to targeted checks,
not another broad suite rerun.

A subsequent focused executor repair binds router maintenance to test lifetime and
removes a redundant pre-reseed around paired `FreshState` mutations. Regression
checks preserve before/after isolation and both frozen transport assertions; ten
hierarchical events passed with no skips. `make test-go` now has a bounded 20-minute
package timeout, based on the earlier completed 872.712-second executor run rather
than the incomplete race attempt. This removes the known ten-minute budget mismatch;
a fresh full run and remote CI outcome remain unverified. See
[scenario runtime and lifetime](scenario-acceptance.md#runtime-budget-and-fixture-lifetime).

The final [contract adjudication](bloem-contract-adjudications.md) closes the
inspected local contract failures: 30 recorded transport decisions passed **52
real-router transport leaves / 99 hierarchical events**, with no skips. Original
catalogs are SHA-pinned and unchanged; negative checks reject changed requests,
principals, rows and expectations, and reject API-key disclosure. The route guards
now require all 28 exact native Live TV replacements and three existing upstream
OAuth additions while preserving the frozen snapshots and rejecting unrelated
drift. The focused route packet passed. No production behavior was weakened or
changed by this adjudication; a fresh full run and remote CI are still unverified.

At this checkpoint no follow-through commit, push or deployment had occurred;
production remained on `0366a7b64`. Xtream and policy migrations still require a
separate restored-backup rehearsal before any production application. Real-provider,
current-feature browser/accessibility, Safari, decoded-media and multi-replica
owner-loss acceptance remain open. Fixture encoder tests are not decoding evidence.

## Where work stopped (original web checkpoint)

The implemented workflows and bounded acceptance checks are complete. The next
milestone is broader media/browser and deployment acceptance, not a replacement
frontend or another authorization model. No production deployment was performed.

See the [28-area coverage matrix](bloem-web-feature-coverage.md) for each feature,
its authority boundary, evidence and remaining backend limits. The
[API reference](../bloem-api-reference.md),
[security foundation](bloem-security-foundation.md) and
[Live TV client contract](live-tv-client-access.md) describe the maintained behavior.
Garden, native-client implementation and billing remain outside this work.

## Completed work

- Organization invitation creation, regeneration, revocation and one-time handoffs;
  grant suspension/restoration/withdrawal; scoped, redacted activity pagination.
- Platform campaign and seasonal authoring, existing viewer placements, authenticated
  current-organization seasonal delivery, opt-outs and nonblocking pre-playback cards.
- Household direct-profile credential management with account reauthentication,
  locked credential-revision checks, conflict preservation and session revocation.
- Native Live TV route/delivery wiring, guide/manual/recurring DVR workflows, and
  captured-authority reconciliation after successful or uncertain writes. Failed
  readback blocks further recording writes until explicit reload succeeds.
- Contextless websocket revalidation using captured account/profile/tenant authority,
  without relaxing one-use tickets, expiration, PIN or revocation checks.
- Atomic setup decorator forwarding and an early transactional-provider check before
  account, membership or filesystem side effects. Supported PostgreSQL and notification
  decorators retain their transactional behavior.
- Real isolated invitation fixtures, realistic direct-profile/API fixtures, current
  server-owned Kotlin/Swift settings bindings and five missing scenario catalog rows.
  Lifecycle scenarios now require actual store readiness; profile-list fixtures use
  the supported local avatar backend instead of inventing storage capability.

Silo implementations remain in place. Shared changes are recorded in
`contracts/seams.txt`; owned adapters and tests carry the Bloem-specific behavior.
No new SQL migration was introduced by this web-completion batch.

## Verification at this checkpoint

- Web gate: **623 files / 4,606 tests passed**, with no added exclusions. TypeScript,
  touched-file lint/format and the frontend production build passed.
- The account/API continuation passed **177 Go test events**, including all **60**
  invitation-package events under race detection, without overlays or skips.
- The final scenario packets passed **98/98 results**, zero skips: 46 added extension
  cases, 20 profile cases, 26 device cases and six paired lifecycle-validation cases.
  Ten focused prerequisite tests passed. All **337** previously frozen scenarios in
  the three edited catalogs remain unchanged.
- Real browser checks passed for websocket/home, credential revision conflicts and
  revocation, invitation handoffs across context changes/logout, engagement lifecycle
  and tenant filtering, and DVR writes with injected lost responses/failed reads.
- Configured-S3 acceptance passed for campaign images, seasonal banners and sprites:
  upload, exact bytes/MIME/dimensions, decoded rendering, ETag revalidation, audience
  filtering, and invalid/oversized-input rejection. The temporary object store and
  test content were removed. Exact original settings, encrypted values and absent
  rows were restored; no test storage configuration remains active.

These are bounded results, not a claim that the full Go suite, every scenario packet,
all lint rules, every browser or a production deployment has passed. Final artifact
and build results are recorded in the coverage matrix. Test harnesses, credentials,
browser state, database snapshots and screenshots are intentionally not committed.

The final backend artifact includes provider preflight and has verified worktree VCS
metadata, but was **not started**. Browser/S3 acceptance used the earlier disposable
build. A first build had incorrectly inherited the parent checkout's VCS metadata;
future builds must verify source revision, dirty state and target platform explicitly.
The final documentation-inclusive seam gate passed with **479** declared entries.

## Remaining limits

1. **Media topology:** actual tuner, server-encoded media, owner-loss and Safari
   acceptance need suitable fixtures. The prepared synthetic transport stream decoded
   offline, but the normal Live TV URL and dial guards intentionally reject a loopback
   tuner. Do not disable those guards, insert unsafe URLs or call fixture encoders a
   real playback pass. Use a separately isolated, supported private network topology.
2. **Backend boundaries:** no automatic DVR catalog import, enforced `keep_last`,
   rule-update API, direct-profile browser login, shared-device profile pairing,
   delegated-role system, SSO-only credential reauthentication or engagement registry
   revision locking was added. Do not advertise these through frontend controls.
3. **Broader validation:** remaining role-expiry/suspension journeys, full keyboard
   coverage, actual hardware and multi-replica playback require additional acceptance.
   A malformed non-UUID bridge playback ID still produces a dependency-unavailable
   response; valid absent UUIDs return the expected not-found response. That separate
   input/error-mapping issue was not changed by the fixture repair.
4. **Deployment:** no release certification or production rollout was performed. The
   disposable running server was restored after storage acceptance; do not assume it
   contains later source changes merely because health is green. Build the committed
   revision and verify its artifact before any later deployment.

## Exact next steps

Commands assume the repository root. Use pnpm and bounded Go parallelism. Supply
credentials through local environment/configuration, never checked-in files.

1. Read the coverage matrix and choose one remaining acceptance milestone. Preserve
   separate account, administrative-context and viewing-profile authority.
2. Provision disposable PostgreSQL and set `SILO_TEST_DATABASE_URL` with
   `SILO_REQUIRE_TEST_DATABASE=1`. Scenario execution additionally uses
   `SILO_SCENARIO_DATABASE_URL` pointing to an explicitly disposable scratch database;
   its reset guards are intentional. Database-backed fixtures may require permission
   to create and drop their own isolated databases.
3. Reproduce the focused checks before broad validation:

   ```sh
   GOMAXPROCS=2 go test -race -p 2 -count=1 ./internal/invitations
   GOMAXPROCS=2 go test -race -p 2 -count=1 ./internal/auth -run '^TestBloemAccountTransactionProviderPreflight$'
   make verify-settings-bindings-all verify-scenario-catalogs
   make verify-route-inventory verify-migration-ledger verify-offline-routes
   make verify-seams verify-local-paths
   git diff --check
   ```

   Closure's plain local-path scanner invocations emitted host-shell `Bus error`
   diagnostics and are not passes. The unchanged complete gate passed under shell
   tracing. If that issue recurs, retain the failed output and distinguish a traced
   rerun from the plain run; do not change the scanner or suppress the diagnostics.

4. Build with `make build`, then follow the full validation requirements in
   [CONTRIBUTING.md](../../CONTRIBUTING.md) for a release or further merge. Existing
   web evidence need not be rerun for a documentation-only change.
5. For media acceptance, use the committed normal server in the isolated supported
   topology. Verify actual decoded frames, owner-bound renewal/release, cleanup and
   denied foreign authority. Record Chromium, WebKit and Safari as distinct evidence.
6. Update durable coverage with observed outcomes and explicit remaining limits.
   Do not add exclusions, weaken frozen assertions or turn unavailable prerequisites
   into success claims.
