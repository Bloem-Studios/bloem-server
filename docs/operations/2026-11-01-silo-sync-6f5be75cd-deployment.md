# Bloem deployment — November 1, 2026: Silo through `6f5be75cd`

Production has run `main` at `46354baee6e6acc1f89a1e789d1585d4d9c58c35` since 21:22:44 UTC.
It replaces `99a4a1133` and adds:

- `de1ea66cf`: the merge of Silo `main` through `6f5be75cd` (November 1, 20:58 UTC), nine
  Silo commits with two migrations. They bring embedded subtitles that are read, kept and
  streamed without reading the whole file; box sets built from TMDB collections; enforced
  negotiated audio tracks; Jellyfin-protocol transcodes that move off a dead transcode node,
  are sized by the source rather than the client's ceiling, and copy video as MPEG-TS for
  clients that list only TS; an artwork reconcile sweep that skips app-relative paths; and
  Sonarr and Radarr webhooks that carry a real token being accepted again.
- `46354baee`: regenerated contracts after the merge (client DTOs, playback fixtures, the API
  catalogue, the client coverage allowlist and digest) and one test seam.

Box sets are off for every library until an administrator turns them on in the library's
settings. The owner asked to merge and deploy. The image was built locally without waiting
for CI.

## Merge resolutions

Silo wins; Bloem's lines stay on their own. Seven files conflicted:

- `cmd/silo/main.go`: Silo's kept-subtitle wiring, then Bloem's native storage artwork line.
- `internal/api/router.go`: Silo's box set syncer and v2 box set lines, then Bloem's library
  storage locations and its preview-browse override on their own lines.
- `internal/api/handlers/libraries.go` and `libraries_service.go`: Silo's box set fields beside
  Bloem's storage-source fields.
- `internal/playback/transcode.go`: Silo's `RequireAudioTrack` beside Bloem's
  `SourceAudioCodec`.
- `internal/api/handlers/playback_v3.go`: Bloem's native-video admission block, then Silo's new
  transcode options line. Silo's edition preflight line keeps the existing Bloem seam: like
  the file's five other preflight calls it goes through `nativeVideoPreflight`, so a file on
  native storage is not marked missing.
- `web/src/components/admin/libraries/useLibraryForm.ts`: both imports.

Silo's three new box set reads are not on Bloem's preview allowlist, so preview principals are
refused them; browsing with `group=box_set` still goes through Bloem's preview reader. The
declared seams went from 304 to 307: Silo's new generated decision fixture (two copies) carries
Bloem's `negotiated_client_features` like its siblings, and Silo's new box set form test mocks
the storage sources query Bloem's library form reads, like its three sibling tests.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `99a4a1133`. Tagged
  `bloem-server:release-46354baee` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `e02e9fafcd0d8fecd7c45018858d7b3118820f47842787b878eca2aea3444f3c` on both hosts.
- **Before the restart:** container snapshot `pre-main-46354baee`. Database restore point of
  the same name at LSN `435/1D39A460`. Segment `00000003000004350000001D`, which holds it, was
  archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `99a4a1133`. One
  transcode was running inside the server, and the stop took 37 seconds (exit 0). Both
  migrations (`20261019120000_add_movie_box_sets`, `20261029232944_add_embedded_subtitle_tracks`)
  applied in about a second; 752 are applied. Both are dated before Bloem's latest migration,
  which the migrator accepts. The server answered ready at 21:22:53, 48 seconds after the stop
  began, and was healthy with no restarts.
- `/api/v2/system/info` reports `46354bae`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
  In the first minute the server warned about slow home sections while its caches were cold.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-46354baee`, healthy, and the server lists both as enabled and healthy on `46354bae`.
  Their previous compose files are kept with suffix `.bak-pre-46354baee`.

## Verification before deploy

Done locally on the merge, with `umask 022` and Go 1.26.10 from `go.mod`:

- the whole Go suite (239 packages), on PostgreSQL started with `max_connections=300` and
  `max_locks_per_transaction=512` and Redis for the storage tests. Four packages failed on the
  first run: the three client contract packages, because the run started before the contracts
  were regenerated, and one markers test that reads rows other packages leave in the shared
  database. All four pass on the final tree, the markers test three times alone;
- the database pins (449) and database contracts (160) on a fresh database, and the 78 mixed
  library location and native audiobook regressions on another;
- Bloem CI's changed-line lint for merges, with no issues on Bloem's lines;
- every contract check (settings bindings, client DTOs, playback fixtures, route inventory,
  migration ledger, scenario catalogs, offline routes, both OpenAPI documents, API catalogue,
  v2 fixtures, seams at 307, local paths);
- the web typecheck, v2 web types, settings bindings, build and bundle budget, and the whole web
  suite. Four tests failed: the two `AuthProvider.sessionRestore` tests, which already fail on
  `main`, and one `OnboardingGate` and one shuffle test that pass alone.

## Rollback

Start `bloem-server:release-99a4a1133` again and set the transcode nodes back from their
`.bak-pre-46354baee` compose files. The two migrations only add tables, columns, triggers and a
widened cleanup rule, which the previous image ignores. Restoring the database to the restore
point together with the container snapshot also rolls back, but discards everything written
since 21:21 UTC.
