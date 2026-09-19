package handlers

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// bloemDeleteFixtureLibraries releases only the entitlements created for the
// caller's fixture libraries. Production deliberately restricts their deletion.
func bloemDeleteFixtureLibraries(ctx context.Context, pool *pgxpool.Pool, ids ...int) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fixture library cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=ANY($1)`, ids); err != nil {
		return fmt.Errorf("delete fixture library entitlements: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_folders WHERE id=ANY($1)`, ids); err != nil {
		return fmt.Errorf("delete fixture libraries: %w", err)
	}
	return tx.Commit(ctx)
}
