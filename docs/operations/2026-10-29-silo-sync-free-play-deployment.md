# Bloem Server deployment — October 29, 2026: Silo through cc8514943 and preview free play

Production moved three times after the
[native scan redesign receipt](2026-10-28-native-scan-redesign-deployment.md):

| Live from (UTC) | `main` at | Migrations | Downtime | Bloem CI on that commit |
| --- | --- | --- | --- | --- |
| 07:48 | `38e58780589e5de8ac5804283460dbc3f68560e1` | 10 | 17 min 41 s | failed: the scenario executor's scope-catalog pins, fixed by `af8a3781b`, which passed |
| 08:18 | `cc37f39000dd11ee15f130991f719b1fac024614` | 0 | 16 s | ran in parallel |
| 12:16 | `09dab0097de5ad29af6fb34237764521569e4287` | 0 | 51 s | ran in parallel |

The owner asked to deploy without waiting for CI ("can you deploy now?", 07:22
UTC). The image was therefore built locally rather than by the CI-gated Bloem
Image workflow. It was built from a `git archive` of the exact commit, with
the workflow's build arguments and OCI labels and the web bundle built from
the same archive. It ran as `bloem-server:release-38e587805`. `cc37f3900` was built the same
way, also at the owner's direction ("do it"), and so was `09dab0097` ("can you
push the changes to server main and deploy?").

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

## Monitoring key events socket: cc37f3900

`cc37f3900` lets an `admin:monitor:read` API key open the events socket, as
Silo does.

- Before it, Bloem's socket wrapper refused the key at ticket mint, because it
  binds every ticket to a login session.
- The key now goes to Silo's key checks, in the legacy tenant its owner's HTTP
  requests resolve to. Login sessions keep Bloem's binding.

Before the restart:

- **Container snapshot** `pre-main-cc37f3900`.
- **Database restore point** of the same name at LSN `3D6/313762B0`. Segment
  `00000003000003D600000031` was archived, with no archive failures.

The restart script refused to run unless the live image was `38e587805`. It
applied no migrations, and 739 stay applied. Ready returned 200 at 08:18:39
UTC, 16 seconds after the stop, and the server was healthy with no restarts.

The container's runtime fingerprint is unchanged. The binary's SHA-256 is
`b1bac245dec411b84a1138b372005f7acab04b2e401dc4c20672a616a91e96bb`, and
`/api/v2/system/info` reports `cc37f390`. The only error line is the
recommendation embeddings 402 described above.

No migrations ran, so to roll back, start `bloem-server:release-38e587805`
again.

## AI subtitles: 09dab0097

`f2f94837e` through `09dab0097` change how AI subtitles are built from
speech-to-text results:

- **`f2f94837e`** accepts a `verbose_json` response that times words at the
  top level and has no segments, as NeMo-Speech.cpp returns. Every job against
  such a server used to fail.
- **`ae7afb0fd`** reads a word's `display` text and `joins_previous` flag, and
  measures cue width in columns, a Chinese or Japanese character counting two.
- **`7417cbafd`** joins Chinese and Japanese words from Whisper servers without
  a space: "では、 万 丈 一 致 で" now reads "では、万丈一致で".
- **`09dab0097`** wraps Chinese and Japanese lines at 16 characters and closes
  a full cue after its last clause mark rather than inside a word.

The transcription provider is unchanged, and cues in languages written with
spaces are built as before.

Before the restart:

- **Container snapshot** `pre-main-09dab0097`.
- **Database restore point** of the same name at LSN `3D6/C6EFB938`. Segment
  `00000003000003D6000000C6`, which holds it, was archived; archiving had
  reached `00000003000003D6000000C8` at 12:16:42 with no failures.

The restart script refused to run unless the live image was `cc37f3900`. It
went as follows:

- **12:15:26–12:16:03:** the server stopped, in 37 seconds. One ffmpeg process
  was running when the stop began.
- **12:16:03–12:16:05:** no migrations applied, and 739 stay applied.
- **12:16:17:** ready returned 200, 51 seconds after the stop began.
- **12:16:20:** healthy, with no restarts.

The container's runtime fingerprint is unchanged. The binary's SHA-256 is
`0b4ac16ebaa9451f1c2bac49d9ea787d8b12e2c413b2ed0707f0bf81c827a39f`, and
`/api/v2/system/info` reports `09dab009`. The only error line is the
recommendation embeddings 402 described above. The warnings are audiobook
metadata lookups, the known `PutBucketCors` 403 on the private assets bucket,
and one download job that the restart interrupted and that was queued again.

No migrations ran, so to roll back, start `bloem-server:release-cc37f3900`
again.
