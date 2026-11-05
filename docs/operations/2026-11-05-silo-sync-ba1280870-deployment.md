# Bloem deployment — November 5, 2026: Silo through `ba1280870`

Production has run `main` at `a96d2efe2588cd21606cfc03c2ccb5130a037ff9` since 02:03:14 UTC.
The merge bot deployed it after Bloem CI passed it. It replaces `d69ce2aa8`, recorded in the
receipt for [Silo through `90c531469`](2026-11-05-silo-sync-90c531469-deployment.md). Since
`d69ce2aa8` production gained:

- `cfd240574`: that deployment's receipt.
- `747960b29`: the merge of Silo `main` from `90c531469` through `ba1280870` (November 5,
  00:43 UTC), one Silo commit with no migration. Silo's #3516 lets an administrator manage
  another account's profiles: `getAdminUserProfile`, `createAdminUserProfile`,
  `updateAdminUserProfile` and `deleteAdminUserProfile` under
  `/api/v2/admin/users/{id}/profiles`, including setting or removing a profile's PIN (the
  primary profile's too). Only the Owner may change the Owner's or another administrator's
  profiles. `listAdminUserProfiles` now reports `has_pin`, `is_primary` and `is_child`, the
  account capabilities report `profile_management`, and the admin user page has a Profiles
  tab.
- `7768bb4a7`: a Bloem fix (below).
- `1758dfa80`: regenerated client DTOs with the admin profile list's three new fields, the
  re-pinned contract digest and the API catalogue (921 `/api/v2` operations).
- `a96d2efe2`: the multitenant administration page and the status rows for the batch.

## Merge resolutions

The merge had no conflicts. The batch touches seven seams (`internal/api/handlers/profiles.go`,
`internal/api/handlers/admin_users_service.go`, `internal/apiv2/router.go`, the route manifest,
the contract ledger test, the v2 OpenAPI document and the web schema), and git merged each
beside Bloem's lines.

One Bloem fix: Silo's admin profile create runs the profile service for another account. Bloem's
profile limit and profile count took the organization from the request tenant, which on that
request is the administrator's own organization. For an account in another organization the
new profile would have taken the caller's default access group, which
`user_profiles_organization_access_group_fkey` refuses. The request tenant's organization now
applies only when that tenant is the account itself; otherwise the account's own group decides
(`7768bb4a7`, with a test that failed before the fix). The fix changes Bloem-owned code and one
existing Bloem line in `profiles.go`; no Silo line changed. No new seam came with the batch; the
seam ledger stays at 316 entries, eight of them temporary web fixes that go when Silo merges its
own versions.

## Procedure and result

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-a96d2efe2` (image `sha256:83d4b85d303deb965c7f03f26229b7b33281cca6ff85a3e9387a00d193406c04`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `db568a010ba610e7d1b7159ddcb01f2a1626dcf54ab8eba4370bf46a9472d670` on both hosts.
- **Compose.** The script rendered the configuration with the overrides file and checked that
  it was unchanged.
- **Before the restart:** container snapshot `pre-main-a96d2efe2`. Database restore point of
  the same name at LSN `48F/7A1CB438`. Segment `000000030000048F0000007A`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `d69ce2aa8`. One transcode process
  was running inside the server, and the stop took 37 seconds (exit 0). No migration ran;
  762 are applied. The server answered ready at 02:03:14, 44 seconds after the stop began,
  and was healthy at 02:03:16. Its runtime configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `a96d2efe`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-a96d2efe2`, healthy, and the server lists both as enabled and healthy on
  `a96d2efe2`.
- **Old images.** The script removed `release-28bdc197b` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

The merge bot's fast local checks (build, verify, changed-line lint and the scenario
executor) passed 25 of 25 steps at the first run, and `go test ./internal/scenariocatalog/`
passed. The `internal/api/handlers` and `internal/apiv2` packages passed with the fix. No test
needed a rerun alone. `main` did not move during the checks.

## Bloem CI

Run 41950475118 on `a96d2efe2` passed at its first attempt, and the deploy used that result.

## Rollback

Start `bloem-server:release-d69ce2aa8` again and set the transcode nodes back to that image;
the deploy script's rollback does both. The batch has no migration, so the previous image runs
on the database as it is. Restoring the database to the restore point together with the
container snapshot also rolls back, but discards everything written since the restore point,
taken just before the 02:02 restart.
