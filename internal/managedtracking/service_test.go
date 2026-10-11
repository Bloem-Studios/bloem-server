package managedtracking

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Service, *pgxpool.Pool, int, string, int, string) {
	t.Helper()
	dsn := os.Getenv("BLOEM_MANAGED_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("explicit task-owned database required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || !strings.HasPrefix(cfg.ConnConfig.Database, "bloem_managed_test") {
		t.Fatal("refusing nonfixture database")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	u, err := auth.NewUserRepository(pool).CreateInTransaction(ctx, tx, models.CreateUserInput{Username: "managed-" + uuid.NewString(), Email: uuid.NewString() + "@example.test", Password: "synthetic-managed-test-only", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,owner_account_id) VALUES($1,$2,'Synthetic tracking tenant','active',$3)`, tenant, tenant, u.ID); err != nil {
		t.Fatal(err)
	}
	var group int64
	if err = tx.QueryRow(ctx, `INSERT INTO access_groups(name,organization_id,is_default) VALUES('Synthetic default',$1,true) RETURNING id`, tenant).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role,access_group_id) VALUES($1,$2,'active','user',$3)`, tenant, u.ID, group); err != nil {
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
		t.Fatal(err)
	}
	return s, pool, u.ID, profile, install, tenant
}

func TestSnapshotFreezesRenameAndAcknowledgesOnlyTraversedWatermark(t *testing.T) {
	s, pool, account, profile, install, tenant := fixture(t)
	ctx := t.Context()
	first, err := s.ListProfiles(ctx, install, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Add later profile after the snapshot is created. It must become later work.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	child := uuid.NewString()
	if err = pgstore.NewPostgresProvider(pool).CreateProfileInTransaction(ctx, tx, account, userstore.Profile{ID: child, Name: "Child", OrganizationID: tenant}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE user_profiles SET name='Renamed' WHERE id=$1`, profile); err != nil {
		t.Fatal(err)
	}
	pages := first
	found := ""
	for {
		for _, p := range pages.Profiles {
			if p.ProfileID == profile {
				found = p.Name
			}
			if p.ProfileID == child {
				t.Fatal("new profile leaked into frozen snapshot")
			}
		}
		if pages.Complete {
			break
		}
		pages, err = s.ListProfiles(ctx, install, first.SnapshotID, pages.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if found != "Main" {
		t.Fatalf("frozen name=%q", found)
	}
	if err = s.AckChanges(ctx, install, first.Watermark); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ReadChanges(ctx, install)
	if err != nil || !pending {
		t.Fatalf("newer work disappeared: %v %v", pending, err)
	}
	fresh, err := s.ListProfiles(ctx, install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, p := range fresh.Profiles {
		if p.ProfileID == child {
			seen = true
		}
	}
	if !seen {
		t.Fatal("new profile not enrolled in later inventory")
	}
}

func TestRollbackHasNoLifecycleEffectAndRevokedGrantDeniesSnapshot(t *testing.T) {
	s, pool, _, profile, install, _ := fixture(t)
	ctx := t.Context()
	before, err := s.ListProfiles(ctx, install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE user_profiles SET name='Must not survive' WHERE id=$1`, profile); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.ListProfiles(ctx, install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AckChanges(ctx, install, before.Watermark); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ReadChanges(ctx, install)
	if err != nil || pending {
		t.Fatalf("rollback published lifecycle work: %v %v", pending, err)
	}
	for _, p := range after.Profiles {
		if p.ProfileID == profile && p.Name != "Main" {
			t.Fatal("rollback changed profile")
		}
	}
	if err = s.AckChanges(ctx, install, "forged"); err == nil {
		t.Fatal("forged acknowledgement accepted")
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, install); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProfiles(ctx, install, before.SnapshotID, "", 100); err == nil {
		t.Fatal("revoked installation reads snapshot")
	}
}
