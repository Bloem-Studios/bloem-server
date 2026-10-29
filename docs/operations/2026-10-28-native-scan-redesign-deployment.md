# Bloem Server deployment — October 28, 2026: native scan redesign

Production moved through these builds after the
[native scan throughput receipt](2026-10-28-native-scan-throughput-deployment.md):

| Live from (UTC) | `main` at | Migrations | Downtime | Bloem CI on that commit |
| --- | --- | --- | --- | --- |
| 19:42 | `41ff1bb00` | 0 | 43 s | failed: gofmt in a test file, fixed by `9497e3edb` |
| 20:09 | `72b346443` | 0 | 30 s | passed |
| 20:35 | `24c1f3354` | 0 | 14 s | passed |
| 20:56 | `54cadaab5` | 0 | 12 s | passed |
| 21:11 | `3ed00c8bd` | 0 | 19 s | failed: two recommendation tests that passed on the next commit |
| 21:33 | `396dd92e9` | 0 | 13 s | passed |
| 22:20 | `203b6e96b` | 1 | 43 s | cancelled by a newer commit |
| 22:37 | `42c88f044` | 0 | 43 s | failed: a storage plugin test could not reach `proxy.golang.org` |
| 23:00 | `4ca1cc3bd` | 0 | 45 s | failed: the kept cover rule below, and a web test timeout |
| 23:26 | `ae7c01c3d` | 0 | 45 s | cancelled by a newer commit; passed on the docs commit `82542b034` |
| 01:30 (October 29) | `deeef897f` | 0 | 45 s | passed |

At the owner's direction these were built locally instead of waiting for the
CI-gated Bloem Image workflow: each image was built from a `git archive` of the
exact commit, with the workflow's build arguments and OCI labels, and ran as
`bloem-server:release-<short commit>`. CI ran on each commit in parallel.

Before each restart: a container snapshot `pre-main-<short commit>` and a
database restore point of the same name (`3C0/FD214BF0`, `3C1/9887CE98`,
`3C2/E5359E40`, `3C3/A70D7060`, `3C3/B855F478`, `3C4/5FFA678`,
`3C4/CAD73D00`, `3C5/148235F8`, `3C5/A2E609C8`, `3C6/57C35488`,
`3C8/7B3C01D0`). Every start
came up healthy with no restarts. The deployments from 22:20 keep the loopback
profiler enabled.

## What changed

Audiobooks are published from the provider's listed metadata:

- `72b346443` publishes a native audiobook track from the codec, duration,
  bitrate and chapters its listing carries, as ebooks are, without opening or
  probing it. Bookwarehouse lists them for every track. A scan also no longer
  stops on one track it cannot read while its authority holds, a failed scan
  resumes its journal (five failures in a row, pausing 30 seconds longer after
  each), and every 50 pages a scan logs time spent waiting for pages and
  applying them.
- `3ed00c8bd` lists a page again after 1, 2 and 4 seconds when the provider
  fails it as unavailable, timed out, exhausted or aborted.
- `396dd92e9` accepts up to 10,000 chapters on a track (a whole Bible read as
  one file has about 1,200), and keeps a track whose chapters outgrow its staged
  probe without them.

Contention that grew with the scan:

- `1aa152ca9` plans the page membership insert from the page's entries (up to
  51 s per ebook page before) and records a page's entries in four statements;
  `f78891f74` lists the next ebook page while the current one is applied.
- `41ff1bb00` lets a scan page's reads share one authority check for up to a
  second instead of a locked check before and after every read.
- `24c1f3354` and `54cadaab5` check the lease without the run row lock where a
  transaction only reads the journal or lists a directory, and plan a page's
  children's memberships from those children under a generic plan.
- `890a36757` and `203b6e96b` add two `media_files` indexes. The first-seen
  triggers on `media_item_libraries` read a held first-seen time by item and
  library, now from a partial index on held rows; the audiobook publication
  statements read a library's files by presentation group, now from a partial
  index on files whose group is set. Each insert into `media_item_libraries`
  took about 80 ms and each group statement about 37 ms, growing with the
  library. The migration is `20261028215150_bloem_media_files_presentation_and_held_indexes`;
  it builds both indexes concurrently and only if missing.

Covers and the journal:

- `42c88f044` lets an audiobook worker look for its next pending directory
  after the furthest one it claimed. Completed directories stay in the pending
  index until vacuumed, and the lookup from the first one had grown to 43 ms,
  about ten times a book.
- `5ea0deffd` keeps a Bookwarehouse ebook or audiobook cover in artwork storage
  after its first authorized read; `ae7c01c3d` serves a kept cover from there
  only after authorizing the request, as every storage cover request is, so a
  hidden or removed book's cover stops at once. `4ca1cc3bd` adds the daily
  `bloem_sweep_storage_covers` task, which deletes kept covers no book names
  any more. `4ca1cc3bd` itself served kept covers on their signed URL alone
  for 26 minutes; CI's `TestNativeAudioHiddenPresentationServesNoCoverDB`
  caught it, and `ae7c01c3d` restored the rule.

Completing a whole location:

- `deeef897f` completes a location from its reach and reachable memberships
  held in analyzed temporary tables. The planner estimated the recursive reach
  at 571 rows and the reachable set at one, and disabled unreached memberships
  with a merge anti join on the location's one root. On the audiobook location
  that statement ran for more than nine minutes, past the one-minute lease, so
  completion failed as stale and every retry failed the same way, each attempt
  long enough to reset the retry count. Measured on production data in a
  rolled-back transaction, the reach took 6.5 s, the memberships 4.0 s, each
  membership write about 0.2 s as a hash anti join, and the absence stamps
  1.3 s. Completion also renews the lease to at least 30 minutes for each of
  its steps; the renewal commits only with the completion.

## Production changes outside the deployments

- 21:22 and 21:35: both Bookwarehouse installations upgraded to plugin 0.3.7
  and then 0.3.8, which keep a book whose chapters do not fit and list up to
  10,000 chapters. The ESV Hear the Word Audio Bible (one track, 1,194
  chapters) had stopped every audiobook scan that reached it.
- About 21:36: that book's one kept probe row was set to `unavailable` so the
  next incremental scan lists the book again, with its chapters.
- 21:42: the server restarted on the same image with the loopback profiler
  enabled (`SILO_DEBUG_LISTEN=127.0.0.1:6060`, read with `docker exec`); the
  22:20 deployment keeps it.
- About 21:55 to 22:20: the two indexes were built concurrently by hand while
  the server ran. A first attempt with a 10-second lock timeout left an invalid
  index, which was dropped before retrying. A full presentation group index
  built then was replaced by the partial one at 22:20, after it drew a
  book-request path match off its file path prefix index.

- 23:31: the storage cover Redis cache was turned off now that covers are
  kept in artwork storage: `BLOEM_STORAGE_COVER_CACHE_URL` was commented out of
  its env file (a dated backup sits beside it), the server restarted on
  `ae7c01c3d` in 42 seconds, and the cache's Redis was then removed from the
  compose project (service, the server's dependency on it and its env file),
  with its container, configuration and env file archived beside the compose
  file. The main Redis is unchanged. A kept
  audiobook cover and a new ebook cover were served afterwards; the ebook cover
  took 2.9 seconds from Bookwarehouse and 0.2 seconds once kept.

- 01:52 (October 29): both Bookwarehouse installations upgraded to plugin
  0.3.9, which lists a one-file audiobook's embedded chapters. See the result
  below.

## Result

The Bookwarehouse ebook library's full scan completed at 20:40. The audiobook
scan, which stopped at 18:52 and again at 21:05, ran at 4.7 books a second
before the indexes, writing each book in about 200 ms, and at 48 books a second
after them, about 18 ms a book, with the database still busy with the catalog
follow-up tasks of the import. About 120,000 of the catalog's roughly 245,000
audiobooks were published at 23:20. A kept audiobook cover was served from
artwork storage after authorization, without reading Bookwarehouse again.

Measurements soon after a restart overlap `rebuild_release_interest`, which
runs at every startup and for one to three minutes resolves each profile's
viewer once per series.

The audiobook scan's run, begun at 15:12, completed at 01:32 on October 29,
about two minutes after `deeef897f` started, with one 30-second retry because
the library kind was not ready at startup. All 245,232 files were present with
enabled memberships, none was marked missing and no staged track was left.
The next scan, from 01:33 to 01:35, carried 244,857 book directories and
listed 12.

That scan listed the ESV Bible again, but without chapters. Bookwarehouse
lists all 1,194 of its chapters, and every chapter of each of 25 sampled
one-file books, without a file association and not marked accurate, and the
plugin listed only accurate associated chapters. While Bloem probed each
track, the probe read the file's own chapters; since `72b346443` publishes
from the listing, nearly every audiobook was published without chapters
(486 of 245,243 kept probes had any). Plugin 0.3.9 lists them on a one-file
book (244,478 of the 244,860 books are one file) and refuses the audiobook
change tokens issued before, so the scan started at 01:52 lists every book in
full. Of its first 1,974 relisted tracks, 1,972 carried chapters, about 30
each. It listed the Bible at 02:04 with all 1,194 chapters, the last ending
with the track, and the catalog API returned them on the book's version and
playback variant.

Not established here: the completion of the full relisting started at 01:52,
which had listed 23,000 of about 245,000 books at 02:06.

## Rollback

All migrations are forward-only; the index migration's down step drops both
indexes. To roll back a deployment, restore the database to its restore point
together with that container snapshot.
