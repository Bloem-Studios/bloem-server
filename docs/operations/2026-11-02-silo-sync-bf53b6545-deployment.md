# Bloem deployment — November 2, 2026: Silo through `bf53b6545`

Production has run `main` at `4f7cfbc9bf83a376f8ca106713bf26d9e3f47b0a` since 23:05:20 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `c5a2b43f3` and adds:

- `05867aa37`: the merge of Silo `main` from `56e6f871c` through `bf53b6545` (November 2,
  22:18 UTC), one Silo commit with no migrations. Silo's #3431 draws the "This film" ring on a
  movie page's box set row inside its card, with the poster's corner radius, so the row no
  longer cuts off its edge on the first and last card. It is one CSS class in
  `web/src/components/boxsets/MovieBoxSetRow.tsx`.
- `34d8b8af8`: regenerated client DTOs, which only take the new server revision.
- `4f7cfbc9b`: the status rows for the batch.
- The commits pushed since `c5a2b43f3`: Bloem's pin of Silo's new auth scenario catalog
  (`34031541d`), one command that moves the adjudication pins after a Silo catalog change
  (`e9ab93a40`), and the previous deployment's receipt (`b84daf290`).

## Merge resolutions

None. The merge had no conflicts, touched no seam and needed no Bloem fix. The seam ledger
stays at 309.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-4f7cfbc9b` (image `sha256:fe3a74425b3e1601166f5b3ea7de55eb70b423118b5b41de6177231e82219793`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `617001f9d316ac5ee51ca861afceacb74f45be83b994eeb38fdcdb1c84269778` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-4f7cfbc9b`. Database restore point of
  the same name at LSN `452/AE02B128`. Segment `0000000300000452000000AE`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `c5a2b43f3`. Two transcodes were
  running inside the server, and the stop took 36 seconds (exit 0). No migrations were
  pending; 754 are applied. The server answered ready at 23:05:20, 45 seconds after the stop
  began, and was healthy at the same time with no restarts. Its runtime configuration
  fingerprint is the same as before.
- `/api/v2/system/info` reports `4f7cfbc9`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-4f7cfbc9b`, healthy, and the server lists both as enabled and healthy on
  `4f7cfbc9b`. Their previous compose files are kept with suffix `.bak-pre-4f7cfbc9b`.
- **Old images.** The script removed `release-99a4a1133` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. No test failed, so
no flake was rerun.

## Bloem CI

Run 41539994622 passed on `4f7cfbc9b` at its first attempt.

## Rollback

Start `bloem-server:release-c5a2b43f3` again and set the transcode nodes back from their
`.bak-pre-4f7cfbc9b` compose files; the deploy script's rollback does both. No migration ran,
so the previous image reads the database as it is. Restoring the database to the restore
point together with the container snapshot also rolls back, but discards everything written
since the restore point, taken just before the 23:04 restart.
