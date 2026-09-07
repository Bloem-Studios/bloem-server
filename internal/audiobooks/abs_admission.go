package audiobooks

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgsourcegate"
	"github.com/jackc/pgx/v5/pgconn"
)

// AcquireLegacyPlaybackAdmission binds ABS launch and its nested writes to the
// same pool/account gate as first admission. It grants no v2 authority.
func (s *ABSProgressStore) AcquireLegacyPlaybackAdmission(ctx context.Context, account int) (context.Context, func(), error) {
	if s == nil || s.Pool == nil || account <= 0 {
		return ctx, nil, userstore.ErrPlaybackSourceUnavailable
	}
	return pgsourcegate.Legacy(ctx, s.Pool, account)
}

// Playback writes check the source in the transaction that changes progress.
// The explicit finished-only edit retains its separate, unmarked path.
func (s *ABSProgressStore) execProgress(ctx context.Context, account int, query string, args ...any) (pgconn.CommandTag, error) {
	if !userstore.IsLegacyPlaybackWrite(ctx) {
		return s.Pool.Exec(ctx, query, args...)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	rollback := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}
	release, err := pgsourcegate.Shared(ctx, tx, s.Pool, account)
	if err != nil {
		rollback()
		return pgconn.CommandTag{}, err
	}
	defer release()
	defer rollback()
	var admitted bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1)`, account).Scan(&admitted); err != nil {
		return pgconn.CommandTag{}, err
	}
	if admitted {
		return pgconn.CommandTag{}, userstore.ErrPlaybackSourceUnbound
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return tag, err
	}
	return tag, tx.Commit(ctx)
}
