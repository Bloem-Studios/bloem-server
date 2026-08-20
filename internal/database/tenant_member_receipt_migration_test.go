package database

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/migrations"
)

const tenantMemberReceiptPreviousMigration int64 = 20260817080527

func TestTenantMemberReceiptMigrationBackfillsLegacyTenantAndRunsDownUp(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("initial up: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", tenantMemberReceiptPreviousMigration); err != nil {
		t.Fatalf("prepare legacy schema: %v", err)
	}
	legacyTenantID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations
		(id,slug,name,status,external_operator_id,external_service_id,slots,transcodes)
		VALUES ($1,$2,'Legacy tenant','initializing','legacy-operator',$3,2,1)`,
		legacyTenantID, "legacy-"+legacyTenantID.String(), "legacy-service-"+legacyTenantID.String()); err != nil {
		t.Fatalf("seed legacy tenant: %v", err)
	}
	var defaultTenantID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultTenantID); err != nil {
		t.Fatalf("load deployment default tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM access_groups WHERE organization_id=$1`, defaultTenantID); err != nil {
		t.Fatalf("remove deployment default group before backfill: %v", err)
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("receipt migration up: %v", err)
	}
	var groups int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1 AND is_default`, legacyTenantID).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("legacy tenant default groups = %d, %v; want 1", groups, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1 AND is_default`, defaultTenantID).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("deployment default tenant groups = %d, %v; want 1", groups, err)
	}
	var receiptTable bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.tenant_member_command_receipts') IS NOT NULL`).Scan(&receiptTable); err != nil || !receiptTable {
		t.Fatalf("receipt table after up = %v, %v", receiptTable, err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", tenantMemberReceiptPreviousMigration); err != nil {
		t.Fatalf("receipt migration down: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.tenant_member_command_receipts') IS NOT NULL`).Scan(&receiptTable); err != nil || receiptTable {
		t.Fatalf("receipt table after down = %v, %v", receiptTable, err)
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("second receipt migration up: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1 AND is_default`, legacyTenantID).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("idempotent default groups = %d, %v; want 1", groups, err)
	}
}
