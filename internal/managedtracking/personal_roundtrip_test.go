package managedtracking

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/historyimport"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func TestRealPluginPersonalStateRoundTripThroughSilo(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id,metadata) VALUES($1,'watch_sync_provider.v1','pastime','{"metadata":{"managed_personal_state":true}}')`, id); err != nil {
		t.Fatal(err)
	}
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	native, err := run.client.WatchSyncProvider("pastime")
	if err != nil {
		t.Fatal(err)
	}
	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	account, _ := strconv.Atoi(p.AccountID)
	provider, err := watchsync.NewPluginProvider(watchsync.PluginProviderOptions{InstallationID: id, ProviderKey: key, CapabilityID: "pastime", DisplayName: "Pastime", Repository: repo,
		Descriptor: &pluginv1.WatchSyncProviderDescriptor{AuthMethods: []pluginv1.WatchSyncAuthMethod{pluginv1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY}, SupportedMediaTypes: []pluginv1.WatchSyncMediaType{pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES}, MaxBatchSize: 100, ImportFavorites: true, ExportFavorites: true, RemoveFavorites: true, ImportWatchlist: true, ExportWatchlist: true, RemoveWatchlist: true, ImportRatings: true, ExportRatings: true},
		ResolveClient: func(context.Context, int, string) (watchsync.WatchSyncPluginClient, error) {
			return s.WrapClient(id, native), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	registry := watchsync.NewRegistry()
	if err = registry.Register(s.WrapProvider(id, provider, func(context.Context) (watchsync.WatchSyncPluginClient, error) { return s.WrapClient(id, native), nil })); err != nil {
		t.Fatal(err)
	}
	stores := pgstore.NewPostgresProvider(s.Pool)
	svc := watchsync.NewService(repo, registry).WithUserStoreProvider(stores).WithRatingStore(catalog.NewRatingsRepo(s.Pool), nil).WithMatcher(historyimport.NewMatcher(historyimport.NewRepository(s.Pool, s.Cipher)))
	store, err := stores.ForUser(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	media := uuid.NewString()
	tmdb := strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err = s.Pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,genres,tmdb_id) VALUES($1,'movie','Synthetic personal film','{}',$2)`, media, tmdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.Pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, media) })
	sync := func() {
		t.Helper()
		c, found, err := repo.GetConnection(ctx, key, account, p.ProfileID)
		if err != nil || !found {
			t.Fatal("connection", err)
		}
		if err = svc.SyncConnection(ctx, c, "scheduled"); err != nil {
			t.Fatal("native reconciliation", err)
		}
	}
	change := func(kind string, present bool, rating *int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"Profile": p.ProfileID, "Kind": kind, "TMDB": tmdb, "Present": present, "Rating": rating})
		req, _ := http.NewRequestWithContext(ctx, "POST", run.url+"/fixture/personal", bytes.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("native Pastime change", res.StatusCode)
		}
	}
	check := func(kind string, want int) {
		t.Helper()
		var count int
		query := `SELECT count(*) FROM user_favorites WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`
		if kind == "watchlist" {
			query = `SELECT count(*) FROM user_watchlist WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`
		}
		if kind == "rating" {
			query = `SELECT COALESCE((SELECT rating FROM user_ratings WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3),0)`
		}
		if err = s.Pool.QueryRow(ctx, query, account, p.ProfileID, media).Scan(&count); err != nil || count != want {
			t.Fatalf("%s value=%d want=%d err=%v", kind, count, want, err)
		}
	}
	if _, err = store.AddFavoriteAt(ctx, p.ProfileID, media, time.Now()); err != nil {
		t.Fatal(err)
	}
	sync()
	sync()
	check("favorite", 1)
	change("favorite", false, nil)
	sync()
	check("favorite", 0)
	change("favorite", true, nil)
	sync()
	check("favorite", 1)
	if err = store.RemoveFavorite(ctx, p.ProfileID, media); err != nil {
		t.Fatal(err)
	}
	if err = svc.HandleLocalListEvent(ctx, watchsync.LocalListEvent{UserID: account, ProfileID: p.ProfileID, List: watchsync.ListKindFavorites, Change: watchsync.ListChangeRemoved, Items: []watchsync.LocalFavorite{{MediaItemID: media, Kind: "movie", TMDBID: tmdb}}}); err != nil {
		t.Fatal(err)
	}

	sync()
	check("favorite", 0)
	change("watchlist", true, nil)
	sync()
	check("watchlist", 1)
	change("watchlist", false, nil)
	sync()
	check("watchlist", 0)
	if _, err = store.AddToWatchlistAt(ctx, p.ProfileID, media, time.Now()); err != nil {
		t.Fatal(err)
	}
	sync()
	check("watchlist", 1)
	for _, score := range []int{2, 8, 10} {
		change("rating", true, &score)
		sync()
		check("rating", score/2)
	}
	change("rating", false, nil)
	sync()
	check("rating", 0)
	if err = catalog.NewRatingsRepo(s.Pool).Set(ctx, account, p.ProfileID, media, 3); err != nil {
		t.Fatal(err)
	}
	sync()
	check("rating", 3)
	if err = catalog.NewRatingsRepo(s.Pool).Delete(ctx, account, p.ProfileID, media); err != nil {
		t.Fatal(err)
	}
	sync()
	check("rating", 0)
	if err = run.host.Stop(id); err != nil {
		t.Fatal(err)
	}
	run.finish()
}
