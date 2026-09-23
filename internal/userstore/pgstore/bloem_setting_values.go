package pgstore

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// ListSettingValuesForResolutionInTransaction reads through a caller-owned transaction.
func (s *PostgresUserStore) ListSettingValuesForResolutionInTransaction(ctx context.Context, tx pgx.Tx, q userstore.SettingResolutionQuery) ([]userstore.SettingValue, error) {
	return listSettingValuesForResolution(ctx, tx, s.userID, q)
}
