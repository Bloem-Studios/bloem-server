package database

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

func TestEntitlementTemplatesMigrationRunsDownAndUp(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("initial up: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", 20260820120000); err != nil {
		t.Fatalf("entitlements down: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.entitlement_templates') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("entitlement_templates still exists after down migration")
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("entitlements re-up: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.entitlement_apply_receipts') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("entitlement_apply_receipts missing after re-up migration")
	}
}
