package pgstore

import (
	"context"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

var _ userstore.PlaybackSourceProvider = (*PostgresProvider)(nil)

type postgresPlaybackSinkHandle struct {
	*PostgresUserStore
	ref    userstore.PlaybackSourceRef
	mu     sync.RWMutex
	closed bool
}

func (h *postgresPlaybackSinkHandle) Source() userstore.PlaybackSourceRef { return h.ref }
func (h *postgresPlaybackSinkHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	return nil
}

func (p *PostgresProvider) OpenPlaybackSink(ctx context.Context, ref userstore.PlaybackSourceRef) (userstore.PlaybackSinkHandle, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Backend != userstore.PlaybackSourcePostgres {
		return nil, userstore.ErrPlaybackSourceMismatch
	}
	if p == nil || p.pool == nil {
		return nil, userstore.ErrPlaybackSourceUnavailable
	}
	handle := &postgresPlaybackSinkHandle{PostgresUserStore: newStore(p.pool, ref.AccountID), ref: ref}
	handle.source = handle
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer rollbackPlaybackSource(ctx, tx)
	if err := handle.checkPlaybackSource(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return handle, nil
}

func rollbackPlaybackSource(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func (s *PostgresUserStore) readSourcePlaybackProgress(ctx context.Context, scope userstore.PlaybackProgressScope) (userstore.PlaybackProgressState, error) {
	var zero userstore.PlaybackProgressState
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return zero, err
	}
	defer rollbackPlaybackSource(ctx, tx)
	if err := s.checkPlaybackSource(ctx, tx); err != nil {
		return zero, err
	}
	state, err := loadPlaybackSink(ctx, tx, s.userID, scope, false)
	if err != nil {
		return zero, err
	}
	if state == nil {
		return zero, userstore.ErrPlaybackSinkNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return *state, nil
}
