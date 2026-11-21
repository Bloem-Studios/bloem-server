package database

import (
	"io/fs"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

// The historical profile-limit migration runs on a fresh database before the
// account policy moves back onto users (20261026003000), while the membership
// still holds it: a default override inherits the group limit.
func TestBloemProfileLimitsMigration(t *testing.T) {
	testBloemProfileLimitsMigration(t, migrations.BloemFS)
}

// cmd/silo's --migrate-status and --migrate-down-to pass Silo's unadapted
// migrations.FS, as Silo does; the rollback still runs Bloem's adapted
// migration, because every migration path adapts the directory it opens.
func TestBloemProfileLimitsMigrationFromSiloFS(t *testing.T) {
	testBloemProfileLimitsMigration(t, migrations.FS)
}

func testBloemProfileLimitsMigration(t *testing.T, fsys fs.FS) {
	t.Helper()
	pool := newDisposableMigrationDatabase(t)
	ctx := t.Context()
	provider, err := newMigrationProvider(pool, fsys, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.UpTo(ctx, 20261013231500); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,max_profiles) VALUES('limits','limits@example.test','x','user',5) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) SELECT id,$1,'active','user' FROM organizations WHERE is_default`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261013231605); err != nil {
		t.Fatalf("profile limit upgrade: %v", err)
	}
	pool.Reset()
	var override *int
	if err := pool.QueryRow(ctx, `SELECT max_profiles FROM organization_memberships WHERE account_id=$1`, id).Scan(&override); err != nil {
		t.Fatal(err)
	}
	if override != nil {
		t.Fatalf("default override = %v; want inherit", *override)
	}
	if err := MigrateDownTo(ctx, pool, fsys, "sql", 20261013231500); err != nil {
		t.Fatalf("profile limit rollback: %v", err)
	}
	var groupColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='access_groups' AND column_name='max_profiles')`).Scan(&groupColumn); err != nil || !groupColumn {
		t.Fatalf("pre-existing group column retained=%t error=%v", groupColumn, err)
	}
	if _, err := provider.UpTo(ctx, 20261013231605); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
}
