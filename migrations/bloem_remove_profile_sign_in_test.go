package migrations

import (
	"database/sql"
	"testing"
)

const (
	beforeRemoveProfileSignIn = 20261116235542
	removeProfileSignIn       = 20261117203403
)

// TestRemoveProfileSignInDropsItsSchema: the migration ends direct-profile
// sessions, drops profile credentials, their registry, triggers and functions,
// and leaves account and preview guest sessions alone. Down restores the
// schema shape, without the removed credentials.
func TestRemoveProfileSignInDropsItsSchema(t *testing.T) {
	db, provider := newTestDBMigratedTo(t, beforeRemoveProfileSignIn)
	exec(t, db,
		`INSERT INTO users (id, username, email, password_hash, role, enabled) VALUES (1, 'account', 'account@example.invalid', 'x', 'user', true)`,
		`INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES (public.bloem_default_organization_id(), 1, 'active', 'user') ON CONFLICT DO NOTHING`,
		`INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id, login_email, password_hash)
		VALUES ('reader', 1, 'Reader', true, public.bloem_default_organization_id(),
			(SELECT id FROM access_groups WHERE organization_id = public.bloem_default_organization_id() AND is_default),
			'reader@example.invalid', 'x')`,
		`INSERT INTO auth_sessions (id, user_id, device_id, expires_at, profile_id, profile_credential_revision, auth_method) VALUES
		('direct', 1, 'tablet', now() + interval '1 day', 'reader', 1, 'direct_profile'),
		('account', 1, NULL, now() + interval '1 day', NULL, NULL, 'account')`,
		`INSERT INTO server_settings (key, value) VALUES ('bloem.auth.profile_sign_in_enabled', 'true')`,
	)
	if _, err := provider.UpTo(t.Context(), removeProfileSignIn); err != nil {
		t.Fatal(err)
	}

	assertCount(t, db, `SELECT count(*) FROM auth_sessions WHERE id = 'direct'`, 0)
	assertCount(t, db, `SELECT count(*) FROM auth_sessions WHERE id = 'account' AND auth_method = 'account'`, 1)
	assertCount(t, db, `SELECT count(*) FROM user_profiles WHERE id = 'reader'`, 1)
	assertCount(t, db, `SELECT count(*) FROM server_settings WHERE key = 'bloem.auth.profile_sign_in_enabled'`, 0)
	assertCount(t, db, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND ((table_name = 'user_profiles' AND column_name IN ('login_email', 'password_hash', 'credential_revision'))
		    OR (table_name = 'auth_sessions' AND column_name IN ('profile_id', 'profile_credential_revision')))`, 0)
	assertCount(t, db, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'auth_sessions' AND column_name = 'auth_method'`, 1)
	assertCount(t, db, `SELECT count(*) FROM pg_class WHERE relname IN ('login_email_registry', 'auth_sessions_direct_profile_idx')`, 0)
	assertCount(t, db, `SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN (
		'users_login_email_registry_sync', 'user_profiles_login_email_registry_sync',
		'user_profiles_rotate_credential_revision', 'user_profiles_revoke_rotated_sessions',
		'user_profiles_mirrored_no_direct_login')`, 0)
	assertCount(t, db, `SELECT count(*) FROM pg_proc WHERE pronamespace = 'public'::regnamespace AND proname IN (
		'bloem_login_whitespace', 'bloem_login_text_blank', 'bloem_normalize_login_email',
		'bloem_sync_account_login_email_registry', 'bloem_sync_profile_login_email_registry',
		'bloem_rotate_profile_credential_revision', 'bloem_revoke_rotated_profile_sessions')`, 0)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_sessions (id, user_id, expires_at, auth_method) VALUES ('stale', 1, now(), 'direct_profile')`); err == nil {
		t.Fatal("auth_sessions still admits a direct_profile session")
	}

	if _, err := provider.DownTo(t.Context(), beforeRemoveProfileSignIn); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND ((table_name = 'user_profiles' AND column_name IN ('login_email', 'password_hash', 'credential_revision'))
		    OR (table_name = 'auth_sessions' AND column_name IN ('profile_id', 'profile_credential_revision')))`, 5)
	assertCount(t, db, `SELECT count(*) FROM login_email_registry WHERE account_id = 1`, 1)
	exec(t, db, `UPDATE user_profiles SET login_email = 'again@example.invalid', password_hash = 'x' WHERE id = 'reader'`)
	assertCount(t, db, `SELECT count(*) FROM login_email_registry WHERE profile_id = 'reader'`, 1)
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
