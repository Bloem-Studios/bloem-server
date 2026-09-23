package jellycompat

import (
	"context"
	"fmt"
)

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
