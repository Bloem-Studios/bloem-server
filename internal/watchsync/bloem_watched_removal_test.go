package watchsync

// Bloem-owned tests for watched removals (bloem_watched_removal.go).

import (
	"context"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/historyimport"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// removedWatched builds the watched state a v0.28.0 plugin sends: the host's
// generated type has no removed field, so it arrives as unknown field 3.
func removedWatched(t *testing.T, removed uint64, at time.Time) *pluginv1.WatchSyncRemoteWatchedState {
	t.Helper()
	data, err := proto.Marshal(&pluginv1.WatchSyncRemoteWatchedState{LastWatchedAt: timestamppb.New(at)})
	if err != nil {
		t.Fatal(err)
	}
	data = protowire.AppendTag(data, watchedRemovedField, protowire.VarintType)
	data = protowire.AppendVarint(data, removed)
	var out pluginv1.WatchSyncRemoteWatchedState
	if err := proto.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestWatchedRemovedReadsTheFieldTheHostDoesNotKnowYet(t *testing.T) {
	at := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	if !watchedRemoved(removedWatched(t, 1, at)) {
		t.Fatal("removed=true was not read")
	}
	if watchedRemoved(removedWatched(t, 0, at)) || watchedRemoved(&pluginv1.WatchSyncRemoteWatchedState{PlayCount: 2}) || watchedRemoved(nil) {
		t.Fatal("a state that is not a removal was read as one")
	}
}

func TestRemoteWatchRemovalDecodesASeriesAndRejectsPlays(t *testing.T) {
	at := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	series := &pluginv1.WatchSyncRemoteState{
		ProviderItemKey: "catalog:7",
		Media: &pluginv1.WatchSyncMedia{
			MediaType:   pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES,
			Title:       "Oak Island",
			ExternalIds: map[string]string{"tvdb": "276515"},
		},
		Watched: removedWatched(t, 1, at),
	}
	row, removal, err := remoteWatchRemovalFromProto("pastime", series)
	if err != nil || !removal || !row.Removed || row.Kind != historyimport.KindSeries || row.TVDBID != "276515" || !row.LastWatchedAt.Equal(at) {
		t.Fatalf("row = %+v removal=%v err=%v", row, removal, err)
	}

	withPlays := proto.Clone(series).(*pluginv1.WatchSyncRemoteState)
	withPlays.Watched.PlayCount = 3
	if _, removal, err := remoteWatchRemovalFromProto("pastime", withPlays); !removal || err == nil {
		t.Fatalf("a removal with plays must be rejected: removal=%v err=%v", removal, err)
	}

	plain := &pluginv1.WatchSyncRemoteState{ProviderItemKey: "catalog:8", Watched: &pluginv1.WatchSyncRemoteWatchedState{PlayCount: 1}}
	if _, removal, err := remoteWatchRemovalFromProto("pastime", plain); removal || err != nil {
		t.Fatalf("an ordinary watched state is not a removal: removal=%v err=%v", removal, err)
	}
}

const (
	lauraProfile = "profile-laura"
	jimProfile   = "profile-jim"
)

func seedWatch(t *testing.T, store *userdb.SQLiteUserStore, profileID, mediaItemID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.AddHistory(ctx, userstore.WatchHistoryEntry{
		ID: profileID + "-" + mediaItemID + "-" + at.Format("150405"), ProfileID: profileID, MediaItemID: mediaItemID,
		WatchedAt: at.Format(time.RFC3339), DurationSeconds: 2700, Completed: true, Source: userstore.WatchHistorySourcePlayback,
	}); err != nil {
		t.Fatalf("AddHistory: %v", err)
	}
	if err := store.SetProgressAt(ctx, profileID, mediaItemID, 2700, 2700, true, at); err != nil {
		t.Fatalf("SetProgressAt: %v", err)
	}
}

func watched(t *testing.T, store *userdb.SQLiteUserStore, profileID string, ids ...string) map[string]bool {
	t.Helper()
	latest, err := latestLocalWatchState(context.Background(), store, profileID, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(latest))
	for id := range latest {
		out[id] = true
	}
	return out
}

func removalService(t *testing.T, store *userdb.SQLiteUserStore, matcher mediaMatcher) *Service {
	t.Helper()
	return NewService(newServiceFakeRepo(), NewRegistry()).
		WithMatcher(matcher).
		WithWatchState(&recordingWatchState{}).
		WithUserStoreProvider(staticStoreProvider{store: store})
}

func importRemoval(t *testing.T, service *Service, profileID string, kind string, removedAt time.Time) ImportWatchedResult {
	t.Helper()
	provider := watchedImporterStub{
		historyStubBase: historyStubBase{key: "pastime", source: userstore.WatchHistorySourcePlayback},
		rows:            []RemoteWatch{{Provider: "pastime", Kind: kind, Title: "Oak Island", LastWatchedAt: &removedAt, Removed: true}},
	}
	result, err := service.ImportWatched(context.Background(), Connection{
		ID: "conn-" + profileID, Provider: "pastime", UserID: 1, ProfileID: profileID,
	}, ServerConfig{}, provider)
	if err != nil {
		t.Fatalf("ImportWatched: %v", err)
	}
	return result
}

// Removing a show in one profile's Pastime clears that profile only: the
// account's other profiles keep their watches of the same episodes.
func TestWatchedRemovalClearsOnlyTheConnectionsProfile(t *testing.T) {
	store := userdb.NewSQLiteUserStore(openUserDB(t))
	watchedAt := time.Date(2026, 4, 8, 19, 34, 0, 0, time.UTC)
	for _, profile := range []string{lauraProfile, jimProfile} {
		seedWatch(t, store, profile, "episode-1", watchedAt)
		seedWatch(t, store, profile, "episode-2", watchedAt)
	}
	service := removalService(t, store, watchedLeafMatcherStub{matches: []historyimport.Match{
		{MediaItemID: "episode-1", Kind: historyimport.KindEpisode},
		{MediaItemID: "episode-2", Kind: historyimport.KindEpisode},
	}})

	result := importRemoval(t, service, lauraProfile, historyimport.KindSeries, time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC))

	if result.Removed != 2 || result.Imported != 0 {
		t.Fatalf("result = %+v, want both episodes removed", result)
	}
	if got := watched(t, store, lauraProfile, "episode-1", "episode-2"); len(got) != 0 {
		t.Fatalf("laura still has %v", got)
	}
	if got := watched(t, store, jimProfile, "episode-1", "episode-2"); len(got) != 2 {
		t.Fatalf("jim's watches were touched: %v", got)
	}
	history, err := store.ListHistory(context.Background(), lauraProfile, 10, 0)
	if err != nil || len(history) != 0 {
		t.Fatalf("laura's history = %+v, %v", history, err)
	}
}

// A series removal reaches only the episodes the profile may open, as Silo's
// whole-series marker does: an episode in a library the profile cannot see
// keeps its watches.
func TestWatchedRemovalReachesOnlyEpisodesTheProfileMayOpen(t *testing.T) {
	store := userdb.NewSQLiteUserStore(openUserDB(t))
	watchedAt := time.Date(2026, 4, 8, 19, 34, 0, 0, time.UTC)
	seedWatch(t, store, lauraProfile, "episode-1", watchedAt)
	seedWatch(t, store, lauraProfile, "episode-2", watchedAt)
	service := removalService(t, store, watchedLeafMatcherStub{matches: []historyimport.Match{
		{MediaItemID: "episode-1", Kind: historyimport.KindEpisode},
		{MediaItemID: "episode-2", Kind: historyimport.KindEpisode},
	}}).WithProfileAccess(func(context.Context, int, string) (ProfileAccess, error) {
		return ProfileAccess{Accessible: func(_ context.Context, ids []string) (map[string]bool, error) {
			open := make(map[string]bool, len(ids))
			for _, id := range ids {
				open[id] = id != "episode-2"
			}
			return open, nil
		}}, nil
	})

	result := importRemoval(t, service, lauraProfile, historyimport.KindSeries, time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC))

	if result.Removed != 1 {
		t.Fatalf("result = %+v, want only the episode the profile may open removed", result)
	}
	if got := watched(t, store, lauraProfile, "episode-1", "episode-2"); got["episode-1"] || !got["episode-2"] {
		t.Fatalf("laura's watches = %v, want episode-2 only", got)
	}
}

// A watch made in Bloem after the Pastime removal is newer and stays.
func TestWatchedRemovalKeepsAWatchMadeAfterIt(t *testing.T) {
	store := userdb.NewSQLiteUserStore(openUserDB(t))
	removedAt := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	seedWatch(t, store, lauraProfile, testMovieMediaID, removedAt.Add(time.Hour))
	service := removalService(t, store, matchedMatcherStub{mediaItemID: testMovieMediaID})

	result := importRemoval(t, service, lauraProfile, historyimport.KindMovie, removedAt)

	if result.Removed != 0 {
		t.Fatalf("result = %+v, want the newer watch kept", result)
	}
	if got := watched(t, store, lauraProfile, testMovieMediaID); !got[testMovieMediaID] {
		t.Fatal("the newer watch was removed")
	}
}

// Pastime repeats a removal in every traversal; after the first, nothing is left to remove.
func TestWatchedRemovalRepeatsWithoutWriting(t *testing.T) {
	store := userdb.NewSQLiteUserStore(openUserDB(t))
	removedAt := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	seedWatch(t, store, lauraProfile, testMovieMediaID, removedAt.Add(-24*time.Hour))
	service := removalService(t, store, matchedMatcherStub{mediaItemID: testMovieMediaID})

	if first := importRemoval(t, service, lauraProfile, historyimport.KindMovie, removedAt); first.Removed != 1 {
		t.Fatalf("first = %+v", first)
	}
	if second := importRemoval(t, service, lauraProfile, historyimport.KindMovie, removedAt); second.Removed != 0 {
		t.Fatalf("second = %+v, want nothing left to remove", second)
	}
	// Watching again after the removal survives the next repetition.
	seedWatch(t, store, lauraProfile, testMovieMediaID, removedAt.Add(2*time.Hour))
	if third := importRemoval(t, service, lauraProfile, historyimport.KindMovie, removedAt); third.Removed != 0 {
		t.Fatalf("third = %+v", third)
	}
	if got := watched(t, store, lauraProfile, testMovieMediaID); !got[testMovieMediaID] {
		t.Fatal("the rewatch was removed by a repeated removal")
	}
}

// pendingFakeRepo keeps pending watches in memory, as PostgresRepository keeps
// them in watch_provider_pending_watches.
type pendingFakeRepo struct {
	*serviceFakeRepo
	kept      []PendingWatch
	inLibrary *bool
}

// The fake must stay a pendingWatchStore, or ImportWatched silently skips it.
var _ pendingWatchStore = (*pendingFakeRepo)(nil)

func (r *pendingFakeRepo) ListPendingWatches(context.Context, string, string) ([]PendingWatch, error) {
	return append([]PendingWatch(nil), r.kept...), nil
}

func (r *pendingFakeRepo) SavePendingWatches(_ context.Context, _, _ string, watches []PendingWatch) error {
	for _, watch := range watches {
		if keepable(watch.Watch) {
			r.kept = append(r.kept, watch)
		}
	}
	return nil
}

func (r *pendingFakeRepo) DeletePendingWatches(_ context.Context, _, _ string, watches []RemoteWatch) error {
	kept := r.kept[:0]
	for _, k := range r.kept {
		drop := false
		for _, w := range watches {
			if keepable(w) && pendingKeyOf(w) == pendingKeyOf(k.Watch) && !k.Watch.LastWatchedAt.After(*w.LastWatchedAt) {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, k)
		}
	}
	r.kept = kept
	return nil
}

func (r *pendingFakeRepo) SeriesLeafHashes(_ context.Context, seriesIDs []string, _ []time.Time, _ []int) ([]string, error) {
	return make([]string, len(seriesIDs)), nil
}

func (r *pendingFakeRepo) MatchPendingWatches(_ context.Context, records []historyimport.Record) ([]*historyimport.Match, error) {
	out := make([]*historyimport.Match, len(records))
	if *r.inLibrary {
		for i := range out {
			out[i] = &historyimport.Match{MediaItemID: testMovieMediaID}
		}
	}
	return out, nil
}

type libraryMatcherStub struct{ inLibrary *bool }

func (m libraryMatcherStub) Match(context.Context, historyimport.Record) (*historyimport.Match, string, error) {
	if !*m.inLibrary {
		return nil, "not in the library", nil
	}
	return &historyimport.Match{MediaItemID: testMovieMediaID}, "", nil
}

// A watch Silo keeps because its title is not in the library yet must not
// import after Pastime removed it: the removal drops the kept watch.
func TestWatchedRemovalDropsAPendingWatch(t *testing.T) {
	store := userdb.NewSQLiteUserStore(openUserDB(t))
	inLibrary := false
	repo := &pendingFakeRepo{serviceFakeRepo: newServiceFakeRepo(), inLibrary: &inLibrary}
	watchState := &recordingWatchState{}
	service := NewService(repo, NewRegistry()).
		WithMatcher(libraryMatcherStub{inLibrary: &inLibrary}).
		WithWatchState(watchState).
		WithUserStoreProvider(staticStoreProvider{store: store})
	conn := Connection{ID: "conn-laura", Provider: "pastime", ProviderAccountID: "laura", UserID: 1, ProfileID: lauraProfile}
	sync := func(rows ...RemoteWatch) ImportWatchedResult {
		t.Helper()
		result, err := service.ImportWatched(context.Background(), conn, ServerConfig{}, watchedImporterStub{
			historyStubBase: historyStubBase{key: "pastime", source: userstore.WatchHistorySourcePlayback},
			rows:            rows,
		})
		if err != nil {
			t.Fatalf("ImportWatched: %v", err)
		}
		return result
	}
	watchedAt := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	removedAt := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	row := RemoteWatch{Provider: "pastime", ProviderItemKey: "tmdb:42", Kind: historyimport.KindMovie, Title: "The Odyssey", TMDBID: "42"}
	watch, removal := row, row
	watch.PlayCount, watch.LastWatchedAt = 1, &watchedAt
	removal.LastWatchedAt, removal.Removed = &removedAt, true

	if got := sync(watch); got.Imported != 0 || len(repo.kept) != 1 {
		t.Fatalf("first sync = %+v kept %d, want the watch kept", got, len(repo.kept))
	}
	inLibrary = true
	if got := sync(removal); got.Imported != 0 {
		t.Fatalf("sync with the removal = %+v, want the kept watch not imported", got)
	}
	if got := sync(); got.Imported != 0 || len(repo.kept) != 0 {
		t.Fatalf("next sync = %+v kept %d, want the removed watch gone", got, len(repo.kept))
	}
	if len(watchState.targetIDs) != 0 {
		t.Fatalf("removed watch was imported for %v", watchState.targetIDs)
	}
}
