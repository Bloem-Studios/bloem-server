package managedtracking

import (
	"context"
	"crypto/subtle"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/watchsync"
)

// personalProvider uses the existing Silo export/rating pipelines. Its list
// imports commit native membership and reconciliation evidence together, so an
// old destination read cannot undo a newer local edit or lose its removal.
type personalProvider struct {
	*watchsync.PluginProvider
	service      *Service
	installation int
	resolve      func(context.Context) (watchsync.WatchSyncPluginClient, error)
}

func (s *Service) WrapProvider(installation int, provider *watchsync.PluginProvider, resolve func(context.Context) (watchsync.WatchSyncPluginClient, error)) watchsync.Provider {
	return &personalProvider{provider, s, installation, resolve}
}
func (p *personalProvider) FetchFavoritesBatch(ctx context.Context, _ watchsync.ServerConfig, conn watchsync.Connection) (watchsync.FavoriteImportBatch, error) {
	return p.readList(ctx, conn, watchsync.ListKindFavorites)
}
func (p *personalProvider) FetchWatchlistBatch(ctx context.Context, _ watchsync.ServerConfig, conn watchsync.Connection) (watchsync.FavoriteImportBatch, error) {
	return p.readList(ctx, conn, watchsync.ListKindWatchlist)
}

// Keep the timestamp and media identity on removal rows. The ordinary decoder
// deliberately retains only the provider key on tombstones; this adapter needs
// the originating evidence to resolve conflicts with a concurrent native edit.
func (p *personalProvider) readList(ctx context.Context, conn watchsync.Connection, list watchsync.ListKind) (watchsync.FavoriteImportBatch, error) {
	var batch watchsync.FavoriteImportBatch
	if p.service == nil || p.service.Pool == nil || p.service.Cipher == nil || p.resolve == nil {
		return batch, ErrAuthority
	}
	client, err := p.resolve(ctx)
	if err != nil {
		return batch, err
	}
	kind, cursorKey := pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE, "plugin.remote.favorites"
	if list == watchsync.ListKindWatchlist {
		kind, cursorKey = pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST, "plugin.remote.watchlist"
	}
	request := &pluginv1.WatchSyncListRemoteStateRequest{Context: &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: conn.AccessToken, SecretAttributes: conn.SecretAttributes}}, StateKinds: []pluginv1.WatchSyncRemoteStateKind{kind}, PageSize: 100}
	seen := map[string]bool{}
	for pages := 0; pages < 101; pages++ {
		out, err := client.ListRemoteState(ctx, request)
		if err != nil {
			return batch, err
		}
		if out == nil || out.Fault != nil || len(out.Items) > 100 {
			return batch, ErrSnapshot
		}
		for _, warning := range out.Warnings {
			if len(batch.Warnings) < 50 && len(warning) <= 300 {
				batch.Warnings = append(batch.Warnings, warning)
			}
		}
		for _, item := range out.Items {
			if item == nil || item.Media == nil || item.ProviderItemKey == "" {
				return batch, ErrSnapshot
			}
			state := item.Favorite
			if list == watchsync.ListKindWatchlist {
				state = item.Watchlist
			}
			if state == nil || state.ListedAt == nil || state.ListedAt.CheckValid() != nil {
				return batch, ErrSnapshot
			}
			row := watchsync.RemoteFavorite{Provider: conn.Provider, ProviderItemKey: item.ProviderItemKey, Removed: state.Removed, FavoritedAt: state.ListedAt.AsTime(), IMDbID: item.Media.ExternalIds["imdb"], TMDBID: item.Media.ExternalIds["tmdb"], TVDBID: item.Media.ExternalIds["tvdb"]}
			switch item.Media.MediaType {
			case pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE:
				row.Kind = "movie"
			case pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES:
				row.Kind = "series"
			default:
				return batch, ErrSnapshot
			}
			batch.Rows = append(batch.Rows, row)
			if len(batch.Rows) > 10000 {
				return batch, ErrSnapshot
			}
		}
		if out.NextPageToken == "" {
			batch.UpdatedCursors = map[string]string{cursorKey: out.NextCursor}
			return p.applyList(ctx, conn, list, batch)
		}
		if seen[out.NextPageToken] || len(out.Items) == 0 {
			return batch, ErrSnapshot
		}
		seen[out.NextPageToken] = true
		request.PageToken = out.NextPageToken
	}
	return batch, ErrSnapshot
}
func (p *personalProvider) applyList(ctx context.Context, conn watchsync.Connection, list watchsync.ListKind, batch watchsync.FavoriteImportBatch) (watchsync.FavoriteImportBatch, error) {
	tx, err := p.service.Pool.Begin(ctx)
	if err != nil {
		return batch, err
	}
	defer tx.Rollback(ctx)
	// Native writers acquire a membership row before the evidence trigger. A
	// contended importer rolls back promptly instead of holding the evidence lock
	// while waiting for that writer's row. The ordinary next sync retries it.
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='250ms'`); err != nil {
		return batch, err
	}
	info, err := authority(ctx, tx, p.installation, grantShare)
	if err != nil {
		return batch, err
	}
	if err = p.service.eventAccess(ctx, tx, p.installation, info, conn.SecretAttributes["account_id"], conn.ProfileID); err != nil {
		return batch, err
	}
	key := watchsync.PluginProviderKey(p.installation, "bloem.pastime", "pastime", false)
	if conn.Provider != key || conn.SecretAttributes["instance_id"] != info.InstanceID || conn.SecretAttributes["profile_id"] != conn.ProfileID {
		return batch, ErrAuthority
	}
	var encrypted string
	var imports, removals bool
	lane, table := "favorite", "user_favorites"
	columns := "import_favorites_enabled,sync_favorite_removals_enabled"
	if list == watchsync.ListKindWatchlist {
		lane, table = "watchlist", "user_watchlist"
		columns = "import_watchlist_enabled,sync_watchlist_removals_enabled"
	}
	err = tx.QueryRow(ctx, `SELECT access_token,`+columns+` FROM watch_provider_connections WHERE id=$1 AND provider=$2 AND user_id=$3 AND profile_id=$4 FOR SHARE`, conn.ID, key, conn.UserID, conn.ProfileID).Scan(&encrypted, &imports, &removals)
	if err != nil || !imports {
		return batch, ErrAuthority
	}
	plain, err := p.service.Cipher.Decrypt(encrypted, watchsync.TokenAAD("access_token", key, conn.UserID, conn.ProfileID))
	if err != nil || plain == "" || subtle.ConstantTimeCompare([]byte(plain), []byte(conn.AccessToken)) != 1 {
		return batch, ErrAuthority
	}
	if len(batch.Rows) > 10000 {
		return batch, ErrSnapshot
	}
	for _, row := range batch.Rows {
		if row.Kind != "movie" && row.Kind != "series" || row.FavoritedAt.IsZero() || row.FavoritedAt.After(time.Now().Add(5*time.Second)) {
			return batch, ErrSnapshot
		}
		if row.Removed && !removals {
			continue
		}
		rows, err := tx.Query(ctx, `SELECT content_id,COALESCE(imdb_id,''),COALESCE(tmdb_id,''),COALESCE(tvdb_id,'') FROM media_items WHERE type=$1
   AND (($2<>'' AND imdb_id=$2) OR ($3<>'' AND tmdb_id=$3) OR ($4<>'' AND tvdb_id=$4))
   AND ($2='' OR COALESCE(imdb_id,'')='' OR imdb_id=$2) AND ($3='' OR COALESCE(tmdb_id,'')='' OR tmdb_id=$3) AND ($4='' OR COALESCE(tvdb_id,'')='' OR tvdb_id=$4) LIMIT 2`, row.Kind, row.IMDbID, row.TMDBID, row.TVDBID)
		if err != nil {
			return batch, err
		}
		var media, imdb, tmdb, tvdb string
		count := 0
		for rows.Next() {
			count++
			if err = rows.Scan(&media, &imdb, &tmdb, &tvdb); err != nil {
				rows.Close()
				return batch, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return batch, err
		}
		if count != 1 {
			if len(batch.Warnings) < 50 {
				batch.Warnings = append(batch.Warnings, "A Pastime title could not be matched to Bloem; its existing state was preserved.")
			}
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_managed_personal_states(user_id,profile_id,media_item_id,kind,present,rating,revision,changed_at) VALUES($1,$2,$3,$4,false,0,1,'epoch') ON CONFLICT DO NOTHING`, conn.UserID, conn.ProfileID, media, lane); err != nil {
			return batch, err
		}
		var local bool
		var changed time.Time
		if err = tx.QueryRow(ctx, `SELECT present,changed_at FROM bloem_managed_personal_states WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND kind=$4 FOR UPDATE`, conn.UserID, conn.ProfileID, media, lane).Scan(&local, &changed); err != nil {
			return batch, err
		}
		remote := !row.Removed
		if row.FavoritedAt.After(changed) {
			if remote {
				_, err = tx.Exec(ctx, `INSERT INTO `+table+`(user_id,profile_id,media_item_id,added_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, conn.UserID, conn.ProfileID, media, row.FavoritedAt)
			} else {
				_, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`, conn.UserID, conn.ProfileID, media)
			}
			if err != nil {
				return batch, err
			}
			local = remote
			changed = row.FavoritedAt
			// Preserve the originating change time for conflicts and echo prevention;
			// the ordinary evidence trigger still allocates the source revision.
			if _, err = tx.Exec(ctx, `UPDATE bloem_managed_personal_states SET present=$5,changed_at=$6 WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND kind=$4`, conn.UserID, conn.ProfileID, media, lane, local, changed); err != nil {
				return batch, err
			}
		}
		identity := ""
		switch {
		case imdb != "":
			identity = "imdb:" + imdb
		case tmdb != "":
			identity = "tmdb:" + tmdb
		case tvdb != "":
			identity = "tvdb:" + tvdb
		}
		_, err = tx.Exec(ctx, `INSERT INTO watch_provider_list_items(connection_id,list_kind,media_item_id,provider_item_key,kind,title,year,remote_present,local_present,last_seen_remote_at,last_seen_local_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(connection_id,list_kind,media_item_id) DO UPDATE SET provider_item_key=EXCLUDED.provider_item_key,remote_present=EXCLUDED.remote_present,local_present=EXCLUDED.local_present,last_seen_remote_at=EXCLUDED.last_seen_remote_at,last_seen_local_at=EXCLUDED.last_seen_local_at,last_error='',updated_at=clock_timestamp()`, conn.ID, string(list), media, identity, row.Kind, row.Title, row.Year, remote, local, row.FavoritedAt, changed)
		if err != nil {
			return batch, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return batch, err
	}
	// Rows were applied transactionally above. Returning a delta with no rows
	// prevents the generic importer from applying them a second time or inferring
	// deletion from an absence that carries no originating change timestamp.
	batch.Rows = nil
	batch.Incremental = true
	return batch, nil
}

var _ watchsync.FavoriteBatchImporter = (*personalProvider)(nil)
var _ watchsync.WatchlistBatchImporter = (*personalProvider)(nil)
