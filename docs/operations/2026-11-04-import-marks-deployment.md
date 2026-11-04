# Bloem deployment — November 4, 2026: bulk marks imported as marks, not plays

Production has run `main` at `eeb919cb596b1f05a63f784d123471005826a011` since 23:36:58 UTC.
It was deployed by hand at the owner's request with the merge bot's deploy script and
`--skip-ci-check`; Bloem CI runs on the commit in parallel. It replaces `66b56baa3`, recorded
in the receipt for
[watched removals from watch-sync plugins](2026-11-04-watch-sync-removals-deployment.md).
Since `66b56baa3` production gained:

- `e4a38741d`: that deployment's receipt.
- `eeb919cb5`: a history import records a bulk "mark played" as a manual mark, not an
  imported play ([the history import overlay](../bloem/overlays/architecture/history-import-execution.md)).
  Three or more different items of one source account stamped in the same minute are
  marked; items stamped on their own stay plays. Two temporary seams in
  `internal/historyimport` (seam ledger 319 to 321). No migration.

## Existing history

Imported history from before the rule was relabelled once by the same rule, counting
distinct items per profile per minute. At 23:39 UTC one transaction changed 73,246 of
237,489 `import` rows to `manual`, across 96 profiles; afterwards the table held 164,243
`import`, 88,566 `manual`, 6,800 `playback` and 15 `mdblist` rows. The table has no
triggers or row policies, so nothing else changed: no export to a watch-sync provider was
queued by it. The changed row ids were saved on the database host before the update, and
undoing it is one statement:
`UPDATE user_watch_history SET source = 'import' WHERE id = ANY(<saved ids>) AND source = 'manual'`.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, tagged
  `bloem-server:release-eeb919cb5` (image `sha256:030550b355a035f44e9a0666e3b799afba2b5de185653cdc123e3540027da08f`), and loaded on both hosts.
- **Before the restart:** container snapshot `pre-main-eeb919cb5` and a database restore
  point of the same name at LSN `48C/9D362558`.
- **Restart.** The script required the live image to be `66b56baa3`. The stop took 37 seconds. No migration ran. The
  server answered ready at 23:36:58 with no error lines after the start.
- **Transcode nodes.** Both run `release-eeb919cb5`, healthy.

## Checks before the push

`go test ./internal/historyimport/...` passed, including
`bloem_marked_records_test.go`; changed-line lint (golangci-lint v2.15.0) found nothing;
`scripts/verify-seams.sh` and `make verify-local-paths` passed.

## Rollback

Start `bloem-server:release-66b56baa3` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The commit has no migration. The relabel is
independent of the image and is undone by the statement above.
