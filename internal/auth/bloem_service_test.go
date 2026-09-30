package auth

import "context"

func (r *setupUserRepository) ReplaceTemporaryPassword(ctx context.Context, id int, old, next, _ string) error {
	return r.CompareAndSwapPassword(ctx, id, old, next)
}
