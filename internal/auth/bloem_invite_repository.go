package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// RedeemCodeInTransaction consumes one use on a caller-owned transaction.
func (r *InviteCodeRepository) RedeemCodeInTransaction(ctx context.Context, tx pgx.Tx, code string) error {
	return redeemCode(ctx, tx, code)
}
