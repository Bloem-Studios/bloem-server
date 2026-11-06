# How backups work in Bloem

> **Provenance and status.** Bloem page, written on 2026-11-06 and updated the same
> day against `main` at `8d3d5e17d`. Backup and restore are Silo's feature, merged
> from Silo through `6791aac86`; this page covers what Bloem adds so they understand
> Bloem's data. The nightly backup is switched off on Bloem's production server
> (`backup.enabled` is false), where point-in-time database recovery covers recovery;
> "Back up now" and each person's "Your data" export still work. Silo's
> [backup reference](backups.md) holds the full design.

A backup is a copy of the server's database and files that an administrator can bring
back later. Bloem has more data than Silo: organizations, plans, Live TV, storage
sources, previews and book requests. Silo's backup code refuses to run on a table or
setting it has not been told about, so Bloem tells it about every one of its own, in
Bloem's own file, without changing Silo's lists.

## What a restore brings back

Every table belongs to one of five groups, and the group decides what a restore does
with it.

| Group | What a full restore does | Bloem examples |
| --- | --- | --- |
| Data | Brings it back as the backup has it | organizations, memberships, plans and their revisions, branding, promotions, book requests, Live TV tuners, channels, guide sources, recording rules and recordings |
| Server state | Brings it back, and the server rebuilds or reconciles it | the storage scan index, the Live TV guide, job and lease state, receipts, counters |
| Logs and queues | Leaves it out | remote commands to devices |
| Short-lived sign-ins | Leaves it out, so people sign in again | preview guest sessions and their playback grants, Watch Together room owners |
| Credentials kept from the live server | Keeps what the server has right before the restore, never the backup's | Xtream provider sign-ins, the preview storefront's ticket keys |

Accounts come back whole with their organizations. A switch that grants or reaches
something, such as an access group's playback switch, a storage source, a Live TV
channel or a guide source, stays off when an administrator turned it off after the
backup, and comes back off when it was removed after the backup.

## A made-up example

Fernwood TV takes a backup on Monday. On Tuesday its administrator changes the Xtream
password of one tuner, hides an adult channel and turns off a storage source. On
Wednesday the server is restored from Monday's backup. The tuner keeps Tuesday's
password, because credentials come from the live server. The channel stays hidden and
the storage source stays off, because switches keep the stricter value. A recording
rule a household added on Tuesday is gone, as is everything else added since Monday.

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

**Not compared yet.** A restore refuses an account the server would give more than
the backup did, field by field. It does not yet compare whether an account may play
at all or is preview-only, because the backup does not record those two.

## Who can change what

| Who | What |
| --- | --- |
| Platform administrator | Takes backups and restores the whole server, when backups are switched on |
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
