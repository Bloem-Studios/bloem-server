package managedtracking

import (
	"context"
	"strconv"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
)

// A default organization without exactly one owner stays 'initializing', and
// Bloem serves it in that state. Its accounts must be grantable and projected
// active, or managed tracking can never enroll a single-organization server.
func TestInitializingDefaultOrganizationIsEnrollable(t *testing.T) {
	_, pool, _, _, _, _ := fixture(t)
	ctx := t.Context()
	var tenant, status string
	if err := pool.QueryRow(ctx, `SELECT id::text,status FROM organizations WHERE is_default`).Scan(&tenant, &status); err != nil {
		t.Fatal(err)
	}
	if status != "initializing" {
		t.Skipf("fixture default organization is %q, not initializing", status)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	u, err := auth.NewUserRepository(pool).CreateInTransaction(ctx, tx, models.CreateUserInput{Username: "managed-default-" + uuid.NewString(), Email: uuid.NewString() + "@example.test", Password: "synthetic-managed-test-only", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	var group int64
	if err = tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, tenant).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role,access_group_id) VALUES($1,$2,'active','user',$3)
 ON CONFLICT DO NOTHING`, tenant, u.ID, group); err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if err = pgstore.NewPostgresProvider(pool).CreateProfileInTransaction(ctx, tx, u.ID, userstore.Profile{ID: profile, Name: "Main", IsPrimary: true, OrganizationID: tenant, AccessGroupID: &group}); err != nil {
		t.Fatal(err)
	}
	var install int
	if err = tx.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('bloem.pastime','test','fixture') RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Service{Pool: pool}
	if err = s.Grant(ctx, install, tenant); err != nil {
		t.Fatalf("grant initializing default organization: %v", err)
	}
	page, err := s.ListProfiles(ctx, install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	account := strconv.Itoa(u.ID)
	for {
		for _, p := range page.Profiles {
			if p.ProfileID == profile {
				if p.AccountID != account || !p.AccountActive || !p.Active {
					t.Fatalf("default organization profile projected inactive: %+v", p)
				}
				return
			}
		}
		if page.Complete {
			t.Fatal("default organization profile missing from inventory")
		}
		if page, err = s.ListProfiles(ctx, install, page.SnapshotID, page.NextCursor, 100); err != nil {
			t.Fatal(err)
		}
	}
}
