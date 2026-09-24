package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

type countingViewerResolver struct{ calls int }

func (r *countingViewerResolver) Resolve(context.Context, access.ResolveInput) (access.Scope, error) {
	r.calls++
	return access.Scope{}, nil
}

// orgRevocationFixture is one account with a membership and a profile in each
// of two organizations, and an account-login session created an hour ago.
// Every case seeds its own account into one shared disposable database.
type orgRevocationFixture struct {
	pool                 *pgxpool.Pool
	account              int
	revokedOrg, otherOrg uuid.UUID
}

const orgRevocationSession = "account-login"

func (f orgRevocationFixture) id(name string) string { return fmt.Sprintf("%s-%d", name, f.account) }

func seedOrgRevocationFixture(t *testing.T, pool *pgxpool.Pool, account int) orgRevocationFixture {
	t.Helper()
	ctx := context.Background()
	f := orgRevocationFixture{pool: pool, account: account, revokedOrg: uuid.New(), otherOrg: uuid.New()}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO users (id,email,username,password_hash,role) VALUES ($1,$2,$2,'unused','user')`, account, fmt.Sprintf("member-%d@example.test", account))
	for i, org := range []uuid.UUID{f.revokedOrg, f.otherOrg} {
		name := []string{"revoked", "other"}[i]
		exec(`INSERT INTO organizations (id,slug,name,status,owner_account_id,is_default) VALUES ($1,$2,$2,'active',$3,false)`, org, f.id(name), account)
		var group int64
		if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Viewers',false) RETURNING id`, org).Scan(&group); err != nil {
			t.Fatalf("seed access group: %v", err)
		}
		exec(`INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
			SELECT $1,$2,'active','user'
			WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, org, account)
		exec(`INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,$1,$3,$4)`, f.id(name+"-profile"), account, org, group)
	}
	exec(`INSERT INTO auth_sessions (id,user_id,created_at,expires_at) VALUES ($1,$2,now()-interval '1 hour',now()+interval '1 hour')`, f.id(orgRevocationSession), account)
	return f
}

// revoke runs the revocation's membership write exactly as
// handlers.revokeOrganizationMemberAccess issues it.
func (f orgRevocationFixture) revoke(t *testing.T, org uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE organization_memberships
		SET security_revision = security_revision + 1, updated_at = now()
		WHERE organization_id = $1 AND account_id = $2`, org, f.account); err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
}

func (f orgRevocationFixture) resolve(t *testing.T, sessionID, profileID string) (int, error) {
	t.Helper()
	inner := &countingViewerResolver{}
	viewer := bloemOrgRevocationAwareViewer(inner, f.pool)
	_, err := viewer.Resolve(context.Background(), access.ResolveInput{
		UserID: f.account, SessionID: sessionID, ProfileID: profileID,
	})
	return inner.calls, err
}

func TestOrgSessionRevocation(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "bloem_org_revocation_", true)
	ctx := context.Background()
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}

	t.Run("refuses the revoked organization's profiles", func(t *testing.T) {
		f := seedOrgRevocationFixture(t, pool, 41)
		session := f.id(orgRevocationSession)
		if calls, err := f.resolve(t, session, f.id("revoked-profile")); err != nil || calls != 1 {
			t.Fatalf("before revocation: err=%v calls=%d, want the profile selectable", err, calls)
		}

		f.revoke(t, f.revokedOrg)

		calls, err := f.resolve(t, session, f.id("revoked-profile"))
		if !errors.Is(err, access.ErrProfileNotFound) || calls != 0 {
			t.Fatalf("revoked organization's profile: err=%v calls=%d, want ErrProfileNotFound before viewer resolution", err, calls)
		}
		// The member still belongs to the other organization, and that
		// tenant's profiles stay reachable from the same session.
		if calls, err := f.resolve(t, session, f.id("other-profile")); err != nil || calls != 1 {
			t.Fatalf("other organization's profile: err=%v calls=%d, want it selectable", err, calls)
		}
		// A profile-less request selects nothing and is not checked.
		if calls, err := f.resolve(t, session, ""); err != nil || calls != 1 {
			t.Fatalf("profile-less request: err=%v calls=%d", err, calls)
		}
	})

	t.Run("allows sessions created after the revocation", func(t *testing.T) {
		f := seedOrgRevocationFixture(t, pool, 42)
		f.revoke(t, f.revokedOrg)
		if _, err := pool.Exec(ctx, `
			INSERT INTO auth_sessions (id,user_id,created_at,expires_at)
			VALUES ($1,$2,now()+interval '1 second',now()+interval '1 hour')`, f.id("fresh-login"), f.account); err != nil {
			t.Fatalf("seed fresh session: %v", err)
		}
		if calls, err := f.resolve(t, f.id("fresh-login"), f.id("revoked-profile")); err != nil || calls != 1 {
			t.Fatalf("session created after the revocation: err=%v calls=%d, want the profile selectable", err, calls)
		}
	})

	t.Run("ignores role changes", func(t *testing.T) {
		f := seedOrgRevocationFixture(t, pool, 43)
		if _, err := pool.Exec(ctx, `
			UPDATE organization_memberships
			SET legacy_role = 'admin', security_revision = security_revision + 1, updated_at = now()
			WHERE organization_id = $1 AND account_id = $2`, f.revokedOrg, f.account); err != nil {
			t.Fatalf("change role: %v", err)
		}
		var recorded int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_org_session_revocations WHERE account_id = $1`, f.account).Scan(&recorded); err != nil {
			t.Fatalf("count revocations: %v", err)
		}
		if recorded != 0 {
			t.Fatalf("a role change recorded %d revocations, want none", recorded)
		}
		if calls, err := f.resolve(t, f.id(orgRevocationSession), f.id("revoked-profile")); err != nil || calls != 1 {
			t.Fatalf("after a role change: err=%v calls=%d, want the profile selectable", err, calls)
		}
	})

	t.Run("skips credentials without an account-login session", func(t *testing.T) {
		f := seedOrgRevocationFixture(t, pool, 44)
		f.revoke(t, f.revokedOrg)
		// An API key carries no session id.
		if calls, err := f.resolve(t, "", f.id("revoked-profile")); err != nil || calls != 1 {
			t.Fatalf("sessionless credential: err=%v calls=%d", err, calls)
		}
		// An unknown session id matches no row; the session middleware, not
		// this check, rejects it.
		if calls, err := f.resolve(t, "no-such-session", f.id("revoked-profile")); err != nil || calls != 1 {
			t.Fatalf("unknown session: err=%v calls=%d", err, calls)
		}
	})
}

func TestOrgSessionRevocationWithoutDatabaseIsTheInnerResolver(t *testing.T) {
	inner := &countingViewerResolver{}
	if got := bloemOrgRevocationAwareViewer(inner, nil); got != inner {
		t.Fatalf("without a database the decorator must return the inner resolver unchanged")
	}
}
