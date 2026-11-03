# Bloem deployment — November 3, 2026: storage ebooks with a placeholder ISBN

Production ran `main` at `040b55f10d8a429db287427198e4e122698d15dd` from 13:20:54 UTC until
the merge bot deployed `aa9e17d90` at 15:23:38, which keeps this change
([receipt](2026-11-03-silo-sync-a5325887c-deployment.md)). It was deployed by hand with the
deploy script, before Bloem CI finished, at Jim's request; CI passed afterwards. It replaced
`3a3fae193` and added:

- `962a3f1a9`: storage ebooks no longer group by a placeholder ISBN (see below).
- `040b55f10`: the lint findings CI raised on `962a3f1a9`.
- The commit pushed since `3a3fae193`: the previous deployment's receipt (`2dcd1aea3`).

## The problem

The Bookwarehouse ebook library (library 25) showed 2,770 different books as one title,
"Ceremony". Bookwarehouse gives `0000000000000` as the ISBN of books that have none. That
string passes the ISBN-13 checksum, and storage publication groups files by ISBN first, so
every such book joined one item. The filesystem ebook library has the same placeholder on
421 files but groups them differently and was out of scope.

`962a3f1a9` changes Bloem's storage publication (`internal/scanner/storage_ebook.go`, a
Bloem file; no seam). An ISBN of one repeated digit, or an ISBN-13 outside 978/979, is
dropped. The book then groups by title and authors, or by its own path when it names no
author, and the placeholder is not published as its ISBN. A file still grouped by a
placeholder regroups the next time it is listed, even when its entry is unchanged, and an
item it leaves empty is removed in the same transaction.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-040b55f10` (image `sha256:75d191f192c8a4fdabdf2837aa5fb4290eef1d38fd6c5364dcbac31dcce1f676`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `6ca7b71c60fab92cc9b2cb2783a5d388bf0ef477fd704160ecba65d87ca71871` on both hosts.
  The first attempt stopped before the restart because the fix worktree had no
  `web/node_modules`; `pnpm install --frozen-lockfile` there, then the second attempt ran.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-040b55f10`. Database restore point of
  the same name at LSN `463/82CC4190`. Segment `000000030000046300000082`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `3a3fae193`. One transcode was
  running inside the server, and the stop took 36 seconds (exit 0). No migration ran; 756 are
  applied. The server answered ready at 13:20:54, 43 seconds after the stop began, and was
  healthy at 13:20:56 with no restarts. Its runtime configuration fingerprint is the same as
  before.
- `/api/v2/system/info` reports `040b55f1`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-040b55f10`, healthy, and the server lists both as enabled and healthy on
  `040b55f10`. Their previous compose files are kept with suffix `.bak-pre-040b55f10`.
- **Old images.** The script removed `release-c5a2b43f3` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Repairing library 25

The books a placeholder had joined are only regrouped when they are listed, and the nightly
scan lists only what changed. At 13:21 the two change tokens of library 25's location
(`epub` and `pdf`) were deleted, so its next scan lists everything, and at 13:22 a scan of
library 25 was started with `POST /api/v2/scan`. Run `9b33aa27-d0a2-418c-a6a1-23716db91098`
listed fully from 13:22:02 to 15:31:24 UTC (2 h 9 min) and completed. The merge bot's
restart at 15:23 stopped it once; the scan queue resumed it from its journal at 15:23:40 under
the same run (lease epoch 2), on `aa9e17d90`, which carries the same fix.

| Library 25 | Before | After |
| --- | --- | --- |
| Items | 796,778 | 803,277 |
| Files | 800,639 | 804,366 |
| Files in the merged "Ceremony" item | 2,770 | 0 (item removed) |
| Files grouped by `ebook:isbn:0000000000000` | 2,770 | 0 |
| Items without a file | — | 0 |
| Items published with a placeholder ISBN | — | 0 |

- 3,727 files are new: books Bookwarehouse added after the 02:12 nightly scan's cutoff. Each
  is its own item.
- 3,444 files that existed before the scan were rewritten: the 2,770 regrouped books and
  entries Bookwarehouse changed. All carry title-and-author keys, and each is in its own item.
- Items rose by 6,499: 3,727 new books, 2,770 split out, one merged item removed, and three
  more splits that were not traced. The largest item now has 10 files, a group that shares
  a real ISBN.
- At 15:35 Bookwarehouse held 802,291 EPUBs and 2,589 PDFs (804,880). Library 25 has
  804,366 files. The 514 missing EPUBs are exactly the ones Bookwarehouse changed after the
  listing began; the next nightly scan adds them. The Bookwarehouse plugin lists only EPUB
  and PDF, so its MOBI and other formats are not in the library.

## Checks before the push

`go test ./internal/scanner/... ./internal/libraryingest/... ./internal/bookrequests/...
./internal/storagesource/...` passed against the local gate's PostgreSQL. The new database
test fails against the previous code with `items=1 files=5`. `golangci-lint` v2.12.2 run with
`--new-from-rev` reports 0 issues on `040b55f10`. A local run of
`scripts/bloem-lint-changed-ci.sh` had reported `962a3f1a9` clean although CI found four
issues; running `golangci-lint` directly reproduces CI's result.

## Bloem CI

- Run 41639927433 on `962a3f1a9` failed in Go lint with four findings, fixed in `040b55f10`.
- Run 41642333720 on `040b55f10` first failed in two jobs: Go contract checks could not
  resolve `github.com` while fetching Silo, and `internal/playback`
  `TestNativeInputExitedWrapperCancellationJoinsDescendant` read `/proc` for a process that
  had already exited. Both are outside this change. The failed jobs were rerun once and
  passed.

## Rollback

Start `bloem-server:release-3a3fae193` again and set the transcode nodes back from their
`.bak-pre-040b55f10` compose files; the deploy script's rollback does both. No migration ran,
so the previous image works on the database as it is. The repaired books stay apart, because
a published file keeps its item, but the previous image would group new placeholder-ISBN
books into one item again. Restoring the database to the restore point together with the
container snapshot also rolls back, but discards everything written since 13:20, including
the repair.
