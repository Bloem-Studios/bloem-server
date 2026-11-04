# Bloem deployment — November 4, 2026: Silo through `e3af98b06`

Production has run `main` at `5d5371d6c38a83f50b1373f7b3cf5951fba6f437` since 15:48:08 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `0edcd6e49`, deployed by hand
at 14:21 UTC with no receipt of its own; the last receipt, for the
[maintenance reconcile](2026-11-04-bloem-maintenance-reconcile-deployment.md), recorded
`ca1740a98`. Since `ca1740a98` production gained:

- `289edcc63`: the maintenance reconcile deployment's receipt.
- `74e207aca`, `d690b98a5`, `405ff9dcc` and `0edcd6e49`: web fixes, deployed by hand one by
  one before this batch. A letter jump lands while a scan refreshes the library, a deep
  library grid stays short enough for the browser to draw, the row window waits for a scroll
  to rest, and the phone layout is tidier. They add nine temporary seams, which go when Silo
  merges its own versions.
- `7baff6e46`: the merge of Silo `main` from `6aa6791ea` through `e3af98b06` (November 4,
  14:56 UTC), three Silo commits with no migration. Silo's #3502 accepts Dolby Vision
  profile 0 in a client's profile level capabilities, so playback starts from TVs with
  MediaTek decoders no longer fail with a 422. Silo's #3498 trips the Meilisearch circuit
  breaker from `/api/v2` cursor searches, so searches during a Meilisearch outage stop
  waiting out the client timeout. Silo's #3500 applies the viewer's access filter before the
  limit on an unsorted collection row, so a row whose first members sit in libraries the
  viewer cannot open still fills with the members they can.
- `362faf5a1`: regenerated client DTOs, whose headers now name the merge commit; no wire
  type changed.
- `5d5371d6c`: the status rows for the batch.

## Merge resolutions

No conflicts. Git merged Silo's changes to two seams, `internal/playback/protocol_v3.go` and
`internal/sections/fetcher.go`, beside Bloem's own lines there: the negotiated client
features field on the decision response and the section resolvers. The new collection row
query keeps Silo's library scope and access filter. Bloem needed no fix. No new seam came
with the batch; the seam ledger stands at 317, nine of them the temporary web fixes above.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-5d5371d6c` (image `sha256:ddeb82b8bdf34b0d3d32c337e7e7afbb1fb461e689304526fa661f6193cfcd01`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `20efc5ae628e52a4486a829dfd0014cf114c2066fa52efa1228174b8e04d8da2` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-5d5371d6c`. Database restore point of
  the same name at LSN `483/6AD49738`. Segment `00000003000004830000006A`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `0edcd6e49`. One transcode process
  was running inside the server, and the stop took 7 seconds (exit 0). No migration ran;
  762 are applied. The server answered ready at 15:48:08, 13 seconds after the stop began,
  and was healthy at 15:48:10 with no restarts. Its runtime configuration fingerprint is the
  same as before.
- `/api/v2/system/info` reports `5d5371d6`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-5d5371d6c`, healthy, and the server lists both as enabled and healthy on
  `5d5371d6c`. Their previous compose files are kept with suffix `.bak-pre-5d5371d6c`.
- **Old images.** The script removed `release-ca1740a98` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. No test failed, so no flake was recorded. `main` did not move during the checks.

## Bloem CI

Run 41857799308 passed on `5d5371d6c` at its first attempt.

## Rollback

Start `bloem-server:release-0edcd6e49` again and set the transcode nodes back from their
`.bak-pre-5d5371d6c` compose files; the deploy script's rollback does both. The batch has no
migration, so the previous image runs on the database as it is. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards everything
written since the restore point, taken just before the 15:47 restart.
