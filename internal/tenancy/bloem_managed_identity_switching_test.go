package tenancy_test

// Managed Pastime tracking projects each account's verified sign-in identity
// through triggers on plugin_auth_identities and plugin_installations, both
// tables upstream Silo writes. While the database is mirrored for Silo
// switching, Silo-shaped writes to each must still succeed and the projection
// must follow them.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const mirroredIssuer = "https://idp.example.test/application/o/silo/"

func TestMirroredSiloWritesPassManagedIdentityTriggers(t *testing.T) {
	ctx, pool, _, admin := mirroredPool(t)
	_, _ = grantDefaultOrganization(t, ctx, pool, admin)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "mi1", 1)
	siloCreateProfileTx(t, ctx, tx, account, "mi1-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	identity := func() (string, string) {
		t.Helper()
		return mirroredProjectedIdentity(t, ctx, pool, account)
	}
	if issuer, subject := identity(); issuer != "" || subject != "" {
		t.Fatalf("account without a sign-in identity projected (%q, %q)", issuer, subject)
	}

	// Silo installs the OIDC sign-in plugin (internal/plugins/installation.go).
	var install int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO plugin_installations (repository_id, plugin_id, version, install_path, enabled, update_policy)
		VALUES (NULL, 'silo.auth.oidc', '1.0.0', '/plugins/silo.auth.oidc/1.0.0/plugin', true, 'auto')
		RETURNING id`).Scan(&install); err != nil {
		t.Fatalf("silo plugin install: %v", err)
	}

	// Silo links an identity on first sign-in (internal/auth/identity_repository.go).
	var link int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO plugin_auth_identities (
			plugin_installation_id, external_subject, user_id, issuer, username, email, display_name,
			linked_at, last_sign_in_at, last_checked_at, last_check_status, last_authenticated_at
		) VALUES ($1, $2, $3, '', 'mi1', 'mi1@example.invalid', 'MI One', NOW(), NOW(), NOW(), 'active', NOW())
		RETURNING id`, install, mirroredIssuer+"|mi1-subject", account).Scan(&link); err != nil {
		t.Fatalf("silo identity link: %v", err)
	}
	if issuer, subject := identity(); issuer != mirroredIssuer || subject != "mi1-subject" {
		t.Fatalf("linked identity projected (%q, %q)", issuer, subject)
	}

	// Silo records a sign-in and a provider re-check (recordIdentitySignIn,
	// recordIdentityCheck).
	if _, err := pool.Exec(ctx, `
		UPDATE plugin_auth_identities
		SET issuer = $2, username = 'mi1', email = 'mi1@example.invalid', display_name = 'MI One',
			last_sign_in_at = NOW(), last_checked_at = NOW(), last_check_status = 'active',
			last_authenticated_at = NOW(), pending_refusal = '', updated_at = NOW()
		WHERE id = $1`, link, mirroredIssuer); err != nil {
		t.Fatalf("silo sign-in record: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE plugin_auth_identities
		SET last_checked_at = NOW(), last_check_status = 'active', last_authenticated_at = NOW(),
			issuer = COALESCE(NULLIF($2, ''), issuer), username = COALESCE(NULLIF('', ''), username),
			pending_refusal = '', updated_at = NOW()
		WHERE id = $1`, link, ""); err != nil {
		t.Fatalf("silo provider re-check: %v", err)
	}
	if issuer, subject := identity(); issuer != mirroredIssuer || subject != "mi1-subject" {
		t.Fatalf("identity after sign-in projected (%q, %q)", issuer, subject)
	}

	// Silo disables and re-enables the plugin.
	for _, enabled := range []bool{false, true} {
		if _, err := pool.Exec(ctx, `UPDATE plugin_installations SET enabled = $2, updated_at = NOW() WHERE id = $1`, install, enabled); err != nil {
			t.Fatalf("silo plugin enabled=%v: %v", enabled, err)
		}
		want := ""
		if enabled {
			want = "mi1-subject"
		}
		if _, subject := identity(); subject != want {
			t.Fatalf("plugin enabled=%v projected subject %q", enabled, subject)
		}
	}

	// Silo unlinks the identity.
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_auth_identities WHERE id = $1 AND user_id = $2 RETURNING id`, link, account); err != nil {
		t.Fatalf("silo identity unlink: %v", err)
	}
	if _, subject := identity(); subject != "" {
		t.Fatalf("unlinked identity still projected %q", subject)
	}

	// Removing the plugin cascades to its identities.
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_auth_identities (plugin_installation_id, external_subject, user_id, issuer)
		VALUES ($1, $2, $3, $4)`, install, mirroredIssuer+"|mi1-subject", account, mirroredIssuer); err != nil {
		t.Fatalf("silo identity relink: %v", err)
	}
	if _, subject := identity(); subject != "mi1-subject" {
		t.Fatalf("relinked identity projected %q", subject)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE plugin_installation_id = $1`, install); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_installations WHERE id = $1`, install); err != nil {
		t.Fatalf("plugin removal under managed identity triggers: %v", err)
	}
	if _, subject := identity(); subject != "" {
		t.Fatalf("removed plugin's identity still projected %q", subject)
	}

	// Silo deletes the account; the identity cascade passes the triggers.
	if err := pool.QueryRow(ctx, `
		INSERT INTO plugin_installations (repository_id, plugin_id, version, install_path, enabled, update_policy)
		VALUES (NULL, 'silo.auth.oidc', '1.0.1', '/plugins/silo.auth.oidc/1.0.1/plugin', true, 'auto')
		RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_auth_identities (plugin_installation_id, external_subject, user_id, issuer)
		VALUES ($1, $2, $3, '')`, install, mirroredIssuer+"|mi1-subject", account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, account); err != nil {
		t.Fatalf("silo account delete under managed identity triggers: %v", err)
	}
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_managed_profiles WHERE account_id = $1 AND (active OR identity_subject <> ''))`, account).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("deleted account keeps an active or identified projection")
	}
}

func mirroredProjectedIdentity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account int) (string, string) {
	t.Helper()
	var issuer, subject string
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT min(identity_issuer), min(identity_subject), count(DISTINCT (identity_issuer, identity_subject))
		FROM bloem_managed_profiles WHERE account_id = $1`, account).Scan(&issuer, &subject, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("account %d has %d distinct projected identities", account, n)
	}
	return issuer, subject
}
