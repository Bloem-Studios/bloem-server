package requests

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UpsertUserLimitInTransaction includes the mutation in the lifecycle receipt transaction.
func (r *Repository) UpsertUserLimitInTransaction(ctx context.Context, tx pgx.Tx, limit UserLimit) (*UserLimit, error) {
	return r.upsertUserLimit(ctx, tx, limit, -1)
}
