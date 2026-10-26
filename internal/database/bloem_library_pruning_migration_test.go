package database

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

// The historical library-pruning migration runs on a fresh database before
// the account policy moves back onto users (20261026003000), while the
// membership still holds it.
func TestBloemLibraryPruningMigration(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	ctx := t.Context()
	provider, err := newMigrationProvider(pool, migrations.BloemFS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.UpTo(ctx, 20261012231339); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_folders(id,type,name) VALUES(2000000000,'movies','Library')`); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,library_ids) VALUES('prune','prune@example.test','x','user',ARRAY[2000000000,2000000001]::bigint[]) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) SELECT id,$1,'active','user' FROM organizations WHERE is_default`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261013021242); err != nil {
		t.Fatalf("prune upgrade: %v", err)
	}
	check := func(want int) {
		t.Helper()
		var ids []int64
		if err := pool.QueryRow(ctx, `SELECT library_ids FROM organization_memberships WHERE account_id=$1`, id).Scan(&ids); err != nil {
			t.Fatal(err)
		}
		if ids == nil || len(ids) != want {
			t.Fatalf("allowlist=%v want %d entries without NULL", ids, want)
		}
	}
	check(1)
	var width string
	if err := pool.QueryRow(ctx, `SELECT pg_typeof(library_ids)::text FROM organization_memberships WHERE account_id=$1`, id).Scan(&width); err != nil || width != "integer[]" {
		t.Fatalf("Silo policy width=%q error=%v", width, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=2000000000; DELETE FROM media_folders WHERE id=2000000000`); err != nil {
		t.Fatalf("delete library: %v", err)
	}
	check(0)
}

func TestBloemLibraryPolicyWidthRejectsOverflow(t *testing.T) {
	pool := newDisposableMigrationDatabase(t)
	ctx := t.Context()
	provider, err := newMigrationProvider(pool, migrations.BloemFS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.UpTo(ctx, 20261012231339); err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(migrations.FS, "sql/20260919095615_bloem_policy_library_ids_bigint.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Rehearse a database widened by the earlier Bloem release.
	if _, err := pool.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role,library_ids) VALUES('overflow','overflow@example.test','x','user',ARRAY[3000000000]::bigint[]) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 20261013021242); err == nil {
		t.Fatal("overflow migration succeeded")
	}
	var ids []int64
	if err := pool.QueryRow(ctx, `SELECT library_ids FROM users WHERE id=$1`, id).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 3000000000 {
		t.Fatalf("overflow refusal changed stored policy: %v", ids)
	}
}
