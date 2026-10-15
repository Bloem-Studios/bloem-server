package database

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestBloemProfileLimitsMigration(t *testing.T) {
	for _, phase := range []string{"compatibility", "mirrored", "finalized"} {
		t.Run(phase, func(t *testing.T) {
			pool := newDisposableMigrationDatabase(t)
			ctx := t.Context()
			provider, err := newMigrationProvider(pool, migrations.BloemFS, "sql")
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
			switch phase {
			case "mirrored":
				if _, err := tenancy.EnableSiloSwitching(ctx, pool); err != nil {
					t.Fatal(err)
				}
			case "finalized":
				if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
					t.Fatal(err)
				}
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
			if phase == "mirrored" {
				// An unmodified Silo writer can insert an inheriting account and
				// change its override without Bloem session markers.
				if _, err := pool.Exec(ctx, `UPDATE users SET max_profiles=3 WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, `SELECT max_profiles FROM organization_memberships WHERE account_id=$1`, id).Scan(&override); err != nil || override == nil || *override != 3 {
					t.Fatalf("mirrored override=%v err=%v", override, err)
				}
				if _, err := pool.Exec(ctx, `UPDATE users SET max_profiles=NULL WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := MigrateDownTo(ctx, pool, migrations.BloemFS, "sql", 20261013231500); err != nil {
				t.Fatalf("profile limit rollback: %v", err)
			}
			var groupColumn bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='access_groups' AND column_name='max_profiles')`).Scan(&groupColumn); err != nil || !groupColumn {
				t.Fatalf("pre-existing group column retained=%t error=%v", groupColumn, err)
			}
			if _, err := provider.UpTo(ctx, 20261013231605); err != nil {
				t.Fatalf("repeat upgrade: %v", err)
			}
		})
	}
}
