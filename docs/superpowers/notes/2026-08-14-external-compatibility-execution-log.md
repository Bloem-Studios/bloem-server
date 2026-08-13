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

Status: fix round 1 in progress.

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
