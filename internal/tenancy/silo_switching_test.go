package tenancy_test

// Silo switching keeps one database servable by upstream Silo and by Bloem,
// one at a time: account policy already lives on users, where both read it,
// and while switching is enabled nothing Silo could not honor is allowed.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// siloAccount inserts an account the way upstream Silo does: every policy
// column on users, non-admins in the default access group, admins ungrouped.
// It is given the default-organization membership a Bloem migration gives
// each Silo-origin account.
func siloAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username, role string) int {
	t.Helper()
	group := "(SELECT id FROM access_groups WHERE is_default)"
	if role == "admin" {
		group = "NULL"
	}
	var id int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled, permissions, max_streams,
		                   transcode_allowed, requests_allowed, access_group_id)
		VALUES ($1::text, $1::text || '@example.invalid', 'x', $2, true, '{}', 2, true, true, `+group+`)
		RETURNING id`, username, role).Scan(&id); err != nil {
		t.Fatalf("insert silo account %s: %v", username, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES (public.bloem_default_organization_id(), $1, 'active', $2)`, id, role); err != nil {
		t.Fatalf("seed membership for %s: %v", username, err)
	}
	return id
}

// switchablePool returns a database with Silo switching enabled, one Silo user
// and one Silo admin.
func switchablePool(t *testing.T) (context.Context, *pgxpool.Pool, int, int) {
	t.Helper()
	ctx, pool, _ := buildTenantPool(t)
	user := siloAccount(t, ctx, pool, "u1", "user")
	admin := siloAccount(t, ctx, pool, "admin1", "admin")
	if _, err := tenancy.EnableSiloSwitching(ctx, pool); err != nil {
		t.Fatalf("enable silo switching: %v", err)
	}
	return ctx, pool, user, admin
}

func switching(t *testing.T, ctx context.Context, pool *pgxpool.Pool) bool {
	t.Helper()
	enabled, err := tenancy.SiloSwitchingEnabled(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	return enabled
}

func wantError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

// Accounts Silo created hold only a users row; enabling gives each its
// default-organization membership.
func TestEnableSiloSwitchingCreatesMissingMemberships(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	for _, account := range []struct{ name, role, group string }{
		{"u1", "user", "(SELECT id FROM access_groups WHERE is_default)"},
		{"admin1", "admin", "NULL"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (username, email, password_hash, role, enabled, max_streams, access_group_id)
			VALUES ($1::text, $1::text || '@example.invalid', 'x', $2, true, 3, `+account.group+`)`,
			account.name, account.role); err != nil {
			t.Fatalf("insert %s: %v", account.name, err)
		}
	}
	if switching(t, ctx, pool) {
		t.Fatal("a fresh database is switchable")
	}
	report, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err != nil {
		t.Fatalf("enable silo switching: %v", err)
	}
	if report.AlreadyEnabled || report.MembershipsCreated != 2 {
		t.Fatalf("report = %+v, want 2 created memberships", report)
	}
	if !switching(t, ctx, pool) {
		t.Fatal("switching not enabled")
	}
	var role string
	if err := pool.QueryRow(ctx, `
		SELECT m.legacy_role FROM organization_memberships m JOIN users u ON u.id = m.account_id
		WHERE u.username = 'admin1' AND m.organization_id = public.bloem_default_organization_id()`).Scan(&role); err != nil {
		t.Fatalf("read admin membership: %v", err)
	}
	if role != "admin" {
		t.Fatalf("admin membership role = %s, want admin", role)
	}
}

func TestEnableSiloSwitchingIsIdempotent(t *testing.T) {
	ctx, pool, _, _ := switchablePool(t)
	report, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err != nil {
		t.Fatalf("second enable: %v", err)
	}
	if !report.AlreadyEnabled {
		t.Fatalf("report = %+v, want AlreadyEnabled", report)
	}
}

func TestEnableSiloSwitchingRefusesTwoOrganizations(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	admin := siloAccount(t, ctx, pool, "admin1", "admin")
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ('two', 'Two', 'active', $1, false)`, admin); err != nil {
		t.Fatalf("second organization: %v", err)
	}
	_, err := tenancy.EnableSiloSwitching(ctx, pool)
	wantError(t, err, "exactly one organization")
	if switching(t, ctx, pool) {
		t.Fatal("switching enabled with two organizations")
	}
}

func TestEnableSiloSwitchingRefusesDirectProfileLogins(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	user := siloAccount(t, ctx, pool, "u1", "user")
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id, login_email, password_hash)
		SELECT 'p1', id, 'Main', true, public.bloem_default_organization_id(), access_group_id, 'p1@example.invalid', 'x'
		FROM users WHERE id = $1`, user); err != nil {
		t.Fatalf("profile with login: %v", err)
	}
	_, err := tenancy.EnableSiloSwitching(ctx, pool)
	wantError(t, err, "direct profile login")
	if switching(t, ctx, pool) {
		t.Fatal("switching enabled with a direct profile login")
	}
}

func TestDisableSiloSwitchingLiftsTheGuards(t *testing.T) {
	ctx, pool, _, admin := switchablePool(t)
	if err := tenancy.DisableSiloSwitching(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ('two', 'Two', 'active', $1, false)`, admin); err != nil {
		t.Fatalf("second organization after disabling: %v", err)
	}
}

func TestSwitchableAcceptsDefaultOrganizationMembership(t *testing.T) {
	ctx, pool, _, _ := switchablePool(t)
	// Bloem's account path: the account and its membership in one transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var account int
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ('m1', 'm1@example.invalid', 'x', 'user', true) RETURNING id`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES (public.bloem_default_organization_id(), $1, 'active', 'user')`, account); err != nil {
		t.Fatalf("default-organization membership while switchable: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit account and membership: %v", err)
	}
}

func TestSwitchableRefusesSecondOrganization(t *testing.T) {
	ctx, pool, _, admin := switchablePool(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ('two', 'Two', 'active', $1, false)`, admin)
	wantError(t, err, "bloem_silo_switching_single_organization")
}

func TestSwitchableRefusesDirectProfileLogin(t *testing.T) {
	ctx, pool, user, _ := switchablePool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id)
		VALUES ('p1', $1, 'Main', true, public.bloem_default_organization_id(), (SELECT id FROM access_groups WHERE is_default))`, user); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	_, err := pool.Exec(ctx, `UPDATE user_profiles SET login_email = 'p@example.invalid', password_hash = 'x' WHERE id = 'p1'`)
	wantError(t, err, "bloem_silo_switching_direct_profile_login")
}
