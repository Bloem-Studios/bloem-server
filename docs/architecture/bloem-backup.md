# How backups work in Bloem

> **Provenance and status.** Bloem page, written on 2026-11-06 and updated the same
> day against `main` at `8a37ea9bc`. Backup and restore are Silo's feature, merged
> from Silo through `cecd5b9bc`; this page covers what Bloem changes. The nightly
> backup is switched off on Bloem's production server (`backup.enabled` is false);
> "Back up now" and each person's "Your data" export still work. Silo's
> [backup reference](backups.md) holds the full design.

A backup is a copy an administrator can bring back later. On Bloem it holds **user
data only**: the accounts and their profiles, everything each profile keeps (watch
history and progress, ratings, favourites, watchlists, lists, settings), the
organizations, memberships and access groups they belong to, their avatars, and the
list of libraries their access is matched by. The catalog, the recommendations'
embeddings, the scan state, the plugins, the server's collections, home rows and
settings, and the server's files (artwork, subtitles, branding) are left out: they
are most of the server, and the server rebuilds them or keeps them itself. A
production backup is tens of megabytes instead of the whole database.

Recovering the whole server is not what a backup is for on Bloem. The database's
point-in-time recovery does that, so a Bloem backup refuses to replace the live
database.

## Bringing back chosen accounts

A **selective restore** copies chosen accounts and profiles from a backup into a
running server. Each backup records the organization every account belonged to and
the access group of every profile.

- An account that is no longer on the server comes back in the organization it was
  in when the backup was taken, if the server still has that organization. If not,
  or if the backup came from another server, it comes back in the default
  organization.
- Its access group is the group of the same name in that organization, never a
  same-named group of another organization.
- A restored profile joins its account's organization, in the group of the name the
  backup recorded there, else the account's group, else the organization's default
  group.

A backup taken before 6 November 2026 does not record organizations, so its
accounts come back in the default organization.

Restoring accounts takes no extra backup first. Silo copies the whole server before
every restore so it can be undone; on Bloem, point-in-time recovery of the database
stands in for that copy, and a restore of accounts only adds what is missing and
merges the data chosen for them.

**Not compared yet.** A restore refuses an account the server would give more than
the backup did, field by field. It does not yet compare whether an account may play
at all or is preview-only, because the backup does not record those two.

## Who can change what

| Who | What |
| --- | --- |
| Platform administrator | Takes backups ("Back up now") and restores chosen accounts and profiles from them |
| Organization administrator | Nothing: backup and restore are server-wide |

## Words used in the code

| Word | Meaning |
| --- | --- |
| `restoredDataTables`, `serverStateTables`, `loggedTables` | The data, server-state and log groups of the table above |
| `secretTables` | Tables holding a sign-in or a credential, each left out, carried from the live server, or restored with a reason |
| `stricterFlags` | Switches a restore keeps at the stricter of the backup's and the live value |
| `restoredFlagColumns` | Other true/false columns, each with why the backup's value is safe |
| `SettingKindOf` | How a restore treats each server setting |

## For developers

- User-data backups (`bloem_user_data_backup.go`): Bloem's server binary calls
  `backup.SetBloemUserDataOnly(true)` first thing (`runBloemCommand`). The dump then
  leaves out the data of every table without a `user_id` or `profile_id` column,
  except `users`, `organizations`, `organization_memberships` and `access_groups`, on
  top of the tables every backup leaves out. A manifest whose `excluded_table_data`
  names `media_items` is a user-data backup, and `RestoreSwap` refuses it (one seam
  line in `swap.go`; the exclusion is one in `service.go`). Its restore set holds only
  the users and libraries sections (`bloemRestoreSetSections`, one line in
  `restoreset_write.go`), and it copies no server files (`withBlobs` is off; avatars
  travel in the users section). A restore whose selection is accounts only takes no
  before-restore backup (`bloemBeforeRestoreContext` in `selective_apply.go`,
  `bloemSkipBeforeRestore` in `backupWith`). The switch is off by default, so Silo's
  own tests keep whole-server backups.
- Whole-server classification, for a backup taken with the switch off. Every table
  belongs to one group: data, restored as the backup has it (organizations,
  memberships, plans, branding, promotions, book requests, the Live TV setup); server
  state, restored and rebuilt (the storage scan index, the Live TV guide, job and lease
  state); logs and queues, left out (remote commands); short-lived sign-ins, left out
  (preview guest sessions and their grants, Watch Together room owners); and
  credentials kept from the live server, never the backup's (Xtream sign-ins, the
  preview storefront's ticket keys). Grant switches (an access group's playback switch,
  a storage source, a Live TV channel or guide source) keep the stricter of the
  backup's and the live value.

- Bloem's classifications live in `internal/backup/bloem_classification.go`. Its
  `init` adds them to Silo's lists, so Silo's `tables.go`, `credentials.go`,
  `credentials_stricter.go`, `users_registry.go` and settings files merge unchanged.
  A new Bloem table, true/false column of a restored table, per-user table, or
  setting fails `TestEveryTableIsClassifiedDB`, `TestUserTablesCoveredDB` or the
  settings tests until it is added there.
- Settings: `livetv.dvr_path`, `livetv.hw_accel` and `livetv.hw_decode` describe this
  server; the other `livetv.` settings are restored, on a "Live TV" settings page.
  `lan.advertisement_enabled` and `bloem.requests.ebooks_enabled` keep their stricter
  value, as `server.lan_discovery` and Silo's request switches do. The Silo user-DB S3
  keys are not Bloem settings and are dropped from the restored secrets.
- Selective restore (`bloem_restore_tenancy.go`): `bloemAttachOrganizations` and
  `bloemAttachProfileGroups` add `bloem_organization` to each account entry and
  `bloem_access_group` to each profile entry of a restore set. `bloemPlanContext`
  names the organization an account is restored into (by id, else the default) as
  the creation organization (`tenancy.WithCreationOrganization`), so
  `access.GroupPolicyByName` matches the group there (`bloem_group_by_name.go`) and
  `auth.CreateRestoredAccountInTx` creates the membership there
  (`linkAccountMembership`). `pgstore.CreateRestoredProfileInTx` writes the profile
  with its account's organization and group (`bloem_restored_profiles.go`).
  `TestBloemRestoredAccountsJoinTheirOrganizationDB` runs a restore with the fixture
  triggers off, as production has none. Seams: `restoreset_users.go`, `users_plan.go`
  and `users_apply.go` in `internal/backup`.
- Bloem's fields on Silo's access-policy and profile types are listed in
  `internal/backup/bloem_coverage_test.go`, which Silo's coverage tests consult through
  two seam lines in `credentials_authority_test.go`. `PlaybackAllowed` and `PreviewOnly`
  are marked "not compared yet". Comparing them needs them in the restore set's
  `EffectivePolicy` first: `access.WiderThan` would otherwise report every restored
  account as wider, since the restore file's policy has no playback switch. A
  profile's `OrganizationID` and `AccessGroupID` are placed, not compared.
- Test databases: Silo's `internal/backup/backuptest` migrates a template of its own.
  `bloem_fixture_compat.go` installs Bloem's fixture-compatibility triggers there
  (one seam line in `backuptest.go`). The triggers arm themselves in any database whose
  name marks it as a test database, so copies of the template and databases restored
  from its dumps work without the `ALTER DATABASE` setting; an explicit
  `SET bloem.fixture_compat = 'off'` still disarms them.
