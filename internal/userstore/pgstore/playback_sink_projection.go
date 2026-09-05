package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func readPlaybackProjection(ctx context.Context, tx pgx.Tx, userID int, profileID, targetID string) (*userstore.WatchProgress, error) {
	progress, err := scanWatchProgress(tx.QueryRow(ctx, progressListSelect+` AND media_item_id = $3 FOR UPDATE`, userID, profileID, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return progress, err
}

// writePlaybackProjection runs only after the sink receipt/fence check. Keep
// watched latching and hidden-history visibility identical to online progress.
func writePlaybackProjection(ctx context.Context, tx pgx.Tx, userID int, profileID, targetID string, position, duration float64, completed bool, at time.Time) error {
	_, err := tx.Exec(ctx, `
		WITH visible AS (
			SELECT CASE WHEN h.hidden_before IS NOT NULL AND $7::timestamptz <= h.hidden_before
				THEN h.hidden_before + interval '1 second' ELSE $7::timestamptz END AS at
			FROM (SELECT 1) seed LEFT JOIN user_history_hidden_items h
			ON h.user_id=$1 AND h.profile_id=$2 AND h.media_item_id=$3
		)
		INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds,completed,updated_at)
		SELECT $1,$2,$3,$4,$5,$6,at FROM visible
		ON CONFLICT(user_id,profile_id,media_item_id) DO UPDATE SET
			position_seconds=excluded.position_seconds,duration_seconds=excluded.duration_seconds,
			completed=user_watch_progress.completed OR excluded.completed,
			updated_at=excluded.updated_at,event_at=excluded.updated_at`,
		userID, profileID, targetID, position, duration, completed, at)
	if err != nil {
		return fmt.Errorf("write playback progress projection: %w", err)
	}
	return nil
}

func writePlaybackHints(ctx context.Context, tx pgx.Tx, userID int, profileID, targetID string, hints userstore.VersionHints) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE user_watch_progress
		SET last_file_id=$4,last_resolution=$5,last_hdr=$6,last_codec_video=$7,last_edition_key=$8
		WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`,
		userID, profileID, targetID, hints.FileID, hints.Resolution, hints.HDR, hints.CodecVideo, nilIfEmpty(hints.EditionKey))
	if err != nil {
		return false, fmt.Errorf("write playback progress hints: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func writePlaybackHistory(ctx context.Context, tx pgx.Tx, userID int, entry userstore.WatchHistoryEntry) (userstore.WatchHistoryEntry, error) {
	identity, err := json.Marshal(entry.Identity)
	if err != nil {
		return entry, fmt.Errorf("marshal playback history identity: %w", err)
	}
	var at time.Time
	err = tx.QueryRow(ctx, `
		WITH visible AS (
			SELECT CASE WHEN h.hidden_before IS NOT NULL AND $5::timestamptz <= h.hidden_before
				THEN h.hidden_before + interval '1 second' ELSE $5::timestamptz END AS at
			FROM (SELECT 1) seed LEFT JOIN user_history_hidden_items h
			ON h.user_id=$2 AND h.profile_id=$3 AND h.media_item_id=$4
		)
		INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,watched_at,duration_seconds,completed,source,watch_identity)
		SELECT $1,$2,$3,$4,at,$6,$7,$8,$9 FROM visible RETURNING watched_at`,
		entry.ID, userID, entry.ProfileID, entry.MediaItemID, entry.WatchedAt, entry.DurationSeconds, entry.Completed, entry.Source, string(identity)).Scan(&at)
	if err != nil {
		return entry, fmt.Errorf("write playback history: %w", err)
	}
	entry.WatchedAt = timeToString(at)
	return entry, nil
}
