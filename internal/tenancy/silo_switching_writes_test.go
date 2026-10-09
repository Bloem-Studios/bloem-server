package tenancy_test

// Silo-shaped account and profile writes while the database is mirrored for
// Silo switching: upstream creates accounts and profiles without an
// organization, and the database places them in the default organization.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// siloCreateAccountTx creates an account the way upstream Silo does inside a
// transaction: the users row only, no membership.
func siloCreateAccountTx(t *testing.T, ctx context.Context, tx pgx.Tx, username string, maxStreams int) int {
	t.Helper()
	var id int
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled, permissions, max_streams, access_group_id)
		VALUES ($1::text, $1::text || '@example.invalid', 'x', 'user', true, '{}', $2, (SELECT id FROM access_groups WHERE is_default))
		RETURNING id`, username, maxStreams).Scan(&id); err != nil {
		t.Fatalf("silo create account %s: %v", username, err)
	}
	return id
}

// siloCreateProfileTx inserts a profile with upstream Silo's column list
// (internal/userstore/pgstore/profiles.go in Silo), which has no organization.
func siloCreateProfileTx(t *testing.T, ctx context.Context, tx pgx.Tx, account int, id string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (
			id, user_id, name, avatar, pin_hash, is_child, is_primary, max_content_rating, max_advisory_age,
			require_advisory_age,
			quality_preference, language, preferred_metadata_language, subtitle_language, subtitle_mode,
			auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview,
			library_restrictions_enabled,
			show_forced_subtitles, max_playback_quality, created_at, updated_at
		) VALUES ($1, $2, 'Main', '', '', false, true, '', NULLIF(0::smallint, 0), false,
		          '', '', '', '', '', false, false, false, true, false, false, '', now(), now())`,
		id, account); err != nil {
		t.Fatalf("silo create profile %s: %v", id, err)
	}
}

func TestMirroredSiloCreatesAccountAndProfile(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "u2", 4)
	siloCreateProfileTx(t, ctx, tx, account, "u2-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit silo account and profile: %v", err)
	}

	var profileOrg, defaultOrg string
	var profileGroup, accountGroup int64
	if err := pool.QueryRow(ctx, `
		SELECT p.organization_id::text, p.access_group_id, public.bloem_default_organization_id()::text, u.access_group_id
		FROM user_profiles p JOIN users u ON u.id = p.user_id WHERE p.id = 'u2-main'`,
	).Scan(&profileOrg, &profileGroup, &defaultOrg, &accountGroup); err != nil {
		t.Fatalf("read profile tenancy: %v", err)
	}
	if profileOrg != defaultOrg {
		t.Fatalf("profile organization = %s, want default %s", profileOrg, defaultOrg)
	}
	if profileGroup != accountGroup {
		t.Fatalf("profile access group = %d, want the account's %d", profileGroup, accountGroup)
	}
	if got := membershipPolicy(t, ctx, pool, account); got.streams == nil || *got.streams != 4 {
		t.Fatalf("membership max_streams = %v, want 4 (seeded from users)", got.streams)
	}
}

func TestMirroredSiloAccountWithoutProfileGetsMembershipAtCommit(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "u3", 1)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit silo account: %v", err)
	}
	var memberships int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_memberships
		WHERE account_id = $1 AND organization_id = public.bloem_default_organization_id() AND status = 'active'`,
		account).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 {
		t.Fatalf("default-organization memberships = %d, want 1", memberships)
	}
}

func TestAccountInsertQueuesNoDeferredWorkOutsideMirrored(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO users (username, email, password_hash, role, enabled) VALUES ('c1', 'c1@example.invalid', 'x', 'user', true)`); err != nil {
		t.Fatal(err)
	}
	// A deferred trigger event would make this fail with "pending trigger events".
	if _, err := tx.Exec(ctx, `ALTER TABLE users DISABLE TRIGGER users_login_identifiers`); err != nil {
		t.Fatalf("alter users after an insert outside mirrored: %v", err)
	}
}

func TestMirroredSiloDeleteAccountCascades(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "u4", 1)
	siloCreateProfileTx(t, ctx, tx, account, "u4-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, account); err != nil {
		t.Fatalf("silo delete account: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organization_memberships WHERE account_id = $1)
		     + (SELECT count(*) FROM user_profiles WHERE user_id = $1)`, account).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("rows left after account delete = %d, want 0", left)
	}
}

func TestMirroredSiloGroupMoveCarriesProfiles(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "g1", 1)
	siloCreateProfileTx(t, ctx, tx, account, "g1-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var group, defaultGroup int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (name) VALUES ('stricter') RETURNING id`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE is_default`).Scan(&defaultGroup); err != nil {
		t.Fatal(err)
	}
	profileGroup := func() int64 {
		var g int64
		if err := pool.QueryRow(ctx, `SELECT access_group_id FROM user_profiles WHERE id = 'g1-main'`).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id = $1 WHERE id = $2`, group, account); err != nil {
		t.Fatalf("silo moves account to a stricter group: %v", err)
	}
	if got := profileGroup(); got != group {
		t.Fatalf("profile access group = %d, want the account's new group %d", got, group)
	}
	// Silo's group delete: move the members to the default group, then delete.
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id = $1 WHERE access_group_id = $2`, defaultGroup, group); err != nil {
		t.Fatalf("silo moves members to default: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM access_groups WHERE id = $1`, group); err != nil {
		t.Fatalf("silo deletes a group whose members have profiles: %v", err)
	}
	if got := profileGroup(); got != defaultGroup {
		t.Fatalf("profile access group = %d, want default %d", got, defaultGroup)
	}
}

func TestMirroredSiloCreatedAdminStaysUngrouped(t *testing.T) {
	ctx, pool, _, _ := mirroredPool(t)
	var admin int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled, access_group_id)
		VALUES ('a2', 'a2@example.invalid', 'x', 'admin', true, NULL) RETURNING id`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	var userGroup, membershipGroup *int64
	if err := pool.QueryRow(ctx, `
		SELECT u.access_group_id, m.access_group_id FROM users u
		JOIN organization_memberships m ON m.account_id = u.id AND m.organization_id = public.bloem_default_organization_id()
		WHERE u.id = $1`, admin).Scan(&userGroup, &membershipGroup); err != nil {
		t.Fatalf("read admin groups: %v", err)
	}
	if userGroup != nil || membershipGroup != nil {
		t.Fatalf("admin groups users=%v membership=%v, want both NULL", deref(userGroup), deref(membershipGroup))
	}
}

func TestMirroredRoleChangesReachTheOtherSide(t *testing.T) {
	ctx, pool, user, admin := mirroredPool(t)
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin', access_group_id = NULL WHERE id = $1`, user); err != nil {
		t.Fatalf("silo promotes: %v", err)
	}
	var legacyRole string
	if err := pool.QueryRow(ctx, `SELECT legacy_role FROM organization_memberships WHERE account_id = $1`, user).Scan(&legacyRole); err != nil {
		t.Fatal(err)
	}
	if legacyRole != "admin" {
		t.Fatalf("membership legacy_role after silo promotion = %s, want admin", legacyRole)
	}
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships SET legacy_role = 'user' WHERE account_id = $1`, admin); err != nil {
		t.Fatalf("bloem demotes: %v", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, admin).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "user" {
		t.Fatalf("users role after bloem demotion = %s, want user", role)
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
