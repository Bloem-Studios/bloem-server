package database

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
)

const organizationAdminProjectionPreviousMigration int64 = 20260813170000

func TestOrganizationAdminProjectionMigrationBackfillsAndRunsDownUp(t *testing.T) {
	ctx := context.Background()
	pool := newTenantIdentityDisposableDatabase(t, ctx, requiredPostgresTestDatabaseURL(t))
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", organizationAdminProjectionPreviousMigration); err != nil {
		t.Fatal(err)
	}
	var inviterID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('projection-migration','projection-migration@example.test','x','admin',true) RETURNING id`).Scan(&inviterID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invitations (email,token_hash,role,invited_by,expires_at) VALUES ('legacy@example.test','legacy-projection-hash','user',$1,$2)`, inviterID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO policy_decisions (decision_name,policy_generation,user_id,eval_time_ns,input_digest) VALUES ('silo.action.decision',1,$1,1,'legacy-projection')`, inviterID); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var defaultOrganizationID, invitationOrganizationID, decisionOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT organization_id FROM invitations WHERE token_hash='legacy-projection-hash'`).Scan(&invitationOrganizationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT organization_id FROM policy_decisions WHERE input_digest='legacy-projection'`).Scan(&decisionOrganizationID); err != nil {
		t.Fatal(err)
	}
	if invitationOrganizationID != defaultOrganizationID || decisionOrganizationID != defaultOrganizationID {
		t.Fatalf("backfill invitation=%s decision=%s default=%s", invitationOrganizationID, decisionOrganizationID, defaultOrganizationID)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", organizationAdminProjectionPreviousMigration); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"invitations", "policy_decisions"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='organization_id'`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s organization column after down = %d, %v", table, count, err)
		}
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("second up: %v", err)
	}
}
