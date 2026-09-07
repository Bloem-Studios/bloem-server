package pgstore

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgsourcegate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type progressWriteExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// The marker check and the actual playback-origin write share a transaction.
// First admission takes the exclusive account gate, ordering even writers that
// observed no marker. A delayed unbound callback cannot write after admission.
func (s *PostgresUserStore) writeProgress(ctx context.Context, write func(progressWriteExecutor) error) error {
	if !userstore.IsLegacyPlaybackWrite(ctx) {
		return write(s.pool)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	release, err := pgsourcegate.Shared(ctx, tx, s.pool, s.userID)
	if err != nil {
		rollbackPlaybackSource(ctx, tx)
		return err
	}
	defer release()
	defer rollbackPlaybackSource(ctx, tx)
	marker, err := readPlaybackSourceMarker(ctx, tx, s.userID)
	if err != nil {
		return err
	}
	if marker != nil {
		return userstore.ErrPlaybackSourceUnbound
	}
	if err := write(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresUserStore) execProgress(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	err := s.writeProgress(ctx, func(exec progressWriteExecutor) error {
		var err error
		tag, err = exec.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

func (p *PostgresProvider) AcquireLegacyPlaybackAdmission(ctx context.Context, accountID int) (context.Context, func(), error) {
	if p == nil || p.pool == nil || accountID <= 0 {
		return ctx, nil, userstore.ErrPlaybackSourceUnavailable
	}
	return pgsourcegate.Legacy(ctx, p.pool, accountID)
}
