package requests

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// UpsertUserLimitInTransaction includes the mutation in the lifecycle receipt transaction.
func (r *Repository) UpsertUserLimitInTransaction(ctx context.Context, tx pgx.Tx, limit UserLimit) (*UserLimit, error) {
	return r.upsertUserLimit(ctx, tx, limit, -1)
}

// InstallationPluginID names the plugin behind an installation, for the
// audiobook lane guard (bloem_lane_integration_guard.go).
func (r *Repository) InstallationPluginID(ctx context.Context, installationID int) (string, error) {
	var plugin string
	err := r.pool.QueryRow(ctx, `SELECT plugin_id FROM plugin_installations WHERE id=$1`, installationID).Scan(&plugin)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return plugin, err
}
