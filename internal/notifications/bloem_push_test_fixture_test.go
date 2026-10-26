package notifications

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The shared push regression needs live recipients because Bloem rechecks
// organization access before sending a queued notification.
func seedPushTestProfiles(t *testing.T, pool *pgxpool.Pool, accounts []int, profiles []string) {
	t.Helper()
	ctx := t.Context()
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(slug,name,status,owner_account_id)
		VALUES($1,'Push fixture','active',$2) RETURNING id`, "push-"+uuid.NewString(), accounts[0]).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, organizationID)
	})
	var groupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups(organization_id,name,is_default)
		VALUES($1,'Default',true) RETURNING id`, organizationID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, account := range accounts {
		if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
			VALUES($1,$2,'active','user')`, organizationID, account); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, account, groupID); err != nil {
			t.Fatal(err)
		}
		for i, profile := range profiles {
			if _, err := tx.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id,is_primary)
				VALUES($1,$2,$1,$3,$4,$5)`, profile, account, organizationID, groupID, i == 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
