# How removing a title in a tracker clears its watch history

> **Provenance and status.** Bloem page, rewritten on 2026-11-04, updated on 2026-11-09
> and checked against `main` at `dba9b95e0`, which includes Silo through `67d0936f3`. Watched removals are a
> Bloem addition to Silo's watch-sync contract, proposed to Silo and carried in Bloem
> until Silo has them. The technical rules are in the [For developers](#for-developers)
> section.

A **tracker** such as Pastime keeps a person's viewing record and stays in step with
Bloem through a **watch-sync connection**. This page walks through what happens when
someone removes a movie or a show in the tracker: how the removal reaches Bloem, why it
touches only that one profile, and what Bloem keeps.

## One profile at a time

Every profile has its own connection to the tracker. Robin's profile and Sam's profile
on the same account each connect separately, with separate credentials, so whatever
travels on Robin's connection can only ever change Robin's profile.

![Four steps from left to right: Robin removes a show in the tracker, the tracker keeps a note of the removal for Robin's profile, the plugin passes the note on Robin's connection, and Bloem clears the show from Robin's watch history. Below: Sam's history, a later watch, and a repeated note all stay unchanged](images/watch-sync-removals/flow.svg)

1. A person deletes a movie or a show from their library in the tracker.
2. The tracker keeps a **removal note** for that profile: this title, removed at this
   time.
3. On the profile's next sync, the tracker's plugin passes the note to Bloem on that
   profile's connection.
4. Bloem removes the title's **watch history** for that profile: every watch of the
   movie, or of each episode of the show, up to the time of the removal. The title's
   watched marks and resume points go with it.
5. Bloem also forgets any earlier watch of the title it was **holding back**. When the
   tracker reports a watch of a title that no library has yet, Bloem keeps it and
   imports it once the title arrives. A removal drops that kept watch, so the title
   does not come back as watched later.

## What Bloem keeps

- **Other profiles.** Sam's watches of the same show stay, even on the same account.
- **Anything newer.** If Robin watched or started an episode again in Bloem after the
  removal, that episode keeps everything. The newer change wins.
- **Everything else on the profile.** Favorites, the watchlist, ratings and other
  titles are not touched.

The tracker repeats its note on every sync, so a profile that was offline still gets it
later. Once Bloem has applied a note, there is nothing left to remove, so repeating it
changes nothing.

## A made-up example

This example is made up. Robin and Sam share an account. Both watched all of a cooking
show. Robin deletes the show in the tracker on Monday evening. At the next sync, Bloem
clears the show from Robin's watch history and continue-watching row. Sam's history
still lists every episode. On Tuesday, Robin watches the final again in Bloem; that
watch stays, and Monday's note, repeated at every sync, does not remove it.

## Who can change what

| Who | What they can do |
| --- | --- |
| The person, in the tracker | Remove a title from their own profile's library, which removes it from that profile's Bloem watch history |
| The person, in Bloem | Remove watch history directly, as before |
| An administrator | Connect or disconnect a profile's tracker; a disconnected profile receives no notes |

## Words used in the code

| Plain words | In the code |
| --- | --- |
| Tracker | A watch-sync provider plugin, such as `bloem.pastime` |
| Watch-sync connection | A `watch_provider_connections` row, one per profile |
| Removal note | `WatchSyncRemoteWatchedState.removed = true` in a `WATCHED` traversal |
| Time of the removal | `WatchSyncRemoteWatchedState.last_watched_at` on a removal |
| Removing watch history | `UserStore.RemoveHistoryItems` with the removal time as `removedAt` |
| A watch held back | A `watch_provider_pending_watches` row (Silo's `pendingWatchStore`) |
| Applying the notes | `Service.applyWatchedRemovals` in `internal/watchsync/bloem_watched_removal.go` |

## For developers

The contract field is `bool removed = 3` on `WatchSyncRemoteWatchedState`, added in
Bloem's plugin SDK v0.28.0 (`proto/silo/plugin/v1/watch_sync_provider.proto`). Bloem
Server builds against Silo's SDK, whose generated type does not have the field yet, so
`watchedRemoved` reads field 3 from the message's unknown fields with `protowire`. When
Silo's SDK carries the field, read the generated getter and drop the temporary seams.

Rules:

- A removal carries `media` (MOVIE, EPISODE or SERIES), `play_count` 0 and the removal
  time in `last_watched_at`. Anything else is reported as a sync warning and skipped.
- SERIES is allowed only on a removal; it expands to the profile's local episodes
  through the same leaf matcher as aggregate watched markers.
- Removals apply before the batch's ordinary watches, only to `conn.ProfileID` of the
  connection that carried them.
- Per matched item, Bloem reads the newest completed watch (`ListCompletedHistoryItems`)
  and progress (`ListProgressByMediaItems`). An item with neither is skipped; an item
  with either after the removal time (compared in whole seconds) is skipped; the rest are
  removed with `RemoveHistoryItems(profileID, ids, removedAt)`, grouped by removal time.
  The store's hidden-history watermark then keeps older imports of the item hidden.
- `ImportWatchedResult.Removed` counts removed items; a run with removals reports a
  recommendation-signal change like an import does.
- Silo keeps a watch that matched no library item in `watch_provider_pending_watches`
  and retries it on every sync (`retryPendingWatches`, since Silo `67d0936f3`).
  `ImportWatched` marks a removed row's kind and key as returned this sync (`fresh`),
  so the kept watch is not retried in the same sync, and `applyWatchedRemovals` calls
  `DeletePendingWatches` with the removals, which drops kept watches of the same kind
  and key no newer than the removal time. A removal therefore also clears a watch the
  profile has not imported yet. Without this, the next sync imported the removed watch
  once the title reached a library.

Temporary seams, listed in `contracts/seams.txt`: `internal/watchsync/types.go`
(`RemoteWatch.Removed`), `internal/watchsync/plugin_provider_state.go` (decode hook) and
`internal/watchsync/service.go` (apply hook, result count, signal change, and marking a
removed row as returned this sync).

Tests: `internal/watchsync/bloem_watched_removal_test.go`
(`TestWatchedRemovedReadsTheFieldTheHostDoesNotKnowYet`,
`TestRemoteWatchRemovalDecodesASeriesAndRejectsPlays`,
`TestWatchedRemovalClearsOnlyTheConnectionsProfile`,
`TestWatchedRemovalKeepsAWatchMadeAfterIt`, `TestWatchedRemovalRepeatsWithoutWriting`,
`TestWatchedRemovalDropsAPendingWatch`).

Related: [How Pastime tracking was removed](bloem-pastime-removal.md), [How Bloem's SDKs
and generated contracts fit together](bloem-sdk-compatibility.md).
