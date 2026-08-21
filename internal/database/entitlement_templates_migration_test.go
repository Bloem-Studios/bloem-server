package database

import (
	"context"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
)

const entitlementPolicyCohortsPreviousMigration int64 = 20260821210000

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

func TestEntitlementPolicyCohortsMigrationRunsDownAndUp(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("initial up: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", entitlementPolicyCohortsPreviousMigration); err != nil {
		t.Fatalf("cohorts down: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.entitlement_policy_cohorts') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("entitlement_policy_cohorts still exists after down migration")
	}
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("cohorts re-up: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.entitlement_policy_cohort_revisions') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("entitlement_policy_cohort_revisions missing after re-up migration")
	}
}

func TestEntitlementPolicyCohortsMigrationDownRefusesDerivedCohort(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("initial up: %v", err)
	}
	var organizationID uuid.UUID
	var groupID int64
	if err := pool.QueryRow(ctx, `
		SELECT o.id,g.id
		FROM organizations o
		JOIN access_groups g ON g.organization_id=o.id AND g.is_default
		WHERE o.is_default`).Scan(&organizationID, &groupID); err != nil {
		t.Fatal(err)
	}
	var actorID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,role,enabled,access_group_id)
		VALUES ($1,'admin',true,$2) RETURNING id`, "cohort-migration-"+uuid.NewString(), groupID).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	var derivedGroupID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (
			organization_id,name,library_ids,playback_allowed,max_streams,max_profiles,
			transcode_allowed,max_transcodes,download_allowed,download_transcode_allowed,
			max_playback_quality,allowed_permissions,requests_allowed,
			managed_template_key,managed_template_revision
		) VALUES ($1,$2,ARRAY[]::integer[],true,2,5,true,1,false,false,'1080p',NULL,true,'standard',1)
		RETURNING id`, organizationID, "Derived migration "+uuid.NewString()).Scan(&derivedGroupID); err != nil {
		t.Fatal(err)
	}
	cohortID := uuid.New()
	revisionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_policy_cohorts (id,organization_id,name) VALUES ($1,$2,'Derived migration')`, cohortID, organizationID); err != nil {
		t.Fatal(err)
	}
	parentCohortID := uuid.New()
	parentRevisionID := uuid.New()
	parentGroupID := groupID
	if _, err := pool.Exec(ctx, `
		UPDATE access_groups
		SET managed_template_key='standard',managed_template_revision=1
		WHERE id=$1`, parentGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_policy_cohorts (id,organization_id,name) VALUES ($1,$2,'Exact migration')`, parentCohortID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_policy_cohort_revisions (
			id,cohort_id,organization_id,name,revision,access_group_id,
			source_template_key,source_template_revision,derivation_kind,
			library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
			max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,
			allowed_permissions,requests_allowed,policy_digest,created_by_account_id
		) VALUES (
			$1,$2,$3,'Exact migration',1,$4,'standard',1,'exact_template',
			ARRAY[]::integer[],true,3,5,true,1,true,true,'1080p',NULL,true,repeat('a',64),$5
		)`, parentRevisionID, parentCohortID, organizationID, parentGroupID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access_groups SET managed_cohort_id=$2 WHERE id=$1`, parentGroupID, parentRevisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entitlement_policy_cohort_revisions (
			id,cohort_id,organization_id,name,revision,access_group_id,
			source_template_key,source_template_revision,parent_id,derivation_kind,
			library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
			max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,
			allowed_permissions,requests_allowed,policy_digest,created_by_account_id
		) VALUES (
			$1,$2,$3,'Derived migration',1,$4,'standard',1,$5,'policy_patch',
			ARRAY[]::integer[],true,2,5,true,1,false,false,'1080p',NULL,true,repeat('b',64),$6
		)`, revisionID, cohortID, organizationID, derivedGroupID, parentRevisionID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE access_groups SET managed_cohort_id=$2 WHERE id=$1`, derivedGroupID, revisionID); err != nil {
		t.Fatal(err)
	}

	err := MigrateDownTo(ctx, pool, migrations.FS, "sql", entitlementPolicyCohortsPreviousMigration)
	if err == nil || !strings.Contains(err.Error(), "derived entitlement policy cohorts exist") {
		t.Fatalf("cohorts down error = %v, want derived-cohort refusal", err)
	}
}
