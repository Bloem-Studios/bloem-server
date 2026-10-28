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
| 22:20 | `203b6e96b` | 1 | 43 s | running when this receipt was written |

At the owner's direction these were built locally instead of waiting for the
CI-gated Bloem Image workflow: each image was built from a `git archive` of the
exact commit, with the workflow's build arguments and OCI labels, and ran as
`bloem-server:release-<short commit>`. CI ran on each commit in parallel.

Before each restart: a container snapshot `pre-main-<short commit>` and a
database restore point of the same name (`3C0/FD214BF0`, `3C1/9887CE98`,
`3C2/E5359E40`, `3C3/A70D7060`, `3C3/B855F478`, `3C4/5FFA678`,
`3C4/CAD73D00`). Every start came up healthy with no restarts.

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

## Result

The Bookwarehouse ebook library's full scan completed at 20:40. The audiobook
scan, which stopped at 18:52 and again at 21:05, ran at 4.7 books a second
before the indexes, writing each book in about 200 ms, and at 48 books a second
after them, about 18 ms a book, with the database still busy with the catalog
follow-up tasks of the import. About 65,000 of the catalog's roughly 245,000
audiobooks were published at 22:23.

Measurements soon after a restart overlap `rebuild_release_interest`, which
runs at every startup and for one to three minutes resolves each profile's
viewer once per series.

Not established here: a completed whole-catalog audiobook scan, and the Bible's
chapters after the next incremental scan.

## Rollback

All migrations are forward-only; the index migration's down step drops both
indexes. To roll back a deployment, restore the database to its restore point
together with that container snapshot.
