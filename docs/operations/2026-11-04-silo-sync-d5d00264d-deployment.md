# Bloem deployment — November 4, 2026: Silo through `d5d00264d`

Production has run `main` at `479b275760e1756b3bedac53b8fb82354c9f4180` since 02:59:29 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `159f2ef46`, which the
[`b8114188f` receipt](2026-11-04-silo-sync-b8114188f-deployment.md) recorded. Since
`159f2ef46` production gained:

- `8529090ef`: the previous deployment's receipt.
- `745b95103`: the merge of Silo `main` from `b8114188f` through `d5d00264d` (November 4,
  02:05 UTC), seven Silo commits with one migration. Silo's #3437 shows collections as one
  full-width poster wall. Silo's #3430 lets requesters remove their own failed requests
  (`DELETE /api/v2/requests/{id}`, advertised by `remove_failed_supported`). Silo's #2349
  relays administrator playback commands and terminates over Redis to the replica that holds
  the session's control lane. Silo's #2372 adds an opt-in `INTEL_QSV_RUNTIME=vpl` image build
  argument; the default image is unchanged. Silo's #2765 gives poster corners to watch status,
  adds badge styles and an administrator Badge images library, and adds `watched_count` and
  `unplayed_count` to the v2 user state. Silo's #3474 ranks series and movies above episodes
  in search. Silo's #3465 makes copy buttons work over plain HTTP.
- `13efbfc5f`: regenerated client DTOs (the new overlay image type and fields, the episode
  counts and `remove_failed_supported`), the re-pinned client digest and the API catalogue.
- `65d18fb10`: the status rows for the batch.
- `57763998d`: a test that keeps removing a failed request inside the organization.
- `479b27576`: Bloem's native settings bindings regenerated for settings manifest
  revision 25.

## Merge resolutions

One conflict, resolved to Silo's side verbatim, from Silo's #3430:

- `internal/requests/service.go`: Silo moved the owner check out of `cancel` into a new
  `ownedRequest`, which its new `RemoveFailed` shares. Bloem's organization check
  (`requireRequestOrganization`, the owner may act) moved from `cancel` onto its own added
  lines in `ownedRequest`, so it guards cancelling and removing alike. A new
  `RemoveFailed` row in `TestBloemAdminCannotMutateAnotherOrganizationsRequest` fails
  without that check; the `Cancel` row fails without it too.

Bloem's copy of the request service test fake
(`internal/api/handlers/bloem_upstream_fixtures_test.go`) gained Silo's new `RemoveFailed`
method. Silo's new overlay image routes need Silo's `admin` role, which only platform
administrators hold, so they need no Bloem change. No new seam; the seam ledger stays at
308. The new wire types are additive, so the client digest was re-pinned with no removal.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-479b27576` (image `sha256:54d2c36f8e863d26442511ec7f12365e2d860d65e38cd1add2f129c471954c23`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `0469a97dc4154b63f0611c01f0c01e0b4393f2f02367857badb35bfb53875549` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-479b27576`. Database restore point of
  the same name at LSN `474/D29D07A8`. Segment `0000000300000474000000D2`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `159f2ef46`. Two transcode processes
  were running inside the server, and the stop took 37 seconds (exit 0). One migration ran,
  `20261020020437_add_overlay_images`, which adds the `overlay_images` table; 757 are
  applied. The server answered ready at 02:59:29, 43 seconds after the stop began, and was
  healthy at 02:59:31 with no restarts. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `479b2757`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-479b27576`, healthy, and the server lists both as enabled and healthy on
  `479b27576`. Their previous compose files are kept with suffix `.bak-pre-479b27576`.
- **Old images.** The script removed `release-aa9e17d90` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed after one fix. The first run failed only `verify/settings-bindings`: Silo's
#2765 moved the settings manifest to revision 25 and Bloem's generated Kotlin and Swift
setting keys still said 24, so `479b27576` regenerated them. The verify and lint phases then
passed 20 of 20 steps; build and the scenario executor had passed in the first run, and
`go test ./internal/scenariocatalog/` passed. No test failed, so no flake was recorded.
`main` did not move during the checks.

## Bloem CI

Run 41762383885 passed on `479b27576` at its first attempt.

## Rollback

Start `bloem-server:release-159f2ef46` again and set the transcode nodes back from their
`.bak-pre-479b27576` compose files; the deploy script's rollback does both. The migration
only adds the `overlay_images` table, which the previous image neither reads nor writes, so
the previous image runs on the database as it is. Restoring the database to the restore point
together with the container snapshot also rolls back, but discards everything written since
the restore point, taken just before the 02:58 restart.
