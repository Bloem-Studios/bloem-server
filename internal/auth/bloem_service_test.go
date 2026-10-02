package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *setupUserRepository) ReplaceTemporaryPassword(ctx context.Context, id int, old, next, _ string) error {
	return r.CompareAndSwapPassword(ctx, id, old, next)
}

// Setup unit tests model only the owner update; unexpected SQL fails closed.
type setupTransaction struct{ pgx.Tx }

func (setupTransaction) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	if query != "UPDATE users SET is_owner = true, break_glass = true WHERE id = $1" {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected setup SQL: %s", query)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
