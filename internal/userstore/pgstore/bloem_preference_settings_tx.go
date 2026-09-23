package pgstore

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// PreferenceSettingsWriterInTransaction exposes the canonical preference
// writer on a caller-owned lifecycle transaction.
func (s *PostgresUserStore) PreferenceSettingsWriterInTransaction(
	ctx context.Context, tx pgx.Tx,
) (userstore.PreferenceSettingsWriter, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1, $2)",
		preferenceSettingsAdvisoryClass, int32(s.userID)); err != nil {
		return nil, fmt.Errorf("locking preference settings transaction: %w", err)
	}
	return &preferenceSettingsTx{exec: tx, userID: s.userID}, nil
}
