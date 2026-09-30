package pgstore

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

var _ userstore.ProfileLifecycleTransactioner = (*PostgresUserStore)(nil)

func (s *PostgresUserStore) WithProfileLifecycleTransaction(ctx context.Context, tx pgx.Tx, fn func(userstore.ProfileLifecycleWriter) error) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1, $2)", preferenceSettingsAdvisoryClass, int32(s.userID)); err != nil {
		return fmt.Errorf("locking profile lifecycle transaction: %w", err)
	}
	return fn(&preferenceSettingsTx{exec: tx, userID: s.userID})
}

func (tx *preferenceSettingsTx) GetProfile(ctx context.Context, id string) (*userstore.Profile, error) {
	return getProfile(ctx, tx.exec, tx.userID, id)
}

func (tx *preferenceSettingsTx) DeleteProfile(ctx context.Context, id string) error {
	return deleteProfile(ctx, tx.exec, tx.userID, id)
}
