# Bloem deployment — November 1, 2026: Silo through `63ef34b23`, and old sign-ins sign in again

Production has run `main` at `f143323865cdec15f15a294048677fc88816a9b4` since 05:22:50 UTC.
It replaces `e5cd1eb6a` and adds:

- `25129bcbd`: a sign-in that started before its organization revoked the account is
  ended. Its requests get `401 session_invalid` (`session_expired` on `/api/v2`), so the
  client signs in again. Before, every profile request got `404` "Profile not found" and
  the web app showed a broken home page;
- `f14332386`: the merge of Silo `main` through `63ef34b23` (November 1, 04:45 UTC),
  57 Silo commits with no migrations. They bring audit records for sign-ins, sessions,
  profiles and API keys, download bandwidth budgets shared across servers, starting
  without root through the `silo` entrypoint, and fixes to subtitles and AI errors,
  collections, autoscan, seasonal rows and password reset.

The owner asked to deploy. The image was built locally without waiting for CI.

## Merge resolutions

Silo wins; the Bloem difference stays as small as possible.

- `internal/ai/llm/transcribe.go`: Bloem's words-only `verbose_json` branch (a declared
  seam) stays, with Silo's `ErrUnusableResponse` wrapping on the errors both share.
- `internal/userstore/pgstore/profiles.go`: Silo's audited profile delete. Bloem's
  in-transaction `deleteProfile` helper keeps the body and takes a lock hook where Silo
  locks the profile row, so the audited path keeps Silo's lock order.
- `internal/userstore/pgstore/profile_lifecycle_tx.go`: Bloem's copy of
  `preferenceSettingsTx.GetProfile` is gone; Silo now declares the same method.
- `internal/auth/bloem_service.go`: the session repository interface gained Silo's
  `revokeChecked`.

Silo now refuses `MODE=transcode` or `MODE=proxy` without `NODE_URL`. Both transcode
nodes set it.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels and the web bundle from the same archive.
  Tagged `bloem-server:release-f14332386` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `974c19f47aadee6789668316a69d902565450848ed6fb4494c3c0abc4f3edba7` on both hosts.
- **Before the restart:** container snapshot `pre-main-f14332386`. Database restore
  point of the same name at LSN `41A/F01D7D18`. Segment `000000030000041A000000F0`,
  which holds it, was archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `e5cd1eb6a`. Two
  transcodes were running, and the stop took 93 seconds (exit 0). No migrations were
  pending; 749 are applied. The server answered ready at 05:22:59, 105 seconds after
  the stop began, and was healthy with no restarts.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `f1433238`, and `/api/bloem/v1/capabilities` lists `promotions_viewer_v1` and
  `playback_overlays_v1`. Protected routes answer `401` without a session; the old
  `/api/v1` ones still answer. No error lines were logged after the start. The
  warnings were the S3 provider refusing CORS on the private bucket and the embedding
  provider's rate limit.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-f14332386`, healthy, and the server lists both as healthy. Their
  previous compose files are kept with suffix `.bak-pre-f14332386`.

## Verification before deploy

Done locally on the merge, with `umask 022` and Go 1.26.10 from `go.mod`:

- the whole Go suite (235 packages), on PostgreSQL started with
  `max_connections=300` and `max_locks_per_transaction=512` and Redis for the
  storage tests;
- Bloem CI's changed-line lint for merges, with no issues on Bloem's lines;
- every contract check (settings bindings, client DTOs, playback fixtures, route
  inventory, migration ledger, scenario catalogs, offline routes, both OpenAPI
  documents, API catalogue, v2 fixtures, seams at 303, local paths, router recovery);
- the web typecheck, v2 web types, settings bindings, build and bundle budget, and the
  whole web suite. Three tests failed: the two `AuthProvider.sessionRestore` tests,
  which already fail on `main`, and one `OnboardingGate` test that passes alone.

## Rollback

Start `bloem-server:release-e5cd1eb6a` again and set the transcode nodes back from their
`.bak-pre-f14332386` compose files. No schema changed. Restoring the database to the
restore point together with the container snapshot also rolls back, but discards
everything written since 05:20 UTC.
