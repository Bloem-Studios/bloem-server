# Bloem Server deployment — October 29, 2026: Silo through cc8514943 and preview free play

Production moved once after the
[native scan redesign receipt](2026-10-28-native-scan-redesign-deployment.md):

| Live from (UTC) | `main` at | Migrations | Downtime | Bloem CI on that commit |
| --- | --- | --- | --- | --- |
| 07:48 | `38e58780589e5de8ac5804283460dbc3f68560e1` | 10 | 17 min 41 s | failed: the scenario executor's scope-catalog pins, fixed by `af8a3781b`, which passed |

The owner asked to deploy without waiting for CI ("can you deploy now?", 07:22
UTC). The image was therefore built locally rather than by the CI-gated Bloem
Image workflow. It was built from a `git archive` of the exact commit, with
the workflow's build arguments and OCI labels and the web bundle built from
the same archive. It ran as `bloem-server:release-38e587805`.

## What changed

- **`d562d7628`** merges Silo main through `cc8514943`: 43 Silo commits and
  nine Silo migrations. See the
  [changelog](../bloem/CHANGELOG.md#october-29-2026--silo-integration-through-cc8514943)
  for what they bring and how each conflict was resolved. Silo won every
  conflict, and the seam ledger went from 305 to 304.
- **`38e587805`** adds library preview free play.
  - A preview guest who confirms an email through the storefront gets a
    metered allowance to watch video. The server charges it on segment
    deliveries and stops playback when it is spent.
  - Migration `20261028222252_bloem_preview_free_play` adds its settings and
    tables.
  - Free play is off until the storefront turns it on. See
    [browse-only content policy](../architecture/bloem-browse-only-content-policy.md).

## Procedure and result

Before the restart:

- **Container snapshot** `pre-main-38e587805`.
- **Database restore point** of the same name at LSN `3D0/A5FB7D0`. Segment
  `00000003000003D00000000A`, which holds it, was archived at 07:30:42 with no
  archive failures.
- **Dumps** of the rows the migrations rewrite, in
  `/var/lib/postgresql/pre-main-38e587805/` on the database host:
  - `goose_db_version`;
  - the multi-episode ranges `20261028165234_reparse_multi_episode_ranges`
    clears: `id`, `multi_episode_start` and `multi_episode_end` of 17,597
    `media_files` rows.

The restart script refused to run unless the live image was `deeef897f`. It
went as follows:

- **07:31:03–07:31:14:** the server stopped.
- **07:31:14–07:48:37:** the migrations applied offline. 739 are now applied
  and none are pending.
  - `20261025203537_track_marker_invalidation` took almost all of it. It sets
    `markers_invalidated_at` on 2,776,504 of the 2,965,389 `media_files`
    rows in one statement and one transaction.
  - The other nine took seconds.
- **07:48:44:** ready returned 200.
- **07:48:48:** healthy, with no restarts.

The container's runtime fingerprint matches the one before the restart. The
binary's SHA-256 is
`7dd55e180d6a040211bdda784507c27165dbbb7f4221a8a9e5c9c41844d527f0`, and
`/api/v2/system/info` reports `38e58780`.

The first minutes of logs hold no error from this deployment. The one error
line is the recommendation embeddings job: the Gemini API answers 402 because
the project's prepaid credit is depleted. The warnings are watch-provider
syncs for accounts Pastime has not given tracking access, and ebook metadata
lookups.

The downtime is the offline migration. The restart script stops the server
before it migrates, and Silo's boot takes the same migration lock, so the old
image could not serve while the backfill ran. Before the next Silo sync,
check its migrations for full-table backfills of large tables.

To roll back, restore the database to the restore point together with the
container snapshot. The migrations are forward-only, so starting `deeef897f`
on the new schema is not a rollback.
