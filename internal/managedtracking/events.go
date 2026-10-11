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

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
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
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_managed_enrollments e JOIN bloem_managed_profiles p ON p.tenant_id=$4 AND p.account_id=e.account_id AND p.profile_id=e.profile_id WHERE e.installation_id=$1 AND e.account_id=$2 AND e.profile_id=$3 AND e.generation=$5 AND e.active AND p.active AND p.account_active)`, id, account, profile, info.Scope, info.Generation).Scan(&authorized)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrAuthority
	}
	return nil
}

// RecordEvents runs only on the trusted host's outbound client path, before
// invoking the plugin. Plugins can resolve these committed receipts, not mint them.
func (s *Service) RecordEvents(ctx context.Context, id int, a *pluginv1.WatchSyncAuthenticatedContext, events []*pluginv1.WatchSyncEvent) error {
	if s.Cipher == nil || a == nil || a.CapabilityId != "pastime" || a.Credentials == nil || len(events) == 0 || len(events) > 100 {
		return ErrAuthority
	}
	attrs := a.Credentials.SecretAttributes
	account, profile := attrs["account_id"], attrs["profile_id"]
	if !positiveID(account) {
		return ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton FOR UPDATE`); err != nil {
		return err
	}
	info, err := authority(ctx, tx, id)
	if err != nil {
		return err
	}
	if attrs["instance_id"] != info.InstanceID {
		return ErrAuthority
	}
	if err = s.eventAccess(ctx, tx, id, info, account, profile); err != nil {
		return err
	}
	var encrypted string
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	err = tx.QueryRow(ctx, `SELECT access_token FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3 AND scrobble_enabled`, key, account, profile).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuthority
	}
	if err != nil {
		return err
	}
	user, _ := strconv.Atoi(account)
	token, err := s.Cipher.Decrypt(encrypted, watchsync.TokenAAD("access_token", key, user, profile))
	if err != nil {
		return ErrAuthority
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.Credentials.AccessToken)) != 1 {
		return ErrAuthority
	}
	for _, event := range events {
		if event == nil || event.EventId == "" || len(event.EventId) > 128 || event.Media == nil || event.Media.MediaItemId == "" || event.OccurredAt == nil || event.OccurredAt.CheckValid() != nil || math.IsNaN(event.PositionSeconds) || math.IsInf(event.PositionSeconds, 0) || math.IsNaN(event.DurationSeconds) || math.IsInf(event.DurationSeconds, 0) || event.DurationSeconds < 1 || event.PositionSeconds < 0 {
			return ErrAuthority
		}
		digest, err := eventDigest(event)
		if err != nil {
			return ErrAuthority
		}
		var existing []byte
		err = tx.QueryRow(ctx, `SELECT digest FROM bloem_managed_events WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND event_id=$4`, id, account, profile, event.EventId).Scan(&existing)
		if err == nil {
			if !bytes.Equal(existing, digest) {
				return ErrConflict
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		action := ""
		history := false
		switch event.Operation {
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
			return ErrAuthority
		}
		if event.Completed && action != "stop" && !history {
			return ErrAuthority
		}
		var revision uint64
		if history {
			if event.WatchHistoryId == "" {
				return ErrAuthority
			}
			err = tx.QueryRow(ctx, `SELECT min(revision) FROM bloem_managed_playback_sources WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND history_id=$4 AND action='history' AND completed AND duration=$5`, account, profile, event.Media.MediaItemId, event.WatchHistoryId, event.DurationSeconds).Scan(&revision)
		} else {
			if event.PlaybackSessionId == "" || event.Completed && event.WatchHistoryId == "" {
				return ErrAuthority
			}
			err = tx.QueryRow(ctx, `SELECT min(revision) FROM bloem_managed_playback_sources WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3 AND session_id=$4 AND (action=$5 OR $5='stop' AND action IN('stop_confirming','stop_confirmed','stop_retry')) AND progress=$6 AND duration=$7 AND completed=$8 AND ($8=false OR history_id=$9)`, account, profile, event.Media.MediaItemId, event.PlaybackSessionId, action, event.PositionSeconds, event.DurationSeconds, event.Completed, event.WatchHistoryId).Scan(&revision)
		}
		if err != nil || revision == 0 {
			return ErrAuthority
		}
		consumption := ""
		if event.Completed || history {
			consumption = stableTuple("play_", account, profile, event.WatchHistoryId)
		}
		entity := stableTuple("media_", event.Media.MediaItemId)
		_, err = tx.Exec(ctx, `INSERT INTO bloem_managed_events(installation_id,account_id,profile_id,event_id,entity_id,revision,consumption_id,digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, account, profile, event.EventId, entity, revision, consumption, digest)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
	if _, err = tx.Exec(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton FOR UPDATE`); err != nil {
		return out, err
	}
	info, err := authority(ctx, tx, id)
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
func (c *outboundClient) ApplyEvents(ctx context.Context, r *pluginv1.WatchSyncApplyEventsRequest) (*pluginv1.WatchSyncApplyEventsResponse, error) {
	if r == nil {
		return nil, ErrAuthority
	}
	if err := c.service.RecordEvents(ctx, c.installation, r.Context, r.Events); err != nil {
		return nil, err
	}
	return c.WatchSyncPluginClient.ApplyEvents(ctx, r)
}
