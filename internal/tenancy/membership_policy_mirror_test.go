package tenancy_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// siloOriginPool is a compatibility-phase database holding accounts that Silo
// created after Bloem's migrations ran: users rows only, no memberships.
func siloOriginPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
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
	return ctx, pool
}

func authorityPhase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var phase string
	if err := pool.QueryRow(ctx, `SELECT phase FROM public.membership_policy_authority WHERE singleton`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	return phase
}

func TestEnableSiloSwitchingFromCompatibility(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	report, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err != nil {
		t.Fatalf("enable silo switching: %v", err)
	}
	if phase := authorityPhase(t, ctx, pool); phase != "mirrored" {
		t.Fatalf("phase = %s, want mirrored", phase)
	}
	if report.AlreadyMirrored || report.Accounts != 2 || report.MembershipsCreated != 2 {
		t.Fatalf("report = %+v, want 2 accounts and 2 created memberships", report)
	}
	var streams int
	if err := pool.QueryRow(ctx, `
		SELECT m.max_streams FROM organization_memberships m JOIN users u ON u.id = m.account_id
		WHERE u.username = 'u1' AND m.organization_id = public.bloem_default_organization_id()`).Scan(&streams); err != nil {
		t.Fatalf("read reconciled membership: %v", err)
	}
	if streams != 3 {
		t.Fatalf("membership max_streams = %d, want 3 from users", streams)
	}
	var userGroup, membershipGroup *int64
	if err := pool.QueryRow(ctx, `
		SELECT u.access_group_id, m.access_group_id FROM users u
		JOIN organization_memberships m ON m.account_id = u.id AND m.organization_id = public.bloem_default_organization_id()
		WHERE u.username = 'admin1'`).Scan(&userGroup, &membershipGroup); err != nil {
		t.Fatalf("read admin groups: %v", err)
	}
	if userGroup != nil || membershipGroup != nil {
		t.Fatalf("admin groups users=%v membership=%v, want both NULL", deref(userGroup), deref(membershipGroup))
	}
}

func TestEnableSiloSwitchingIsIdempotent(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	if _, err := tenancy.EnableSiloSwitching(ctx, pool); err != nil {
		t.Fatal(err)
	}
	report, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err != nil {
		t.Fatalf("second enable: %v", err)
	}
	if !report.AlreadyMirrored {
		t.Fatalf("report = %+v, want AlreadyMirrored", report)
	}
}

func TestEnableSiloSwitchingRefusesTwoOrganizations(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		SELECT 'two', 'Two', 'active', id, false FROM users WHERE username = 'admin1'`); err != nil {
		t.Fatalf("second organization: %v", err)
	}
	_, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "exactly one organization") {
		t.Fatalf("error = %v, want exactly one organization", err)
	}
	if phase := authorityPhase(t, ctx, pool); phase != "compatibility" {
		t.Fatalf("phase = %s, want compatibility", phase)
	}
}

func TestEnableSiloSwitchingRefusesDirectProfileLogins(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT public.bloem_default_organization_id(), id, 'active', 'user' FROM users WHERE username = 'u1'`); err != nil {
		t.Fatalf("membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id, login_email, password_hash)
		SELECT 'p1', id, 'Main', true, public.bloem_default_organization_id(), access_group_id, 'p1@example.invalid', 'x'
		FROM users WHERE username = 'u1'`); err != nil {
		t.Fatalf("profile with login: %v", err)
	}
	_, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "direct profile login") {
		t.Fatalf("error = %v, want direct profile login", err)
	}
	if phase := authorityPhase(t, ctx, pool); phase != "compatibility" {
		t.Fatalf("phase = %s, want compatibility", phase)
	}
}

func TestEnableSiloSwitchingRefusesFinalized(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err := tenancy.EnableSiloSwitching(ctx, pool)
	if err == nil || !strings.Contains(err.Error(), "finalized") {
		t.Fatalf("error = %v, want finalized", err)
	}
}

func TestFinalizeFromMirroredDropsSwitchingTriggers(t *testing.T) {
	ctx, pool := siloOriginPool(t)
	if _, err := tenancy.EnableSiloSwitching(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize from mirrored: %v", err)
	}
	if phase := authorityPhase(t, ctx, pool); phase != "finalized" {
		t.Fatalf("phase = %s, want finalized", phase)
	}
	var left int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_trigger
		WHERE tgname IN ('users_policy_mirror_to_membership', 'users_silo_default_membership')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("switching triggers on users after finalize = %d, want 0", left)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_memberships SET max_streams = 5`); err != nil {
		t.Fatalf("finalized policy write: %v", err)
	}
}
