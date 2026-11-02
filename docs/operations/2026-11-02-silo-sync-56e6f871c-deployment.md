# Bloem deployment — November 2, 2026: Silo through `56e6f871c`, the merge bot's first batch

Production has run `main` at `c5a2b43f313020b92d463c037950089776841eea` since 21:14:17 UTC.
It replaces `e3a096b20` and adds:

- `209447445`: the merge bot's first batch, the merge of Silo `main` from `07f0ae21c` through
  `56e6f871c` (November 2, 18:16 UTC), nineteen Silo commits with two migrations. They bring
  seek previews served straight from an external read endpoint, a deleted audiobook folder
  removed on the monitor scan, podcast show folders scanned as podcasts, local artwork read in
  title folders linked out of the library, a plugin list that survives one unreadable stored
  archive, played dates from Jellyfin clients accepted, marker grants required for secondary
  admin profiles, "limit reached" instead of "server busy" when an account limit blocks
  playback, push recovery after an iPhone reinstall, MPTCP off on Silo's listeners, titles held
  by the removal grace left out of counts, accounts with no email, username or password hash
  read, visible unchecked checkboxes, and synced lists that sort in either direction.
- `631730bb4`, `5a044930c` and `60a7effa9`: three Bloem fixes on Bloem's own lines (below).
- `a7359de60`: regenerated client DTOs, coverage reasons, client digest and API catalogue.
- `adf035b66`: the documentation for the batch.
- `c5a2b43f3`: `main` merged into the batch. It brings the commits pushed since `e3a096b20`:
  Silo's session restore waits for Bloem's preview status only on a known preview host
  (`83e262d38`); the rest are CI fixes, the local gate, the merge bot's review list, the daily
  sync switched off, and documentation.

The owner asked for the batch to go live immediately. The merge bot was waiting for Bloem CI
on `c5a2b43f3` and was stopped there. The commit went out by hand with the deploy script's
operator-only `--skip-ci-check`, which the bot never passes. [Bloem CI](#bloem-ci) says what
CI found.

## Merge resolutions

Silo wins; Bloem's lines stay on their own. Six files conflicted, all from Silo's #2177
(`243d7e0da`), and every hunk took Silo's side:

- `internal/auth/permissions.go`: Silo's exported `AssignablePermissions`, which the frozen v1
  admin list reads. Silo's map holds only Silo's two permissions, so the merge dropped
  `watch_live_tv` from an admin's v1 list. `631730bb4` registers Bloem's whole permission
  catalog in Silo's map at start-up. The file matches Silo again, and the seam ledger goes
  from 310 to 309.
- `internal/policy/vendor/permission.rego` and `internal/policy/permission_parity_test.go`:
  Silo's deletions, with Bloem's unused `watch_live_tv` rule and import going too.
- `internal/api/handlers/admin.go`: Silo's `auth.PolicyPermissions` line, with Bloem's two
  bitrate fields back on their own lines.
- `internal/api/handlers/auth.go`: Silo's `effectiveAccountPolicy` line in Login. The sign-in
  request names no organization, so a grouped account was told it may download, and got
  permissions its group masks. `5a044930c` adds two Bloem lines before Silo's return that
  resolve the policy in the account's own organization, and fails closed when that cannot be
  worked out.
- `contracts/api/v2/fixtures/admin_policy_vendor.json`: Silo's side, regenerated so it embeds
  Bloem's `permission.rego`.

Silo's #3392 taught Silo's account reads to accept an account with only a username or only an
email. Bloem's member list, member actions and member delete still failed on the missing
value, so one such member broke the whole list. `60a7effa9` reads it as empty, as Silo does.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-c5a2b43f3`, and loaded it on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `7d65e45215c7bc89d900a04ca9575bdb4d510029be95d4acef85b5cff5ca98fe` on both hosts.
- **Compose.** The server's compose overrides file did not exist yet. The script wrote it from
  the live compose files and checked that the rendered configuration was unchanged.
- **Before the restart:** container snapshot `pre-main-c5a2b43f3`. Database restore point of
  the same name at LSN `450/9364B580`. Segment `000000030000045000000093`, which holds it, was
  archived after a WAL switch, with no new archive failures.
- **Restart.** The script required the live image to be `e3a096b20`. Two transcodes were
  running inside the server, and the stop took 9 seconds (exit 0). Both migrations
  (`20261102003211_requeue_local_artwork_refused_outside_roots`,
  `20261102144235_add_trickplay_delivery_scope`) applied in about a second; 754 are applied.
  The server answered ready at 21:14:21, 15 seconds after the stop began, and was healthy at
  21:14:23 with no restarts. Its runtime configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `c5a2b43f`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-c5a2b43f3`, healthy, and the server lists both as enabled and healthy on
  `c5a2b43f3`. Their previous compose files are kept with suffix `.bak-pre-c5a2b43f3`.
- **Old images.** The script removed release images beyond the five newest on both hosts and
  on the build machine, keeping `release-c5a2b43f3` and `release-e3a096b20`. On the build
  machine one of them, `release-8065e5746`, stayed because a container still uses it.

## Bloem CI

The deploy did not wait for a passing Bloem CI run. The run on `c5a2b43f3` passed 22 of its
24 jobs. The two that failed, the Scenario executor and the Go test shard that holds
`internal/scenariocatalog`, failed on the same thing: Bloem's pin of Silo's auth scenario
catalog `api/api-v1-auth.json`, a test fixture. Silo's #2177 changed that catalog's admin
login rows, so the digest Bloem had pinned no longer matched ("adjudicated baseline
changed"). No server code was involved. `34031541d` moved the pin afterwards. None of Bloem's
adjudications on that catalog names a changed row, and the scenario executor passes on it
locally.

## Rollback

Start `bloem-server:release-e3a096b20` again and set the transcode nodes back from their
`.bak-pre-c5a2b43f3` compose files; the deploy script's rollback does both. The previous image
ignores the two new nullable columns, and any artwork job requeued by the other migration is
only refused again. Restoring the database to the restore point together with the container
snapshot also rolls back, but discards everything written since the restore point, taken just
before the 21:14 restart.
