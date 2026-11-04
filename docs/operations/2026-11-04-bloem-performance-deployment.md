# Bloem deployment — November 4, 2026: database performance fixes

Production has run `main` at `58b738a411306e6229c61a2c638612a6b2045871` since 08:42:37 UTC.
It was deployed by hand with `bloem-deploy` after Bloem CI passed it. It replaces
`baba53141`, which the [`6aa6791ea` receipt](2026-11-04-silo-sync-6aa6791ea-deployment.md)
recorded. No Silo commits came with it. Since `baba53141` production gained:

- `4a14071d0`: the previous deployment's receipt.
- `60db6373e`, `9514467be`, `58b738a41`: every text column with a foreign key to
  `media_items`, `seasons` or `episodes` now has the content ID collation, `COLLATE "C"`.
  Ten Silo tables created after Silo's June collation migration had the database default,
  so a join from `media_items.content_id` could not use their index. The bulk enrichment
  pass's hourly check for remaining work compared every matched movie and series with
  every `metadata_enrichment_state` row: 890 seconds of database time an hour on
  production, 2.7 seconds against a copy of the table in `C`. The manga columns, which a
  fresh install creates in `C` but production had in the default, moved too. A follow-up
  migration analyzes the twelve tables, because a type change drops a column's statistics.
  The same fix is proposed to Silo as Silo-Server/silo-server#3480; when it merges, its
  migration finds nothing left to change here.
- `d5f11a724`: four native storage indexes no query reads are dropped, about 850 MB that
  every storage scan rewrote: the sidecar name and suffix indexes and the ingestion index
  on `bloem_storage_entries`, whose queries went with native onboarding (`f59aaafce`), and
  `bloem_storage_scan_directories_pending_idx`, a copy of
  `bloem_storage_pending_directories`.
- `2c4f5c088`: an open audiobook request's library match looks the request's ISBN or ASIN
  up first, through the new partial index `bloem_audiobook_identifiers_idx`, instead of
  reading every media file in content order. With one open request it took 7.5 seconds on
  every five-minute reconcile pass.
- `d0fba2122`, `c1d56f82d`: a request's organization and membership are one statement, and
  under Bloem tenancy the viewer gate reads the account, profile, the profile's group, the
  viewer preferences and the tenant's libraries as one batch. Silo's own batch (#3453)
  stays off under tenancy. The profile prelude takes 3 round trips instead of 8, plus the
  organization revocation check; a stream-token media request takes about 3 instead of 8.

## Merge resolutions

None; no Silo merge. The viewer gate's batch adds one hook line to Silo's `Resolve` in
`internal/policy/viewer_resolver.go`, an existing seam. The seam ledger stays at 308. No new
wire types.

## Procedure and result

- **Image.** `bloem-deploy` built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-58b738a41` (image
  `sha256:437e995fc30d5df97a1cc1a90b3aa88ff7027cc3b784f74800d6006812e64585`), and loaded it on
  CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `f9a875d690e8bb25a8721e052f09dc0708ce89944b68314f557f57fe731711df` on both hosts.
- **Compose.** Rendered with the overrides file and unchanged.
- **Before the restart:** container snapshot `pre-main-58b738a41`. Database restore point of
  the same name at LSN `47B/672D4F38`. Segment `000000030000047B00000067`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `baba53141`. One transcode process
  was running inside the server, and the stop took 7 seconds (exit 0). Five migrations ran
  in 8 seconds (08:42:27–08:42:35); 762 are applied. The server answered ready at 08:42:42,
  22 seconds after the stop began, and was healthy at 08:42:44 with no restarts. Its runtime
  configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `58b738a4`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **After the restart.** No foreign key to `media_items` has a text column outside `C`;
  `bloem_audiobook_identifiers_idx` is valid (7.4 MB); the four dropped indexes are gone; the
  changed tables were analyzed at 08:42.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) run
  `release-58b738a41`, healthy, and the server lists both as enabled and healthy on
  `58b738a41`.
- **Old images.** The script removed `release-4939c4cfa` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The local gate passed every step on the code (build, verify, changed-line lint, the scenario
executor), and the packages the changes touch passed against a migrated database, among them
the new `TestBloemViewerResolverBatchedReadsMatchEachDB` (one batch, the same answers as
reading each fact) and `TestProfileScopedPreludeStatementBudget` at 3.

## Bloem CI

Run 41798853847 passed on `58b738a41` at its first attempt.

## Rollback

Start `bloem-server:release-baba53141` again and set the transcode nodes back; the deploy
script's rollback does both. The previous image works on the migrated database: the
collation and index changes are compatible with its queries, and it does not read the new
index. Restoring the database to the restore point together with the container snapshot also
rolls back, but discards everything written since 08:42.
