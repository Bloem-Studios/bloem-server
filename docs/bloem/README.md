# Bloem documentation

Index of Bloem-owned documentation in this tracking fork of Silo Server. A
**tracking fork** keeps Silo's Git history and merges Silo's `main` as it moves
(see [fork provenance](../../FORK.md)). Any Markdown file that also exists in
Silo's `main`, including the root `README.md`, `CONTRIBUTING.md`, `AGENTS.md`
and most of `docs/`, is Silo's and stays as Silo wrote it. Bloem's material
lives in the Bloem-owned files listed here; where Bloem differs from a Silo
document, an **overlay** records the difference. The code is the source of truth.

Every current Bloem page is written the same way, after
[How organizations work](../architecture/bloem-organizations.md): plain language and
pictures first, a made-up example where it helps, a "Words used in the code" table, and
a "For developers" section that holds every technical rule.
[Writing Bloem documentation](documentation-workflow.md) sets out that shape.

Pages marked *historical* are dated records: their facts are true to their date,
not to the current source, and they keep the form they were written in. The current
state is on the [front page](../../.github/README.md#current-state).

## Start here

- [Bloem Server front page](../../.github/README.md) — what Bloem is, where it comes from, current state, quick start. GitHub shows it in place of the root `README.md`, which is Silo's.
- [How Bloem forks Silo](../../FORK.md) — tracking-fork policy, the "Silo wins" rule, seams and overlays, what Bloem adds and removed, the current integration boundary.
- [Agent instructions](../../BLOEM_AGENTS.md) — Bloem's overrides of Silo's `AGENTS.md` / `CLAUDE.md`; they take precedence where the two differ.
- [Contributing to Bloem](../../.github/CONTRIBUTING.md) — repository ownership, AI disclosure and the validation commands; the root `CONTRIBUTING.md` is Silo's.
- [Integration changelog](CHANGELOG.md) — every Silo integration and Bloem change, newest first, with commits and seam counts.
- [Writing Bloem documentation](documentation-workflow.md) — Silo's manual/reference model, where each kind of Bloem change is documented, regeneration and verification commands.
- [How Bloem CI works](ci.md) — the Bloem CI jobs, the merge bot's CI wait and deploy, the switched-off daily upstream sync, the published image's gate, runner placement and tool pins.
- [API catalogue](api-catalogue.md) — generated list of typed `/api/v2` and `/api/bloem/v1` operations and native documentation gaps.
- [API by topic](api/README.md) — every typed `/api/bloem/v1` operation, generated from the OpenAPI contract, one page per topic, with credentials, fields and error codes; plus the hand-written page for Bloem's remaining [`/api/v1` routes](api/v1-routes.md).

## How Bloem works: start here

The overview pages, one per area, for readers who don't work on the code. Each links the
more detailed pages of its area, and ends with a "For developers" section.

- [How organizations work](../architecture/bloem-organizations.md) — the platform, organizations, accounts and profiles; what someone can watch; a reseller's year.
- [How organization domains and branding work](../architecture/bloem-organization-domains.md) — each organization on its own domains, with its own branding and email links; refusing a sign-in on another organization's domain.
- [How plans, previews and upgrades work](../architecture/bloem-plans.md) — the built-in plans, how a plan combines with an account and its profiles, changing a plan safely, the storefront preview, the upgrade prompt.
- [How Live TV works](../architecture/bloem-live-tv.md) — sources, tuning in, recordings.
- [How storage sources work](../architecture/bloem-storage-sources.md) — libraries read from S3 and Bookwarehouse.
- [How requests work](../architecture/bloem-requests.md) — movie and series, audiobook and ebook requests.
- [How search finds titles by words and by meaning](../architecture/bloem-search-and-embeddings.md) — the two halves of a search, dropping the most common words first, task labels for `gemini-embedding-2`, changing the embedding model.
- [How messages and promotions work](../architecture/bloem-messages-and-promotions.md) — announcements, campaign and playback cards, seasonal packs.
- [How apps connect](../architecture/bloem-apps.md) — the three entrances, signing in, remote control.
- [Switching between Silo and Bloem](../architecture/bloem-silo-switching-explained.md) — one database, two servers taking turns, both install layouts.
- [How Bloem keeps up with Silo](../architecture/bloem-following-silo.md) — the merge bot and each Silo batch, its deploy, "Silo wins", where Bloem keeps its own work.
- [How backups work in Bloem](../architecture/bloem-backup.md) — what a restore brings back of Bloem's data, credentials kept from the live server, what selective restore does not cover yet.

The pictures are drawn by `scripts/bloem/draw-pictures.py`.

## Deployment receipts

Dated records of what production ran, how it was deployed and what was checked.
A receipt is not native-client or playback acceptance.

- [November 9 and 10 deployment: gemini-embedding-2, task labels and search matching](../operations/2026-11-10-search-and-embeddings-deployment.md) — current: `7acd2c7e0` (embeddings moved to `gemini-embedding-2` with a settings change and a full re-embed; `9df39cadb` labels its input by task; `7acd2c7e0` lets search drop the most common words first, now production's setting, with a 3,000 ms query timeout; no migrations, seams 328 to 333, 43 s and 14 s restarts, deployed by hand without waiting for Bloem CI, which then passed both)
- [November 5 deployment: AI subtitle cue hold](../operations/2026-11-05-ai-subtitle-cue-hold-deployment.md) — `a1315bd8b` (AI cues stay on screen 0.6 s after speech; no migration, seams stay at 320, 46 s restart behind a transcode, both transcode nodes moved, deployed by hand without waiting for Bloem CI)
- [November 5 deployment: Silo through `d941225b4`](../operations/2026-11-05-silo-sync-d941225b4-deployment.md) — `a9f230203` (one Silo commit with no migrations, one test conflict resolved to Silo's side: season and episode pages keep the library they were opened from; no Bloem fix, seams stay at 320, 13 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the first attempt)
- [November 5 deployment: Silo through `2d5f22e7d`](../operations/2026-11-05-silo-sync-2d5f22e7d-deployment.md) — `38156af18` (25 Silo commits with three migrations, six conflicts resolved to Silo's side: bound TV sign-ins, web-only hero rows, Dolby Vision 7 as 8.1 over HLS, faster subtitle burn-in, audio-only downloads, Matroska subtitle regions; a Bloem fix brings the regions to the native-storage probe; seams 321 to 320, 46 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the first attempt)
- [November 5 deployment: Silo through `ba1280870`](../operations/2026-11-05-silo-sync-ba1280870-deployment.md) — `a96d2efe2` (one Silo commit with no migrations or conflicts: administrators manage another account's profiles and reset profile PINs; a Bloem fix keeps those profile writes in the account's own organization; seams stay at 316, 44 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the first attempt)
- [November 5 deployment: Silo through `90c531469`](../operations/2026-11-05-silo-sync-90c531469-deployment.md) — `d69ce2aa8` (one Silo commit with no migrations or conflicts: laced Matroska audio kept continuous in the web player's decoder, so it no longer goes silent and stalls the picture once per cluster; seams stay at 316, 45 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the first attempt)
- [November 4 deployment: bulk marks imported as marks](../operations/2026-11-04-import-marks-deployment.md) — `eeb919cb5` (a bulk "mark played" on another server is imported as watched, not as plays; 73,246 earlier imported rows relabelled; no migration)
- [November 4 deployment: watched removals from watch-sync plugins](../operations/2026-11-04-watch-sync-removals-deployment.md) — `66b56baa3` (a profile's watch history follows a movie or show deleted in Pastime, through plugin 0.4.0 and Pastime schema 63; no migration)
- [November 4 deployment: Silo through `6d1562473`](../operations/2026-11-04-silo-sync-6d1562473-deployment.md) — `6604bfd44` (one Silo commit with no migrations: mediabunny 1.62.1, so laced DTS audio with stripped headers decodes in the web player; its `mediabunny` line conflicted beside Bloem's `mpegts.js` dependency in `web/package.json` and `web/pnpm-lock.yaml`, Silo's lines taken and Bloem's kept on their own; seams stay at 316, 45 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the first attempt)
- [November 4 deployment: Silo through `4e3020a26`](../operations/2026-11-04-silo-sync-4e3020a26-deployment.md) — `28bdc197b` (six Silo commits with no migrations or conflicts: box sets ranked by how their names open, Silo's own deep library grid, device headers declared on v2 sign-in, MDBList's refusal reason, a request server's connection tested before it is reported connected, curated lists cut to the item limit on capped section reads; replaces the hand-deployed web fixes through `c2b43286e`; seams at 316 with the library grid's temporary seam dropped, eight temporary web fixes left, 100 s restart behind a transcode, both transcode nodes moved, deployed by the merge bot after Bloem CI passed it at the third attempt)
- [November 4 deployment: Silo through `e3af98b06`](../operations/2026-11-04-silo-sync-e3af98b06-deployment.md) — `5d5371d6c` (three Silo commits with no migrations or conflicts: Dolby Vision profile 0 accepted in level capabilities, the Meilisearch circuit breaker tripped from `/api/v2` cursor searches, unsorted collection rows filled from the members a viewer can open; replaces the hand-deployed web fixes through `0edcd6e49`; seams at 317, nine of them temporary web fixes, 13 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 4 deployment: maintenance reconcile candidates](../operations/2026-11-04-bloem-maintenance-reconcile-deployment.md) — `ca1740a98` (no Silo commits or migrations: the native storage maintenance reconcile picks its candidates in the database instead of reading and resending every ID of a library, checked equal on production data; enrichment probe measured at 2.96 s, was 890 s; seams at 308, 18 s restart, both transcode nodes moved, deployed by hand after Bloem CI passed).
- [November 4 deployment: database performance fixes](../operations/2026-11-04-bloem-performance-deployment.md) — `58b738a41` (no Silo commits; five migrations: content ID foreign keys moved to `COLLATE "C"` as proposed in Silo #3480, which ends a 15-minute hourly enrichment probe, then analyzed; four unread storage indexes dropped; an audiobook identifier index for the book-request match; the profile prelude from 8 round trips to 3 with Bloem's own viewer batch; seams at 308, 22 s restart, both transcode nodes moved, deployed by hand after Bloem CI passed).
- [November 4 deployment: Silo through `6aa6791ea`](../operations/2026-11-04-silo-sync-6aa6791ea-deployment.md) — `baba53141` (eight Silo commits with no migrations or conflicts: NVENC detected under WSL, mixed-library sidebar icons, scans joining an active run without duplicate-key errors, Jellyfin rewatch marks clearing the resume position and direct play of video without audio, carousel scroll kept, search capabilities answered during a Meilisearch outage; seams at 308, 46 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 4 deployment: Silo through `d5d00264d`](../operations/2026-11-04-silo-sync-d5d00264d-deployment.md) — `479b27576` (seven Silo commits with one migration, the `overlay_images` table: removable failed requests, admin playback commands relayed to the session's replica, poster watch status with badge styles and images, series and movies ranked above episodes in search, a collections poster wall and copy buttons over plain HTTP; one conflict resolved to Silo with Bloem's request organization check on its own lines, native settings bindings regenerated; seams at 308, 43 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 4 deployment: Silo through `b8114188f`](../operations/2026-11-04-silo-sync-b8114188f-deployment.md) — `159f2ef46` (nine Silo performance commits with no migrations: the viewer gate in one batch, memoized policy decisions, cheaper Similar lists and catalog reads; five conflicts resolved to Silo with Bloem's profile columns and organization group read on its own lines, Silo's batched viewer read kept off under Bloem tenancy; seams at 308, 45 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 4 deployment: Silo through `f2393ad21`](../operations/2026-11-04-silo-sync-f2393ad21-deployment.md) — `341108c6e` (three Silo performance commits with no migrations or conflicts: fewer reads and round trips on card lists, the item detail page and season episode lists; seams at 309, 43 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: Silo through `527e40436`](../operations/2026-11-03-silo-sync-527e40436-deployment.md) — `4939c4cfa` (one Silo commit with no migrations or conflicts: packaged TV apps can open v2 sockets with an opaque Origin and read every subtitle sidecar; seams at 309, 44 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: Silo through `d10d9c937`](../operations/2026-11-03-silo-sync-d10d9c937-deployment.md) — `dc4f67200` (one Silo commit with no migrations or conflicts: metadata capability parsing moved into its own file, no behavior change; seams at 309, 45 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: Silo through `a5325887c`](../operations/2026-11-03-silo-sync-a5325887c-deployment.md) — `aa9e17d90` (one Silo commit with no migrations or conflicts: box sets leave library grids and stay in search, on their own page and on movie pages, with the client digest re-pinned for the removed box set card; also storage ebooks that share a placeholder ISBN kept apart (`962a3f1a9`); seams at 309, 42 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: storage ebooks with a placeholder ISBN](../operations/2026-11-03-storage-ebook-placeholder-isbn-deployment.md) — `040b55f10` (Bookwarehouse's `0000000000000` ISBN no longer joins books into one item; library 25 relisted fully and 2,770 books split out of one item; no seam, no migration, 43 s restart, both transcode nodes moved, deployed by hand before Bloem CI finished, which then passed).
- [November 3 deployment: Silo through `bb1ee4352`](../operations/2026-11-03-silo-sync-bb1ee4352-deployment.md) — `3a3fae193` (one Silo commit with no migrations or conflicts: scans skip every hidden folder, and Bloem's native storage scan skips the same folders (`dbe43fa54`); seams at 309, 48 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: Silo through `391e90910`](../operations/2026-11-03-silo-sync-391e90910-deployment.md) — `0fc66436b` (one Silo commit with one migration and no conflicts: artwork on a separate read endpoint is no longer re-verified every week, and the migration drops the verifier's columns; seams at 309, 15 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 3 deployment: Silo through `90f88f2d6`](../operations/2026-11-03-silo-sync-90f88f2d6-deployment.md) — `4d2b68167` (one Silo commit with one migration and no conflicts: the calendar lists a movie on its first digital or disc release; seams at 309, 49 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 2 deployment: Silo through `bf53b6545`](../operations/2026-11-02-silo-sync-bf53b6545-deployment.md) — `4f7cfbc9b` (one Silo commit with no migrations or conflicts: the box set row's "This film" ring drawn inside its card; seams at 309, 45 s restart, both transcode nodes moved, deployed by the merge bot after Bloem CI passed).
- [November 2 deployment: Silo through `56e6f871c`, the merge bot's first batch](../operations/2026-11-02-silo-sync-56e6f871c-deployment.md) — `c5a2b43f3` (nineteen Silo commits with two migrations; six conflicts from Silo's #2177, each taken on Silo's side, with Live TV kept among the assignable permissions, the sign-in policy resolved in the account's own organization and members without a username or email read; seams at 309, 15 s restart, both transcode nodes moved, deployed by hand at the owner's request with `--skip-ci-check` while Bloem CI failed only on a scenario catalog pin).
- [November 2 deployment: Silo through `07f0ae21c`](../operations/2026-11-02-silo-sync-07f0ae21c-deployment.md) — `e3a096b20` (two Silo commits with no migrations or conflicts: Jellyfin Latest rows honor hide-played, the admin Users table stops scrolling sideways; 51 s restart, both transcode nodes moved, an image built locally before CI finished).
- [November 2 deployment: Silo through `257390ba1`, and Silo's preferred-version line restored](../operations/2026-11-02-silo-sync-257390ba1-deployment.md) — `7eada59a0` (four Silo commits with no migrations or conflicts; Silo's preferred-version preflight line taken verbatim, with Bloem's native-storage check on its own lines so a native file is never marked missing; seams at 308, 46 s restart, both transcode nodes moved, an image built locally before CI finished).
- [November 1 deployment: Silo through `6f5be75cd`](../operations/2026-11-01-silo-sync-6f5be75cd-deployment.md) — `46354baee` (nine Silo commits: embedded subtitles kept and streamed, box sets from TMDB collections that stay off until turned on, audio track and Jellyfin-protocol transcode fixes; two additive migrations, seams at 307, 48 s restart, both transcode nodes moved, an image built locally before CI finished).
- [November 1 deployment: a pinned S3 release on every platform](../operations/2026-11-01-s3-pinned-manifest-deployment.md) — `99a4a1133` (a pinned S3 release is offered on every platform it ships for, no migrations, 46 s restart, both transcode nodes moved, an image built locally before CI finished).
- [November 1 deployment: level with `main` after the documentation rewrite](../operations/2026-11-01-docs-sync-deployment.md) — `ab4aadc75` (documentation and comments only, no migrations, 13 s restart, both transcode nodes moved, one viewer's stream restarted on its node at the owner's request, an image built locally before CI finished).
- [November 1 deployment: provider sign-out on its domain](../operations/2026-11-01-provider-sign-out-deployment.md) — `59616c4af` (a provider sign-out returns to the organization domain it started on, seams stay at 304, no migrations, 18 s restart, an image built locally before CI finished).
- [November 1 deployment: organization domains](../operations/2026-11-01-organization-domains-deployment.md) — `8a721cff2` (organization domains and branding, provider sign-in kept on its domain, the 304th seam, one migration adding two tables, 18 s restart, the server's address registered to the default organization, Garden `d9f77cc2`, an image built locally before CI finished).
- [November 1 deployment: Silo through `63ef34b23`](../operations/2026-11-01-silo-sync-63ef34b23-deployment.md) — `f14332386` (Silo through `63ef34b23`, and a sign-in its organization revoked is told to sign in again; no migrations, 105 s restart, an image built locally before CI finished).
- [November 1 deployment: off Silo's `/api/v1`](../operations/2026-11-01-off-silo-v1-deployment.md) — `e5cd1eb6a` (the storefront and the web app off Silo's `/api/v1`, native platform and viewer campaign routes, one migration that changed no rows, 46 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment through `32a2f63e1`](../operations/2026-10-31-silo-sync-32a2f63e1-deployment.md) — historical: `4edbee99d` (Silo through `32a2f63e1`, Silo's lines verbatim in the session-revoked hook and household check, 303 seams, one migration, 48 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment through `288f58200`](../operations/2026-10-31-silo-sync-288f58200-deployment.md) — historical: `29c23fc2c` (Silo through `288f58200`: password changes that can sign out other devices, PIN revocation checked on household writes, no migrations, 46 s restart, an image built locally before CI finished).
- [October 31 Silo sync deployment](../operations/2026-10-31-silo-sync-deployment.md) — historical: `8065e5746` (Silo through `07c05cad6`, seven additive migrations applied online beside the live server in 126 s, 52 s restart, an image built locally before CI finished).
- [October 30 upgrade prompt and new arrivals deployment](../operations/2026-10-30-upgrade-prompt-new-arrivals-deployment.md) — historical: `b99aa105c` (the stream-limit upgrade prompt and the new-arrivals list for storefronts, one migration, an image built locally before CI finished).
- [October 30 S3 all-buckets deployment](../operations/2026-10-30-s3-all-buckets-deployment.md) — historical: `8f4ba105c` (guided S3 setup and all-bucket sources with S3 plugin 0.4.0, no migrations).
- [October 29 deployment](../operations/2026-10-29-silo-sync-free-play-deployment.md) — historical: `38e587805` (Silo through `cc8514943` and preview free play, ten forward-only migrations, 17 min 41 s offline for Silo's marker backfill, an image built locally before CI finished), then `cc37f3900` (monitoring API keys open the events socket, no migrations), then `09dab0097` (AI subtitles from word-only speech-to-text responses, Chinese and Japanese without spaces and in shorter lines, no migrations).
- [October 27 main deployment](../operations/2026-10-27-main-deployment.md) — historical: `422a51fed` (Silo through `3e2d306b2`, the first CI-built production image, three forward-only migrations), then `276becf4a` (the admin-context fix, no migrations), PostgreSQL tuning sized for the database container and then turned off, then `7a3b59027` (the proxy, OpenAPI and handler fixes, no migrations), then `bee8a7728` (the rest of the OpenAPI review's status fixes, no migrations), then `aeeea2357` (Silo through `73a6ae6a3` and `golang.org/x/net` v0.62.0, one migration), then on October 28 `9cc80cd33` (Silo through `564b906b8`, one migration, an image built locally before CI finished).
- [October 26 Stage A deployment](../operations/2026-10-26-stage-a-deployment.md) — historical: `7831034ea` (account policy on users, music and lifecycle receipts removed, three forward-only migrations), then `d064644a8`.
- [October 26 main deployment](../operations/2026-10-26-main-deployment.md) — historical: `d183b286f`, Silo through `6de6d5e5e`, 21 Silo migrations, a locally built image.
- [October 15 preview server deployment](../operations/2026-10-15-preview-server-deployment.md) — historical: published source, offline migrations, presentation workers since removed, public preview left disabled.
- [October 7 native storage deployment](../operations/2026-10-07-native-storage-deployment.md) — historical: `c87b44545`, Silo through `74158b4a8`, 407 seams.
- [September 30 upstream deployment](../operations/2026-09-30-upstream-deployment.md) — historical: exact revision and verification limits.
- [September 19 Xtream deployment](../operations/2026-09-19-xtream-deployment.md) — historical: `418a18b7d`, Xtream live providers, migration and rollback boundaries.

## Source integration records

All historical; the [changelog](CHANGELOG.md) carries every later integration.

- [October 10 upstream resolutions](upstream-merge-2026-10-10.md) — historical: conflict resolutions for download, thumbnail, metadata and web changes.
- [October 5 merge reduction](../operations/2026-10-05-merge-reduction.md) — historical: owned tenant modules, restored shared files, the ledger from 396 to 387 paths.
- [October 5 presentation plugin cutover](../operations/2026-10-05-presentation-plugin-cutover.md) — historical: the worker processes that [in-process evaluators](../architecture/bloem-presentation-evaluators.md) replaced on October 26.
- [October 5 integration verification](../operations/2026-10-05-upstream-integration.md) — historical: Silo build 1116, checks and inherited test limitations.
- [October 2 integration verification](../operations/2026-10-02-upstream-integration.md) — historical: imported revision, regressions and validation gaps.
- [Upstream reconciliation — 2026-09-08](../architecture/2026-09-08-upstream-reconciliation.md) — historical: the September 8 reconciliation and its deferred items.

## Bloem editions of upstream documents

For these, Bloem's text differs throughout; read the Bloem edition instead of the upstream file.

- [docs/non-goals.md](overlays/non-goals.md) (upstream: [docs/non-goals.md](../non-goals.md)) — Live TV in scope, remote-URL ingestion still out.
- [Installing and running Bloem with Docker](overlays/wiki/deployment/docker.md) — maintained Bloem installation and operations guide; the former upstream wiki page was removed.
- [Bloem guides for operators and viewers](overlays/wiki/index.md) — Bloem's guide index, alongside the current [Silo reference index](../README.md).

## Overlays on upstream documents

Each overlay lists, by section, what Bloem adds to or replaces in the upstream document of the
same path. Read the upstream document together with its overlay.

- [DEVELOPMENT.md](overlays/DEVELOPMENT.md) — overlay on [DEVELOPMENT.md](../../DEVELOPMENT.md)
- [docs/admin-api.md](overlays/admin-api.md) — overlay on [docs/admin-api.md](../admin-api.md)
- [docs/api-docs.md](overlays/api-docs.md) — overlay on [docs/api-docs.md](../api-docs.md)
- [docs/architecture/api-contract.md](overlays/architecture/api-contract.md) — overlay on [docs/architecture/api-contract.md](../architecture/api-contract.md)
- [docs/architecture/core-id-range.md](overlays/architecture/core-id-range.md) — overlay on [docs/architecture/core-id-range.md](../architecture/core-id-range.md)
- [docs/architecture/history-import-execution.md](overlays/architecture/history-import-execution.md) — overlay on [docs/architecture/history-import-execution.md](../architecture/history-import-execution.md)
- [docs/architecture/invitations-onboarding.md](overlays/architecture/invitations-onboarding.md) — overlay on [docs/architecture/invitations-onboarding.md](../architecture/invitations-onboarding.md)
- [docs/architecture/observability.md](overlays/architecture/observability.md) — overlay on [docs/architecture/observability.md](../architecture/observability.md)
- [docs/architecture/playback-protocol-v3.md](overlays/architecture/playback-protocol-v3.md) — overlay on [docs/architecture/playback-protocol-v3.md](../architecture/playback-protocol-v3.md)
- [docs/architecture/recommendations.md](overlays/architecture/recommendations.md) — overlay on [docs/architecture/recommendations.md](../architecture/recommendations.md)
- [docs/architecture/scenario-acceptance.md](overlays/architecture/scenario-acceptance.md) — overlay on [docs/architecture/scenario-acceptance.md](../architecture/scenario-acceptance.md)
- [docs/architecture/secret-encryption.md](overlays/architecture/secret-encryption.md) — overlay on [docs/architecture/secret-encryption.md](../architecture/secret-encryption.md)
- [docs/architecture/settings-contract.md](overlays/architecture/settings-contract.md) — overlay on [docs/architecture/settings-contract.md](../architecture/settings-contract.md)
- [docs/architecture/streaming-write-deadline.md](overlays/architecture/streaming-write-deadline.md) — overlay on [docs/architecture/streaming-write-deadline.md](../architecture/streaming-write-deadline.md)
- [docs/architecture/v1-scope.md](overlays/architecture/v1-scope.md) — overlay on [docs/architecture/v1-scope.md](../architecture/v1-scope.md)
- [docs/auth-api.md](overlays/auth-api.md) — overlay on [docs/auth-api.md](../auth-api.md)
- [docs/invitations-api.md](overlays/invitations-api.md) — overlay on [docs/invitations-api.md](../invitations-api.md)
- [docs/libraries-api.md](overlays/libraries-api.md) — overlay on [docs/libraries-api.md](../libraries-api.md)
- [docs/realtime-api.md](overlays/realtime-api.md) — overlay on [docs/realtime-api.md](../realtime-api.md)
- [docs/release-versioning.md](overlays/release-versioning.md) — overlay on [docs/release-versioning.md](../release-versioning.md)
- [Bloem S3 storage notes](overlays/s3-storage-setup.md) — maintained additions to the current [blob-storage architecture](../architecture/blob-storage.md).
- [docs/settings-api.md](overlays/settings-api.md) — overlay on [docs/settings-api.md](../settings-api.md)
- [Bloem artwork storage](overlays/wiki/admin/artwork-storage.md) — Bloem campaign/seasonal storage alongside the current [blob-storage architecture](../architecture/blob-storage.md).

## Guides (wiki)

- [Bloem Server admin guide](../wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback, access policy, Live TV, maintenance.
- [Entitlement templates](../wiki/admin/entitlement-templates.md) — operator guide to revisioned access-policy templates.
- [Bloem user guide](../wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests.

## Every page, by area

### Organizations, accounts and policy

- [How organizations work](../architecture/bloem-organizations.md) — start here: the platform, organizations, accounts and profiles in plain language, with six pictures.
- [How multitenant administration works](../architecture/multitenant-administration.md) — the Platform and Organization administrative contexts and what each manages.
- [How library ownership and grants work](../architecture/resource-tenancy-foundation.md) — resource owners and the first resource-isolation boundary.
- [How tenant authorization works](../architecture/opa-tenant-authorization.md) — how tenant facts, OPA and SQL scope catalog and playback reads.
- [How Silo backend switching works](../architecture/silo-backend-switching.md) — Silo and Bloem taking turns on one database: the flag, its invariants, the all-in-one procedure, and `make silo-switch-check` and `make silo-switch-check-all-in-one`.
- [How request rules apply to organizations and profiles](../architecture/bloem-request-policy.md) — verified profile quotas, scoped administrative usage and group limits on Silo's request service.
- [How retiring an organization and deleting a library stay safe](../architecture/bloem-lifecycle-safety.md) — atomic tenant retirement and library deletion.
- [How Bloem's security foundation works](../architecture/bloem-security-foundation.md) — the `/api/bloem/v1` discovery and administration boundary beside Silo's `/api/v1` login.
- [How Bloem's contract adjudications work](../architecture/bloem-contract-adjudications.md) — reviewed, pinned replacements for Silo scenario expectations Bloem deliberately differs from.
- [How Bloem's adapters over Silo work](../architecture/bloem-upstream-adapters.md) — migration filesystem, profile-aware interfaces and embedded-web boundaries over Silo's code.

### Native storage and media delivery

- [How native storage works](../architecture/bloem-native-storage.md) — experimental storage-source (S3, Bookwarehouse) ebook, movie and audiobook libraries: locations, scanning, covers and reading.
- [How stored audiobooks play](../architecture/bloem-native-audio-playback.md) — complete-book publication and pinned direct audio playback.
- [How stored movies play](../architecture/bloem-native-video-playback.md) — source admission, movie publication, execution ownership, worker HLS and supported surfaces.
- [How a trusted tool reads a stored file](../architecture/bloem-native-tool-input.md) — bounded pinned reads for trusted local tools, child lifetime and host admission.
- [How a scan reads a stored file](../architecture/bloem-native-scan-reader.md) — sealed scan pages, pinned entry reads and current scan authority.
- [How Bloem looks inside a stored file](../architecture/bloem-native-probe.md) — trusted ffprobe input, shared read budgets and process cleanup.
- [How playback works across several API servers](../architecture/bloem-cluster-media.md) — which node owns Live TV and native media sessions, on Silo's playback attempts and heartbeats.
- [How access is checked as content leaves the server](../architecture/delivery-boundary-enforcement.md) — library access rechecked where bytes or rows leave the server.

### Requests and preview

- [How audiobook requests work](../architecture/bloem-audiobook-requests.md) — request authority, independent quotas, identifier matching and notification scope.
- [How ebook requests work](../architecture/bloem-ebook-request-foundation.md) — default-off web/admin requests, Bookwarehouse fulfillment, durable recovery and linked-library completion.
- [How browse-only access and the preview work](../architecture/bloem-browse-only-content-policy.md) — protected preview principals, signed guest sessions, catalogue hygiene and native content-delivery boundaries.

### Search and recommendations

- [How search finds titles by words and by meaning](../architecture/bloem-search-and-embeddings.md) — start here: the words and meaning halves of a search, matching strategies, task labels, changing the embedding model, with two pictures.
- [Recommendations overlay](overlays/architecture/recommendations.md) — what Bloem adds to Silo's recommendations page: task labels for `gemini-embedding-2`.

### Announcements, promotions and seasonal presentation

- [How announcements work](../architecture/admin-announcements.md) — admin-authored messages, targeting and inbox rules.
- [How playback cards work](../architecture/playback-overlays.md) — chapter-triggered campaign cards during playback.
- [How seasonal packs are scheduled](../architecture/seasonal-scheduling.md) — annual ambience schedules evaluated in the selected timezone.
- [Where campaign and seasonal artwork is stored](../architecture/bloem-campaign-asset-storage.md) — campaign and seasonal uploads use the catalog Assets store (local or S3); public S3 is only a fallback.
- [How Bloem picks which campaigns and seasonal packs to show](../architecture/bloem-presentation-evaluators.md) — in-process promotions and ambience evaluation, host authority and section extension boundaries.

### Live TV

- [How Xtream Live TV providers work](../architecture/xtream-live-tv.md) — encrypted provider accounts, live channels, XMLTV, connection limits; no VOD or remote URLs.
- [How apps reach Live TV](../architecture/live-tv-client-access.md) — the `watch_live_tv` permission, capability discovery and tuner-session handling.
- [How Live TV finds tuners](../livetv-tuner-discovery.md) — HDHomeRun and Dispatcharr discovery and probing.
- [prairie-source-manifest.tsv](../livetv/prairie-source-manifest.tsv) — every Prairie Server source blob the Live TV subsystem imports or adapts.

### Clients, protocols and compatibility

- [Leaving Silo's `/api/v1`](../architecture/bloem-leaving-silo-v1.md) — the rule that Bloem adds nothing to Silo's `/api/v1`, what moved off it, and what still calls it.
- [How Bloem's native API is organized](../architecture/bloem-client-surface.md) — why Bloem-only features live under `/api/bloem/v1`, and the native document set.
- [How Bloem's SDKs and generated contracts fit together](../architecture/bloem-sdk-compatibility.md) — plugin SDK pins, generated client DTOs and consumer synchronization.
- [How the client contract generator works](../architecture/bloem-client-contract-generator.md) — the registry, the Kotlin and Swift files the apps copy, the contract digest, and the rules that keep older apps working.
- [How Bloem runs Silo's Jellyfin and Audiobookshelf servers](../architecture/bloem-compatibility-removal.md) — Silo's Jellyfin and Audiobookshelf servers, their tenant wiring, and the removed companion gateway.
- [How Pastime tracking was removed](../architecture/bloem-pastime-removal.md) — forward-only cleanup migration and the rule that Pastime returns only as an ordinary plugin.
- [How removing a title in a tracker clears its watch history](../architecture/bloem-watch-sync-removals.md) — watched removals on a watch-sync connection, applied to that one profile, newer watches kept.
- [How remote control works](../architecture/admin-remote-control.md) — invariants for server-issued commands to client apps.

### Embedded web

- [What the web app covers](../architecture/bloem-web-feature-coverage.md) — Bloem features in Silo's web app: implemented screens, authority boundaries, verification and limits.
- [How Bloem's web text is translated](web-messages.md) — Bloem's message catalog beside Silo's: where the text lives, keys and the lazy admin part, adding a message, translations, the generator, the coverage test and the pseudo-locales.

## API references and specs

- [Bloem native API reference](../bloem-api-reference.md) — the maintained wire reference for Bloem's native extensions.
- [Playback cards: who calls which route](../playback-overlays-api.md) — campaign CRUD and the save-to-inbox endpoint for playback overlays.
- [How Bloem announces itself on a home network (`_bloem._tcp`)](../specs/lan-service-advertisement.md) — the opt-in DNS-SD service (`internal/lanadvert`) Bloem's clients browse for.

## Operations runbooks

- [Managing entitlement templates](../operations/entitlement-templates.md) — platform-administrator operation of revisioned policy templates.
- [How bulk policy changes work](../operations/bulk-policy-cohorts.md) — reviewed policy changes for up to 10,000 snapshotted accounts.
- [Releasing bulk policy changes: the canary runbook](../operations/bulk-policy-cohorts-runbook.md) — releasing the cohort capability and validating it with one test account.

## Other Bloem-owned files

- [How audiobooks and podcasts work in Bloem](../../internal/audiobooks/README.md) — package notes for Silo's audiobook code and its Audiobookshelf-compatible server in Bloem.
- [Bloem's brand assets](../../assets/brand/README.md) — vector sources, palette and where the rendered icons live.

## Upstream documents that still carry Bloem edits

The playback schema files below remain seams (`contracts/seams.txt`) because tooling reads them.
The retired `playback.proxy_policy` explanation now lives only in the
[Bloem v1 overlay](overlays/architecture/v1-scope.md#retired-bloem-playback-proxy-policy);
Silo's `v1-scope.md` no longer carries that fork-only row.

- `docs/design/schemas/playback-v3/v3/**` — playback schema and fixtures validated by `make verify-playback-fixtures`.
