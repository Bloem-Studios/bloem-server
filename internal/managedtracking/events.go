package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type EventAuthority struct {
	InstanceID, AccountID, ProfileID, EventID, EntityID, ConsumptionID string
	Revision                                                           uint64
	Digest                                                             []byte
}

func stableTuple(prefix string, parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return prefix + hex.EncodeToString(h[:])
}
func eventDigest(e *pluginv1.WatchSyncEvent) ([]byte, error) {
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e)
	h := sha256.Sum256(b)
	return h[:], err
}
func (s *Service) eventAccess(ctx context.Context, tx pgx.Tx, id int, info Info, account, profile string) error {
	var authorized bool
	// Enrollments from before a periodic renewal stay valid until the profile
	// is re-enrolled; rotation and revocation raise min_generation instead.
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_managed_enrollments e JOIN bloem_managed_profiles p ON p.tenant_id=$4 AND p.account_id=e.account_id AND p.profile_id=e.profile_id WHERE e.installation_id=$1 AND e.account_id=$2 AND e.profile_id=$3 AND e.generation BETWEEN $5 AND $6 AND e.active AND p.active AND p.account_active)`, id, account, profile, info.Scope, info.minGeneration, info.Generation).Scan(&authorized)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrAuthority
	}
	return nil
}

// errEventRejected marks one event the host refuses to vouch for. It never
// fails the rest of its batch.
var errEventRejected = errors.New("managed_tracking_event_rejected")

// wireEventPrefix marks event IDs the host derives for the plugin.
const wireEventPrefix = "bloem_evt_"

// RecordEvents runs only on the trusted host's outbound client path, before
// invoking the plugin. Plugins can resolve these committed receipts, not mint
// them. It returns, aligned with events, the event to forward for each one the
// host vouches for, or nil for an event it rejects. Only a failure of the
// batch's own authority (grant, enrollment, credential) or of storage is an
// error.
//
// A forwarded event carries a host-derived ID that is a digest of its
// payload. Watch sync reuses one event ID for different payloads (a resumed
// session restarts with the same START ID, the startup sweep re-sends a STOP
// with a new time); a payload-derived ID keeps each one distinct for the host
// receipt and for Pastime while exact retries stay idempotent.
func (s *Service) RecordEvents(ctx context.Context, id int, a *pluginv1.WatchSyncAuthenticatedContext, events []*pluginv1.WatchSyncEvent) ([]*pluginv1.WatchSyncEvent, error) {
	if s.Cipher == nil || a == nil || a.CapabilityId != "pastime" || a.Credentials == nil || len(events) == 0 || len(events) > 100 {
		return nil, ErrAuthority
	}
	attrs := a.Credentials.SecretAttributes
	account, profile := attrs["account_id"], attrs["profile_id"]
	if !positiveID(account) {
		return nil, ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, id, grantRead)
	if err != nil {
		return nil, err
	}
	if attrs["instance_id"] != info.InstanceID {
		return nil, ErrAuthority
	}
	if err = s.eventAccess(ctx, tx, id, info, account, profile); err != nil {
		return nil, err
	}
	var encrypted string
	var permissions eventPermissions
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	err = tx.QueryRow(ctx, `SELECT access_token,scrobble_enabled,export_favorites_enabled,export_watchlist_enabled,export_ratings_enabled,sync_favorite_removals_enabled,sync_watchlist_removals_enabled FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, key, account, profile).Scan(&encrypted, &permissions.scrobble, &permissions.favorites, &permissions.watchlist, &permissions.ratings, &permissions.favoriteRemovals, &permissions.watchlistRemovals)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAuthority
	}
	if err != nil {
		return nil, err
	}
	user, _ := strconv.Atoi(account)
	token, err := s.Cipher.Decrypt(encrypted, watchsync.TokenAAD("access_token", key, user, profile))
	if err != nil {
		return nil, ErrAuthority
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.Credentials.AccessToken)) != 1 {
		return nil, ErrAuthority
	}
	out := make([]*pluginv1.WatchSyncEvent, len(events))
	for i, event := range events {
		if !permissions.allows(event) {
			continue
		}
		wire, err := s.recordEvent(ctx, tx, id, user, account, profile, event)
		if errors.Is(err, errEventRejected) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[i] = wire
	}
	return out, tx.Commit(ctx)
}

func validEvent(e *pluginv1.WatchSyncEvent) bool {
	return e != nil && e.EventId != "" && len(e.EventId) <= 128 && e.Media != nil && e.Media.MediaItemId != "" && e.OccurredAt != nil && e.OccurredAt.CheckValid() == nil &&
		!math.IsNaN(e.PositionSeconds) && !math.IsInf(e.PositionSeconds, 0) && !math.IsNaN(e.DurationSeconds) && !math.IsInf(e.DurationSeconds, 0) &&
		e.DurationSeconds >= 1 && e.PositionSeconds >= 0
}

// recordEvent vouches for one event and returns the event to forward, or
// errEventRejected.
func (s *Service) recordEvent(ctx context.Context, tx pgx.Tx, id, user int, account, profile string, event *pluginv1.WatchSyncEvent) (*pluginv1.WatchSyncEvent, error) {
	if personalLane(event) != "" {
		return s.recordPersonalEvent(ctx, tx, id, user, account, profile, event)
	}
	if !validEvent(event) {
		return nil, errEventRejected
	}
	e, ok := proto.Clone(event).(*pluginv1.WatchSyncEvent)
	if !ok {
		return nil, errEventRejected
	}
	var action string
	history := false
	switch e.Operation {
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED:
		history = true
		action = "history"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START:
		action = "start"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE:
		action = "pause"
	case pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP:
		action = "stop"
	default:
		return nil, errEventRejected
	}
	if e.Completed && action != "stop" {
		return nil, errEventRejected
	}
	if !history && e.PlaybackSessionId == "" {
		return nil, errEventRejected
	}
	// A completion is anchored to the profile's current history row: its
	// media item decides the entity and its watch time is the occurrence, so
	// every delivery of one viewing (live stop, history export, retry) carries
	// the same consumption payload. A stop whose session recorded a completed
	// viewing is that completion even when the session row lost the flag.
	if history || e.Completed || action == "stop" && e.WatchHistoryId != "" {
		if e.WatchHistoryId == "" {
			return nil, errEventRejected
		}
		var media string
		var completed bool
		var watchedAt time.Time
		err := tx.QueryRow(ctx, `SELECT media_item_id,completed,watched_at FROM user_watch_history WHERE id=$1 AND user_id=$2 AND profile_id=$3`, e.WatchHistoryId, user, profile).Scan(&media, &completed, &watchedAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		viewing := err == nil && completed && media == e.Media.MediaItemId
		switch {
		case viewing:
			if action == "stop" {
				e.Completed = true
			}
			e.OccurredAt = timestamppb.New(watchedAt)
		case history || e.Completed:
			return nil, errEventRejected
		}
	}
	sum, err := eventDigest(e)
	if err != nil {
		return nil, errEventRejected
	}
	e.EventId = wireEventPrefix + hex.EncodeToString(sum)
	digest, err := eventDigest(e)
	if err != nil {
		return nil, errEventRejected
	}
	var existing []byte
	err = tx.QueryRow(ctx, `SELECT digest FROM bloem_managed_events WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND event_id=$4`, id, account, profile, e.EventId).Scan(&existing)
	if err == nil {
		if !bytes.Equal(existing, digest) {
			return nil, errEventRejected
		}
		return e, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var revision *int64
	if history {
		// The oldest source of this viewing on its current item: completion
		// time, or the time it moved to this item.
		err = tx.QueryRow(ctx, `SELECT min(revision) FROM bloem_managed_playback_sources WHERE action='history' AND history_id=$1 AND media_item_id=$2 AND user_id=$3 AND profile_id=$4 AND completed`, e.WatchHistoryId, e.Media.MediaItemId, user, profile).Scan(&revision)
		if err == nil && revision == nil {
			// Committed concurrently with the grant's backfill; its source
			// starts now.
			err = tx.QueryRow(ctx, `INSERT INTO bloem_managed_playback_sources(user_id,profile_id,media_item_id,history_id,action,duration,completed) VALUES($1,$2,$3,$4,'history',$5,true) RETURNING revision`, user, profile, e.Media.MediaItemId, e.WatchHistoryId, e.DurationSeconds).Scan(&revision)
		}
	} else {
		// The latest time the session reached exactly this state. Duration and
		// the session's completed flag are not compared: watch sync does not
		// store them on every update, and the dispatched event is the host's.
		err = tx.QueryRow(ctx, `SELECT max(revision) FROM bloem_managed_playback_sources WHERE user_id=$1 AND profile_id=$2 AND session_id=$3 AND media_item_id=$4
 AND (action=$5 OR $5='stop' AND action IN('stop_confirming','stop_confirmed','stop_retry')) AND progress=$6 AND ($7::boolean=false OR history_id=$8)`,
			user, profile, e.PlaybackSessionId, e.Media.MediaItemId, action, e.PositionSeconds, e.Completed, e.WatchHistoryId).Scan(&revision)
	}
	if err != nil {
		return nil, err
	}
	if revision == nil || *revision < 1 {
		return nil, errEventRejected
	}
	consumption := ""
	if e.Completed || history {
		consumption = stableTuple("play_", account, profile, e.WatchHistoryId)
	}
	entity := stableTuple("media_", e.Media.MediaItemId)
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_managed_events(installation_id,account_id,profile_id,event_id,entity_id,revision,consumption_id,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, id, account, profile, e.EventId, entity, *revision, consumption, digest)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// Recorded concurrently by another delivery of the same payload.
		if err = tx.QueryRow(ctx, `SELECT digest FROM bloem_managed_events WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND event_id=$4`, id, account, profile, e.EventId).Scan(&existing); err != nil {
			return nil, err
		}
		if !bytes.Equal(existing, digest) {
			return nil, errEventRejected
		}
	}
	return e, nil
}
func (s *Service) ResolveEvent(ctx context.Context, id int, instance, account, profile, event string, digest []byte) (EventAuthority, error) {
	out := EventAuthority{InstanceID: instance, AccountID: account, ProfileID: profile, EventID: event}
	if !positiveID(account) || len(digest) != sha256.Size {
		return out, ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, id, grantRead)
	if err != nil {
		return out, err
	}
	if instance != info.InstanceID {
		return out, ErrAuthority
	}
	if err = s.eventAccess(ctx, tx, id, info, account, profile); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT entity_id,revision,consumption_id,digest FROM bloem_managed_events WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND event_id=$4`, id, account, profile, event).Scan(&out.EntityID, &out.Revision, &out.ConsumptionID, &out.Digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrAuthority
	}
	if err != nil {
		return out, err
	}
	if !bytes.Equal(digest, out.Digest) {
		return out, ErrAuthority
	}
	return out, tx.Commit(ctx)
}

type outboundClient struct {
	watchsync.WatchSyncPluginClient
	service      *Service
	installation int
}

func (s *Service) WrapClient(id int, c watchsync.WatchSyncPluginClient) watchsync.WatchSyncPluginClient {
	return &outboundClient{WatchSyncPluginClient: c, service: s, installation: id}
}

// ApplyEvents forwards only the events the host vouches for, under their
// host-derived IDs, and answers for the rest itself with REJECTED. Results go
// back under the caller's event IDs and in the caller's order.
func (c *outboundClient) ApplyEvents(ctx context.Context, r *pluginv1.WatchSyncApplyEventsRequest) (*pluginv1.WatchSyncApplyEventsResponse, error) {
	if r == nil {
		return nil, ErrAuthority
	}
	wire, err := c.service.RecordEvents(ctx, c.installation, r.Context, r.Events)
	if err != nil {
		return nil, err
	}
	forward, ok := proto.Clone(r).(*pluginv1.WatchSyncApplyEventsRequest)
	if !ok {
		return nil, ErrAuthority
	}
	forward.Events = nil
	for _, event := range wire {
		if event != nil {
			forward.Events = append(forward.Events, event)
		}
	}
	response := &pluginv1.WatchSyncApplyEventsResponse{}
	if len(forward.Events) > 0 {
		if response, err = c.WatchSyncPluginClient.ApplyEvents(ctx, forward); err != nil {
			return nil, err
		}
		if response == nil {
			response = &pluginv1.WatchSyncApplyEventsResponse{}
		}
	}
	byWire := make(map[string]*pluginv1.WatchSyncApplyResult, len(response.GetResults()))
	for _, result := range response.GetResults() {
		if result != nil {
			byWire[result.GetEventId()] = result
		}
	}
	out, ok := proto.Clone(response).(*pluginv1.WatchSyncApplyEventsResponse)
	if !ok {
		return nil, ErrAuthority
	}
	out.Results = nil
	for i, event := range r.Events {
		if wire[i] == nil {
			out.Results = append(out.Results, &pluginv1.WatchSyncApplyResult{
				EventId: event.GetEventId(),
				Status:  pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED,
				Fault:   &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_REQUEST, SafeMessage: "The server has no tracking record for this playback event."},
			})
			continue
		}
		if result, found := byWire[wire[i].GetEventId()]; found {
			if mapped, ok := proto.Clone(result).(*pluginv1.WatchSyncApplyResult); ok {
				mapped.EventId = event.GetEventId()
				out.Results = append(out.Results, mapped)
			}
		}
	}
	return out, nil
}
