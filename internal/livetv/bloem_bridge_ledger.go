package livetv

import (
	"context"
	"fmt"
	"log/slog"
)

// playbackSessionStore is the optional store capability the HLS bridge uses to
// reconcile a running remux with its durable session row. PgStore implements
// it; a store without it leaves remuxes to the stale-session TTL.
type playbackSessionStore interface {
	// PlaybackSessionActive reports whether an active session row is served by
	// the playback bridge session id. A missing row is simply not active.
	PlaybackSessionActive(ctx context.Context, playbackSessionID string) (bool, error)
	// ReleaseSessionByPlaybackID releases the active session served by the
	// playback bridge session id, returning nil when none was active.
	ReleaseSessionByPlaybackID(ctx context.Context, playbackSessionID string) (*LiveSession, error)
}

// bridgeLedger is what a bridge consults about the sessions it serves. Service
// implements it and installs itself in SetPlaybackBridge.
type bridgeLedger interface {
	bridgeSessionActive(ctx context.Context, playbackSessionID string) (bool, error)
	releaseBridgeSession(ctx context.Context, playbackSessionID string)
}

// ledgerAware bridges accept the service as their ledger.
type ledgerAware interface {
	setLedger(ledger bridgeLedger)
}

func (s *Service) playbackSessions() (playbackSessionStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	store, ok := s.store.(playbackSessionStore)
	return store, ok
}

// bridgeSessionActive reports whether a remux still serves an active session.
// Without the store capability the answer is "active", leaving the remux to the
// stale-session reclaim exactly as before.
func (s *Service) bridgeSessionActive(ctx context.Context, playbackSessionID string) (bool, error) {
	store, ok := s.playbackSessions()
	if !ok {
		return true, nil
	}
	return store.PlaybackSessionActive(ctx, playbackSessionID)
}

// releaseBridgeSession frees the tuner of a session whose remux died, so the
// next tune need not wait out StaleSessionTTL.
func (s *Service) releaseBridgeSession(ctx context.Context, playbackSessionID string) {
	store, ok := s.playbackSessions()
	if !ok || playbackSessionID == "" {
		return
	}
	session, err := store.ReleaseSessionByPlaybackID(ctx, playbackSessionID)
	if err != nil {
		slog.WarnContext(ctx, "livetv release after remux exit failed",
			"playback_session_id", playbackSessionID, "error", err)
		return
	}
	if session == nil {
		return
	}
	slog.InfoContext(ctx, "livetv released session after remux exit",
		"session_id", session.ID, "channel_id", session.ChannelID, "tuner_index", session.TunerIndex)
	s.forgetTouch(*session)
	s.recordSessionHistory(ctx, *session)
}

func (s *PgStore) PlaybackSessionActive(ctx context.Context, playbackSessionID string) (bool, error) {
	if playbackSessionID == "" {
		return false, nil
	}
	var active bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM livetv_sessions
			WHERE status = 'active' AND playback_session_id = $1
		)`, playbackSessionID).Scan(&active); err != nil {
		return false, fmt.Errorf("check playback session: %w", err)
	}
	return active, nil
}

func (s *PgStore) ReleaseSessionByPlaybackID(ctx context.Context, playbackSessionID string) (*LiveSession, error) {
	if playbackSessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		UPDATE livetv_sessions SET status = 'released', released_at = now()
		WHERE status = 'active' AND playback_session_id = $1
		RETURNING `+sessionSelectCols, playbackSessionID)
	if err != nil {
		return nil, fmt.Errorf("release playback session: %w", err)
	}
	defer rows.Close()
	var released *LiveSession
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		released = &session
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("release playback session: %w", err)
	}
	return released, nil
}
