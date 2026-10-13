# Pastime managed-tracking removal

Bloem no longer carries a Pastime integration. The managed-tracking bridge and
its broker, the list collections, calendar, recommendation and library bridges,
and the host hooks they needed are removed. Pastime may come back only as an
ordinary plugin on public Silo capabilities such as watch sync and the plugin
SDK. Bloem adds no triggers, identity fences, private broker services or direct
writes into Silo tables for it.

## Persistence boundary

Migration `20261013231500_remove_pastime_managed_tracking.sql` drops what the
Pastime migrations created:

- the 17 `bloem_managed_*` and `bloem_companion_*` triggers (15 on Silo-written
  tables, 2 on `organizations` and `organization_memberships`) and their 14
  functions;
- the `bloem_managed_*`, `bloem_companion_*` and `bloem_list_*` tables and
  sequences.

Before the enrollment table goes, the migration deletes the
`watch_provider_connections` rows that enrollment wrote: provider
`plugin:<installation>:pastime`, matched to an enrollment by account and
profile. Silo's `ON DELETE CASCADE` removes their sync runs, exports, list items,
scrobble sessions and other child rows. Every other connection stays, including a Pastime
connection a profile linked through ordinary watch sync. Collections a list
binding published keep their items; the `bloem_list_binding` marker leaves
`source_config`, and a synced native personal collection becomes manual.
Favorites, watchlists, ratings, progress and watch history are not touched.

The migration runs in one transaction, uses `IF EXISTS` and no `CASCADE`, so an
unknown dependent aborts all of it. It writes no membership-policy column and
runs unchanged in the mirrored Silo-switching phase. Its Down refuses: a restore
needs the pre-removal database backup and the matching old binary.

The historical Pastime migrations stay byte-identical and executable. Rollback
tests that walk the whole chain skip the removal file, as they skip the
compatibility removal (`compatibilityBaselineFS`, `opaCompatibilityBaselineFS`).

## Upgrade constraints

- The enrollment rows cascade from the Pastime plugin installation
  (`plugin_installations` to `bloem_managed_grants` to
  `bloem_managed_enrollments`), and Silo's plugin uninstall does not delete
  watch-provider connections. If the installation is uninstalled before the
  migration runs, the migration finds no enrollments and the managed
  connections stay behind as ordinary rows for a plugin that no longer exists.
  The installation must still exist when the migration runs; disabling it is
  enough. Uninstall it only after the migration has committed.
- The migration takes `ACCESS EXCLUSIVE` locks on `users` and the other
  formerly triggered Silo tables and holds them until commit. It sets a 5 s
  `lock_timeout`: behind a longer-running transaction on any of those tables it
  rolls back whole and the next start retries, instead of blocking logins and
  playback writes while it queues. Run it with no other database clients so it
  is not blocked at all.

## Host boundary

- `internal/pluginhost/host.go` and `internal/auth/login_identity_space_db_test.go`
  match upstream Silo again and are no longer seams.
- Lifecycle repeatable-read transactions are not rerun after a serialization
  failure. That retry existed only for the managed-tracking fence.
- The host no longer serves the managed-tracking broker service. A
  `bloem.pastime` 0.1.x plugin requires it, so when its installation is enabled
  every start fails at `BindHostBroker` (`FailedPrecondition`) and the host
  stops the process. Disable that installation and uninstall it after the
  migration; only a plugin built on public Silo capabilities can replace it.

## Evidence

`internal/tenancy/bloem_pastime_removal_switching_test.go` covers a fresh
migration chain with no Pastime objects, a seeded upgrade in the mirrored phase
that keeps Silo data and accepts Silo-shaped writes, atomic failure when an
unknown view depends on a dropped table, and an atomic give-up and later retry
when a long-running transaction holds a lock on a formerly triggered table.
