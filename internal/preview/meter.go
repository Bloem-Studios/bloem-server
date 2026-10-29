package preview

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MaxChargedGap is the most one segment delivery charges: the time since the
// grant's previous delivery, capped, so a pause stops the meter once the
// player's buffer is full.
const MaxChargedGap = 20

// FreePlayBusyWindow is how recently a grant must have had a segment delivered
// to count against the free-play capacity, in seconds.
const FreePlayBusyWindow = 60

// liveGrantForPlayback joins a playback session to a grant that may still
// play: preview and free play on, time left, grant not ended.
const liveGrantForPlayback = `FROM bloem_preview_play_sessions ps
	JOIN bloem_preview_play_grants g ON g.grant_id = ps.grant_id
	JOIN bloem_preview_settings s ON s.enabled AND s.free_play_enabled
	WHERE ps.playback_session_id = $1 AND g.used_seconds < g.allowance_seconds AND g.expires_at > now()`

// BindPlayback records that grant started the playback session. Media
// requests authorized by a stream token find the grant to charge here.
func BindPlayback(ctx context.Context, db execer, playbackSessionID, grantID string) error {
	if _, err := db.Exec(ctx, `INSERT INTO bloem_preview_play_sessions (playback_session_id, grant_id)
		VALUES ($1, $2) ON CONFLICT (playback_session_id) DO NOTHING`, playbackSessionID, grantID); err != nil {
		return fmt.Errorf("binding a free-play playback session: %w", err)
	}
	return nil
}

// ChargeDelivery charges one segment delivery to the grant that started the
// playback session, in one statement: the seconds since the grant's previous
// delivery, at most MaxChargedGap, none for the first. It reports false, and
// charges nothing, once the allowance is used, the grant has ended, free play
// is off or no grant started the session.
func ChargeDelivery(ctx context.Context, db queryRower, playbackSessionID string) (string, bool, error) {
	var grant string
	err := db.QueryRow(ctx, `UPDATE bloem_preview_play_grants AS charged SET
		used_seconds = charged.used_seconds + COALESCE(LEAST(GREATEST(EXTRACT(EPOCH FROM now() - charged.last_delivery_at), 0), $2), 0),
		last_delivery_at = now()
		FROM (SELECT g.grant_id `+liveGrantForPlayback+`) AS live
		WHERE charged.grant_id = live.grant_id
		RETURNING charged.grant_id`, playbackSessionID, MaxChargedGap).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("charging free play: %w", err)
	}
	return grant, true, nil
}

// LivePlayback reports, without charging, whether the playback session's
// grant may still play: playlists and subtitles need that, not the meter.
func LivePlayback(ctx context.Context, db queryRower, playbackSessionID string) (string, bool, error) {
	var grant string
	err := db.QueryRow(ctx, `SELECT g.grant_id `+liveGrantForPlayback, playbackSessionID).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading free play: %w", err)
	}
	return grant, true, nil
}

// FreePlayBusy reports whether capacity other grants had a segment delivered
// in the last FreePlayBusyWindow seconds. A grant's own streams never make it
// busy.
func FreePlayBusy(ctx context.Context, db queryRower, grantID string, capacity int) (bool, error) {
	var busy bool
	if err := db.QueryRow(ctx, `SELECT count(*) >= $2 FROM bloem_preview_play_grants
		WHERE grant_id <> $1 AND last_delivery_at > now() - make_interval(secs => $3)`, grantID, capacity, FreePlayBusyWindow).Scan(&busy); err != nil {
		return false, fmt.Errorf("counting free play: %w", err)
	}
	return busy, nil
}

// PlaybackGrant names the grant that started a playback session, live or not.
func PlaybackGrant(ctx context.Context, db queryRower, playbackSessionID string) (string, bool, error) {
	var grant string
	err := db.QueryRow(ctx, `SELECT grant_id FROM bloem_preview_play_sessions WHERE playback_session_id = $1`, playbackSessionID).Scan(&grant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading a free-play playback session: %w", err)
	}
	return grant, true, nil
}
