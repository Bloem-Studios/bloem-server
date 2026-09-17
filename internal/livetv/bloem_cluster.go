package livetv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrPeerUnavailable = errors.New("live TV owner is temporarily unavailable")

// SetClusterOwner binds a session to one API process, not just a reusable host
// name. Heartbeats fence a restarted host from serving the previous process's
// local HLS files. Set once before the service starts accepting requests.
func (s *Service) SetClusterOwner(nodeID, instanceID string) {
	s.ownerNodeID, s.ownerInstanceID = nodeID, instanceID
}

type hlsOwner struct {
	userID     int
	profileID  string
	nodeID     string
	instanceID string
	peerURL    string
}

type hlsRoutingStore interface {
	BindSessionOwner(context.Context, string, string, string) error
	HLSOwner(context.Context, string) (hlsOwner, error)
}

func (s *Service) bindSessionOwner(ctx context.Context, sessionID string) error {
	if s.ownerNodeID == "" {
		return nil
	}
	store, ok := s.store.(hlsRoutingStore)
	if !ok {
		return ErrPeerUnavailable
	}
	return store.BindSessionOwner(ctx, sessionID, s.ownerNodeID, s.ownerInstanceID)
}

// PeerHLSURL authorizes the durable session before deciding where bytes live.
// An empty URL means this process owns the session (or standalone test mode).
// The destination comes only from a live registered API heartbeat, never a
// request parameter. Failure must not launch a replacement encoder/tuner.
func (s *Service) PeerHLSURL(ctx context.Context, playbackID string, userID int, profileID string, enforceOwner bool) (string, error) {
	if s.ownerNodeID == "" {
		return "", nil
	}
	store, ok := s.store.(hlsRoutingStore)
	if !ok {
		return "", ErrPeerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, sessionLeaseCallTimeout)
	defer cancel()
	owner, err := store.HLSOwner(ctx, playbackID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: lookup failed", ErrPeerUnavailable)
	}
	if enforceOwner && !ownerMatches(owner.userID, owner.profileID, userID, profileID) {
		return "", ErrNotFound
	}
	// Pre-migration sessions have no route. Keep their existing local bridge
	// authorization instead of inventing a peer or an unowned replacement.
	if owner.nodeID == "" && owner.instanceID == "" {
		return "", nil
	}
	if owner.nodeID == s.ownerNodeID && owner.instanceID == s.ownerInstanceID {
		return "", nil
	}
	if owner.peerURL == "" {
		return "", ErrPeerUnavailable
	}
	u, err := url.Parse(owner.peerURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrPeerUnavailable
	}
	return u.Scheme + "://" + u.Host, nil
}

func (s *PgStore) BindSessionOwner(ctx context.Context, sessionID, nodeID, instanceID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE livetv_sessions SET owner_node_id=$2, owner_instance_id=$3::uuid WHERE id=$1 AND status='active'`, sessionID, nodeID, instanceID)
	if err != nil {
		return fmt.Errorf("bind live TV owner: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) HLSOwner(ctx context.Context, playbackID string) (hlsOwner, error) {
	var owner hlsOwner
	err := s.db.QueryRow(ctx, `SELECT COALESCE(s.user_id,0), s.profile_id, s.owner_node_id,
 COALESCE(s.owner_instance_id::text,''), COALESCE(h.node_url,'')
 FROM livetv_sessions s LEFT JOIN node_heartbeats h
 ON h.node_id=s.owner_node_id AND h.instance_id=s.owner_instance_id
 AND h.node_type IN ('api','integrated') AND h.updated_at > now()-interval '45 seconds'
 WHERE s.playback_session_id=$1 AND s.status='active'`, playbackID).Scan(&owner.userID, &owner.profileID, &owner.nodeID, &owner.instanceID, &owner.peerURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return hlsOwner{}, ErrNotFound
	}
	if err != nil {
		return hlsOwner{}, fmt.Errorf("resolve live TV owner: %w", err)
	}
	return owner, nil
}

// CompatStream contains no credentials or tuner URLs. The verified compat
// principal supplies its own token; the database stores only its SHA-256 hash.
type CompatStream struct {
	ID            string
	NativeSession string
	ChannelID     string
	OpenedAt      time.Time
}

type compatStreamStore interface {
	PutCompatStream(context.Context, CompatStream, string) error
	GetCompatStream(context.Context, string, string) (CompatStream, error)
}

func (s *Service) HasSharedCompatStreams() bool {
	if s == nil {
		return false
	}
	_, ok := s.store.(compatStreamStore)
	return ok
}

func openerHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) PutCompatStream(ctx context.Context, stream CompatStream, token string) error {
	store, ok := s.store.(compatStreamStore)
	if !ok || token == "" {
		return ErrInvalidArgument
	}
	return store.PutCompatStream(ctx, stream, openerHash(token))
}

func (s *Service) GetCompatStream(ctx context.Context, id, token string) (CompatStream, error) {
	store, ok := s.store.(compatStreamStore)
	if !ok || token == "" {
		return CompatStream{}, ErrNotFound
	}
	if _, err := uuid.Parse(id); err != nil {
		return CompatStream{}, ErrNotFound
	}
	return store.GetCompatStream(ctx, id, openerHash(token))
}

func (s *PgStore) PutCompatStream(ctx context.Context, stream CompatStream, hash string) error {
	// Keep the map bounded without keeping an extra scheduled cleanup job.
	// Released rows carry no authority; pruning them cannot affect an open stream.
	_, err := s.db.Exec(ctx, `DELETE FROM bloem_livetv_compat_streams WHERE id IN (
 SELECT c.id FROM bloem_livetv_compat_streams c JOIN livetv_sessions s ON s.id=c.native_session_id
 WHERE s.status<>'active' LIMIT 64 FOR UPDATE OF c SKIP LOCKED)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO bloem_livetv_compat_streams (id,native_session_id,opener_hash) VALUES ($1,$2,$3)`, stream.ID, stream.NativeSession, hash)
	return err
}

func (s *PgStore) GetCompatStream(ctx context.Context, id, hash string) (CompatStream, error) {
	var result CompatStream
	err := s.db.QueryRow(ctx, `SELECT c.id::text,c.native_session_id,s.channel_id,c.created_at
 FROM bloem_livetv_compat_streams c JOIN livetv_sessions s ON s.id=c.native_session_id
 WHERE c.id=$1::uuid AND c.opener_hash=$2 AND s.status='active'`, id, hash).Scan(&result.ID, &result.NativeSession, &result.ChannelID, &result.OpenedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CompatStream{}, ErrNotFound
	}
	return result, err
}
