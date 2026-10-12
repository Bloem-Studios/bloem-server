package managedtracking

import (
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestManagedPersonalStateRequiresCommittedStateAndOwnToggle(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	if _, err := f.s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET scrobble_enabled=false,export_favorites_enabled=true WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, f.key, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	event := &pluginv1.WatchSyncEvent{EventId: "favorite", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE, Media: movie("fixture-media")}
	record := func() *pluginv1.WatchSyncEvent {
		t.Helper()
		out, err := f.s.RecordEvents(ctx, f.id, f.authContext(), []*pluginv1.WatchSyncEvent{event})
		if err != nil || len(out) != 1 {
			t.Fatalf("record: %v %v", out, err)
		}
		return out[0]
	}
	if record() != nil {
		t.Fatal("uncommitted favorite accepted")
	}
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	first := record()
	if first == nil {
		t.Fatal("committed favorite rejected with its export toggle enabled")
	}
	authority, err := f.s.ResolveEvent(ctx, f.id, f.info.InstanceID, f.p.AccountID, f.p.ProfileID, first.EventId, digestEvent(first))
	if err != nil || authority.Revision == 0 || authority.ConsumptionID != "" {
		t.Fatalf("state authority: %+v %v", authority, err)
	}
	if replay := record(); replay == nil || replay.EventId != first.EventId {
		t.Fatal("exact replay changed")
	}
	if _, err := f.s.Pool.Exec(ctx, `DELETE FROM user_favorites WHERE user_id=$1 AND profile_id=$2`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if record() != nil {
		t.Fatal("superseded favorite addition accepted")
	}
	event.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE
	if record() != nil {
		t.Fatal("disabled favorite removals accepted")
	}
	if _, err := f.s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET sync_favorite_removals_enabled=true WHERE provider=$1`, f.key); err != nil {
		t.Fatal(err)
	}
	removed := record()
	if removed == nil {
		t.Fatal("committed removal rejected")
	}
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	event.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE
	again := record()
	if again == nil || again.EventId == first.EventId {
		t.Fatal("later re-add reused earlier receipt")
	}
	later, err := f.s.ResolveEvent(ctx, f.id, f.info.InstanceID, f.p.AccountID, f.p.ProfileID, again.EventId, digestEvent(again))
	if err != nil || later.Revision <= authority.Revision {
		t.Fatal("re-add lost ordering", err)
	}
	if _, err := f.s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET export_favorites_enabled=false,scrobble_enabled=true WHERE provider=$1`, f.key); err != nil {
		t.Fatal(err)
	}
	if record() != nil {
		t.Fatal("scrobbling allowed a disabled favorite export")
	}
}

func TestManagedPersonalJournalPreservesLegacyWritesAndProfileDeletion(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	// Legacy upstream rows can refer to a profile that no longer exists. The
	// optional bridge must not add a new constraint to that upstream write.
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,'legacy-absent','fixture-media')`, f.account); err != nil {
		t.Fatal("upstream legacy write failed", err)
	}
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool.Exec(ctx, `DELETE FROM user_profiles WHERE user_id=$1 AND id=$2`, f.account, f.p.ProfileID); err != nil {
		t.Fatal("profile cleanup blocked", err)
	}
}

func TestManagedPersonalStateRatingsWatchlistRollbackAndIsolation(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	if _, err := f.s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET scrobble_enabled=false,export_watchlist_enabled=true,sync_watchlist_removals_enabled=true,export_ratings_enabled=true WHERE provider=$1`, f.key); err != nil {
		t.Fatal(err)
	}
	record := func(event *pluginv1.WatchSyncEvent) *pluginv1.WatchSyncEvent {
		t.Helper()
		out, err := f.s.RecordEvents(ctx, f.id, f.authContext(), []*pluginv1.WatchSyncEvent{event})
		if err != nil || len(out) != 1 {
			t.Fatal(out, err)
		}
		return out[0]
	}
	rating := &pluginv1.WatchSyncEvent{EventId: "rating", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING, Media: movie("fixture-media")}
	for stars := int32(1); stars <= 5; stars++ {
		if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_ratings(user_id,profile_id,media_item_id,rating) VALUES($1,$2,'fixture-media',$3) ON CONFLICT(user_id,profile_id,media_item_id) DO UPDATE SET rating=EXCLUDED.rating,rated_at=clock_timestamp()`, f.account, f.p.ProfileID, stars); err != nil {
			t.Fatal(err)
		}
		rating.Rating = stars * 2
		if record(rating) == nil {
			t.Fatalf("valid %d-star rating rejected", stars)
		}
		rating.Rating = stars*2 - 1
		if record(rating) != nil {
			t.Fatal("inexact or stale rating accepted")
		}
	}
	rating.Rating = 0
	rating.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING
	if record(rating) != nil {
		t.Fatal("uncommitted rating removal accepted")
	}
	if _, err := f.s.Pool.Exec(ctx, `DELETE FROM user_ratings WHERE user_id=$1 AND profile_id=$2`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if record(rating) == nil {
		t.Fatal("rating removal rejected")
	}
	watch := &pluginv1.WatchSyncEvent{EventId: "watchlist", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, Media: movie("fixture-media")}
	tx, err := f.s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_watchlist(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if record(watch) != nil {
		t.Fatal("rolled-back watchlist state accepted")
	}
	if _, err = f.s.Pool.Exec(ctx, `INSERT INTO user_watchlist(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if record(watch) == nil {
		t.Fatal("watchlist state rejected")
	}
	if _, err = f.s.Pool.Exec(ctx, `UPDATE user_watchlist SET media_item_id='moved-media' WHERE user_id=$1 AND profile_id=$2 AND media_item_id='fixture-media'`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	watch.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST
	if record(watch) == nil {
		t.Fatal("key move lost old membership removal")
	}
	watch.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST
	watch.Media = movie("moved-media")
	if record(watch) == nil {
		t.Fatal("key move lost new membership")
	}
	watch.Media = movie("foreign-media")
	if record(watch) != nil {
		t.Fatal("other item's membership authorized")
	}
	watch.Media = movie("fixture-media")
	watch.Operation = pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST
	if _, err = f.s.Pool.Exec(ctx, `DELETE FROM user_watchlist WHERE user_id=$1 AND profile_id=$2`, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if record(watch) == nil {
		t.Fatal("watchlist removal rejected")
	}
}
