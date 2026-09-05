package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

type playbackSourceMarker struct {
	sourceID   string
	generation int64
	gate       string
}

// The marker lock precedes profile history and receipt locks. A source gate
// update cannot commit until every writer that observed the old gate completes.
func lockPlaybackSourceMarker(ctx context.Context, tx pgx.Tx, userID int) (*playbackSourceMarker, error) {
	// Provisioning a previously absent marker must take this account key exclusively.
	// The shared lock closes the absent-row race without serializing ordinary readers.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('playback-source:' || $1::integer::text, 0))`, userID); err != nil {
		return nil, fmt.Errorf("lock playback source account: %w", err)
	}
	var marker playbackSourceMarker
	err := tx.QueryRow(ctx, `SELECT source_id::text,selection_generation,gate FROM playback_source_markers WHERE user_id=$1 FOR SHARE`, userID).Scan(&marker.sourceID, &marker.generation, &marker.gate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock playback source marker: %w", err)
	}
	return &marker, nil
}

func (s *PostgresUserStore) checkPlaybackSource(ctx context.Context, tx pgx.Tx) error {
	marker, err := lockPlaybackSourceMarker(ctx, tx, s.userID)
	if err != nil {
		return err
	}
	if s.source == nil {
		if marker != nil {
			return userstore.ErrPlaybackSourceUnbound
		}
		return nil
	}
	if marker == nil {
		return userstore.ErrPlaybackSourceUnavailable
	}
	ref := s.source.ref
	if marker.sourceID != ref.SourceID || marker.generation != ref.SelectionGeneration {
		return userstore.ErrPlaybackSourceMismatch
	}
	if marker.gate != "writable" {
		return userstore.ErrPlaybackSourceUnavailable
	}
	return nil
}
