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

## Host boundary

- `internal/pluginhost/host.go` and `internal/auth/login_identity_space_db_test.go`
  match upstream Silo again and are no longer seams.
- Lifecycle repeatable-read transactions are not rerun after a serialization
  failure. That retry existed only for the managed-tracking fence.
- A Pastime plugin installation that stays enabled has no private broker to
  call, and Silo's generic watch sync treats its capability as an ordinary
  provider.

## Evidence

`internal/tenancy/bloem_pastime_removal_switching_test.go` covers a fresh
migration chain with no Pastime objects, a seeded upgrade in the mirrored phase
that keeps Silo data and accepts Silo-shaped writes, and atomic failure when an
unknown view depends on a dropped table.
