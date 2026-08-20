package database

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/migrations"
)

const tenantMemberReceiptPreviousMigration int64 = 20260817080527

func TestTenantMemberReceiptMigrationPromotesLegacyGroupsAndRunsDownUp(t *testing.T) {
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
	var namedGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,is_default)
		VALUES ($1,'Default Group',false) RETURNING id`, legacyTenantID).Scan(&namedGroupID); err != nil {
		t.Fatalf("seed existing non-default named group: %v", err)
	}
	multipleTenantID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations
		(id,slug,name,status,external_operator_id,external_service_id,slots,transcodes)
		VALUES ($1,$2,'Multiple policy tenant','initializing','legacy-operator',$3,2,1)`,
		multipleTenantID, "multiple-"+multipleTenantID.String(), "multiple-service-"+multipleTenantID.String()); err != nil {
		t.Fatalf("seed multiple-group tenant: %v", err)
	}
	var firstGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,is_default)
		VALUES ($1,'First policy',false) RETURNING id`, multipleTenantID).Scan(&firstGroupID); err != nil {
		t.Fatalf("seed first policy group: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default)
		VALUES ($1,'Second policy',false)`, multipleTenantID); err != nil {
		t.Fatalf("seed second policy group: %v", err)
	}
	retiredTenantID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations
		(id,slug,name,status,external_operator_id,slots,transcodes,is_default)
		VALUES ($1,$2,'Retired organization','initializing','legacy-operator',2,1,false)`,
		retiredTenantID, "retired-"+retiredTenantID.String()); err != nil {
		t.Fatalf("seed retired organization: %v", err)
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
	var defaultGroupID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, legacyTenantID).Scan(&defaultGroupID); err != nil || defaultGroupID != namedGroupID {
		t.Fatalf("legacy tenant default group = %d, %v; want existing named group %d", defaultGroupID, err, namedGroupID)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, multipleTenantID).Scan(&defaultGroupID); err != nil || defaultGroupID != firstGroupID {
		t.Fatalf("multiple tenant default group = %d, %v; want lowest existing group %d", defaultGroupID, err, firstGroupID)
	}
	var groups int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1 AND is_default`, defaultTenantID).Scan(&groups); err != nil || groups != 1 {
		t.Fatalf("deployment default tenant groups = %d, %v; want 1", groups, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1`, retiredTenantID).Scan(&groups); err != nil || groups != 0 {
		t.Fatalf("retired organization groups = %d, %v; want 0", groups, err)
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
