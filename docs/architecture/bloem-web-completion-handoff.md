# Embedded-web completion handoff

**Deployment complete:** `418a18b7d4ae7060ebc574eb882bc525e7e1a4c2` was committed,
pushed to `origin/main` and deployed on September 19, 2026, at 21:26 UTC. Both the
policy-library array and Xtream migrations were applied. Health/readiness and
Chromium login smoke checks passed; the container was healthy with zero restarts
at verification. See the [deployment record](../operations/2026-09-19-xtream-deployment.md)
for backup, restored-data rehearsal, migration checks and image-first rollback.

**CI remains failed:** run `35467142414` exceeded the executor's 20-minute package
timeout; 189 other Go packages passed. Deployment proceeded with explicit approval
of that exact exception, not a green-suite claim. Real-provider, decoded-media,
authenticated new-feature browser/accessibility, Safari and replica owner-loss
acceptance remain open. The next step is a selected remaining milestone, not
another deployment of this already-running revision or an automatic broad rerun.

The original web checkpoint below dates to September 18, based on Bloem `95403cedd`
and Silo `0362b6dae`. Security/setup follow-through shipped in `0366a7b64`, including
Go 1.26.8, pre-snapshot setup admission and malformed playback-stop ID handling.
Historical no-migration/no-rollout statements apply only to that original web batch;
the two later migrations and their operational requirements are recorded separately.

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

Prepublication local validation, in execution order:

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

These local checks did **not** certify a green full suite. The expanded scenario
race run timed out at 20 minutes while still progressing; a subsequent non-race
run was interrupted, not completed. Neither proves closure of the later hosted
CI timeout described below.
Frozen route/scenario source snapshots and test exclusions remain unchanged. The
later explicit downstream adjudications below change current Bloem expectations,
not those historical baselines. Further validation is limited to targeted checks,
not another broad suite rerun.

A subsequent focused executor repair binds router maintenance to test lifetime and
removes a redundant pre-reseed around paired `FreshState` mutations. Regression
checks preserve before/after isolation and both frozen transport assertions; ten
hierarchical events passed with no skips. `make test-go` now has a bounded 20-minute
package timeout, based on the earlier completed 872.712-second executor run rather
than the incomplete race attempt. This removed the known ten-minute budget mismatch,
but the subsequent hosted run still exceeded 20 minutes; the performance issue
remains unresolved. See
[scenario runtime and lifetime](scenario-acceptance.md#runtime-budget-and-fixture-lifetime).

The final [contract adjudication](bloem-contract-adjudications.md) closes the
inspected local contract failures: 30 recorded transport decisions passed **52
real-router transport leaves / 99 hierarchical events**, with no skips. Original
catalogs are SHA-pinned and unchanged; negative checks reject changed requests,
principals, rows and expectations, and reject API-key disclosure. The route guards
now require all 28 exact native Live TV replacements and three existing upstream
OAuth additions while preserving the frozen snapshots and rejecting unrelated
drift. The focused route packet passed. No production behavior was weakened or
changed by this adjudication.

### Publication, remote CI and deployment

The implementation was published in `23ee4c1ac`. Its Linux CI found three capitalized
transport-error strings; `418a18b7d` corrected those strings, with 16 targeted test
events passing. Remote CI `35467142414` for the corrected revision passed build,
format, vet, vulnerability, lint and artifact/contract guards, the web, tenancy and
compatibility jobs, and 189 Go packages. Only the executor package failed, at its
20-minute timeout. The stack showed fixture seeding/progress, not a proven deadlock;
no assertion-failure markers appeared, but unfinished scenarios are not passes.
The separate Discord notification workflow also failed.

After explicit approval of that timeout exception, the exact published image was
deployed. A fresh full backup and a scoped restore of the complete schema plus all
rows of 22 affected/dependent tables preceded migration. Both migrations applied;
production row hashes, array bounds and policy triggers were preserved, all eight
policy arrays widened, new Xtream tables were empty and no invalid indexes remained.
The application replacement recycled database pools; Redis and other configuration
were preserved. The previous image/config and backup remain protected. No Down
migration or restore over live data occurred.

The [deployment record](../operations/2026-09-19-xtream-deployment.md) separates
these results from the earlier full-catalog restore and from open acceptance.
No provider was configured. Fixture encoder tests and unauthenticated Chromium
login smoke are not real-provider, authenticated-feature or decoding evidence.

## Where work stopped (original web checkpoint)

At the original September 18 checkpoint, the implemented workflows and bounded
acceptance checks were complete, but production deployment had not yet occurred.
The publication and rollout above supersede that earlier deployment status.
The following completed-work and verification sections preserve the original
web batch's evidence rather than presenting it as new Xtream acceptance.

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
3. **Broader validation:** remaining role-expiry/suspension journeys, authenticated
   Xtream workflows, full keyboard coverage, actual hardware and multi-replica
   playback require additional acceptance. Malformed playback-stop IDs were fixed
   in the deployed security/setup follow-through; they are no longer an open defect.
4. **CI and recovery scope:** the scenario timeout and separate notification workflow
   remain unresolved. Deployment and scoped migration rehearsal succeeded with a
   narrowly approved CI exception. They do not certify the full scenario suite,
   every old-image runtime workflow or a recovery-time objective.

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
3. Run only the focused checks needed for the chosen change. Examples:

   ```sh
   GOMAXPROCS=2 go test -race -p 2 -count=1 ./internal/invitations
   GOMAXPROCS=2 go test -race -p 2 -count=1 ./internal/auth -run '^TestBloemAccountTransactionProviderPreflight$'
   make verify-settings-bindings-all verify-scenario-catalogs
   make verify-route-inventory verify-migration-ledger verify-offline-routes
   make verify-seams verify-local-paths
   git diff --check
   ```

   Historical plain local-path scanner invocations emitted host-shell `Bus error`
   diagnostics and were not passes; later plain runs passed. If that issue recurs,
   retain the failed output rather than suppressing diagnostics.

4. For a runtime change, build the exact committed source and follow the applicable
   validation requirements in [CONTRIBUTING.md](../../.github/CONTRIBUTING.md). Documentation-only
   changes need link/anchor, whitespace and local-path checks, not a new full build
   or test suite. The current deployment exception does not cover another revision.
5. For media acceptance, use the committed normal server in the isolated supported
   topology. Verify actual decoded frames, owner-bound renewal/release, cleanup and
   denied foreign authority. Record Chromium, WebKit and Safari as distinct evidence.
6. Update durable coverage with observed outcomes and explicit remaining limits.
   Do not add exclusions, weaken frozen assertions or turn unavailable prerequisites
   into success claims.
