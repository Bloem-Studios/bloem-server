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
