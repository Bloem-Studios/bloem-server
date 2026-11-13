package migrations

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// beforeOrganizationSupport is the newest migration that predates the
// organization support schema.
const beforeOrganizationSupport = 20261113020510

func TestOrganizationSupportMigrationsUpDownUp(t *testing.T) {
	db, provider := newMigratedTestDB(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	queryInt := func(query string, args ...any) int64 {
		t.Helper()
		var value int64
		if err := db.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return value
	}

	var accountID, memberID int
	if err := db.QueryRowContext(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('support-owner','support-owner@example.test','x','user',true) RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('support-member','support-member@example.test','x','user',true) RETURNING id`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	var organizationID string
	if err := db.QueryRowContext(ctx, `INSERT INTO organizations (slug,name,status,owner_account_id) VALUES ('support-migration','Support Migration','active',$1) RETURNING id::text`, accountID).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	var groupID int
	if err := db.QueryRowContext(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Everyone',true) RETURNING id`, organizationID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','admin'),($1,$3,'active','user')`, organizationID, accountID, memberID)
	exec(`INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ('p1',$1,'First',$2,$3)`, memberID, organizationID, groupID)

	t.Run("profile revision", func(t *testing.T) {
		revision := func() int64 {
			return queryInt(`SELECT revision FROM user_profiles WHERE id='p1'`)
		}
		if got := revision(); got != 1 {
			t.Fatalf("new profile revision = %d, want 1", got)
		}
		exec(`UPDATE user_profiles SET updated_at=now() WHERE id='p1'`)
		if got := revision(); got != 1 {
			t.Fatalf("revision after a touch = %d, want 1", got)
		}
		exec(`UPDATE user_profiles SET name='Second' WHERE id='p1'`)
		if got := revision(); got != 2 {
			t.Fatalf("revision after a rename = %d, want 2", got)
		}
		exec(`UPDATE user_profiles SET pin_hash='hash' WHERE id='p1'`)
		if got := revision(); got != 3 {
			t.Fatalf("revision after a PIN change = %d, want 3", got)
		}
		exec(`UPDATE user_profiles SET language='en' WHERE id='p1'`)
		if got := revision(); got != 3 {
			t.Fatalf("revision after a no-op write = %d, want 3", got)
		}
	})

	t.Run("promotion retires a reset link", func(t *testing.T) {
		link := func() int64 {
			return queryInt(`SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, memberID)
		}
		issue := func() {
			exec(`DELETE FROM password_reset_tokens WHERE user_id=$1`, memberID)
			exec(`INSERT INTO password_reset_tokens (user_id,token_hash,password_fingerprint,expires_at) VALUES ($1,'digest-'||($1::int)::text,'fingerprint',now()+interval '1 hour')`, memberID)
		}
		issue()
		exec(`UPDATE organization_memberships SET status='suspended' WHERE account_id=$1`, memberID)
		exec(`UPDATE organization_memberships SET status='active' WHERE account_id=$1`, memberID)
		if link() != 1 {
			t.Fatal("a status change retired the link")
		}
		exec(`UPDATE organization_memberships SET legacy_role='admin' WHERE account_id=$1`, memberID)
		if link() != 0 {
			t.Fatal("promotion to administrator kept the link")
		}
		exec(`UPDATE organization_memberships SET legacy_role='user' WHERE account_id=$1`, memberID)
		issue()
		exec(`UPDATE organizations SET owner_account_id=$2 WHERE id=$1`, organizationID, memberID)
		if link() != 0 {
			t.Fatal("becoming organization owner kept the link")
		}
		exec(`UPDATE organizations SET owner_account_id=$2 WHERE id=$1`, organizationID, accountID)
	})

	var accessID string
	t.Run("support access is append-only and capped", func(t *testing.T) {
		if err := db.QueryRowContext(ctx, `INSERT INTO bloem_support_access (organization_id,actor_account_id,login_session_id,reason,expires_at) VALUES ($1,$2,'session-1','Investigating a sign-in problem',now()+interval '4 hours') RETURNING id::text`, organizationID, memberID).Scan(&accessID); err != nil {
			t.Fatal(err)
		}
		for name, query := range map[string]string{
			"over four hours": `INSERT INTO bloem_support_access (organization_id,actor_account_id,login_session_id,reason,expires_at) VALUES ($1,$2,'s','A long enough reason',now()+interval '5 hours')`,
			"short reason":    `INSERT INTO bloem_support_access (organization_id,actor_account_id,login_session_id,reason,expires_at) VALUES ($1,$2,'s','short',now()+interval '1 hour')`,
		} {
			if _, err := db.ExecContext(ctx, query, organizationID, memberID); err == nil {
				t.Fatalf("%s: insert succeeded", name)
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE bloem_support_access SET reason='A different reason now' WHERE id=$1`, accessID); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("editing a record: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM bloem_support_access WHERE id=$1`, accessID); err == nil {
			t.Fatal("deleting a record succeeded")
		}
		exec(`UPDATE bloem_support_access SET closed_at=now() WHERE id=$1`, accessID)
		if _, err := db.ExecContext(ctx, `UPDATE bloem_support_access SET closed_at=NULL WHERE id=$1`, accessID); err == nil {
			t.Fatal("reopening a record succeeded")
		}
	})

	t.Run("audit accepts the new targets", func(t *testing.T) {
		for _, target := range []string{"session", "device", "stream", "support_access"} {
			exec(`INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,before_revision,after_revision,outcome,support_access_id) VALUES ($1,'platform_admin','organization','test.'||$2::text,$2,'t',$3,0,0,'success',$4)`, memberID, target, organizationID, accessID)
		}
	})

	// An ordinary event must survive the rollback, and stay immutable after it.
	exec(`INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,before_revision,after_revision,outcome) VALUES ($1,'organization_admin','organization','test.ordinary','membership','m',$2,0,0,'success')`, accountID, organizationID)

	if _, err := provider.DownTo(ctx, beforeOrganizationSupport); err != nil {
		t.Fatalf("down: %v", err)
	}
	t.Run("down restores the predecessor", func(t *testing.T) {
		for _, table := range []string{"bloem_support_access"} {
			if exists(t, db, `SELECT to_regclass('public.`+table+`') IS NOT NULL`) {
				t.Fatalf("%s still exists after down", table)
			}
		}
		if exists(t, db, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='user_profiles' AND column_name='revision')`) {
			t.Fatal("user_profiles.revision still exists after down")
		}
		if got := queryInt(`SELECT count(*) FROM admin_audit_events WHERE target_type IN ('session','device','stream','support_access')`); got != 0 {
			t.Fatalf("%d support audit rows survived down", got)
		}
		if got := queryInt(`SELECT count(*) FROM admin_audit_events WHERE action='test.ordinary'`); got != 1 {
			t.Fatalf("ordinary audit rows after down = %d, want 1", got)
		}
		if _, err := db.ExecContext(ctx, `UPDATE admin_audit_events SET outcome='failure'`); err == nil {
			t.Fatal("audit immutability trigger was not restored")
		}
	})
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !exists(t, db, `SELECT to_regclass('public.bloem_support_access') IS NOT NULL`) {
		t.Fatal("support access table missing after up again")
	}
	if got := queryInt(`SELECT revision FROM user_profiles WHERE id='p1'`); got != 1 {
		t.Fatalf("profile revision after up again = %d, want the default 1", got)
	}
}

func exists(t *testing.T, db *sql.DB, query string) bool {
	t.Helper()
	var value bool
	if err := db.QueryRowContext(t.Context(), query).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}
