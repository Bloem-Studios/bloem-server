# Bloem deployment — November 3, 2026: Silo through `527e40436`

Production has run `main` at `4939c4cfa03e8ed6abf59f9249677c9df46dcd8b` since 17:46:51 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `dc4f67200`, which the
[`d10d9c937` receipt](2026-11-03-silo-sync-d10d9c937-deployment.md) recorded. Since
`dc4f67200` production gained:

- `1983228e1`: the previous deployment's receipt.
- `29a393b93`: the merge of Silo `main` from `d10d9c937` through `527e40436` (November 3,
  16:59 UTC), one Silo commit with no migration. Silo's #3327 lets packaged TV web apps
  (webOS, Tizen), which send an opaque (`null`) or non-web Origin, open the v2 events,
  playback-control and Watch Together sockets; the single-use ticket still authorizes each
  socket, and `http` and `https` Origins keep the exact match. Every subtitle sidecar
  answer, errors and `HEAD` included, now carries `Access-Control-Allow-Origin: *`, so those
  apps can read external and downloaded subtitles.
- `66ab93064`: regenerated client DTOs (the server revision stamp only).
- `4939c4cfa`: the status rows for the batch.

## Merge resolutions

None. The merge had no conflicts. It touched three seams, `internal/api/handlers/stream.go`,
`contracts/api/v2/openapi.json` and `web/src/api/v2/schema.ts`, and each merged
automatically with Bloem's lines unchanged. Silo moved the subtitle CORS header to the top
of `HandleSubtitle`, so Bloem's native storage subtitle branches carry it too. Bloem's
events socket keeps its tenant binding on tickets, and Bloem has no origin check of its own.
No new seam; the seam ledger stays at 309. No new wire types, so the client coverage and
digest are unchanged.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-4939c4cfa` (image `sha256:70724cf00c05162823aaac427a45d2b36a9272df3608879253f32f591fb75d92`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `a8eb0a742131251cd79e4677a50636d9e0c51bc806be0dba13f554c5ebc85c6c` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-4939c4cfa`. Database restore point of
  the same name at LSN `46A/3973E2B0`. Segment `000000030000046A00000039`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `dc4f67200`. Two transcode processes
  were running inside the server, and the stop took 37 seconds (exit 0). No migration ran;
  756 are applied. The server answered ready at 17:46:51, 44 seconds after the stop began,
  and was healthy at 17:46:56 with no restarts. Its runtime configuration fingerprint is the
  same as before.
- `/api/v2/system/info` reports `4939c4cf`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-4939c4cfa`, healthy, and the server lists both as enabled and healthy on
  `4939c4cfa`. Their previous compose files are kept with suffix `.bak-pre-4939c4cfa`.
- **Old images.** The script removed `release-0fc66436b` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks passed 25 of 25 steps (build, verify, changed-line lint and
the scenario executor), and `go test ./internal/scenariocatalog/` passed. `main` did not move
during the checks. No test failed, so no flake was rerun.

## Bloem CI

Run 41684245860 passed on `4939c4cfa` at its first attempt.

## Rollback

Start `bloem-server:release-dc4f67200` again and set the transcode nodes back from their
`.bak-pre-4939c4cfa` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is; going back only restores the old
refusal of packaged-app socket Origins and the narrower subtitle CORS. Restoring the
database to the restore point together with the container snapshot also rolls back, but
discards everything written since the restore point, taken just before the 17:46 restart.
