package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type eventPermissions struct{ scrobble, favorites, watchlist, ratings, favoriteRemovals, watchlistRemovals bool }

func (p eventPermissions) allows(e *pluginv1.WatchSyncEvent) bool {
	if e == nil {
		return false
	}
	switch e.Operation {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE:
		return p.favorites && p.favoriteRemovals
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST:
		return p.watchlist && p.watchlistRemovals
	}
	switch personalLane(e) {
	case "favorite":
		return p.favorites
	case "watchlist":
		return p.watchlist
	case "rating":
		return p.ratings
	}
	// Preserve the existing playback/history admission boundary.
	return p.scrobble
}
func personalLane(e *pluginv1.WatchSyncEvent) string {
	if e == nil {
		return ""
	}
	switch e.Operation {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE, pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE:
		return "favorite"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST:
		return "watchlist"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING, pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING:
		return "rating"
	}
	return ""
}
func (s *Service) recordPersonalEvent(ctx context.Context, tx pgx.Tx, id, user int, account, profile string, event *pluginv1.WatchSyncEvent) (*pluginv1.WatchSyncEvent, error) {
	if event.EventId == "" || len(event.EventId) > 128 || event.Media == nil || event.Media.MediaItemId == "" || event.Completed || event.PlaybackSessionId != "" || event.WatchHistoryId != "" || event.PositionSeconds != 0 || event.DurationSeconds != 0 {
		return nil, errEventRejected
	}
	if event.OccurredAt != nil && event.OccurredAt.CheckValid() != nil {
		return nil, errEventRejected
	}
	if event.ListPosition != nil && (personalLane(event) != "watchlist" || event.GetListPosition() < 0) {
		return nil, errEventRejected
	}
	lane := personalLane(event)
	wantsPresent := event.Operation == pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE || event.Operation == pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST || event.Operation == pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING
	var present bool
	var rating int32
	var revision int64
	var changed time.Time
	err := tx.QueryRow(ctx, `SELECT present,rating,revision,changed_at FROM bloem_managed_personal_states WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND kind=$4 FOR SHARE`, user, profile, event.Media.MediaItemId, lane).Scan(&present, &rating, &revision, &changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errEventRejected
	}
	if err != nil {
		return nil, err
	}
	if present != wantsPresent {
		return nil, errEventRejected
	}
	if lane == "rating" && (wantsPresent && event.Rating != rating*2 || !wantsPresent && event.Rating != 0) || lane != "rating" && event.Rating != 0 {
		return nil, errEventRejected
	}
	e, ok := proto.Clone(event).(*pluginv1.WatchSyncEvent)
	if !ok {
		return nil, errEventRejected
	}
	e.OccurredAt = timestamppb.New(changed)
	// Bind the receipt to a committed incarnation. Removing then re-adding the
	// same membership must not replay the first addition forever.
	e.EventId = stableTuple("state_", event.EventId, lane, strconv.FormatInt(revision, 10))
	sum, err := eventDigest(e)
	if err != nil {
		return nil, err
	}
	e.EventId = wireEventPrefix + hex.EncodeToString(sum)
	digest, err := eventDigest(e)
	if err != nil {
		return nil, err
	}
	entity := stableTuple("state_", lane, e.Media.MediaItemId)
	_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_events(installation_id,account_id,profile_id,event_id,entity_id,revision,consumption_id,digest) VALUES($1,$2,$3,$4,$5,$6,'',$7) ON CONFLICT DO NOTHING`, id, account, profile, e.EventId, entity, revision, digest)
	if err != nil {
		return nil, err
	}
	var saved []byte
	if err = tx.QueryRow(ctx, `SELECT digest FROM bloem_managed_events WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND event_id=$4`, id, account, profile, e.EventId).Scan(&saved); err != nil {
		return nil, err
	}
	if len(saved) != sha256.Size || !bytes.Equal(saved, digest) {
		return nil, errEventRejected
	}
	return e, nil
}
