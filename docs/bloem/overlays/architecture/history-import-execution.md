# Bloem overlay: History import execution

> **Provenance and status.** Bloem page, rewritten on 2026-11-04, updated on 2026-11-09
> and checked against `main` at `5a93e8b41`, which includes Silo through `8487cd7b1`. It lists what Bloem
> adds to Silo's
> [`docs/architecture/history-import-execution.md`](../../../architecture/history-import-execution.md),
> which stays exactly as Silo wrote it. Bloem replaces nothing; every section of Silo's
> page applies as written. The addition is in the [For developers](#for-developers)
> section.

A **history import** copies what a person watched on another media server, such as
Emby, into their Bloem profile. Silo's page explains how an import runs. Bloem adds one
rule about **bulk marks**: when someone marks a whole season or show as played on the
other server, those items are watched, but they were not played.

## What Bloem changes

- **A bulk mark is watched, not played.** The other server stamps every item a person
  marks played at once with the same minute. When three or more different items of one
  person share a minute, Bloem records them as watched with a manual mark, the same
  record Bloem's own **Mark watched** button writes, and not as plays.
- **Real plays stay plays.** An item stamped on its own is a play, as before.
- **The other server is read once, as Silo reads it.** Silo imports a history page by
  page, and the other server need not list one minute's items together: Emby lists
  them by name. Bloem counts each minute's items as the pages arrive. Items of a minute
  that share a page are all marks, but an item that arrived on an earlier page, before
  its minute reached three items, stays a play. Silo wins here: its resume, cancel and
  retry rules depend on one read.
- **Everything else is unchanged.** Marked items still count as watched, still reach
  trackers such as Pastime (which applies the same rule), and still shape
  recommendations. Bloem's play statistics never counted imported rows.

This example is made up. Robin marks seasons one to three of a cooking show played at
9:41 one evening, then watches a film at 10:15. After the next import, Robin's profile
shows every episode of those seasons as watched, recorded as marked, and the film as
one play at 10:15.

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Bulk mark | `Record.Marked`, set by `withBulkMarks` (`bulkMarkProvider`) |
| Manual mark | `user_watch_history.source = 'manual'` (`WatchHistorySourceManual`) |
| Imported play | `user_watch_history.source = 'import'` (`WatchHistorySourceImport`) |

## For developers

- `internal/historyimport/bloem_marked_records.go` marks every played record whose
  `LastPlayedAt` minute (UTC) holds at least `bulkMarkMinimum` (3) distinct
  `ExternalID`s. One run reads one source account. A multi-episode file expands to
  several records with one `ExternalID` and counts once; unplayed, undated and
  watchlist records never mark.
- Since Silo streams imports (Silo #3715, merged in `5a93e8b41`), `executeRunWithClaim`
  wraps the provider with `withBulkMarks` on one added line. `bulkMarkProvider.Stream`
  calls Silo's `Provider.Stream` once, from the run's resume position, and counts each
  batch's minutes before it marks that batch and hands it to Silo's sink. A resumed
  run counts from where it resumes. Silo's queue, checkpoint and requeue rules assume
  one `Stream` call per run (a second call broke `TestInterruptedRunResumesWithoutDuplicates`,
  `TestMatchFailureRequeuesTheRun` and the queue worker tests), so Bloem does not read
  ahead. Counts are per-minute hashes of at most two IDs, capped at
  `maxBulkMarkMinutes` (500,000) minutes; past the cap a new minute stays plays.
- `applyImportedWatch` records history through
  `watchstate.Service.RecordImportedHistoryWithSource` with `importedHistorySource`:
  `manual` for a marked record, `import` otherwise. Progress is written as before.
- Temporary seams, listed in `contracts/seams.txt`: `internal/historyimport/types.go`
  (`Record.Marked`) and `internal/historyimport/service.go` (the `withBulkMarks` line
  and the source choice), until the Emby servers retire at the end of November 2026.
- History imported before this rule was relabelled once in production on
  2026-11-04 by the same rule; see the
  [deployment receipt](../../../operations/2026-11-04-import-marks-deployment.md).
- Tests: `internal/historyimport/bloem_marked_records_test.go`.

Related: [Silo's history import execution](../../../architecture/history-import-execution.md),
[how removing a title in a tracker clears its watch history](../../../architecture/bloem-watch-sync-removals.md).
