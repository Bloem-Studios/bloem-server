package scanner

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// repairTxKey carries an outer catalog-repair transaction through Silo's
// pool-based repair helpers (syncPresentState and
// syncFolderScopedAudioLibraryState) so Bloem can run them atomically, and
// inside the music scanner's locked transaction, without forking their SQL.
type repairTxKey struct{}

func withRepairTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, repairTxKey{}, tx)
}

func repairTxFrom(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(repairTxKey{}).(pgx.Tx)
	return tx
}

func inRepairTx(ctx context.Context) bool {
	return repairTxFrom(ctx) != nil
}

// beginRepairTx opens a savepoint on the carried repair transaction, or a
// fresh pool transaction when none is carried (Silo's behavior).
func (s *Scanner) beginRepairTx(ctx context.Context) (pgx.Tx, error) {
	if tx := repairTxFrom(ctx); tx != nil {
		return tx.Begin(ctx)
	}
	return s.fileRepo.Pool().Begin(ctx)
}

type repairExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// repairExecer returns the carried repair transaction, or the pool.
func (s *Scanner) repairExecer(ctx context.Context) repairExecer {
	if tx := repairTxFrom(ctx); tx != nil {
		return tx
	}
	return s.fileRepo.Pool()
}

// syncFolderScopedAudioLibraryStateAtomic runs the present-state repair and
// the folder-scoped audio root restore in one transaction: a crash part-way
// through must not leave a row with some links cleared and its memberships
// or roots unrepaired.
func (s *Scanner) syncFolderScopedAudioLibraryStateAtomic(ctx context.Context, folderID int) error {
	tx, err := s.fileRepo.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning folder-scoped audio state repair: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.syncFolderScopedAudioLibraryState(withRepairTx(ctx, tx), folderID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing folder-scoped audio state repair: %w", err)
	}
	return nil
}
