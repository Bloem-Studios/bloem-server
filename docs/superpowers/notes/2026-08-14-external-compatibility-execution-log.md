# External Compatibility Applications Execution Log

Durable implementation notes for the approved external compatibility application design and its four-plan roadmap. This file records process, rulings, commits, verification, review findings, and the next safe continuation point. Update it at every accepted task boundary and every material review/fix round.

## Canonical documents

- Design: `docs/superpowers/specs/2026-08-12-vondel-compatibility-sidecars-design.md`
- Roadmap: `docs/superpowers/plans/2026-08-12-vondel-compatibility-sidecars.md`
- Foundation plan: `docs/superpowers/plans/2026-08-13-vondel-compatibility-1-foundation.md`
- Audiobookshelf plan: `docs/superpowers/plans/2026-08-13-vondel-compatibility-2-audiobookshelf.md`
- Jellyfin plan: `docs/superpowers/plans/2026-08-13-vondel-compatibility-3-jellyfin.md`
- Cutover plan: `docs/superpowers/plans/2026-08-13-vondel-compatibility-4-cutover.md`

The design was approved and the roadmap/plans were committed in:

- `35fd15c0 docs: redesign compatibility as removable applications`
- `81047e91 docs: plan external compatibility applications`

## Working method

The four plans execute serially. Each implementation task uses this gate:

1. Extract a task-specific brief from the approved plan.
2. Record the starting commit.
3. Use a fresh implementer working in this isolated feature branch.
4. Require RED/GREEN TDD, focused relevant regression tests, self-review, a local task commit, and a detailed ignored task report.
5. Generate an exact base-to-head review package.
6. Send that package to a fresh independent reviewer for both specification compliance and code quality.
7. Resume the original implementer for fix rounds 1–3. Require new regression tests and a new commit; never amend the reviewed commit.
8. Re-review only the original findings plus the fix diff.
9. Mark a task complete only after both review verdicts approve it.
10. Run a broad whole-plan review after the final task, then use the branch-finishing workflow.

Implementation stays on the isolated `feature/opa-tenant-foundation` branch. Plan execution does not authorize pushes, merges, deployments, repository creation, releases, tags, visibility changes, or destructive production operations. Those remain explicit external gates.

An ignored recovery ledger and task reports live under `.superpowers/sdd/2026-08-13-vondel-compatibility-1-foundation/`. This tracked note is the maintainer-facing durable summary; it must not contain local filesystem paths or secrets.

## Binding architecture rulings

- Vondel remains authoritative for credentials, profiles, organizations, policy, media state, playback, delivery, events, and Prairie-derived Live TV/DVR.
- The removable private applications are named `vondel-audiobookshelf` and `vondel-jellyfin`.
- Both use Vondel's canonical public address. Audiobookshelf owns only `/audiobookshelf/**`; Jellyfin owns a reviewed fixed protocol route set and `/web`.
- Companions receive no Vondel database, Redis, media filesystem, Docker socket, signing key, provider credential, or tuner credential.
- Vondel never controls Docker. Administration shows health and exact operator commands but performs no container mutation.
- A profile is either shared-only or has both a globally unique email and password. Partial direct credentials are invalid.
- Account owners own profiles, unchanged. A profile's optional email and password are a second front door to that one profile, usable from any client — Jellyfin, the Silo and Vondel clients, Audiobookshelf — and the profile still belongs to its parent account.
- A direct profile login carries least privilege, not parity with reaching that profile through the account login: it may browse, play, and keep its own progress, settings, devices, and profile record, and nothing else. Household management, account surfaces, and anything that mints a differently scoped credential are refused even when the bound profile is the household primary. The reason is exposure — this password is typed into third-party clients, so it must not be spendable as the account. Confirmed by the maintainer on 2026-08-14.
- Legacy account login and current PIN-based shared-device profile switching remain supported.
- Direct profile login binds the session to exactly one organization, account, profile, device, authentication method, audience, capabilities, and current security/policy revisions.
- Unknown devices receive no public profile directory.
- Canonical progress, bookmarks, favorites, collections, playlists, downloads, playback state, Live TV rules, and recordings remain in Vondel.
- Companion persistence contains disposable protocol state only. Default storage is companion-owned SQLite/WAL; a separate companion-owned PostgreSQL database is optional.
- Both source repositories and release artifacts remain private indefinitely.
- Audiobookshelf extraction precedes Jellyfin. Embedded code is removed only after dual-run parity and exact private release-image acceptance.
- Existing compatibility sessions are not migrated at cutover; one fresh login is accepted.
- Old protocol-only tables remain inert for one rollback release and are dropped only in a later separately approved migration.

## Foundation progress

### Task 1 — Optional direct profile credentials

Status: ACCEPTED at `3ffc355b`. The eleventh full review returned no findings.

Initial implementation commit:

- `8ba00106 feat(auth): add optional direct profile login`

Initial verification reported by the implementer:

- Focused real-PostgreSQL auth and handler suite passed.
- Focused auth race suite passed.
- API and PostgreSQL profile-store compile checks passed.
- Diff and credential-literal scans passed.

The independent review rejected the initial commit. All findings are binding because they affect direct-profile isolation or credential revocation:

1. A password reset could race between authentication and session insertion, permitting a session authenticated with the old credential revision to be inserted after revocation.
2. Direct-profile claims were minted with a profile, but downstream viewer resolution still accepted a caller-selected profile header and exposed sibling/account-scoped surfaces.
3. The database synchronized account emails with the canonical registry but did not enforce the same invariant for direct profile inserts/updates/deletes performed outside the service.
4. Refresh trusted old token profile, tenant, device, and revision claims rather than comparing them with the persisted session binding and current authoritative subject state.
5. PostgreSQL unique-violation detail could propagate the normalized email through an error and into logs.

Fix-round requirements:

- Serialize credential verification/session creation against credential reset, or use an equivalent atomic current-revision predicate.
- Enforce a direct token's profile binding throughout middleware and account/profile/session endpoints while preserving legacy account sessions and explicitly authorized shared-device switching.
- Add database-side profile registry synchronization and direct-SQL/migration regressions.
- Revalidate persisted binding and current account/profile/membership/organization/credential state on refresh.
- Return stable sanitized collision errors containing no submitted email.
- Add deterministic RED/GREEN regressions for each issue, rerun focused PostgreSQL/API/race tests, create a new fix commit, and obtain scoped independent approval.

### Remaining foundation tasks

1. Freeze external protocol and subject semantics.
2. Add one-use companion enrollment and revocable service trust.
3. Implement private Compatibility API v1 from the generated OpenAPI contract.
4. Add the fixed-path edge gateway and Compatibility Applications administration.
5. Add private Compose deployment contracts and operator instructions.
6. Add the disposable-system foundation acceptance and required CI gate.

The Audiobookshelf, Jellyfin/Live TV, and final cutover plans remain pending until the foundation is accepted.

### 2026-08-14 — Foundation Task 1, fix round 1

- Base/head commits: `af681894` → `cabaeafc fix(auth): bind direct profile sessions and close credential race`.
- Decision or ruling: the partially written fix round was audited before being continued. Three of the five review findings were substantively addressed but unproven, one was only partially addressed, and one shipped a regression that could not fail. Two further defects in the reviewed commit surfaced during the audit and are fixed in the same round, because the branch cannot reach a green gate without them.
- Audit result on the five original findings:
  1. Session-creation race: the row-lock guard was sound but carried no regression and was wired through a runtime type assertion instead of the session repository interface.
  2. Profile binding: enforced only in viewer access. `RequireProfile` and the household-manager check still trusted the self-asserted profile header, and six of eight profile routes were ungated, so a direct-profile session could manage a household whose primary profile carries no PIN.
  3. Database registry synchronization: the trigger was correct, but its regression addressed a nonexistent profile row, so "no rows updated" was reported as an enforced collision. The test failed as written.
  4. Refresh revalidation: correct in substance, untested, and it returned before the session window slid, so direct-profile sessions could never extend their expiry. A missing credential service revoked the session.
  5. Collision error sanitization: the fix is sound, but the accompanying test passed against the reviewed commit unchanged, because the driver's error string never carried the submitted email.
- Additional defects found in the reviewed commit: the new `user_profiles` columns broke the tenant-identity migration snapshot invariant, and three files were not `gofmt` clean.
- RED evidence: every regression was confirmed failing against the reviewed behavior by temporarily restoring it. Refresh accepted a token naming a sibling profile; `RequireProfile` honored a sibling header; unserialized session creation survived a committed credential rotation; the tenant-identity backfill failed on four subtests at `af681894`.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint` with `--new-from-merge-base` reports nothing in the changed files. `make verify-local-paths` passes. The frontend gates were not run: no frontend file changed.
- Independent review verdict: not yet requested. The fix round is scoped to the five original findings plus the two defects named above.
- Fix commits/findings, if any: `cabaeafc`.
- External side effects: none. No push, no deployment, no repository or issue activity.
- Next continuation point: send `af681894..cabaeafc` to a fresh independent reviewer for a scoped re-review of the original five findings plus the fix diff. Task 1 is complete only on approval; foundation tasks 2 through 7 remain pending.

### 2026-08-14 — Foundation Task 1, fix round 2

- Base/head commits: `cabaeafc` → `d405f821 fix(auth): close remaining direct profile escapes and enforce rotation`.
- Independent review verdict on fix round 1: rejected, two critical and two important findings. Verdicts by original finding: race not fully closed; profile boundary not closed; registry synchronization closed; refresh closed for identity claims but with a new reliability defect; email leakage closed; the repaired zero-row regression closed; the migration snapshot fix closed.
- Decision or ruling: all four findings accepted without dispute; two were reproduced directly before implementing. The reviewer's database-boundary design for credential rotation was adopted as recommended rather than substituted, after an explicit maintainer decision to take the full version.
- Findings and what closed them:
  1. Boundary escapes. Device pairing approval and denial hand the paired device a full account session; an account API key is not profile-bound and skips PIN verification. Both were reachable from a direct-profile session, along with account diagnostics and Discord linking. A direct-profile session on a profile that is the household primary also passed the household-manager check, which widened into sibling devices and settings. All now guarded at the router, and `canManageHousehold` refuses direct-profile sessions outright.
  2. Out-of-band credential writes. `credential_revision` is the revocation mechanism but only the service maintained it, so any other writer left old sessions valid and allowed a login that verified the superseded password. A `BEFORE UPDATE` trigger now forces the increment and an `AFTER UPDATE` trigger revokes that profile's direct sessions in the same transaction; the service no longer does either by hand.
  3. Refresh revoked a valid session on any revalidation error, including transient database failures. It now revokes only for a genuinely invalid binding.
  4. The credential pair constraint accepted a blank email or hash.
- RED evidence: every regression confirmed failing against the previous behavior. The router acceptance test failed on device approval, device denial, API key creation, API key listing, and account diagnostics; the rotation, blank-pair, and transient-refresh tests all failed with their fixes reverted.
- Self-inflicted defect caught in the round: the first version of the pair constraint compared only trimmed values, so a half-set pair evaluated to NULL and the CHECK accepted it. Its own test caught this before commit and the constraint now states nullness explicitly.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- External side effects: none. No push, no deployment, no repository or issue activity.
- Next continuation point: scoped re-review of `cabaeafc..d405f821` covering the four findings and anything this round introduced. Task 1 completes only on approval.

### 2026-08-14 — Foundation Task 1, fix round 3

- Base/head commits: `d405f821` → `16baf555 fix(auth): make the direct profile boundary default-deny`.
- Independent review verdict on fix round 2: database-enforced credential rotation and the refresh failure handling both accepted, including trigger interaction, multi-row behavior, insert behavior, and the down migration. The profile boundary and the blank-credential constraint were rejected again.
- Decision or ruling: the reviewer recommended continuing to guard named account route groups. That recommendation was NOT followed, by explicit maintainer decision, and the divergence is recorded here deliberately. The router registers 656 routes across 65 groups; three review rounds each surfaced new reachable surfaces, so enumeration was judged not to converge. The boundary is now default-deny.
- What changed:
  - A direct-profile session is refused at authentication unless the route it matched is named in the allowlist. Entries are exact chi route patterns, resolved by matching the request against the root router, because a path prefix cannot separate a profile's own record from the household collection that shares its prefix. This also splits the mixed settings group without restructuring it: the contract, device, and effective views are admitted and the account-wide list and single-key routes are not.
  - An inventory test walks the real router and pins the admitted set, so a route added under an allowed subtree fails until someone confirms a single profile may use it. A second test rejects allowlist entries the router no longer registers.
  - Login normalization moved into one immutable database function matching Go's whitespace trimming, used by the pairing constraint, the registry key check, both sync triggers, and the backfill. PostgreSQL's default trim removes spaces only, so a tab, newline, or non-breaking space had been passing as a real credential.
- RED evidence: the whitespace regressions fail against the previous constraint. The allowlist tests are new-surface tests rather than regressions of old behavior, and the router acceptance test covering the newly named account routes fails without the guard.
- Defect found and fixed inside the round: the blank-credential subtests shared one profile row and ran in map order, so a write that slipped through changed what later cases asserted. Each case now resets the row first. Two allowlist entries also named route shapes the router does not register, which the stale-entry test caught.
- GREEN verification: full `make test-go` against real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- External side effects: none.
- Next continuation point: scoped re-review of `d405f821..16baf555`. The review brief asks the reviewer to attack the default-deny design directly, including whether every authenticated path passes through the guard and whether the admitted subtrees are genuinely profile-scoped. Task 1 completes only on approval.

### 2026-08-14 — Foundation Task 1, fix round 4

- Base/head commits: `16baf555` → `e9255d41 fix(auth): restore playback for bound profiles and close the plugin bypass`.
- Independent review verdict on fix round 3: rejected, one critical and two important. The default-deny design itself was accepted — the reviewer confirmed that re-matching the request against the root router resolves the same pattern dispatch would, for wildcards, method-specific chains, and mounted subrouters — and the previously cleared rotation and refresh items were confirmed undisturbed.
- Process note: the plan allows fix rounds 1 to 3, and this is round 4. It was taken because round 3 left a functional regression on the branch that could not responsibly be left in place. If round 4 rejects, continuing needs a maintainer decision rather than another implementer round.
- Findings and what closed them:
  1. The plugin proxy authenticates itself and never passes the authentication middleware, so the default-deny guard never saw it, while the proxy hands plugin code the owning account's id and role. It now refuses profile-bound tokens outright instead of extending the allowlist to routes a plugin defines at runtime. The profile-bound plugin access path added in round 2 became unreachable and was removed rather than left as dead code.
  2. The allowlist omitted playback negotiation, start, replan, progress, stop, stream and subtitle delivery, and the client's library bootstrap, so round 3 shipped a 403 on the entire watch path. The root cause was the inventory fixture: it never mounted those handlers, so the exact-set test pinned a partial router as though it were the whole one. The fixture now supplies a session manager and the file and folder repositories, and the acceptance test drives playback and stream through the boundary with a direct-profile token.
  3. Login email had two normalizations — Go trimming and lowering for the lookup, PostgreSQL lower() for the registry key — which can disagree on Unicode case. Go now trims and passes the value; the database folds case for both storage and lookup.
- RED evidence: with the fixes reverted, the acceptance test reports 403 for user libraries, playback capability, playback start, progress, stop, and stream, and the plugin proxy proceeds past the boundary instead of refusing. The normalization regression covers mixed case and surrounding whitespace on both storage and lookup.
- Lesson recorded: a golden inventory test is only as honest as the router it walks. Pinning an exact set proved nothing about routes the fixture never mounted, and it made a shipped regression look verified.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- External side effects: none.
- Next continuation point: scoped re-review of `16baf555..e9255d41`, which also asks the reviewer whether the accumulated design now warrants a fresh full review rather than another scoped one.

### 2026-08-14 — Foundation Task 1, fix round 5 and move to full review

- Base/head commits: `e9255d41` → `c232350b fix(auth): scope playback sessions to the profile that started them`.
- Independent review verdict on fix round 4: rejected, one critical and two important, plus an explicit process verdict that the next review should be a fresh full one rather than another scoped round. Database-owned email normalization was accepted, including that the lookup preserves primary-key index use. The round-2 rotation and refresh items remain clean.
- Decision or ruling: the maintainer chose to close the two mechanical findings and the named authorization sites in one more round, then move to a full review of the whole task. The wider question the critical finding raises — where else account identity is treated as sufficient authorization — is carried into that full review rather than guessed at here.
- Findings and what closed them:
  1. A playback session id is a bearer for progress, stop, control, and media delivery, and those handlers compared the caller's account alone. Household profiles share an account, so a bound profile holding a sibling's session id could drive and consume that session. One helper now answers whether a caller may act on a session, narrowing to the profile only when the bearer is direct-profile bound, so players that authenticate by signed delivery URL keep the account check they have always had. Replan already compared the profile and was untouched.
  2. The plugin asset route resolved identity through a helper that discarded the profile-bound flag, so it stayed open after the route proxy was closed. Both plugin entry points now share one resolver and one refusal, and the two-value helper was deleted rather than left as a trap.
  3. The route inventory now also mounts the user collection service, so imported-collection routes are pinned instead of admitted invisibly by a prefix.
- RED evidence: with the ownership helper neutered, all six sibling probes — progress, stop, control socket, stream, subtitles, transcode — reach the sibling's session. The test also asserts the sibling's session still exists after a refused stop.
- Known gap, recorded rather than hidden: the inventory fixture cannot mount the S3-backed subtitle search, download, upload, and AI routes, which the allowed subtitle prefix admits in production without pinning them. This is stated in the test and handed to the full review.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- External side effects: none.
- Next continuation point: a fresh full review of `81047e91..c232350b`, judging the accumulated design rather than the latest patch, and asked specifically to find remaining places where account identity is treated as sufficient authorization and to name tests that would still pass if the behavior they describe were removed.

### 2026-08-14 — Foundation Task 1, first full review and fix round 6

- Base/head commits: `c232350b` → `a10e93e7 fix(auth): repair registry writes, tenancy binding and the profile surface`, plus `d28936ff` recording the authorization ruling.
- Full review verdict on `81047e91..c232350b`: Task 1 cannot be accepted. Six findings, three of them high.
- Ruling obtained during this round, previously an unstated implementer assumption: a direct profile login carries least privilege rather than parity with reaching that profile through the account login. Account owners still own profiles; the profile's optional email and password are simply a second front door to that one profile from any client. Household management, account surfaces, and anything minting a differently scoped credential are refused even when the bound profile is the household primary, because that password is typed into third-party clients and must not be spendable as the account. This ruling is now in the binding list above.
- Findings and what closed them:
  1. Re-saving a record without changing its login identity failed on the registry's own primary key. Both triggers deleted the prior row only when the value changed but always re-inserted, so an ordinary account save that re-supplied the current email errored. This regressed existing account management rather than the new feature, and the whole suite passed over it. Both triggers now return early when the login identity is unchanged, which also leaves the credential revision alone so a no-op write cannot revoke live sessions.
  2. v1 projected every request into the deployment's default organization, including direct-profile sessions that were bound to a specific organization, membership, and revisions at login. A profile in another organization was evaluated against the wrong tenant and a rotated revision was never noticed. The legacy path now branches for direct-profile sessions and validates the exact binding, sharing one implementation with the v2 path.
  3. Session creation asserted that the account, organization, and membership were current but locked only the profile row. Those rows are now taken FOR SHARE while the profile is taken FOR UPDATE, deliberately not FOR UPDATE, which would serialize every login in an organization behind one row.
  4. The boundary was keyed by path alone, so a new verb on an admitted path was allowed automatically. It is now keyed by method and pattern, and the inventory pins method-pattern pairs. The live example was the profile record: a bound profile edits itself and does not delete itself.
  5. The ebook surface was refused although every ebook route is profile-gated end to end, denying a bound profile its own reading state.
  6. The positive boundary tests asserted only that a response was not a refusal, so a deleted handler or a failing tenant resolution would have passed. They now pin the status each handler answers, and a profile update is verified in the database.
- RED evidence: no-op account writes fail three ways without the trigger fix; a stale-revision token reaches the handler without the tenancy branch; all five subject-change races admit a stale session without the widened lock.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- Standing caution: six rounds in, each of the last two closed defects the previous review missed, including one that broke pre-existing behaviour. A green suite has been a weak signal on this branch, so acceptance should rest on review rather than on the gates.
- Next continuation point: second full review of `81047e91..a10e93e7`, given the least-privilege ruling explicitly and asked again for remaining places where account identity is treated as sufficient authorization, and for tests that would still pass if their behaviour were removed.

### 2026-08-14 — Foundation Task 1, second full review and fix round 7

- Base/head commits: `a10e93e7` → `6426a1c2 fix(auth): admit reads by subtree and writes one route at a time`.
- Second full review verdict on `81047e91..a10e93e7`: cannot be accepted. Six findings, three high. The reviewer had the least-privilege ruling this time and judged the implementation against it.
- Findings and what closed them:
  1. A profile password could still reach a differently scoped credential. PIN verification was on the surface and the PIN was a permitted self-service field, so the credential could both exercise and replace the lock that gates profile switching for account sessions. Both are refused, and the regression checks the stored hash rather than the status code.
  2. Broad subtrees admitted mutations, and the ownership checks behind some of them compare accounts, which cannot separate two profiles of one household. Reading is still admitted by subtree, because those surfaces are uniformly profile-scoped and resolved through viewer access, but every write is now named individually. Subtitle search, download, upload, and the AI jobs left the surface entirely as shared state.
  3. The inventory could not see the S3-backed subtitle routes it was admitting. Removing that subtree from the surface retired the gap rather than papering over it.
  4. The lock order introduced in the previous round deadlocked against profile-group administration, which locks a membership before the profile it moves. Session creation now acquires organization, account, membership, profile, one statement at a time, in that order. The regression fails with SQLSTATE 40P01 against the previous joined statement.
  5. A bound profile could not read its own record. A new additive route serves it, guarded to the session's own profile, while the household list stays account-scoped.
  6. Three positive assertions passed on any non-refusal. They now pin the status and the resulting state.
- Defects found in this round's own work: the subject-change race test held the profile row before the tenancy rows, an order no real writer uses, so it deadlocked rather than testing anything once the lock order was corrected; it now holds only the row it changes. One positive assertion had been posting a field name the handler does not read, so it asserted nothing at all, which only surfaced once the assertion required a real state change. Two allowlist entries named routes the router does not register.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- Next continuation point: third full review of `81047e91..6426a1c2`, asked to check every admitted mutation for whether its handler can tell two profiles of one household apart, to test rather than accept the argument that reads admitted by subtree are profile-scoped, and again to name tests that would pass with their behaviour removed.

### 2026-08-14 — Foundation Task 1, third full review and fix round 8

- Base/head commits: `6426a1c2` → `2ff7894e fix(auth): restore shadowed reads, retire collections, bind the device`.
- Third full review verdict on `81047e91..6426a1c2`: cannot be accepted. Two high, two important.
- Findings and what closed them:
  1. An exact allowlist entry swallowed the reads its subtree already granted: a pattern with a listed PUT denied its own GET, cutting a bound profile off from its favorites, ratings, downloads, and preferences — and the exact-set inventory had codified the loss. Exact entries now add methods; reads fall through to the subtree check.
  2. Personal collections authorize by account alone at both handler and store level: deletion, item mutation, ordering, and groups affect the whole household, and reads show any account-owned collection. Rather than invent a profile-ownership model mid-task, the entire personal-collection surface — reads included — left the direct-profile surface, with only the server-wide collection views remaining. The routes return when collection ownership is profile-aware, which is product design and belongs to its own task.
  3. The device binding minted at login was recorded but never enforced; handlers kept trusting the device header. Direct login now requires a device id, and after authentication the header is made canonical — a conflicting value is refused, an absent one is filled from the binding — so downloads, device settings, and policy input all see the authenticated identity.
  4. The boundary tests never exercised the collection or device surfaces. They now do, state-checked, each confirmed red against the unfixed behavior.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes. Frontend gates not run: no frontend file changed.
- Deferred, recorded rather than silently dropped: profile-aware collection ownership (visibility and mutation rights per profile) is real product work and is out of Task 1's scope by decision; the routes stay off the direct-profile surface until it exists.
- Next continuation point: fourth full review of `81047e91..2ff7894e`.

### 2026-08-14 — Foundation Task 1, fourth full review and fix round 9

- Base/head commits: `2ff7894e` → `69a83a35 fix(auth): close the library collection door and finish the device binding`.
- Fourth full review verdict on `81047e91..2ff7894e`: cannot be accepted, but for the first time no high findings — four important ones, all narrow.
- Findings and what closed them:
  1. Personal collections were still readable through the library routes, which the admitted library subtree covered after round 8 removed the /collections routes. A denied-routes check now runs before the subtree fallthrough and both patterns are pinned as refused.
  2. The stream handler refused the very stream_url the server issues: playback start appends a signed session-bound token because native players cannot attach a bearer to range requests, and the handler rejected claimless requests regardless. The defect predates this task and was verified against the base before touching it. A verified token now satisfies the handler; bare and cross-session requests still refuse, all covered end to end.
  3. The device requirement lived only in the HTTP handler, so an adapter calling the service directly could mint a device-less session, and the session table accepted one. Service and database now both refuse, with regressions at each layer.
  4. The device-injection test asserted a status the handler never returns for a settings key that does not exist. It now writes a real device-scoped setting with no header and reads it back on the bound device.
- RED evidence: all four fixes reverted individually fail their tests — the library reads reach the handlers, tokened delivery 401s, the service and database both accept device-less sessions.
- Suite note: one pre-existing flake surfaced in the full run (an adminpeople cursor-tampering test whose random tamper occasionally yields a decodable cursor); it passes repeatedly in isolation, was not introduced by this branch, and is left untouched.
- GREEN verification: full `make test-go` against a real PostgreSQL passes (129 packages). `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes.
- Next continuation point: fifth full review of `81047e91..69a83a35`.

### 2026-08-14 — Foundation Task 1, fifth full review and fix round 10

- Base/head commits: `69a83a35` → `9d5824a2 fix(auth): advertise the capability, equalize login cost, readmit library reads`.
- Fifth full review verdict on `81047e91..69a83a35`: cannot be accepted. Three medium, one low — the second consecutive review with nothing high.
- Findings and what closed them:
  1. Capability discovery reported direct profile login unavailable while the route was live, and three contract tests pinned the stale value, so they would have survived the feature's removal. The capability is now advertised when the auth stack is wired, and the pins demand it.
  2. Login timing distinguished registered direct-profile emails from unknown ones: an unknown email returned before any hashing while a registered one paid a full bcrypt comparison. Both paths — and malformed stored hashes — now cost exactly one comparison, proven by a comparer-injected regression.
  3. The round-9 denial of the two library collection reads over-restricted: the listing query requires a per-profile visibility row, so those views are legitimate browse results under the ruling. Notably, the implementer had verified exactly this in round 7 and then deferred to the round-8 review finding without re-checking; the reviewer has now converged on the original evidence. The reads are readmitted, and the blanket-403 test became a visibility-isolation test with a bound-profile and a sibling-only collection.
  4. The progressive-delivery test signed its own token, so it could not notice playback ceasing to emit one. The contract is now pinned at both ends: the URL builder emits a verified session-bound token, and the router test proves tokened claimless delivery reaches the handler. Deliberate divergence, recorded: the reviewer asked for one test driving a real playback start; the two halves cover the same property without the media-file fixture a v3 start requires.
- RED evidence: the capability contract test fails with the advertisement removed; the timing regression fails with the dummy comparison removed; the visibility test replaces a blanket refusal that the surface change would have caught in the inventory.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes.
- Next continuation point: sixth full review of `81047e91..9d5824a2`.

### 2026-08-14 — Foundation Task 1, sixth full review and fix round 11

- Base/head commits: `9d5824a2` → the round-11 fix commit.
- Sixth full review verdict on `81047e91..9d5824a2`: not accepted. One medium, two low — the smallest set of any round. The reviewer also ruled on the recorded divergence from round 10: the split progressive-stream tests were judged insufficient because neither pinned the production composition path.
- Findings and what closed them:
  1. The capability advertised on the database alone while the auth stack mounts only with database and config together, so a config-less router advertised a route it did not serve. The advertisement now derives from the same condition that mounts the route.
  2. Keyed settings accept a query-selected scope, and the account scope skips profile binding. Nothing exposes it today, but the first account-scoped definition would have granted direct sessions account-wide access with no inventory change. Direct sessions refuse the account scope ahead of any contract logic.
  3. The production composition path is pinned: prepareIdentityTransportV3 must emit a token verified against its session. Both new regressions confirmed red.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes.
- Next continuation point: seventh full review.

### 2026-08-14 — Foundation Task 1, seventh full review and fix round 12

- Base/head commits: `997f7f92` → `4ec751e7 fix(auth): admit HEAD probes and the proxy download route`.
- Seventh full review verdict on `81047e91..997f7f92`: not accepted, but for the first time no security findings — two over-restrictions that broke legitimate playback clients and two test-integrity repairs.
- Findings and what closed them:
  1. The router registers HEAD for stream and subtitle delivery, players probe with it, and the allowlist admitted only GET — the inventory had pinned the breakage as though intended. Both admit HEAD now, proven in both directions: an own-session probe reaches the handler, sibling probes refuse. The own-session probe runs last in its test because a probe that finds no media file aborts the session, which the earlier assertions need alive — itself discovered when the probe silently destroyed the fixture.
  2. The proxy-served direct-download route authorizes identically to the admitted tokened route but was absent from the surface.
  3. The round-11 capability condition was unpinned: existing tests forced the flag or built only the fully wired router. A wiring test now builds all four database and config combinations and asserts the advertisement agrees with whether the login route mounts.
  4. The account-scope refusal ran after the definition lookup, so its guarantee depended on the key existing. It now precedes any contract logic and the regression covers an unknown key.
- RED evidence: reverting the two admissions fails the reach-the-handler probes; reverting the refusal ordering fails the unknown-key case; the composition and account-scope regressions from round 11 fail with their fixes reverted.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes.
- Next continuation point: eighth full review of `81047e91..4ec751e7`.

### 2026-08-14 — Foundation Task 1, eighth full review and fix round 13

- Base/head commits: `4ec751e7` → `36e53e10 fix(auth): fail closed without a route guard and finish the HEAD coverage`.
- Eighth full review verdict on `81047e91..4ec751e7`: not accepted. One medium, one low — the smallest set of any round.
- Findings and what closed them:
  1. The middleware failed open: an AuthMiddleware without an installed route guard admitted direct-profile sessions everywhere. A nil guard now refuses them — forgetting the allowlist means nothing is reachable, not everything — with a regression covering guardless refusal, allow-all admission, and account sessions untouched. No existing caller relied on the fail-open.
  2. The round-12 positive probes missed the subtitle HEAD and only probed the proxy with GET, accepting a not-configured 503. Both verbs are now probed against the wired download service with the handler's own answers pinned, and the subtitle probe is ordered before the stream probe, which aborts a media-less session.
- RED evidence: reverting the fail-closed check fails the guardless-refusal case; the round-12 admissions were already red-proven.
- GREEN verification: full `make test-go` against a real PostgreSQL passes. `gofmt` clean. `golangci-lint --new-from-merge-base` clean on changed files. `make verify-local-paths` passes.
- Next continuation point: ninth full review of `81047e91..36e53e10`.

### 2026-08-14 — Foundation Task 1 ACCEPTED

- Final head: `3ffc355b`. The eleventh full review of `81047e91..3ffc355b` returned no findings and the verdict "Task 1 can be accepted."
- The closing rounds, for the record: the ninth review's only finding was a stale comment still describing the pre-round-13 fail-open (fixed in `5f919a9f`); the tenth's only finding was the login-response test decoding into the production type, unable to notice a dropped or newly exposed field (frozen to an exact key-set assertion in `3ffc355b`, red-proven by dropping a field from the handler). Both reviews stated no behavioral authorization issue remained.
- Totals: one initial implementation commit, fifteen fix rounds across five scoped and eleven full reviews, every finding either implemented or explicitly overruled by the maintainer with the divergence recorded. The durable outcome beyond the feature itself: the least-privilege ruling for direct profile logins, a default-deny method-and-pattern route boundary that fails closed, database-enforced credential rotation, and a test suite whose positive assertions pin exact outcomes.
- Parallel execution of the remaining foundation tasks began before acceptance, by maintainer decision: Tasks 2, 3, 4, 5, and 6 are being implemented concurrently by independent agents in separate worktrees branched from `5f919a9f`, with merge order 2→3→4→5→6, pre-assigned migration number ranges, and interface seams where Tasks 4 and 5 consume Task 3's service. Each merged task still passes through the independent review gate before it is marked complete.

## Update template

Append one section per material transition:

```text
### YYYY-MM-DD — Plan/Task and phase

- Base/head commits:
- Decision or ruling:
- RED evidence:
- GREEN verification:
- Independent review verdict:
- Fix commits/findings, if any:
- External side effects: none, or exact authorized action:
- Next continuation point:
```
