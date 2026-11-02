# Bloem deployment — November 2, 2026: Silo through `257390ba1`, and Silo's preferred-version line restored

Production has run `main` at `7eada59a05a9d1e1258e2f9fc0567e672d0d2b26` since 01:48:18 UTC.
It replaces `46354baee` and adds:

- `605bd1003`: the merge of Silo `main` through `257390ba1` (November 2, 00:36 UTC), four
  Silo commits with no migrations and no conflicts. Matroska `S_TEXT/WEBVTT` subtitle tracks
  play through the index, and the dashboard leaderboards count only finished plays; the other
  two only steady tests.
- `f49e73147`: Silo's preferred-version preflight line in `executeReplanV3`, taken verbatim,
  with Bloem's native-storage check moved onto its own lines (below).
- `7eada59a0`: regenerated client DTOs and contract digest for Silo's new `index_only` and
  `unplayable` subtitle track fields.

The owner asked to merge and deploy. The image was built locally without waiting for CI.

## Merge resolutions

Silo wins every conflict. This merge had none. Silo's `matroskaSubtitleTrackIDs` now returns a
match per stream (ID, codec, index-only) instead of a bare ID, so Bloem's native storage probe
(`internal/scanner/bloem_native_probe.go`) applies it the way Silo's
`applyMatroskaSubtitleTrackIDs` does for a local file.

The previous merge had kept Bloem's `nativeVideoPreflight` inside Silo's conflicted
preferred-version line in `internal/api/handlers/playback_v3.go`. That line is Silo's again.
Silo's `preflightPlaybackFile` stats a path, and a native-storage file has none, so the stat
would mark it missing. Native audiobooks in production can reach this line: a quality or output
change during v3 playback replans through it. Bloem's check now sits on its own lines:

- `preflightPlaybackFile` hands a native-storage file to `bloemNativePreflight` just before the
  stat (`internal/api/handlers/playback_missing.go`, a new declared seam).
- `executeReplanV3` puts its playback handler in the request context, so that check goes
  through the handler's provider as before: a healthy native preferred version is played
  again, and a refused one is not. With no handler in the context a native file is
  unavailable. A native file is never marked missing either way.

New tests cover the provider accepting, the provider refusing and no handler, and fail when the
hook is removed. Local files keep Silo's check. The declared seams went from 307 to 308. No
native file in production is marked missing.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `46354baee`. Tagged
  `bloem-server:release-7eada59a0` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `74ed2d06661f2523966ec426d79d61d6f3ae6e74e787a3c7224d91e5e856b116` on both hosts.
- **Before the restart:** container snapshot `pre-main-7eada59a0`. Database restore point of
  the same name at LSN `43A/20BE6CB0`. Segment `000000030000043A00000020`, which holds it, was
  archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `46354baee`. The stop
  took 37 seconds (exit 0). No migrations were pending; 752 are applied. The server answered
  ready at 01:48:25, 46 seconds after the stop began, and was healthy with no restarts.
- `/api/v2/system/info` reports `7eada59a`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-7eada59a0`, healthy, and the server lists both as enabled and healthy on `7eada59a`.
  Their previous compose files are kept with suffix `.bak-pre-7eada59a0`.

## Verification before deploy

Done locally, with `umask 022` and Go 1.26.10 from `go.mod`:

- the whole Go suite (239 packages), on PostgreSQL started with `max_connections=300` and
  `max_locks_per_transaction=512` and Redis for the storage tests. One test failed:
  `TestImageLadderBackfillCurrentArtworkKeepsCompletedVersion`, in a package this change does
  not touch, which passes three times alone;
- the database pins (450) and database contracts (160) on a fresh database, and the 78 mixed
  library location and native audiobook regressions on another;
- Bloem CI's changed-line lint, with no issues on Bloem's lines;
- every contract check (settings bindings, client DTOs, playback fixtures, route inventory,
  migration ledger, scenario catalogs, offline routes, both OpenAPI documents, API catalogue,
  v2 fixtures, seams at 308, local paths);
- the web typecheck, v2 web types, settings bindings, build and bundle budget, and the whole web
  suite. Four tests failed: the two `AuthProvider.sessionRestore` tests, which already fail on
  `main`, and one `OnboardingGate` and one shuffle test that pass alone.

## Rollback

Start `bloem-server:release-46354baee` again and set the transcode nodes back from their
`.bak-pre-7eada59a0` compose files. No schema changed. Restoring the database to the restore
point together with the container snapshot also rolls back, but discards everything written
since 01:47 UTC.
