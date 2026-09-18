package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ActivateInitialOwnershipInTransaction preserves Silo's atomic initial-setup
// boundary when adapting Bloem's ownership state result to the auth interface.
func (b tenancyOwnershipBootstrapper) ActivateInitialOwnershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int) error {
	_, err := b.store.ActivateInitialOwnershipInTransaction(ctx, tx, accountID)
	return err
}
