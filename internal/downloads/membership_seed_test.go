package downloads

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDefaultOrgMembership gives an account the default-organization membership
// that user_profiles_organization_membership_fkey has required since
// 20260829085838_membership_policy_isolation. Plain INSERT on purpose: an upsert
// would trip guard_membership_policy_write during the compatibility phase.
func seedDefaultOrgMembership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships
			(organization_id, account_id, status, legacy_role, access_group_id)
		SELECT o.id, $1, 'active', 'user', ag.id
		FROM organizations o
		JOIN access_groups ag ON ag.organization_id = o.id AND ag.is_default
		WHERE o.is_default
		ON CONFLICT (organization_id, account_id) DO NOTHING`, userID); err != nil {
		t.Fatalf("seed default organization membership for account %d: %v", userID, err)
	}
}
