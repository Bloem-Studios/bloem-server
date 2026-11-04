# Bloem deployment — November 4, 2026: maintenance reconcile candidates

Production has run `main` at `ca1740a98f6adf1d0b8fb9ff39351b09ded6a207` since 09:25:23 UTC.
It was deployed by hand with `bloem-deploy` after Bloem CI passed it. It replaces
`58b738a41`, which the [database performance receipt](2026-11-04-bloem-performance-deployment.md)
recorded. No Silo commits came with it. Since `58b738a41` production gained:

- `ffe073a86`: the previous deployment's receipt.
- `ca1740a98`: the whole-folder reconcile under the native storage maintenance scope picks its
  candidates in the database. It used to list every item and episode the local files of a
  library name, read them into the server and send them back as arrays: 1.07 million rows read
  and 1.05 million episode IDs sent for the TV library, 291,000 item IDs sent three times for
  the local ebook library, about 24 times a day. It now sends only the items whose membership
  has no present file or whose missing files are all that is left of them, and the episodes
  whose span reaches a stale membership, to the same removal and orphan purge as before.
  Before the deploy, read-only queries on production data found the new candidates equal to
  the old lists narrowed to what a removal can change in the five largest libraries (TV: 1 of
  33,619 items and none of 1,073,246 episodes; local ebooks: 52 of 291,756), and the item
  candidates took 0.6 to 2 seconds per library where listing the targets alone took 3.5 to 8.
  `TestMaintenanceReconcileMatchesListedReconcileDB` keeps the earlier path as its reference.

## Merge resolutions

None; no Silo merge. Only Bloem files changed. The seam ledger stays at 308.

## Procedure and result

- **Image.** `bloem-server:release-ca1740a98` (image
  `sha256:ad5db4a4561fa0634cd088676e5cf255a2dd539d463b1498784e7568582379d1`), loaded on CT135 and
  CT172. The binary `/usr/local/bin/silo` has SHA-256
  `d9947a9eadcc4e8d3f3cd479818b6289fc385861df9ba1e541685f641e78e1bb` on both hosts.
- **Compose.** Rendered with the overrides file and unchanged.
- **Before the restart:** container snapshot `pre-main-ca1740a98`. Database restore point of the
  same name at LSN `47C/3B9EE4A0`. Segment `000000030000047C0000003B`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `58b738a41`. One transcode process was
  running inside the server, and the stop took 6 seconds. No migration ran; 762 are applied. The
  server answered ready and healthy at 09:25:32, 18 seconds after the stop began, with no
  restarts. Its runtime configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `ca1740a9`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers run `release-ca1740a98`, healthy, and
  the server lists both as enabled and healthy on `ca1740a98`.
- **Earlier change, measured.** The bulk enrichment probe that
  `20261104065534_bloem_content_id_collation` fixed runs in 2.96 seconds on the production table,
  through `metadata_enrichment_state_pkey`, where it took 890; the book-request library match
  takes 0.04 ms where it took 7.5 seconds.
- **Old images.** The script removed `release-341108c6e` on CT135 and on the build machine. On
  the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The local gate passed build, verify and changed-line lint; the scanner, catalog, library
ingest and native storage packages passed against a migrated database.

## Bloem CI

Run 41804724401 passed on `ca1740a98` at its first attempt.

## Rollback

Start `bloem-server:release-58b738a41` again and set the transcode nodes back; the deploy
script's rollback does both. No migration ran, so either image works on the database as it is.
