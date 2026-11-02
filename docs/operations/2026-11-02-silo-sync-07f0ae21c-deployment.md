# Bloem deployment — November 2, 2026: Silo through `07f0ae21c`

Production has run `main` at `e3a096b20b9fe146af7b783aa8600665af167b20` since 02:11:36 UTC.
It replaces `7eada59a0` and adds the merge of Silo `main` through `07f0ae21c` (November 2,
01:45 UTC): two Silo commits with no migrations and no conflicts. Jellyfin-protocol Latest rows
honor the user's HidePlayedInLatest setting, and the admin dashboard's Users table no longer
scrolls sideways. Nothing needed regenerating.

The owner asked to merge and deploy. The image was built locally without waiting for CI.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `7eada59a0`. Tagged
  `bloem-server:release-e3a096b20` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `294984add6adac2fde38b079378e1d92d4a7a0786b3f26057b08f900521d2a3b` on both hosts.
- **Before the restart:** container snapshot `pre-main-e3a096b20`. Database restore point of
  the same name at LSN `43A/9CEA15E0`. Segment `000000030000043A0000009C`, which holds it, was
  archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `7eada59a0`. One
  transcode was running inside the server, and the stop took 38 seconds (exit 0). No
  migrations were pending; 752 are applied. The server answered ready at 02:11:43, 51 seconds
  after the stop began, and was healthy with no restarts.
- `/api/v2/system/info` reports `e3a096b2`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-e3a096b20`, healthy, and the server lists both as enabled and healthy on `e3a096b2`.
  Their previous compose files are kept with suffix `.bak-pre-e3a096b20`.

## Verification before deploy

Done locally, with `umask 022` and Go 1.26.10 from `go.mod`:

- the whole Go suite (239 packages) on a sized, fresh PostgreSQL and Redis. One test failed:
  `TestPopulationSyncResumesAfterQuotaAcrossRuns`, which reads rows other packages leave in the
  shared database and passes three times alone;
- the database pins (450) and database contracts (160) on a fresh database, and the 78 mixed
  library location and native audiobook regressions on another;
- Bloem CI's changed-line lint, with no issues on Bloem's lines;
- every contract check, with seams at 308;
- the web typecheck, v2 web types, settings bindings, build and bundle budget, and the whole web
  suite. Three tests failed: the two `AuthProvider.sessionRestore` tests, which already fail on
  `main`, and one `OnboardingGate` test that passes alone.

## Rollback

Start `bloem-server:release-7eada59a0` again and set the transcode nodes back from their
`.bak-pre-e3a096b20` compose files. No schema changed.
