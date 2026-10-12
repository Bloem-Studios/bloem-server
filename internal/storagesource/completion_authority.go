package storagesource

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CompletionAuthorizer retains current resource authority before the journal
// acquires its source and run locks, through finalization and commit.
type CompletionAuthorizer func(context.Context, pgx.Tx) error

// CompletionFinalizer performs catalog cleanup in the journal's completion
// transaction. Provider and artwork I/O must stay outside this transaction.
type CompletionFinalizer func(context.Context, pgx.Tx) error

// CompleteAuthorized commits successful discovery and catalog finalization
// together under retained resource authority. Standalone discovery may use
// Complete without a catalog authorizer or finalizer.
func (r *Repository) CompleteAuthorized(ctx context.Context, lease Lease, authorize CompletionAuthorizer, finalize CompletionFinalizer) error {
	if authorize == nil {
		return fmt.Errorf("storage completion authority required")
	}
	return r.complete(ctx, lease, authorize, finalize, nil)
}

// CompleteLocationAuthorized returns newly missing files only after the
// location graph and the caller's finalizer have committed together.
func (r *Repository) CompleteLocationAuthorized(ctx context.Context, lease Lease, authorize CompletionAuthorizer, finalize CompletionFinalizer) (int, error) {
	if authorize == nil {
		return 0, fmt.Errorf("storage completion authority required")
	}
	var missing int
	err := r.complete(ctx, lease, authorize, finalize, &missing)
	if err != nil {
		return 0, err
	}
	return missing, nil
}
