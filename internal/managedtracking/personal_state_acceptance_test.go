package managedtracking

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func TestRealPluginPersonalStateUsesCommittedSourcesAndExactRatings(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	native, err := run.client.WatchSyncProvider("pastime")
	if err != nil {
		t.Fatal(err)
	}
	account, _ := strconv.Atoi(p.AccountID)
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	conn, found, err := watchsync.NewPostgresRepository(s.Pool, s.Cipher).GetConnection(ctx, key, account, p.ProfileID)
	if err != nil || !found {
		t.Fatal("connection missing", err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE watch_provider_connections SET import_favorites_enabled=true,import_watchlist_enabled=true,import_ratings_enabled=true,export_favorites_enabled=true,export_watchlist_enabled=true,export_ratings_enabled=true,sync_favorite_removals_enabled=true,sync_watchlist_removals_enabled=true WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, key, account, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	auth := &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: conn.AccessToken, SecretAttributes: conn.SecretAttributes}}
	apply := func(op pluginv1.WatchSyncOperation, rating int32) {
		t.Helper()
		event := &pluginv1.WatchSyncEvent{EventId: op.String(), Operation: op, Media: movie("fixture-media"), Rating: rating}
		out, err := s.WrapClient(id, native).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: auth, Events: []*pluginv1.WatchSyncEvent{event}})
		if err != nil || out.Fault != nil || len(out.Results) != 1 || out.Results[0].Fault != nil || out.Results[0].Status != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED && out.Results[0].Status != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE {
			t.Fatalf("apply %s: %+v %v", op, out, err)
		}
	}
	read := func(kind pluginv1.WatchSyncRemoteStateKind) *pluginv1.WatchSyncListRemoteStateResponse {
		t.Helper()
		out, err := s.WrapClient(id, native).ListRemoteState(ctx, &pluginv1.WatchSyncListRemoteStateRequest{Context: auth, StateKinds: []pluginv1.WatchSyncRemoteStateKind{kind}, PageSize: 100})
		if err != nil || out.Fault != nil || !out.CompleteSnapshot || out.NextPageToken != "" {
			t.Fatalf("remote read: %+v %v", out, err)
		}
		return out
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err = s.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO user_favorites(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, account, p.ProfileID)
	apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE, 0)
	fav := read(pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE)
	if len(fav.Items) != 1 || fav.Items[0].GetFavorite().GetRemoved() || fav.Items[0].Media.ExternalIds["tmdb"] != "42" {
		t.Fatal("favorite absent", fav)
	}
	exec(`DELETE FROM user_favorites WHERE user_id=$1 AND profile_id=$2`, account, p.ProfileID)
	apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE, 0)
	fav = read(pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE)
	if len(fav.Items) != 1 || !fav.Items[0].GetFavorite().GetRemoved() {
		t.Fatal("favorite removal absent", fav)
	}
	exec(`INSERT INTO user_watchlist(user_id,profile_id,media_item_id) VALUES($1,$2,'fixture-media')`, account, p.ProfileID)
	apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, 0)
	watch := read(pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST)
	if len(watch.Items) != 1 || watch.Items[0].GetWatchlist().GetRemoved() {
		t.Fatal("watchlist absent", watch)
	}
	exec(`DELETE FROM user_watchlist WHERE user_id=$1 AND profile_id=$2`, account, p.ProfileID)
	apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST, 0)
	for stars := int32(1); stars <= 5; stars++ {
		exec(`INSERT INTO user_ratings(user_id,profile_id,media_item_id,rating) VALUES($1,$2,'fixture-media',$3) ON CONFLICT(user_id,profile_id,media_item_id) DO UPDATE SET rating=EXCLUDED.rating,rated_at=clock_timestamp()`, account, p.ProfileID, stars)
		apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING, stars*2)
		ratings := read(pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING)
		if len(ratings.Items) != 1 || ratings.Items[0].GetRating().GetRating() != stars*2 {
			t.Fatal("rating lost precision", stars, ratings)
		}
	}
	exec(`DELETE FROM user_ratings WHERE user_id=$1 AND profile_id=$2`, account, p.ProfileID)
	apply(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING, 0)
	if ratings := read(pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING); len(ratings.Items) != 1 || !ratings.Items[0].GetRating().GetRemoved() {
		t.Fatal("rating clear absent", ratings)
	}
}
