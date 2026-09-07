package planstore

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgsourcegate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockSourceAdmission orders allocation against the first source marker. Only
// a reservation carrying the captured admission decision may enter afterward.
// A supplied stale decision never falls back to unbound allocation.
func lockSourceAdmission(ctx context.Context, tx pgx.Tx, pool *pgxpool.Pool, accountID int, admissionID string) (func(), error) {
	release, err := pgsourcegate.Shared(ctx, tx, pool, accountID)
	if err != nil {
		return release, err
	}

	if admissionID != "" {
		// Registration precedes attempt row locking, matching initial activation.
		// Withdrawal cannot race this reservation's captured admission decision.
		var current string
		err := tx.QueryRow(ctx, `SELECT admission_id::text FROM playback_source_registrations WHERE user_id=$1 AND admission_state='admitting' FOR UPDATE`, accountID).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			return release, userstore.ErrPlaybackSourceUnavailable
		}
		if err != nil {
			return release, err
		}
		if current != admissionID {
			return release, userstore.ErrPlaybackSourceUnavailable
		}
		return release, nil
	}
	var marked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1)`, accountID).Scan(&marked); err != nil {
		return release, err
	}

	// Include registrations for SQLite, whose marker is in the selected file.
	var registered bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1)`, accountID).Scan(&registered); err != nil {
		return release, err
	}
	if marked || registered {
		return release, userstore.ErrPlaybackSourceUnbound
	}
	return release, nil
}
