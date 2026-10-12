package managedtracking

import (
	"context"
	"crypto/subtle"
	"errors"
	"strconv"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func (s *Service) authorizePersonalRead(ctx context.Context, id int, r *pluginv1.WatchSyncListRemoteStateRequest) error {
	if s.Cipher == nil || r == nil || r.Context == nil || r.Context.CapabilityId != "pastime" || r.Context.Credentials == nil {
		return ErrAuthority
	}
	creds := r.Context.Credentials
	attrs := creds.SecretAttributes
	account, profile := attrs["account_id"], attrs["profile_id"]
	if !positiveID(account) {
		return ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, id, grantRead)
	if err != nil {
		return err
	}
	if attrs["instance_id"] != info.InstanceID {
		return ErrAuthority
	}
	if err = s.eventAccess(ctx, tx, id, info, account, profile); err != nil {
		return err
	}
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	var encrypted string
	var favorites, watchlist, ratings bool
	err = tx.QueryRow(ctx, `SELECT access_token,import_favorites_enabled,import_watchlist_enabled,import_ratings_enabled FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3 FOR SHARE`, key, account, profile).Scan(&encrypted, &favorites, &watchlist, &ratings)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthority
	}
	if err != nil {
		return err
	}
	user, _ := strconv.Atoi(account)
	token, err := s.Cipher.Decrypt(encrypted, watchsync.TokenAAD("access_token", key, user, profile))
	if err != nil || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(creds.AccessToken)) != 1 {
		return ErrAuthority
	}
	kinds := r.StateKinds
	if len(kinds) == 0 {
		kinds = []pluginv1.WatchSyncRemoteStateKind{pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE, pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST, pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING}
	}
	for _, kind := range kinds {
		switch kind {
		case pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE:
			if !favorites {
				return ErrAuthority
			}
		case pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST:
			if !watchlist {
				return ErrAuthority
			}
		case pluginv1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING:
			if !ratings {
				return ErrAuthority
			}
		default:
			return ErrAuthority
		}
	}
	return tx.Commit(ctx)
}
func (c *outboundClient) ListRemoteState(ctx context.Context, r *pluginv1.WatchSyncListRemoteStateRequest) (*pluginv1.WatchSyncListRemoteStateResponse, error) {
	if err := c.service.authorizePersonalRead(ctx, c.installation, r); err != nil {
		return nil, err
	}
	out, err := c.WatchSyncPluginClient.ListRemoteState(ctx, r)
	if err != nil {
		return nil, err
	}
	// The plugin's HTTP read may outlive a source-side revocation or preference
	// change. Do not return its page for publication under the old authority.
	if err = c.service.authorizePersonalRead(ctx, c.installation, r); err != nil {
		return nil, err
	}
	return out, nil
}
