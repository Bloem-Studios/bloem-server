package pgstore

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresUserStore) CollectionOrderRevision(ctx context.Context) (int64, error) {
	if _, err := s.pool.Exec(ctx, `INSERT INTO user_collection_order_revisions(user_id,revision) VALUES($1,1) ON CONFLICT DO NOTHING`, s.userID); err != nil {
		return 0, err
	}
	var revision int64
	err := s.pool.QueryRow(ctx, `SELECT revision FROM user_collection_order_revisions WHERE user_id=$1`, s.userID).Scan(&revision)
	return revision, err
}

func (s *PostgresUserStore) lockCollectionOrder(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `INSERT INTO user_collection_order_revisions(user_id,revision) VALUES($1,1) ON CONFLICT DO NOTHING`, s.userID); err != nil {
		return err
	}
	var revision int64
	return tx.QueryRow(ctx, `SELECT revision FROM user_collection_order_revisions WHERE user_id=$1 FOR UPDATE`, s.userID).Scan(&revision)
}
func (s *PostgresUserStore) checkCollectionRevision(ctx context.Context, tx pgx.Tx, id string, expected *int64) error {
	if expected == nil {
		return nil
	}
	// Every guarded mutation acquires account then collection, including deletes
	// that touch both. Reserving a version also consumes no-op PATCH witnesses.
	if err := s.lockCollectionOrder(ctx, tx); err != nil {
		return err
	}
	var revision int64
	err := tx.QueryRow(ctx, `UPDATE user_collection_revisions SET revision=revision+1 WHERE user_id=$1 AND collection_id=$2 AND (revision=$3 OR $3=-1) AND EXISTS(SELECT 1 FROM user_personal_collections WHERE user_id=$1 AND id=$2) RETURNING revision`, s.userID, id, *expected).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return userstore.ErrCollectionRevisionMismatch
	}
	return err
}
func (s *PostgresUserStore) checkCollectionOrderRevision(ctx context.Context, tx pgx.Tx, expected *int64) error {
	if expected == nil {
		return nil
	}
	if err := s.lockCollectionOrder(ctx, tx); err != nil {
		return err
	}
	var revision int64
	err := tx.QueryRow(ctx, `UPDATE user_collection_order_revisions SET revision=revision+1 WHERE user_id=$1 AND (revision=$2 OR $2=-1) RETURNING revision`, s.userID, *expected).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return userstore.ErrCollectionRevisionMismatch
	}
	return err
}
