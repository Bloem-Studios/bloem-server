package pgstore

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// SettingMutationWriterInTransaction binds canonical setting writes to a
// caller-owned lifecycle transaction.
func (s *PostgresUserStore) SettingMutationWriterInTransaction(_ context.Context, tx pgx.Tx) userstore.SettingMutationWriter {
	return &postgresSettingMutationWriter{exec: tx, userID: s.userID}
}
