# How backups work in Bloem

> **Provenance and status.** Bloem page, written on 2026-11-06 against `main` at
> `2b1131b3d`. Backup and restore are Silo's feature, merged from Silo through
> `cb4f45d0c`; this page covers what Bloem adds so they understand Bloem's data.
> Backups are switched off on Bloem's production server (`backup.enabled` is false),
> where point-in-time database recovery covers recovery. Silo's
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

## What is not finished yet

A **selective restore** copies chosen accounts from a backup into a running server.
Silo's restore file does not yet carry four things Bloem's access rules depend on:
whether an account may play at all, whether it is preview-only, and a profile's
organization and access group. Until it does, a selective restore cannot compare
them, and one that creates a profile fails, because Bloem requires every profile to
belong to an organization. Backups are off in production, so nothing uses this path
today.

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
- Bloem's fields on Silo's access-policy and profile types are listed in
  `internal/backup/bloem_coverage_test.go`, which Silo's coverage tests consult through
  two seam lines in `credentials_authority_test.go`. `PlaybackAllowed`, `PreviewOnly`,
  `OrganizationID` and `AccessGroupID` are marked "not compared yet". Comparing them
  needs them in the restore set's `EffectivePolicy` and `ProfileEntry` first:
  `access.WiderThan` would otherwise report every restored account as wider, since
  the restore file's policy has no playback switch.
- Test databases: Silo's `internal/backup/backuptest` migrates a template of its own.
  `bloem_fixture_compat.go` installs Bloem's fixture-compatibility triggers there
  (one seam line in `backuptest.go`). The triggers arm themselves in any database whose
  name marks it as a test database, so copies of the template and databases restored
  from its dumps work without the `ALTER DATABASE` setting; an explicit
  `SET bloem.fixture_compat = 'off'` still disarms them.
