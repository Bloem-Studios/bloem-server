# Bloem deployment — October 31, 2026: Silo through `288f58200`

Production has run `main` at `29c23fc2ccef6cadaa7d6c5904676139b9517e1d` since 18:18 UTC.
The commit merges the four Silo commits after `07c05cad6`, through `288f58200`, under
the rule that Silo wins every conflict, plus a docs commit. The owner asked for a push
and deploy. The image was built locally without waiting for CI.

## What changed

- A password change can sign out the account's other devices (#2925). The v2
  password capability gains `sign_out_other_sessions`.
- Household writes are rejected once the acting profile's PIN is revoked. The PIN
  revision is checked under the write's transaction lock (#2704).
- Who may create an API key is checked in the transaction that stores it (#3317).
- Silo publishes one Docker image, with PostgreSQL and Redis bundled, under its plain
  tags (#3319). Bloem's image is built from the root `Dockerfile`, which this does not
  change: it still runs `silo` as root, with no bundled services. The Silo-switch steps
  now say which Silo image to pin
  ([Docker deployment](../bloem/overlays/wiki/deployment/docker.md#switching-between-silo-and-bloem)).

The merge commit message records how each conflict was resolved. The seam ledger is
unchanged. There are no migrations.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-29c23fc2c` and loaded on CT135 and the transcode node
  CT172. The binary `/usr/local/bin/silo` has SHA-256
  `88d6608c4edaefa3f4093ad2c944ec819a2fd79601834b7b7567d2fb62bd1ab8` on both hosts.
- **Before the restart:** container snapshot `pre-main-29c23fc2c`. Database restore
  point of the same name at LSN `40E/6C27E788`. Segment `000000030000040E0000006C`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `8065e5746`. One
  transcode was running, and the stop took 37 seconds (exit 0). The migration step
  applied nothing; 747 migrations are applied, none pending. The server answered
  ready at 18:18:56, 46 seconds after the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `29c23fc2`. No error lines were logged after the start, and a transcode was
  running again. The startup warnings came from the environment: the S3 provider
  refuses `PutBucketCors` (403), and the scanner cannot read a hidden folder another
  tool keeps in one television scope, which it leaves out of missing-file
  reconciliation.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-29c23fc2c`, healthy. The API marked both nodes unhealthy while they
  restarted and recovered both at 18:19:50. Their previous compose files are kept with
  suffix `.bak-pre-29c23fc2c`.

## Verification before deploy

Done locally on the merge:

- `go build` and `go vet`
- the full Go suite against a PostgreSQL sized like CI's. One multi-node timing test
  in `cmd/silo` failed under the suite's load and passed three runs alone; the merge
  does not touch its code.
- the database pins on a fresh database, including Silo's three new ones
- Bloem CI's changed-lines lint, with no issues
- every contract check, after regenerating the client DTOs and contract digest for the
  new field
- web typecheck, format and lint, and the account settings tests

## Rollback

Start `bloem-server:release-8065e5746` again. The merge adds no migrations, so that
image runs on the current schema. Restoring the database to the restore point together
with the container snapshot also rolls back, but discards everything written since
18:17.
