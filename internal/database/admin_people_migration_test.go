package database

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

const adminPeoplePreviousVersion int64 = 20260813143000

func TestAdminPeopleMigrationsUpDownUpWithOrganizationAuditRows(t *testing.T) {
	db := newDisposableMigrationDatabase(t)
	runAllMigrations(t, db)
	ctx := context.Background()
	var actorID int
	if err := db.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('admin-people-migration','admin-people-migration@example.test','x','admin',true) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	var organizationID string
	if err := db.QueryRow(ctx, `INSERT INTO organizations (slug,name,status,owner_account_id) VALUES ('admin-people-migration','Admin People Migration','active',$1) RETURNING id::text`, actorID).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome) VALUES ($1,'organization_admin','organization','people.bulk_job_completed','bulk_job','job-1',$2,'42',1,2,'partial_failure')`, actorID, organizationID); err != nil {
		t.Fatal(err)
	}
	var selectionID string
	if err := db.QueryRow(ctx, `INSERT INTO admin_people_selections (id,organization_id,canonical_filter,snapshot_at,account_ids,matched_count,expires_at) VALUES (gen_random_uuid(),$1,'{}',now(),ARRAY[]::integer[],0,now()+interval '15 minutes') RETURNING id::text`, organizationID).Scan(&selectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO admin_jobs (id,job_type,status,created_by_user_id) VALUES ('admin-people-migration-job','organization_people_bulk','completed',$1)`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO admin_people_bulk_jobs (job_id,organization_id,selection_reference,action_kind,action_key,actor_account_id,actor_authority,actor_security_revision,organization_policy_revision) VALUES ('admin-people-migration-job',$1,$2,'suspend_memberships','suspend_memberships:',$3,'platform_admin',1,1)`, organizationID, selectionID, actorID); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDownTo(ctx, db, migrations.FS, "sql", adminPeoplePreviousVersion); err != nil {
		t.Fatalf("admin people down migration: %v", err)
	}
	if err := RunMigrations(ctx, db, migrations.FS, "sql"); err != nil {
		t.Fatalf("admin people reapply: %v", err)
	}
	var hasTargets, hasSelectionReference, hasSelectionID bool
	if err := db.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='admin_people_selections' AND column_name='targets'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='admin_people_bulk_jobs' AND column_name='selection_reference'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='admin_people_bulk_jobs' AND column_name='selection_id')`).Scan(&hasTargets, &hasSelectionReference, &hasSelectionID); err != nil {
		t.Fatal(err)
	}
	if !hasTargets || !hasSelectionReference || hasSelectionID {
		t.Fatal("durable admin people schema not restored")
	}
}
