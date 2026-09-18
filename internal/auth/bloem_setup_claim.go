package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ClaimInitialSetupInTransaction applies the same deployment-wide fence when
// the lifecycle coordinator, rather than ClaimInitialSetup, owns the transaction.
// A preauth lifecycle actor is not an account lock, and distinct setup requests
// or API surfaces must still serialize before checking account cardinality.
func (r *UserRepository) ClaimInitialSetupInTransaction(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", InitialSetupAdvisoryLock); err != nil {
		return fmt.Errorf("acquiring initial setup lock: %w", err)
	}
	count, err := r.CountInTransaction(ctx, tx)
	if err != nil {
		return fmt.Errorf("counting users: %w", err)
	}
	if count != 0 {
		return ErrSetupAlreadyComplete
	}
	return nil
}
