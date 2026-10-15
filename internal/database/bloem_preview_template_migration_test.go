package database

import (
	"strings"
	"testing"
)

func TestBloemPreviewTemplateMigrationPreservesCuratedHistoryOnReapply(t *testing.T) {
	ctx := t.Context()
	pool := newDisposableMigrationDatabase(t)
	migrationFS := compatibilityBaselineFS(t)
	if err := RunMigrations(ctx, pool, migrationFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlement_template_revisions
  (template_key,revision,library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,allowed_permissions,requests_allowed)
  SELECT template_key,2,'{}'::integer[],playback_allowed,max_streams,max_profiles,transcode_allowed,max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,allowed_permissions,requests_allowed
  FROM entitlement_template_revisions WHERE template_key='bloem-preview' AND revision=1;
  UPDATE entitlement_templates SET current_revision=2 WHERE key='bloem-preview'`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDownTo(ctx, pool, migrationFS, "sql", 20261013231605); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(ctx, pool, migrationFS, "sql"); err != nil {
		t.Fatalf("reapply protected preview: %v", err)
	}
	var revision, count int
	if err := pool.QueryRow(ctx, `SELECT current_revision,(SELECT count(*) FROM entitlement_template_revisions WHERE template_key='bloem-preview') FROM entitlement_templates WHERE key='bloem-preview'`).Scan(&revision, &count); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || count != 2 {
		t.Fatalf("revision=%d history rows=%d; want curated revision 2 with both snapshots preserved", revision, count)
	}
}

func TestBloemPreviewTemplateMigrationRefusesUnownedReservedKey(t *testing.T) {
	ctx := t.Context()
	pool := newDisposableMigrationDatabase(t)
	migrationFS := compatibilityBaselineFS(t)
	provider, err := newMigrationProvider(pool, migrationFS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err = provider.UpTo(ctx, 20261013231605); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO entitlement_templates (key,name,current_revision,enabled,archived) VALUES ('bloem-preview','Existing operator template',1,true,false)`); err != nil {
		t.Fatal(err)
	}
	err = RunMigrations(ctx, pool, migrationFS, "sql")
	if err == nil || !strings.Contains(err.Error(), "is reserved for the preview template") {
		t.Fatalf("reserved-key collision error = %v", err)
	}
	var name string
	if err = pool.QueryRow(ctx, `SELECT name FROM entitlement_templates WHERE key='bloem-preview'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Existing operator template" {
		t.Fatalf("operator template overwritten: %q", name)
	}
}
