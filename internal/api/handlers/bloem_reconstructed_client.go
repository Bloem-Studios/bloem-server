package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// A session rebuilt from a stream token after a restart has no client: the
// token's recipe card leaves client metadata out to keep stream URLs short.
// The session's lifecycle row kept the client it started on, so the rebuild
// takes it back from there and the admin views and history keep the device.

// reconstructedClientTimeout bounds the lifecycle read a rebuild makes, so a
// stalled database delays a recovering stream by at most this much.
const reconstructedClientTimeout = 2 * time.Second

// lifecycleClientReader is the admin store's read of the client a session
// recorded when it started.
type lifecycleClientReader interface {
	RestoreLifecycleClient(ctx context.Context, card *playback.RecipeCard) error
}

// InstallReconstructedClientRestore lets the transcode manager fill a rebuilt
// session's client from its lifecycle row. The read is best effort: a failure
// costs the session its device label, never its playback.
func (h *PlaybackHandler) InstallReconstructedClientRestore() {
	if h == nil || h.tm == nil {
		return
	}
	h.tm.RestoreReconstructedClient = func(ctx context.Context, card *playback.RecipeCard) {
		store, ok := h.AdminStore.(lifecycleClientReader)
		if !ok || card == nil {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, reconstructedClientTimeout)
		defer cancel()
		if err := store.RestoreLifecycleClient(ctx, card); err != nil {
			slog.WarnContext(ctx, "could not restore the client of a rebuilt playback session", "component", "api",
				"playback_session_id", card.SessionID, "error", err)
		}
	}
}

// RestoreLifecycleClient fills the client fields card lacks from the facts
// the session's lifecycle row recorded at its start. Only the card owner's
// row is read, and a field the card already has is kept.
func (s *PGPlaybackAdminStore) RestoreLifecycleClient(ctx context.Context, card *playback.RecipeCard) error {
	if s == nil || s.pool == nil || card == nil {
		return nil
	}
	id := lifecycleIDParam(card.SessionID)
	if id == nil {
		return nil
	}
	var name, version, build, channel, userAgent, deviceID, platform string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(start_facts->>'client_name', ''), COALESCE(start_facts->>'client_version', ''),
			COALESCE(start_facts->>'client_build', ''), COALESCE(start_facts->>'client_channel', ''),
			COALESCE(start_facts->>'client_user_agent', ''), COALESCE(start_facts->>'device_id', ''),
			COALESCE(start_facts->>'device_platform', '')
		FROM playback_session_lifecycle
		WHERE session_id = $1::uuid AND user_id = $2`, id, card.UserID).Scan(
		&name, &version, &build, &channel, &userAgent, &deviceID, &platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the client a playback session started on: %w", err)
	}
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&card.ClientName, name)
	fill(&card.ClientVersion, version)
	fill(&card.ClientBuild, build)
	fill(&card.ClientChannel, channel)
	fill(&card.ClientUserAgent, userAgent)
	fill(&card.DeviceID, deviceID)
	fill(&card.DevicePlatform, platform)
	return nil
}
