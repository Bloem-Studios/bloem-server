# Bloem deployment — November 5, 2026: Silo through `2d5f22e7d`

Production has run `main` at `38156af189c53338cfa3e041b11e888db6248427` since 04:04:10 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `a96d2efe2`, recorded in the
receipt for [Silo through `ba1280870`](2026-11-05-silo-sync-ba1280870-deployment.md). Since
`a96d2efe2` production gained:

- `99e5529f0`: that deployment's receipt.
- `3ab048c07`: the merge of Silo `main` from `ba1280870` through `2d5f22e7d` (November 5,
  02:34 UTC), 25 Silo commits with three migrations. Among them: a TV can bind its sign-in
  requests to a secret it keeps, so a phone can sign it in to several accounts after one code
  comparison (Silo #3520); an administrator can keep a hero banner row off the apps with a
  section's `web_only` (Silo #2891); Dolby Vision Profile 7 plays as Profile 8.1 over the HLS
  copy remux (Silo #3447); text subtitle burn-in no longer reads the whole media file before
  the first frame (Silo #3519); audiobook and podcast downloads keep their audio instead of
  failing as a video transcode (Silo #3488); Matroska subtitle languages keep their region,
  such as `pt-BR` and `pt-PT` (Silo #3081); Jellyfin apps and Seerr show profile avatars
  (Silo #2685); a new combined resolution, HDR and audio poster badge (Silo #3523); plus fixes
  to Jellyfin compatibility, personal collection syncs, the matcher queue, stale scans and
  recommendation reads, and eight documentation corrections.
- `b0537be3c` and `38156af18`: a Bloem fix (below) and a refactor of it for the nesting lint.
- `0ae71c000`: regenerated client DTOs with Silo's additive `web_only`, `binding` and
  `session_binding` fields, the re-pinned contract digest, the new v2 fixture and the scenario
  adjudication pin for `api/api-v1-auth.json`.
- `5589fcf9a`: the adapter, native probe and protocol compatibility pages and the status rows
  for the batch.
- `504bc8039`: the native settings bindings for Silo's settings revision 26.

## Merge resolutions

Six files conflicted, and each hunk took Silo's side verbatim:

- `internal/api/handlers/sections.go`, `internal/apiv2/home.go` and
  `internal/api/handlers/library_views_service.go`: Silo's web-only hero rows changed the home
  and library layout calls. Bloem's promoted-row opt-in (`promoted=1`) used to sit inside
  Silo's arguments; it now reaches the home reads only through the context, set on Bloem's own
  line before each Silo call. `library_views_service.go` is Silo's bytes again.
- `internal/downloads/policy.go`: Silo's served-quality `CheckAction` line. Bloem's tenant
  binding moved to its own `bloemBindDownloadTenant` line before it.
- `internal/playback/transcode.go`: Silo's subtitle staging and `AudioOnly`, with Bloem's
  native input fields, Live TV's `SourceAudioCodec` and the native-input skip on their own
  lines. Silo's new subtitle filter writes the `fontsdir` option Bloem's own helper used to add,
  so that helper is gone.
- `internal/jellycompat/dv_strip_test.go`: Silo's comment. Silo now turns tone mapping off in
  those fixtures itself (Silo #3470), so the file is Silo's bytes again.

One Bloem fix: Silo's #3081 made the local probe take each Matroska subtitle track's
`LanguageBCP47`, and Bloem's native-storage probe mirrored the old pass, so native files would
have kept the legacy language. The native probe now does what Silo's local probe does; a test
that compares the two on Silo's new fixture failed before the fix (`b0537be3c`).

The seam ledger went from 321 to 320 entries: `library_views_service.go` and
`dv_strip_test.go` left it, and Silo's new generated fixture
`contracts/api/v2/fixtures/get_device_login_bound.json`, which answers Bloem's server name like
its sibling `get_device_login_ok.json`, joined it with the sibling's reason. No other seam
was added.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-38156af18` (image `sha256:d7e23564a2e379fe09290dea2b692f28dc5241474b7da2f339c0b512c1c91a18`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `eb5037a8d796f3dd2303160ec397168be2ff00e42e88db5745cd8e0ab8891626` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-38156af18`. Database restore point of
  the same name at LSN `491/F2C71AD8`. Segment `0000000300000491000000F2`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `a96d2efe2`. One transcode process
  was running inside the server, and the stop took 38 seconds (exit 0). Three migrations ran,
  in one second: `20261018212157_index_jellycompat_sessions_pseudo_user`,
  `20261022001108_add_page_sections_web_only` and
  `20261104215950_device_login_session_binding`; 765 are applied. The server answered ready
  and healthy at 04:04:10, 46 seconds after the stop began. Its runtime configuration
  fingerprint is the same as before.
- `/api/v2/system/info` reports `38156af1`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-38156af18`, healthy, and the server lists both as enabled and healthy on
  `38156af18`.
- **Old images.** The script removed `release-6604bfd44` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 23 of 25 steps at the first run. The two failures were not tests: the native
settings bindings were stale after Silo's settings revision 26, and the changed-line lint
flagged nesting in the native probe fix. Both were fixed (`504bc8039`, `38156af18`), and the
verify and lint phases then passed 20 of 20 steps. `go test ./internal/scenariocatalog/`
passed. No test needed a rerun alone. `main` did not move during the checks.

## Bloem CI

Run 41963401390 on `38156af18` passed at its first attempt, and the deploy used that result.

## Rollback

Start `bloem-server:release-a96d2efe2` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The three migrations only add an index, two
defaulted flag columns and a nullable binding column whose checks a request without a binding
always meets, so the previous image runs on the database as it is. Restoring the database to the restore point
together with the container snapshot also rolls back, but discards everything written since
the restore point, taken just before the 04:03 restart.
