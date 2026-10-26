package downloads

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDefaultOrgMembership gives an account the default-organization membership
// that user_profiles_organization_membership_fkey requires, puts it in that
// organization's default group, and allows downloads.
func seedDefaultOrgMembership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT id, $1, 'active', 'user' FROM organizations WHERE is_default
		ON CONFLICT (organization_id, account_id) DO NOTHING`, userID); err != nil {
		t.Fatalf("seed default organization membership for account %d: %v", userID, err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE users SET download_allowed = true,
		       access_group_id = COALESCE(users.access_group_id, ag.id)
		FROM organizations o JOIN access_groups ag ON ag.organization_id = o.id AND ag.is_default
		WHERE o.is_default AND users.id = $1`, userID); err != nil {
		t.Fatalf("seed download policy for account %d: %v", userID, err)
	}
}
