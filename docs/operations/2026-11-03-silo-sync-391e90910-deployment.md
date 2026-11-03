# Bloem deployment — November 3, 2026: Silo through `391e90910`

Production has run `main` at `0fc66436b17858927209a4a4179f4d2182611e91` since 02:30:20 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `4d2b68167` and adds:

- `0bf1a9a12`: the merge of Silo `main` from `90f88f2d6` through `391e90910` (November 3,
  01:46 UTC), one Silo commit with one migration. Silo's #3429 stops re-verifying published
  artwork through a separate public or token read endpoint: catalog reads trust publication
  manifests on every backend, the `verify_artwork_delivery` task and the catalog rules that
  demoted unverified rungs are gone, and `reconcile_artwork_cache` now checks every published
  size and resets provider artwork that is missing one. Migration
  `20261102203838_remove_artwork_delivery_verifier.sql` drops the six `delivery_*` columns of
  `artwork_revision_gc_candidates`, the indexes `artwork_delivery_pending_idx` and
  `artwork_delivery_due_idx`, and the task's saved triggers and schedule.
- `e575e29df`: regenerated client DTOs, which only take the new server revision.
- `0fc66436b`: the status rows for the batch.
- The commit pushed since `4d2b68167`: the previous deployment's receipt (`d8aff257b`).

## Merge resolutions

None. The merge had no conflicts and needed no Bloem fix. It touched two seams only on
Silo's own lines (`cmd/silo/main.go`, `internal/api/router.go`); no Bloem code used the
removed verifier, its task or its columns. The seam ledger stays at 309.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-0fc66436b` (image `sha256:ebed1adddc6deda295b9a7602878b848fbf00b9289fd9472a9ab2748b0b6cf79`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `5e4dbd5db10df08ddc4123457cb8ab3ccee88c920762e4cc1c5705df97501896` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-0fc66436b`. Database restore point of
  the same name at LSN `457/80E6458`. Segment `000000030000045700000008`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `4d2b68167`. One transcode was
  running inside the server, and the stop took 7 seconds (exit 0). One migration ran, from
  02:30:12 to 02:30:14; 756 are applied. The server answered ready at 02:30:20, 15 seconds
  after the stop began, and was healthy at 02:30:22 with no restarts. Its runtime
  configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `0fc66436`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-0fc66436b`, healthy, and the server lists both as enabled and healthy on
  `0fc66436b`. Their previous compose files are kept with suffix `.bak-pre-0fc66436b`.
- **Old images.** The script removed `release-7eada59a0` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. No test failed, so
no flake was rerun.

## Bloem CI

Run 41564499173 passed on `0fc66436b` at its first attempt.

## Rollback

Start `bloem-server:release-4d2b68167` again and set the transcode nodes back from their
`.bak-pre-0fc66436b` compose files; the deploy script's rollback does both. Unlike the
previous batch, the migration is not invisible to the previous image: `4d2b68167` still reads
and writes the dropped `delivery_*` columns. On the migrated database its artwork lookups fail
and fall back to the established variants (a logged warning), and its artwork publication
upsert in `internal/catalog/artwork_selection.go` fails, so new artwork cannot be published.
A lasting rollback therefore also runs the migration's Down step, which adds the columns back
with defaults (every revision unverified) and rebuilds the two indexes. Restoring the database
to the restore point together with the container snapshot also rolls back, but discards
everything written since the restore point, taken just before the 02:30 restart.
