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

func TestBuiltInEntitlementTemplatesAreEnabledAfterMigration(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", 20260821175101); err != nil {
		t.Fatalf("roll back built-in enablement migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE entitlement_templates
		SET enabled = false
		WHERE key = ANY($1::text[])`, []string{"browse-only", "viewer", "standard", "premium", "reseller-member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_templates (key, name, current_revision, enabled, archived)
		VALUES ('custom-disabled', 'Custom disabled', 1, false, false)`); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("reapply built-in enablement migration: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT key, enabled
		FROM entitlement_templates
		WHERE key = ANY($1::text[])
		ORDER BY key`, []string{"browse-only", "premium", "reseller-member", "standard", "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var key string
		var enabled bool
		if err := rows.Scan(&key, &enabled); err != nil {
			t.Fatal(err)
		}
		got[key] = enabled
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"browse-only", "viewer", "standard", "premium", "reseller-member"} {
		if !got[key] {
			t.Errorf("built-in template %q is not enabled after migration", key)
		}
	}
	var customEnabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM entitlement_templates WHERE key = 'custom-disabled'`).Scan(&customEnabled); err != nil {
		t.Fatal(err)
	}
	if customEnabled {
		t.Error("custom disabled template was enabled by the built-in-template migration")
	}
}
