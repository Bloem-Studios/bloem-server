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
	return r.complete(ctx, lease, authorize, finalize)
}
