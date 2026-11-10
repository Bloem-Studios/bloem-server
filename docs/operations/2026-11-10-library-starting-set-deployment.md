# Bloem deployment — November 10, 2026: libraries join the starting set, and deleting one takes it out

Production has run `main` at `65498dedb89a5fa15cc8bee6ebe68a9195fc3846` since 13:13:26
UTC on November 10. Jim could not delete the original Audio Books and Electronic Books
libraries, both switched off: the starting set named them, so deletion was refused with
`409 conflict` ("Library is retained by an entitlement bundle"), and nothing could take
a library out of the starting set. Jim chose that deleting a library takes it out of the
starting set, and that a library created from now on joins it
([changelog](../bloem/CHANGELOG.md#november-10-2026--new-libraries-join-the-starting-set-and-deleting-one-takes-it-out)).
Before the deploy, the progress people had on titles found only in those two libraries
was moved to the same titles in the new libraries, where they could be matched.

The deploy also carried what `main` had gained since `a7bbc8404`: `f4a2bc7d2`, which
checks the profile passwords switch inside the credential write, the Silo merge through
`6c1f4c919` (`521a83f0f`, Silo's CI and test changes, with Silo's migration
`20261109145651`) and documentation commits. The seam ledger stays at 332.
[How library ownership and grants work](../architecture/resource-tenancy-foundation.md#the-starting-set)
and [deleting a library](../architecture/bloem-lifecycle-safety.md#deleting-a-library)
explain the behaviour.

## 1. Moving progress off the old libraries

- **What was there.** On titles found only in the two old libraries there were 29
  listening progress rows (7 people), 31 listening history rows (3 people) and 17
  reading positions (3 people).
- **How titles were matched.** The old audiobooks are named "Series 3 - Title" and the
  new ones "Title". A title matched a new title of the same kind when the names agreed
  after dropping such a leading part, ignoring case and punctuation, and both had the
  same set of words across their author names. A tie at the best match moved nothing.
  The author check kept "Awaken Online 1 - Catharsis" from matching another author's
  "Catharsis".
- **What moved**, in one transaction that compared each count with the preview and
  would have rolled back on any difference: 26 progress rows (their last-played file
  cleared), 31 history rows, and 14 reading positions, each pointed at the new title's
  file of the same format.
- **What was left**, to go with the libraries: two progress rows with no match, one
  older than the person's progress on the new copy (they had finished it that morning),
  two reading positions with no match, and one MOBI position whose new copy is an EPUB.
- The affected rows and the list of matches were saved to files on the database host
  before the move.

## 2. The deploy (`65498dedb`)

- Deployed with `--skip-ci-check`. Image `bloem-server:release-65498dedb`
  (`sha256:7c55ca9aa692e07014379a7336a13f082e4b08d96d4d4ca095107d33c6f81a16`), binary
  SHA-256 `4fa2ed7b9c110234c0c3ac1ecf6288a9d6af41c1c1d3f8e933a28c07f96b97b1` on both
  hosts.
- **Before the restart:** container snapshot `pre-main-65498dedb`; database restore
  point of the same name at LSN `4DE/B0059C48`.
- **Restart.** The stop took 37 seconds (exit 0). Two migrations ran at 13:13:19:
  Silo's `20261109145651` and `20261110123408_bloem_library_starting_set.sql`. Ready and
  healthy at 13:13:26, 46 seconds after the stop began. The runtime configuration
  fingerprint did not change; `/api/v1/ready` and `GET /api/bloem/v1/ambience/public`
  answered `200`, and no error lines were logged. Both transcode nodes run
  `release-65498dedb`, healthy.
- **After the restart.** The bundle members' folder key is `ON DELETE CASCADE` and the
  `media_folders_join_starting_set` trigger is deferred to commit. The new Electronic
  Books and Audio books libraries joined version 1 of the starting set at 13:13:19,
  which now names ten libraries.

## Checks before the push

- The new tests in `internal/catalog/bloem_starting_set_test.go` failed before the
  change, three for the reason under test, and pass after it.
- `make test-go` on the 16 packages whose tests touch bundles, organization grants or
  library creation, on a disposable database: all passed. Five tests that had added a
  library to the starting set by hand, or asserted that new libraries never join it,
  were updated.
- golangci-lint v2.12.2 built with Go 1.26.10 reported no issues on the changed
  packages. `make verify-seams`, `make verify-migration-ledger`,
  `make verify-local-paths`, `scripts/bloem/draw-pictures.py --check` and a link check
  passed.

## 3. Deleting the old libraries

Both deletions were started through `DELETE /api/v2/libraries/{id}` at 13:36 UTC and
were accepted, which the previous release refused. Audio Books removed its 127,637
titles in a few minutes, then slowed to one 5,000-file batch in about 45 minutes.

- **Cause.** Silo's `queue_deleted_media_file_blobs` trigger looks up each deleted file
  in `blob_gc_queue` (949,319 rows) with
  `max(not_before) ... WHERE prefix LIKE 'chapter-images/%' AND split_part(prefix, '/', 2) = <file id>`.
  Silo's partial index `blob_gc_queue_chapter_file_idx` covers that expression, but
  PostgreSQL does not use a partial index's statistics for estimates. The planner
  assumed 4,747 matches instead of about 17, and chose to walk `blob_gc_queue_due_idx`
  backwards, reading the whole queue for every file: about 0.55 seconds each, or about
  three and a half days for both libraries.
- **Fix, applied by hand.** At 14:31 an extended statistics object on the expression,
  `blob_gc_queue_chapter_image_id_stats`, then `ANALYZE blob_gc_queue`. The lookup now
  uses Silo's index and takes 0.06 to 0.2 milliseconds. Two duplicate indexes made
  while diagnosing it were dropped again at 14:41; the statistics object stays until a
  migration carries the fix.
- **Restart.** The batch running on the old plan was cancelled at 14:33 rather than
  left to run into the job's two-hour limit. The first Audio Books job failed with
  that cancellation and was queued again. Electronic Books completed at 14:38:38 and
  Audio Books at 14:39:45, at about 60,000 files a minute. No file, bundle member or
  grant of either library remains; the starting set names eight libraries. An image
  cache cleanup of 273,241 images followed.

## Rollback

- **Image:** `bloem-deploy --rollback --sha 65498dedb89a5fa15cc8bee6ebe68a9195fc3846 --previous a7bbc8404`.
  The older server works with the new schema. It refuses again to delete a library the
  starting set names, and new libraries still join the set, because the trigger is in
  the database.
- **Schema:** the migration's Down drops the trigger and restores `ON DELETE RESTRICT`;
  the libraries it added stay in the starting set.
- **Moved progress:** put back from the saved files by hand. There is no script.
- **The deleted libraries** come back only from the restore point taken before the
  deploy, which would also undo everything written since.
- **Statistics object:** `DROP STATISTICS blob_gc_queue_chapter_image_id_stats` brings
  the slow plan back; nothing depends on it otherwise.
