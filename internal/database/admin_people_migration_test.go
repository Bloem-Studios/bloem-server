package database

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

const adminPeoplePreviousVersion int64 = 20260813143000

func TestAdminPeopleMigrationsUpDownUpWithOrganizationAuditRows(t *testing.T) {
	db:=newDisposableMigrationDatabase(t)
	runAllMigrations(t,db)
	ctx:=context.Background()
	var actorID int
	if err:=db.QueryRow(ctx,`INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('admin-people-migration','admin-people-migration@example.test','x','admin',true) RETURNING id`).Scan(&actorID);err!=nil{t.Fatal(err)}
	var organizationID string
	if err:=db.QueryRow(ctx,`INSERT INTO organizations (slug,name,status,owner_account_id) VALUES ('admin-people-migration','Admin People Migration','active',$1) RETURNING id::text`,actorID).Scan(&organizationID);err!=nil{t.Fatal(err)}
	if _,err:=db.Exec(ctx,`INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome) VALUES ($1,'organization_admin','organization','people.bulk_job_completed','bulk_job','job-1',$2,'42',1,2,'success')`,actorID,organizationID);err!=nil{t.Fatal(err)}
	if err:=MigrateDownTo(ctx,db,migrations.FS,"sql",adminPeoplePreviousVersion);err!=nil{t.Fatalf("admin people down migration: %v",err)}
	if err:=RunMigrations(ctx,db,migrations.FS,"sql");err!=nil{t.Fatalf("admin people reapply: %v",err)}
	var hasTargets bool
	if err:=db.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='admin_people_selections' AND column_name='targets')`).Scan(&hasTargets);err!=nil{t.Fatal(err)}
	if !hasTargets{t.Fatal("durable admin people schema not restored")}
}
