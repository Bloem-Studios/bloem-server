# Bloem upstream deployment — September 30, 2026

This is a dated deployment record, not a live status page or a guarantee that a
registry tag still names the same image. Later documentation commits do not
change the application revision recorded here.

## Source and deployed revision

| Item | Revision |
| --- | --- |
| Silo upstream included | `8e2e840474a085c6df6571a5a2850f7eb996810c` |
| Bloem application deployed | `b2ad352a08a7a4b5b1d678dfdba45c7fd006dc96` |
| Application image | `bloem-server:main-b2ad352a0` |
| Previous Bloem deployment before this day's updates | `418a18b7d4ae7060ebc574eb882bc525e7e1a4c2` |

The main upstream merge was `6b769afe7`, incorporating Silo through `b6f5b2f21`.
Commit `396643a5a` added the finalized-membership migration adapter. The final
merge, `b2ad352a0`, included Silo's Trakt pagination consistency retry fix
(`8e2e84047`, upstream PR #1739). Both merges preserve upstream ancestry.

The operator explicitly requested deployment to the Bloem development system
without further backup rehearsals or waiting for a green full suite. This was a
decision for these deployments; it does not certify a public release or change
the repository's normal verification policy.

## Startup and migration correction

The first image applied pending migrations, including the content-rating age
backfill, then stopped at `20260926181741_request_group_limits.sql`. Upstream
updates `users.requests_allowed`; Bloem had already finalized membership policy
authority and retired that live account column.

The fix lives in [`migrations/bloem.go`](../../migrations/bloem.go). The normal
Bloem binary supplies `migrations.BloemFS` to its migration runner. On a finalized
database, the adapted statement transfers legacy blocked request limits to the
account's organization memberships, marks the transaction as a membership-policy
writer, and advances the affected policy revisions. The upstream SQL file and
version number stay unchanged. The adapter rejects an unexpected upstream
statement instead of silently skipping the correction.

The corrected deployment completed the remaining 21 migrations in 10.6 seconds.
The later Trakt-only merge introduced no database migration. See
[upstream integration adapters](../architecture/bloem-upstream-adapters.md) for
the durable implementation boundary and regression command.

## Verification observed

- The merged Go application built, and every Go test package compiled.
- The embedded web production build and both corrected container builds passed.
- The finalized-membership migration regression passed, including blocked
  requests across both memberships of one account and all subsequent migrations.
- The final Trakt package tests passed after the latest upstream merge.
- The seam checker passed all 16 tests and verified **396 declared upstream
  seams**, unchanged by the merges and adapter correction.
- At **21:43:46 UTC**, the final container was healthy with zero restarts.
  `/api/v1/health` and `/api/v1/ready` returned `status: ok`; the web page and its
  entrypoint JavaScript assets returned HTTP 200.
- The final application commit was pushed to `origin/main`; the source worktree
  was clean at verification.

These checks establish build and startup behavior. They do not establish decoded
playback, real-provider Live TV, every native client, or full-suite acceptance.

## Outstanding validation

The full integration run was not green:

- Client DTO coverage did not yet account for newly imported upstream types;
  the client-contract digest also needed reconciliation after documentation moved.
- Scenario adjudication rejected the changed upstream API-key fixture hash.
  Review the fixture and adjudication decision; do not blindly accept new hashes.
- Two `GlobalSearch` tests failed in the full web run. A focused result must not
  be substituted for a new complete-run claim.
- The full Go suite was interrupted when the operator directed deployment.

The final Trakt tests and healthy deployment do not resolve those earlier
failures. The [September 19 record](2026-09-19-xtream-deployment.md) remains
historical evidence for its own revision and acceptance limits.

## Operating this build

Use the existing installation's Compose project, override files, volumes, and
image selection. An image built locally must first be available on the deployment
host; it is not necessarily published under the same tag in GHCR. Check the
running image's revision label as well as the health and readiness endpoints.

Use the Bloem binary for migration status and execution so the owned adapters and
registered Go migrations participate. Running raw SQL through a standalone Goose
CLI is not equivalent. An image rollback does not undo schema migrations; assess
compatibility with the applied schema before changing binaries or restoring data.

The separately operated original Silo server was updated to upstream
`build-1033` on the same date. That Silo build number is not a Bloem release
number and does not identify the Bloem commit above.
