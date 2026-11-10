# Bloem deployment — November 9 and 10, 2026: gemini-embedding-2, task labels and search matching

Production has run `main` at `7acd2c7e05627f48df0d6b7cbe5312f9e999507d` since 00:52:04 UTC on
November 10. Over the evening Jim asked for three changes, made in this order:

1. **November 9, 23:30 UTC:** recommendation embeddings moved from `gemini-embedding-001` to
   `gemini-embedding-2`. This was a settings change and a restart of `14e21e7d6`, with no
   new code.
2. **November 10, 00:10 UTC:** `9df39cadb` labels `gemini-embedding-2` input by task
   ([changelog](../bloem/CHANGELOG.md#november-10-2026--gemini-embedding-2-gets-task-labels)).
3. **November 10, 00:52 UTC:** `7acd2c7e0` lets search drop the most common words first,
   which production now uses, with a shorter query timeout
   ([changelog](../bloem/CHANGELOG.md#november-10-2026--search-can-drop-the-most-common-words-first)).

Neither commit has a migration, client DTO, fixture, catalogue or scenario pin change. The seam
ledger went from 328 to 330 with `9df39cadb` and to 333 with `7acd2c7e0`, all temporary seams.
[How search finds titles by words and by meaning](../architecture/bloem-search-and-embeddings.md)
explains the behaviour.

## 1. The embedding model

Before the change, 1,683,617 titles had `gemini-embedding-001` vectors. Google states that
vectors from `gemini-embedding-001` and `gemini-embedding-2` cannot be compared, so the whole
catalog has to be embedded again.

- The old values of `recommendations.embedding_model` and `recommendations.embedding_lock` were
  saved to a file on the database host. In one transaction the model became
  `gemini-embedding-2` and the lock was rewritten to it (3,072 source and storage dimensions).
  The admin **Reset embeddings** was not used, so the old vectors stayed in place until
  replaced.
- The server container restarted at 23:30:00 and was healthy at 23:30:20. Its startup
  catch-up began at once. The first 90 titles had `gemini-embedding-2` vectors 15 seconds
  later, with 3,072 dimensions and no errors. The rate settled at about 1,100 titles a minute,
  close to the Gemini per-minute limit. During the label test below, the run waited out
  several rate-limit answers and carried on.
- Meaning-based search is off until 85% of each kind of title has a vector from the locked
  model. Keyword search works throughout.

## 2. Task labels (`9df39cadb`)

A test the same evening embedded 6,432 production titles both ways. Labelled input found the
right title first for 87.0% of 200 descriptive searches, against 68.5% unlabelled. Jim chose to
build it.

- Deployed with `--skip-ci-check`. Image `bloem-server:release-9df39cadb`
  (`sha256:566032c84492f10d7d7025c5eb5cdcfd8675d24c56a68e75cd713a84c6c58470`), binary SHA-256
  `5afec140ee79fcc35e641b59f4892a66add3ddf4b7242b404a8198316c01e1d3` on both hosts.
- **Before the restart:** container snapshot `pre-main-9df39cadb`; database restore point of
  the same name at LSN `4BC/FCED9710` (segment `00000003000004BC000000FC`).
- **Restart.** The stop took 37 seconds (exit 0). No migrations ran. Ready at 00:10:25, 43
  seconds after the stop began, and healthy at 00:10:27. The runtime configuration fingerprint
  did not change. `/api/v1/ready` and `GET /api/bloem/v1/ambience/public` answered `200`, and
  no error lines were logged. Both transcode nodes run `release-9df39cadb`, healthy.
- **The labels are in use.** Three titles embedded before the restart and four after were
  embedded again, plain and labelled, and compared with their stored vectors. The earlier
  ones match the plain form (cosine 1.00000, against 0.84 to 0.87 labelled), and the later
  ones match the labelled form.
- **Redoing the unlabelled vectors.** The old server had written 40,130 unlabelled
  `gemini-embedding-2` vectors, all before 00:10:21; its last write was at 00:10:17 and the new
  server's first at 00:10:24. Their `model` was set to `gemini-embedding-2-unlabeled`, so the
  catch-up embeds them again. By 01:24 UTC 1,086 were left, and 82,744 titles had labelled
  vectors.
- Bloem CI run `42725423710` passed on the commit after the deploy.

## 3. Search matching (`7acd2c7e0`)

A keyword-only test on the live index (2.79 million documents) put the right title in the top
50 for 36.5% of 200 descriptive searches with Meilisearch's `frequency` matching, against 16.5%
with `last`. Exact and misspelled titles were found as before. Jim chose to switch.

- **Settings, set just before the deploy.** The old values were saved to a file on the
  database host. `catalog.search.meilisearch.matching_strategy` went from `last` to
  `frequency`, and `catalog.search.meilisearch.timeout_ms` from 50000 to 3000. Meilisearch's
  logs for November 3 to 9 show 613 searches, the slowest 1.53 seconds. The server running at
  the time could not read `frequency`, so its indexer skipped its syncs until the restart;
  searches were not affected.
- Deployed with `--skip-ci-check`. Image `bloem-server:release-7acd2c7e0`
  (`sha256:a1d58361f4fae6d6d3a095030b683e1815a10d12f6354bb8d17d8fcdd6da26cd`), binary SHA-256
  `3a1c97b246d3c0ffd77d5e5cd3c1bc872d72dd7fc4854d76f85c15ce12457412` on both hosts.
- **Before the restart:** container snapshot `pre-main-7acd2c7e0`; database restore point of
  the same name at LSN `4BE/716650` (segment `00000003000004BE00000000`).
- **Restart.** The stop took 8 seconds (exit 0). No migrations ran. Ready at 00:52:04, 14
  seconds after the stop began, and healthy at 00:52:07. The fingerprint did not change;
  ready and ambience answered `200`, and no error lines were logged. Both transcode nodes run
  `release-7acd2c7e0`, healthy.
- **After the restart.** The server logged no fallback to PostgreSQL. The indexer synced at
  00:53:31 and 00:54:30, and its backlog, which had built up while it skipped syncs, fell from
  1,915 to 1,607 events in a minute. Nobody has searched through the server to confirm the new
  strategy end to end.
- Bloem CI run `42730817744` passed on the commit after the deploy.

## Checks before each push

- `9df39cadb`: `go test ./internal/recommendations/...`, and `go test ./cmd/... ./contracts/...`
  apart from `cmd/silo`, which needs a built web app the worktree did not have.
- `7acd2c7e0`: `go test ./internal/catalog/ ./internal/config/` and the 434 web tests in
  `web/src/pages/admin-settings/`; eslint and the web typecheck.
- Each commit's new Bloem tests failed before the change and passed after it.
- golangci-lint v2.12.2 built with Go 1.26.10 reported no issues on the changed packages.
- `scripts/verify-seams.sh` passed for both; `make verify-local-paths` for `9df39cadb`.

## Rollback

- **Search matching:** set `catalog.search.meilisearch.matching_strategy` back to `last`
  *before* rolling back, then
  `bloem-deploy --rollback --sha 7acd2c7e05627f48df0d6b7cbe5312f9e999507d --previous 9df39cadb`.
  A server older than `7acd2c7e0` cannot read `frequency`.
- **Task labels:** `bloem-deploy --rollback --sha 9df39cadb87e12c0d5ea1100be139bde880bb7a6 --previous 14e21e7d6`
  after the step above. Vectors already labelled would then have to be embedded again.
- **Embedding model:** restore the two saved settings and restart. Titles not yet embedded
  again still have their `gemini-embedding-001` vectors; the others would be embedded again.
- No migration needs undoing.
