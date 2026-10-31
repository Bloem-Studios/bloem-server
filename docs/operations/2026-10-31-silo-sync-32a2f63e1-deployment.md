# Bloem deployment — October 31, 2026: Silo through `32a2f63e1`

Production has run `main` at `4edbee99d1f2d0d408728229701cf379ec83eb3a` since 20:25 UTC.
It replaces `29c23fc2c` and adds:

- `8c99f9c60`: the merge of Silo through `fbe0e89ca`;
- `bfb7594d0`: Silo's household line restored verbatim;
- `18087a03b`: Silo's session-revoked hook type restored;
- `4edbee99d`: the merge of Silo through `32a2f63e1`;
- documentation.

The owner asked to merge and deploy. The image was built locally without waiting for CI.

## What changed

- **From Silo:**
  - upload spools stay private (#3308);
  - stream tokens are refused when the signing secret is empty (#3310);
  - a late admin session restore keeps a sign-out or new sign-in (#3314);
  - a Kindle conversion cache another account controls is refused (#3315);
  - the Roku client can upload diagnostics (#3298);
  - series requests are routed by whether the show is still airing (#3324);
  - an expired `/activate` code no longer asks the sign-in question (#3291).
- **Silo wins.** Silo's lines are verbatim again in `household.go` and, for the
  session-revoked hook, in `router.go`, `admin.go`, `admin_login_sessions.go` and
  `main.go`. Bloem's callers that must fail closed use a Bloem hook. Silo's admin
  user update, delete, sign-out-everywhere, password change and password reset paths
  behave as in Silo. The seam ledger went from 305 to 303.
- **Migration.** One Silo migration,
  `20261031021616_allow_roku_client_diagnostic_reports`, widens a check constraint.
  The previous image accepts the wider constraint.

See the [changelog](../bloem/CHANGELOG.md) for each commit.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-4edbee99d` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `2a8225eb2782542b736bbd517832cea1619fcbaf92ba3235eb0332d44157bcd9` on both hosts.
- **Before the restart:** container snapshot `pre-main-4edbee99d`. Database restore
  point of the same name at LSN `410/E208E328`. Segment `0000000300000410000000E2`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `29c23fc2c`. Two
  transcodes were running, and the stop took 38 seconds (exit 0). The migration step
  applied one migration; 748 are applied, none pending. The server answered ready at
  20:25:55, 48 seconds after the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `4edbee99`. No error lines were logged after the start, and two transcodes
  were running again. The warnings were the S3 provider refusing `PutBucketCors` and
  the embedding provider's rate limit.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-4edbee99d`, healthy. The API marked both unhealthy while they
  restarted and recovered both at 20:26:18. Their previous compose files are kept with
  suffix `.bak-pre-4edbee99d`.

## Verification before deploy

Done locally on `4edbee99d`, with `umask 022` as in CI:

- `go build` and `go vet`;
- the full Go suite (234 packages) against a PostgreSQL sized like CI's;
- the database pins on a fresh database (580);
- Bloem CI's changed-lines lint, with no issues;
- every contract check, after regenerating the client DTOs and contract digest for the
  additive series-status fields.

## Rollback

Start `bloem-server:release-29c23fc2c` again. The one migration only widens a check
constraint, so that image runs on the current schema. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards
everything written since 20:24.
