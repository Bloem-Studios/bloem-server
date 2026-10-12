package managedtracking

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

type listImportReadClient struct {
	watchsync.WatchSyncPluginClient
	read func(*pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error)
}

func (c listImportReadClient) ListRemoteState(_ context.Context, r *pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
	return c.read(r)
}

func TestPersonalImportPreservesNewerNativeChangesAndQueuesCorrection(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(strconv.FormatBool(present), func(t *testing.T) {
			s, id, p := enrollmentFixture(t)
			ctx := t.Context()
			info, err := s.GetInfo(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Pool.Exec(ctx, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id,metadata) VALUES($1,'watch_sync_provider.v1','pastime','{"metadata":{"managed_personal_state":true}}')`, id)
			if err != nil {
				t.Fatal(err)
			}
			e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_synthetic_conflict", Active: true, Generation: info.Generation}
			e.ID = EnrollmentID(info.InstanceID, e)
			if err = s.EnsureConnection(ctx, id, e); err != nil {
				t.Fatal(err)
			}
			account, _ := strconv.Atoi(p.AccountID)
			key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
			repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
			conn, found, err := repo.GetConnection(ctx, key, account, p.ProfileID)
			if err != nil || !found {
				t.Fatal(err)
			}
			media, tmdb := uuid.NewString(), strconv.FormatInt(time.Now().UnixNano(), 10)
			if _, err = s.Pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,genres,tmdb_id) VALUES($1,'movie','Synthetic conflict','{}',$2)`, media, tmdb); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = s.Pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, media) })
			store, _ := pgstore.NewPostgresProvider(s.Pool).ForUser(ctx, account)
			if _, err = store.AddFavoriteAt(ctx, p.ProfileID, media, time.Now()); err != nil {
				t.Fatal(err)
			}
			if !present {
				if err = store.RemoveFavorite(ctx, p.ProfileID, media); err != nil {
					t.Fatal(err)
				}
			}
			item := &pluginv1.WatchSyncRemoteState{ProviderItemKey: "catalog:42", Media: &pluginv1.WatchSyncMedia{MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": tmdb}}, Favorite: &pluginv1.WatchSyncRemoteListState{Removed: present, ListedAt: timestamppb.New(time.Now().Add(-time.Minute))}}
			response := &pluginv1.WatchSyncListRemoteStateResponse{Items: []*pluginv1.WatchSyncRemoteState{item}, CompleteSnapshot: true, NextCursor: "full-v1"}
			client := listImportReadClient{read: func(*pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
				return response, nil
			}}
			provider := s.WrapProvider(id, nil, func(context.Context) (watchsync.WatchSyncPluginClient, error) { return client, nil }).(*personalProvider)
			if _, err = provider.FetchFavoritesBatch(ctx, watchsync.ServerConfig{}, conn); err != nil {
				t.Fatal(err)
			}
			var native bool
			if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_favorites WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3)`, account, p.ProfileID, media).Scan(&native); err != nil || native != present {
				t.Fatal("stale remote replaced native state", native, err)
			}
			if present {
				rows, err := repo.ListPendingListItemExports(ctx, conn.ID, watchsync.ListKindFavorites, 100)
				if err != nil || len(rows) != 1 {
					t.Fatal("correction export not retained", rows, err)
				}
			} else {
				rows, err := repo.ListPendingListItemRemovals(ctx, conn.ID, watchsync.ListKindFavorites, 100)
				if err != nil || len(rows) != 1 {
					t.Fatal("removal retry not retained", rows, err)
				}
			}
			// A failed continuation must not commit the first page's rows or cursor.
			calls := 0
			client.read = func(*pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
				calls++
				if calls == 1 {
					return &pluginv1.WatchSyncListRemoteStateResponse{Items: []*pluginv1.WatchSyncRemoteState{item}, CompleteSnapshot: true, NextPageToken: "synthetic-next"}, nil
				}
				return nil, errors.New("synthetic interruption")
			}
			if _, err = provider.FetchFavoritesBatch(ctx, watchsync.ServerConfig{}, conn); err == nil {
				t.Fatal("interrupted traversal accepted")
			}
			// A source revocation during I/O is rejected at the transaction boundary.
			client.read = func(*pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
				if err = s.Revoke(ctx, id); err != nil {
					t.Fatal(err)
				}
				return response, nil
			}
			if _, err = provider.FetchFavoritesBatch(ctx, watchsync.ServerConfig{}, conn); !errors.Is(err, ErrAuthority) {
				t.Fatal("revoked source applied", err)
			}
		})
	}
}
