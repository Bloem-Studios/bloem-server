package tenancy_test

// The 'mirrored' membership policy phase, which lets upstream Silo and Bloem
// each serve one database: the legacy users policy columns (Silo's) and the
// default-organization membership (Bloem's) are copied into each other in the
// same transaction, and nothing Silo could not honour is allowed.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// siloAccount inserts an account the way upstream Silo does: every policy
// column on users, non-admins in the default access group, admins ungrouped.
// A Silo-origin database migrated by Bloem gives each such account a
// default-organization membership seeded from users; the unmarked insert here
// reproduces that seed.
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

func enterMirrored(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_mirror_enabler', 'v1', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE public.membership_policy_authority SET phase = 'mirrored', mirrored_at = now() WHERE singleton`); err != nil {
		t.Fatalf("enter mirrored phase: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// mirroredPool returns a database in the 'mirrored' phase with one Silo user
// and one Silo admin.
func mirroredPool(t *testing.T) (context.Context, *pgxpool.Pool, int, int) {
	t.Helper()
	ctx, pool, _ := buildTenantPool(t)
	user := siloAccount(t, ctx, pool, "u1", "user")
	admin := siloAccount(t, ctx, pool, "admin1", "admin")
	enterMirrored(t, ctx, pool)
	return ctx, pool, user, admin
}

type policySnapshot struct {
	group     *int64
	streams   *int
	transcode *bool
	requests  *bool
	libraries []int32
}

func membershipPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account int) policySnapshot {
	t.Helper()
	var p policySnapshot
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id, max_streams, transcode_allowed, requests_allowed, library_ids
		FROM organization_memberships
		WHERE account_id = $1 AND organization_id = public.bloem_default_organization_id()`, account,
	).Scan(&p.group, &p.streams, &p.transcode, &p.requests, &p.libraries); err != nil {
		t.Fatalf("read membership policy: %v", err)
	}
	return p
}

func userPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account int) policySnapshot {
	t.Helper()
	var p policySnapshot
	if err := pool.QueryRow(ctx, `
		SELECT access_group_id, max_streams, transcode_allowed, requests_allowed, library_ids
		FROM users WHERE id = $1`, account,
	).Scan(&p.group, &p.streams, &p.transcode, &p.requests, &p.libraries); err != nil {
		t.Fatalf("read users policy: %v", err)
	}
	return p
}

func wantError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestMirroredUserPolicyWriteReachesMembership(t *testing.T) {
	ctx, pool, user, _ := mirroredPool(t)
	if _, err := pool.Exec(ctx, `UPDATE users SET max_streams = 7, transcode_allowed = false, requests_allowed = false WHERE id = $1`, user); err != nil {
		t.Fatalf("silo policy write: %v", err)
	}
	got := membershipPolicy(t, ctx, pool, user)
	if got.streams == nil || *got.streams != 7 {
		t.Fatalf("membership max_streams = %v, want 7", got.streams)
	}
	if got.transcode == nil || *got.transcode {
		t.Fatalf("membership transcode_allowed = %v, want false", got.transcode)
	}
	if got.requests == nil || *got.requests {
		t.Fatalf("membership requests_allowed = %v, want false", got.requests)
	}
}

func TestMirroredMembershipPolicyWriteReachesUser(t *testing.T) {
	ctx, pool, user, _ := mirroredPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer', public.bloem_membership_policy_writer_marker(), true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships SET max_streams = 3, library_ids = '{1,2}'
		WHERE account_id = $1 AND organization_id = public.bloem_default_organization_id()`, user); err != nil {
		t.Fatalf("bloem policy write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := userPolicy(t, ctx, pool, user)
	if got.streams == nil || *got.streams != 3 {
		t.Fatalf("users max_streams = %v, want 3", got.streams)
	}
	if len(got.libraries) != 2 || got.libraries[0] != 1 || got.libraries[1] != 2 {
		t.Fatalf("users library_ids = %v, want [1 2]", got.libraries)
	}
}

func TestMirroredGroupMoveFollowsToMembership(t *testing.T) {
	ctx, pool, user, _ := mirroredPool(t)
	var group int64
	if err := pool.QueryRow(ctx, `INSERT INTO access_groups (name) VALUES ('second') RETURNING id`).Scan(&group); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id = $1 WHERE id = $2`, group, user); err != nil {
		t.Fatalf("move to group: %v", err)
	}
	if got := membershipPolicy(t, ctx, pool, user); got.group == nil || *got.group != group {
		t.Fatalf("membership access_group_id = %v, want %d", got.group, group)
	}
	// What Silo does before deleting a group: move its members to the default.
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id = (SELECT id FROM access_groups WHERE is_default) WHERE access_group_id = $1`, group); err != nil {
		t.Fatalf("move members back: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM access_groups WHERE id = $1`, group); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	var defaultGroup int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE is_default`).Scan(&defaultGroup); err != nil {
		t.Fatal(err)
	}
	if got := membershipPolicy(t, ctx, pool, user); got.group == nil || *got.group != defaultGroup {
		t.Fatalf("membership access_group_id = %v, want default %d", got.group, defaultGroup)
	}
}

func TestMirroredAdminKeepsNullGroup(t *testing.T) {
	ctx, pool, _, admin := mirroredPool(t)
	if _, err := pool.Exec(ctx, `UPDATE users SET max_streams = 9 WHERE id = $1`, admin); err != nil {
		t.Fatalf("admin policy write: %v", err)
	}
	got := membershipPolicy(t, ctx, pool, admin)
	if got.group != nil {
		t.Fatalf("admin membership access_group_id = %d, want NULL", *got.group)
	}
	if got.streams == nil || *got.streams != 9 {
		t.Fatalf("admin membership max_streams = %v, want 9", got.streams)
	}
}

func TestMirroredWriterMarker(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	var marker string
	if err := pool.QueryRow(ctx, `SELECT public.bloem_membership_policy_writer_marker()`).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != "" {
		t.Fatalf("compatibility marker = %q, want empty", marker)
	}
	enterMirrored(t, ctx, pool)
	if err := pool.QueryRow(ctx, `SELECT public.bloem_membership_policy_writer_marker()`).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != "v1" {
		t.Fatalf("mirrored marker = %q, want v1", marker)
	}
}

func TestMirroredRefusesSecondOrganization(t *testing.T) {
	ctx, pool, _, admin := mirroredPool(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
		VALUES ('two', 'Two', 'active', $1, false)`, admin)
	wantError(t, err, "bloem_silo_switching_single_organization")
}

func TestMirroredRefusesDirectProfileLogin(t *testing.T) {
	ctx, pool, user, _ := mirroredPool(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id)
		VALUES ('p1', $1, 'Main', true, public.bloem_default_organization_id(), (SELECT id FROM access_groups WHERE is_default))`, user); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	_, err := pool.Exec(ctx, `UPDATE user_profiles SET login_email = 'p@example.invalid', password_hash = 'x' WHERE id = 'p1'`)
	wantError(t, err, "bloem_silo_switching_direct_profile_login")
}

func TestCompatibilityStillFreezesPolicy(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	user := siloAccount(t, ctx, pool, "u1", "user")
	_, err := pool.Exec(ctx, `UPDATE users SET max_streams = 4 WHERE id = $1`, user)
	wantError(t, err, "membership_policy_fenced")
}

func TestEnablingMirroredRequiresMarker(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	_, err := pool.Exec(ctx, `UPDATE public.membership_policy_authority SET phase = 'mirrored', mirrored_at = now() WHERE singleton`)
	wantError(t, err, "membership_policy_authority_immutable")
}
