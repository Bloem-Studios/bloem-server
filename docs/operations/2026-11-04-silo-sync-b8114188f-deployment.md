# Bloem deployment — November 4, 2026: Silo through `b8114188f`

Production has run `main` at `159f2ef468c0efcb80e5b3557d247d931ca7e10b` since 02:17:02 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `341108c6e`, which the
[`f2393ad21` receipt](2026-11-04-silo-sync-f2393ad21-deployment.md) recorded. Since
`341108c6e` production gained:

- `788f018e5`: the previous deployment's receipt.
- `c1aae06b2`: the merge of Silo `main` from `f2393ad21` through `b8114188f` (November 4,
  00:31 UTC), nine Silo commits with no migration. Silo's #3460 sends compressed responses
  up to 8 MiB with their length instead of chunked. Silo's #3461 adds the
  `silo postgres-tune-conf` subcommand for the all-in-one image. Silo's #3457 makes the
  Similar lists cheaper and adds a `withReasons` argument to `SimilarItems`. Silo's #3453
  reads the viewer gate's account, access group, profile and preferences as one batch, reads
  a profile's allowed libraries with its row, and carries `home.hide_watched_items` on the
  access scope. Silo's #3452 memoizes viewer-scope policy decisions per bundle. Silo's #3454,
  #3455 and #3456 send read-only catalog statements in one round trip, read a card's
  resumable progress once, and keep generic plans for hot single-item statements. Silo's
  #3466 changes only Silo's own CI.
- `7d5f8b586`, `0462cd353`, `76c2b64d7`: Silo's batched viewer read stays off under Bloem
  tenancy, applied through a wiring helper that refuses to start without it.
- `b70596c9d`: regenerated client DTOs (the server revision stamp only).
- `b436fdc79`: Silo's queued group and profile reads answer on Bloem's schema.
- `9202011e1`: the policy package's tests run on their own prepared database.
- `e3c712235`, `44b11d380`: the status rows and the adapters page for the batch.
- `159f2ef46`: the profile prelude's statement budget lowered from 9 to 8.

## Merge resolutions

Five conflicts, each resolved to Silo's side verbatim, all from Silo's #3453:

- `internal/access/group_store.go`: Bloem's organization-scoped account group read
  (`bloemAccountGroupPolicy`) runs from a hook line ahead of Silo's read.
- `internal/api/router.go`: Silo's viewer-resolver line is verbatim; Bloem applies tenancy
  on its own next line (`withBloemViewerTenancy`).
- `internal/policy/viewer_resolver.go`: Silo's struct fields verbatim; Bloem's
  `tenantLibraries` field sits on its own line below them.
- `internal/userstore/pgstore/profiles.go`: Silo's `scanProfile` and `getProfile` verbatim.
  Bloem's profile columns (sign-in, credential revision, organization, group) are selected
  after Silo's columns and scanned through `scanProfile`'s new extra targets, in
  `bloem_profiles.go`, reached by hook lines in `getProfile` and `ListProfiles`.
- `internal/userstore/pgstore/profile_libraries.go`: Silo's file whole. It is no longer a
  seam.

Silo's #3453 also needed three Bloem fixes, each with a test that failed without it.
Silo's batched viewer read would have read the account's group and a profile without
Bloem's columns. In a test on Bloem's schema it gave a group member
`PlaybackAllowed: false`, where the one-at-a-time read gives `true`. Silo's queued reads
(`QueueGroupPolicy`, `QueueProfile`) now take Bloem's hooks, so Silo's new
`TestViewerResolverBatchedReadsMatchEachDB` passes. Bloem tenancy keeps the batch off in
production, because a batch carries no request tenant and Bloem applies the profile's own
group. Bloem's resolved-similar route asks for reasons under Silo's new `SimilarItems`
argument. No new seam; the seam ledger went from 309 to 308. No new wire types, so the
client coverage and digest are unchanged.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-159f2ef46` (image `sha256:a3a02f860020db0a0c196ed4260f0c79e930072e54383510ece7bc0a01358363`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `8b797485ba3adb4cd0bddddbc0c7905f9ace6863db43f7dacba12f37a46d3976` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-159f2ef46`. Database restore point of
  the same name at LSN `473/C19E34B8`. Segment `0000000300000473000000C1`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `341108c6e`. One transcode process
  was running inside the server, and the stop took 37 seconds (exit 0). No migration ran;
  756 are applied. The server answered ready at 02:17:02, 45 seconds after the stop began,
  and was healthy at 02:17:06 with no restarts. Its runtime configuration fingerprint is the
  same as before.
- `/api/v2/system/info` reports `159f2ef4`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-159f2ef46`, healthy, and the server lists both as enabled and healthy on
  `159f2ef46`. Their previous compose files are kept with suffix `.bak-pre-159f2ef46`.
- **Old images.** The script removed `release-040b55f10` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed after one fix: changed-line lint refused an inline type assertion on
Bloem's router line, which moved into `withBloemViewerTenancy` (`76c2b64d7`). Then all 24
build, verify and lint steps and the scenario executor passed, and
`go test ./internal/scenariocatalog/` passed. The tenancy jobs' profile persistence and
access, policy and resource-tenancy suites passed on their own database. `main` did not move
during the checks.

## Bloem CI

Run 41753691959 on `44b11d380` failed twice in Go test (3/4):
`TestProfileScopedPreludeStatementBudget` saw 8 statements against a budget of 9, because
a profile's allowed libraries now come with its row. It failed alone too and passed on the
previous `main`, so `159f2ef46` lowered the budget. Run 41756767561 then passed on
`159f2ef46` at its second attempt. The first attempt failed in Go test (3/4) on Silo's
`TestDirectDownloadLinkDelivery/tampered`, which passed alone 20 times out of 20. That test
replaces a token's last two characters with `xx`, which leaves a signature already ending in
`x` and one of `w`–`z` unchanged (the last base64url character carries two padding bits), so
it fails about once in a thousand runs.

## Rollback

Start `bloem-server:release-341108c6e` again and set the transcode nodes back from their
`.bak-pre-159f2ef46` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is; going back only restores the slower
viewer gate, Similar lists and catalog reads. Restoring the database to the restore point
together with the container snapshot also rolls back, but discards everything written since
the restore point, taken just before the 02:16 restart.
