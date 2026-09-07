package planstore

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// lockSourceAdmission orders allocation against the first source marker. Only
// a reservation carrying the captured admission decision may enter afterward.
// A supplied stale decision never falls back to unbound allocation.
func lockSourceAdmission(ctx context.Context, tx pgx.Tx, accountID int, admissionID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('playback-source:'||$1::integer::text,0))`, accountID); err != nil {
		return err
	}
	var marked, admitted bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1),
 EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1 AND admission_state='admitting' AND admission_id::text=$2)`, accountID, admissionID).Scan(&marked, &admitted)
	if err != nil {
		return err
	}
	if admissionID != "" {
		if !admitted {
			return userstore.ErrPlaybackSourceUnavailable
		}
		return nil
	}
	// Include registrations for SQLite, whose marker is in the selected file.
	var registered bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1)`, accountID).Scan(&registered); err != nil {
		return err
	}
	if marked || registered {
		return userstore.ErrPlaybackSourceUnbound
	}
	return nil
}
