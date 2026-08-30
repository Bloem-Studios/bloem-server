package downloads

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedDefaultOrgMembership gives an account the default-organization membership
// that user_profiles_organization_membership_fkey has required since
// 20260829085838_membership_policy_isolation. It carries the v1 membership
// policy marker, which seed_legacy_membership_policy requires once the
// authority is finalized.
func seedDefaultOrgMembership(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin membership seed: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer','v1',true)`); err != nil {
		t.Fatalf("mark membership policy writer: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_memberships
			(organization_id, account_id, status, legacy_role, access_group_id, download_allowed)
		SELECT o.id, $1, 'active', 'user', ag.id, true
		FROM organizations o
		JOIN access_groups ag ON ag.organization_id = o.id AND ag.is_default
		WHERE o.is_default
		ON CONFLICT (organization_id, account_id) DO NOTHING`, userID); err != nil {
		t.Fatalf("seed default organization membership for account %d: %v", userID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit membership seed: %v", err)
	}
}
