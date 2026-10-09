package managedtracking

import (
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestInventoryNeverExportsAnotherTenantProfile(t *testing.T) {
	s, pool, account, _, id, _ := fixture(t)
	ctx := t.Context()
	other := uuid.NewString()
	foreign := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,owner_account_id) VALUES($1,$2,'Other','active',$3)`, other, other, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user')`, other, account); err != nil {
		t.Fatal(err)
	}
	var group int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups(name,organization_id,is_default) VALUES('Foreign default',$1,true) RETURNING id`, other).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id,is_primary) VALUES($1,$2,'Foreign household',$3,$4,false)`, foreign, account, other, group); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListProfiles(ctx, id, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Profiles {
		if p.ProfileID == foreign {
			t.Fatal("cross-tenant profile exported")
		}
	}
}
func TestInventoryRejectsIncompleteForgedExpiredAndOtherInstallationReceipts(t *testing.T) {
	s, pool, account, _, id, tenant := fixture(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) SELECT $1,$2,'Another',$3,id FROM access_groups WHERE organization_id=$3 AND is_default`, uuid.NewString(), account, tenant); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	first, err := s.ListProfiles(ctx, id, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete {
		t.Fatal("fixture needs multi-page inventory")
	}
	if !errors.Is(s.AckChanges(ctx, id, first.Watermark), ErrSnapshot) {
		t.Fatal("incomplete snapshot acknowledged")
	}
	if _, err = s.ListProfiles(ctx, id, first.SnapshotID, uuid.NewString(), 1); !errors.Is(err, ErrSnapshot) {
		t.Fatal("forged cursor accepted")
	}
	var other int
	if err = pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('bloem.pastime','test','fixture') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err = s.Grant(ctx, other, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProfiles(ctx, other, first.SnapshotID, first.NextCursor, 1); !errors.Is(err, ErrSnapshot) {
		t.Fatal("other installation read snapshot")
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_managed_snapshots SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProfiles(ctx, id, first.SnapshotID, first.NextCursor, 1); !errors.Is(err, ErrSnapshot) {
		t.Fatal("expired snapshot accepted")
	}
}
