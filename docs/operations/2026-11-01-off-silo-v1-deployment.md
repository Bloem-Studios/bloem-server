# Bloem deployment — November 1, 2026: the storefront and the web app leave Silo's `/api/v1`

Production has run `main` at `e5cd1eb6a9fe8d103a95f0d40080c99a280fa54a` since 01:34 UTC.
It replaces `4edbee99d` and adds:

- `3b9e96939`: the Silo switch check for the all-in-one layout, and the check's driver on
  Silo's `/api/v2`;
- `3eb77be33`: the storefront (Bloem Garden) and the web app off Silo's `/api/v1`;
- `cb7ed04f9`: documentation (explainers with pictures, the native API by topic);
- `e5cd1eb6a`: the announcements settings page renders outside an admin context.

The owner asked to deploy. The image was built locally without waiting for CI.

## What changed

- **Native platform routes** for the storefront under `/api/bloem/v1/admin/platform`:
  reseller organizations and members, account profiles, devices and sign-in sessions,
  creating and updating an account on a plan (built on the account service behind
  Silo's `/api/v2/admin/users`), announcements, campaigns and seasonal packs.
- **Native viewer campaign routes**, advertised as `promotions_viewer_v1`:
  `GET /api/bloem/v1/promotions` (Home included, with `home_position`),
  `POST /api/bloem/v1/promotions/{id}/save` and
  `PUT /api/bloem/v1/promotions/{id}/dismissals/{surface}`.
- **The web app** calls none of Silo's `/api/v1`. Settings, Announcements asks for the
  platform context.
- **Artwork** is served from `/api/bloem/v1/ambience/assets/`; `playback_overlays_v1` is in
  `/api/bloem/v1/capabilities`.
- The old `/api/v1` routes stay mounted for the phone apps and for the storefront
  release now in production, which still calls them.
- **Migration.** One Bloem migration,
  `20261031234831_bloem_native_ambience_asset_paths`, rewrites stored artwork paths.
  Seasonal packs and campaigns were empty in production, so it changed no rows.

See [leaving Silo's `/api/v1`](../architecture/bloem-leaving-silo-v1.md) and the
[changelog](../bloem/CHANGELOG.md).

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-e5cd1eb6a` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `eca25b1a287a8d839717d4005e2427087ca4a19ccfa117b2a815e2fea76c7172` on both hosts.
- **Before the restart:** container snapshot `pre-main-e5cd1eb6a`. Database restore
  point of the same name at LSN `416/B6333688`. Segment `0000000300000416000000B6`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `4edbee99d`. One
  transcode was running, and the stop took 37 seconds (exit 0). The migration step
  applied one migration; 749 are applied, none pending. The server answered ready at
  01:34:34, 46 seconds after the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `e5cd1eb6`, and `/api/bloem/v1/capabilities` lists `promotions_viewer_v1` and
  `playback_overlays_v1`. The new routes answer `401` without a session; the old
  `/api/v1` ones still answer. No error lines were logged after the start, and a
  transcode was running again. The warnings were the S3 provider refusing CORS on the
  private bucket, the embedding provider's rate limit, and the scanner refused one
  hidden directory in a media share.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-e5cd1eb6a`, healthy. The API marked both unhealthy while they
  restarted and recovered both at 01:35:28. Their previous compose files are kept with
  suffix `.bak-pre-e5cd1eb6a`.

## Verification before deploy

Done locally, with `umask 022` as in CI and Go 1.26.10, the version in Silo's `go.mod`:

- the Go packages the change touches (`internal/api/...`, `internal/apiv2/...`,
  `internal/promotions/...`, `internal/notifications/...`, `internal/routeinventory/...`,
  `internal/contractledger/...`, `cmd/...`, `contracts/...`), with the migration's
  PostgreSQL test;
- Bloem CI's changed-lines lint, with no issues;
- every contract check (OpenAPI, route inventory, offline routes, route ledger, client
  DTOs and coverage, seams at 303, local paths, API catalogue and topic pages);
- the whole web suite. Three tests failed: the two `AuthProvider.sessionRestore` tests,
  which already fail on `main`, and one `GlobalSearch` test that passes alone.

**Bloem CI** on `cb7ed04f9` showed one failure this change caused: the settings layout
lost the announcements page outside an admin context. `e5cd1eb6a` fixes it. On
`e5cd1eb6a` the web tests fail only the two `AuthProvider.sessionRestore` tests, and the
scenario executor fails on Silo's new `sign_out_other_sessions` field in the account
capability. Both failed on `main` before this change. The Go jobs were still running at
deploy time.

## Rollback

Start `bloem-server:release-4edbee99d` again. The migration changed no rows here; once
the storefront or the web app has saved artwork under the new path, run the migration's
Down step first, because `4edbee99d` serves artwork only from `/api/v1/ambience/assets/`.
Roll back the storefront to its previous release with the server: the current storefront
calls routes `4edbee99d` does not have. Restoring the database to the restore point
together with the container snapshot also rolls back, but discards everything written
since 01:33.
