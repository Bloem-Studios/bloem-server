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

The migration writes no `watch_provider_connections` row and none of their
child rows (sync runs, history exports, list, rating and dropped items,
scrobble sessions). The connections enrollment wrote stay as ordinary,
user-removable watch-sync rows for the plugin installation that owns provider
`plugin:<installation>:pastime`, alongside any Pastime connection a profile
linked itself. While that installation is disabled no provider serves the key,
so Silo's watch sync sends nothing for these connections (a scheduled sync
records a failed run): pending history exports stay pending, and plays
finished meanwhile stay in Silo's watch history with no export row. Once a
version with watched export serves the key again, its scheduled sync
reconciles each connection's completed history and queues the missing plays.

Collections a list binding published keep their items; the
`bloem_list_binding` marker leaves `source_config`, and a synced native
personal collection becomes manual. Favorites, watchlists, ratings, progress
and watch history are not touched.

The migration runs in one transaction, uses `IF EXISTS` and no `CASCADE`, so an
unknown dependent aborts all of it. It writes no membership-policy column and
runs unchanged in the mirrored Silo-switching phase. Its Down refuses: a restore
needs the pre-removal database backup and the matching old binary.

The historical Pastime migrations stay byte-identical and executable. Rollback
tests that walk the whole chain skip the removal file, as they skip the
compatibility removal (`compatibilityBaselineFS`, `opaCompatibilityBaselineFS`).

## Upgrade constraints

- Disable the `bloem.pastime` installation before the release and keep it
  disabled until a version built on public Silo capabilities replaces it in
  place (same plugin ID, same capability ID, so the provider key and its
  connections carry over). Do not uninstall it: Silo's uninstall does not
  delete watch-provider connections, so its rows would be left for a provider
  no installation serves.
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
  stops the process. Keep that installation disabled until an upgrade built on
  public Silo capabilities replaces it in place.

## Evidence

`internal/tenancy/bloem_pastime_removal_switching_test.go` covers a fresh
migration chain with no Pastime objects; a seeded upgrade in the mirrored phase
that leaves every watch-provider connection and its child rows unchanged
(enrolled, self-linked and other-provider), keeps Silo data and accepts
Silo-shaped writes; atomic failure when an unknown view depends on a dropped
table; and an atomic give-up and later retry when a long-running transaction
holds a lock on a formerly triggered table.
