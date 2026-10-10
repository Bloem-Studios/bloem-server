package jellycompat

import (
	"context"
	"fmt"
)

// Keep the production repository aligned with the store's optional Bloem
// revocation interfaces when upstream changes its session method set.
var (
	_ sessionPersistence              = (*SessionRepository)(nil)
	_ sessionUserDeletePersistence    = (*SessionRepository)(nil)
	_ sessionProfileDeletePersistence = (*SessionRepository)(nil)
)

// DeleteByUserID removes an account's compat sessions for Bloem revocation
// paths. Upstream sign-out already removes these rows in its transaction;
// deleting an already-revoked account succeeds so cache eviction can proceed.
func (r *SessionRepository) DeleteByUserID(ctx context.Context, userID int) (int, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM jellycompat_sessions WHERE streamapp_user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete compat sessions by user: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteByUserAndProfileIDs removes compat sessions attributed to a tenant's
// profiles without affecting the user's other tenants or account-mode login.
func (r *SessionRepository) DeleteByUserAndProfileIDs(ctx context.Context, userID int, profileIDs []string) (int, error) {
	if len(profileIDs) == 0 {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM jellycompat_sessions
		WHERE streamapp_user_id = $1 AND profile_id = ANY($2)`, userID, profileIDs)
	if err != nil {
		return 0, fmt.Errorf("delete compat sessions by user and profile: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
