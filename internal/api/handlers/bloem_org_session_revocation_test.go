package handlers

// Bloem-owned. An organization administrator's "revoke all sessions" used to
// revoke only sessions bound to that organization's profiles. Account-login
// sessions (profile_id NULL) survived and could still select the
// organization's profiles, and nothing bound to the membership noticed.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

type orgRevocationFixture struct {
	pool                     *pgxpool.Pool
	actor, target            *models.User
	orgA, orgB               uuid.UUID
	profileA                 string
	sessionA, accountSession string
	wholeUserCalls           int
	profileInvalidations     []string
	handler                  *AdminHandler
	router                   chi.Router
	revisionA, revisionB     int64
	targetInOrgB             bool
}

func newOrgRevocationFixture(t *testing.T, targetInOrgB, lifecycle bool) *orgRevocationFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	f := &orgRevocationFixture{pool: pool, targetInOrgB: targetInOrgB}
	users := auth.NewUserRepository(pool)
	if f.actor, err = users.Create(ctx, models.CreateUserInput{Username: "orgrev-actor-" + uuid.NewString(), Email: uuid.NewString() + "@orgrev.test", Password: "test-password", Role: models.RoleAdmin}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	if f.target, err = users.Create(ctx, models.CreateUserInput{Username: "orgrev-target-" + uuid.NewString(), Email: uuid.NewString() + "@orgrev.test", Password: "test-password", Role: models.RoleUser}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	f.orgA, f.orgB = uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=ANY($1::uuid[])`, []uuid.UUID{f.orgA, f.orgB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::integer[])`, []int{f.actor.ID, f.target.ID})
	})
	// A freshly created account is given a default-organization membership;
	// remove the target's so its only memberships are the ones seeded here.
	if _, err := pool.Exec(ctx, `DELETE FROM organization_memberships WHERE account_id=$1`, f.target.ID); err != nil {
		t.Fatalf("clear target memberships: %v", err)
	}
	groupIDs := map[uuid.UUID]int64{}
	for _, organizationID := range []uuid.UUID{f.orgA, f.orgB} {
		if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,status,owner_account_id) VALUES ($1,$2,$3,'active',$4)`, organizationID, "OrgRev "+organizationID.String(), "orgrev-"+organizationID.String(), f.actor.ID); err != nil {
			t.Fatalf("create organization: %v", err)
		}
		var groupID int64
		if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Default',true) RETURNING id`, organizationID).Scan(&groupID); err != nil {
			t.Fatalf("create default access group: %v", err)
		}
		groupIDs[organizationID] = groupID
		members := []int{f.actor.ID}
		if organizationID == f.orgA || targetInOrgB {
			members = append(members, f.target.ID)
		}
		for _, accountID := range members {
			if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT $1,$2,'active',$3
WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
ON CONFLICT (organization_id, account_id) DO UPDATE SET status = EXCLUDED.status`, organizationID, accountID, map[bool]string{true: "admin", false: "user"}[accountID == f.actor.ID]); err != nil {
				t.Fatalf("create membership: %v", err)
			}
		}
	}
	f.profileA = uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,$3,$4,$5)`, f.profileA, f.target.ID, f.profileA, f.orgA, groupIDs[f.orgA]); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	f.sessionA, f.accountSession = uuid.NewString(), uuid.NewString()
	for sessionID, profileID := range map[string]*string{f.sessionA: &f.profileA, f.accountSession: nil} {
		if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id,user_id,device_id,expires_at,profile_id,profile_credential_revision,auth_method) VALUES ($1,$2,'test',now()+interval '1 hour',$3,CASE WHEN $3::text IS NULL THEN NULL ELSE 1 END,CASE WHEN $3::text IS NULL THEN 'account' ELSE 'direct_profile' END)`, sessionID, f.target.ID, profileID); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	f.revisionA = f.revision(t, f.orgA)
	if targetInOrgB {
		f.revisionB = f.revision(t, f.orgB)
	}

	f.handler = NewAdminHandler(users, pool, pgstore.NewPostgresProvider(pool))
	if lifecycle {
		secret := []byte("org-revocation-secret")
		f.handler.SetLifecycleIdempotency(lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)), lifecycleidempotency.NewRequestDigester(secret))
	}
	f.handler.OnUserProfileSessionsRevoked = func(_ context.Context, _ int, profileIDs []string) error {
		f.profileInvalidations = append(f.profileInvalidations, profileIDs...)
		return nil
	}
	f.handler.OnUserSessionsRevoked = func(context.Context, int) error {
		f.wholeUserCalls++
		return nil
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: f.actor.ID, AccountIncarnationID: f.actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			requestCtx := apimw.SetClaims(r.Context(), claims)
			requestCtx = withAdminResourceOrganization(requestCtx, f.orgA)
			next.ServeHTTP(w, r.WithContext(requestCtx))
		})
	})
	router.Delete("/users/{user_id}/auth-sessions", f.handler.HandleRevokeAllUserAuthSessions)
	f.router = router
	return f
}

func (f *orgRevocationFixture) revision(t *testing.T, organizationID uuid.UUID) int64 {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(context.Background(), `SELECT security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, organizationID, f.target.ID).Scan(&revision); err != nil {
		t.Fatalf("load security revision: %v", err)
	}
	return revision
}

func (f *orgRevocationFixture) active(t *testing.T, sessionID string) bool {
	t.Helper()
	var active bool
	if err := f.pool.QueryRow(context.Background(), `SELECT revoked_at IS NULL FROM auth_sessions WHERE id=$1`, sessionID).Scan(&active); err != nil {
		t.Fatalf("load session: %v", err)
	}
	return active
}

func (f *orgRevocationFixture) revokeAll(t *testing.T, lifecycle bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/users/"+strconv.Itoa(f.target.ID)+"/auth-sessions", nil)
	if lifecycle {
		req.Header.Set("Idempotency-Key", "orgrev-"+uuid.NewString())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke all = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBloemOrganizationRevokeAllInvalidatesMembership(t *testing.T) {
	for _, lifecycle := range []bool{true, false} {
		name := map[bool]string{true: "lifecycle", false: "direct"}[lifecycle]

		// The member belongs to another organization too: the account-login
		// session serves that organization and survives, but the membership's
		// security revision moves so every session bound to it is stale.
		t.Run(name+"/other membership", func(t *testing.T) {
			f := newOrgRevocationFixture(t, true, lifecycle)
			f.revokeAll(t, lifecycle)
			if f.active(t, f.sessionA) {
				t.Fatal("organization profile session survived")
			}
			if !f.active(t, f.accountSession) {
				t.Fatal("account session revoked although the account has another active membership")
			}
			if got := f.revision(t, f.orgA); got <= f.revisionA {
				t.Fatalf("organization A security revision = %d, want > %d", got, f.revisionA)
			}
			if got := f.revision(t, f.orgB); got != f.revisionB {
				t.Fatalf("organization B security revision = %d, want unchanged %d", got, f.revisionB)
			}
			if f.wholeUserCalls != 0 {
				t.Fatalf("whole-account compat invalidations = %d, want 0", f.wholeUserCalls)
			}
			if len(f.profileInvalidations) != 1 || f.profileInvalidations[0] != f.profileA {
				t.Fatalf("profile invalidations = %v, want [%s]", f.profileInvalidations, f.profileA)
			}
		})

		// The organization is the member's only one: the account-login session
		// can reach nothing but this organization, so it is revoked too.
		t.Run(name+"/only membership", func(t *testing.T) {
			f := newOrgRevocationFixture(t, false, lifecycle)
			f.revokeAll(t, lifecycle)
			if f.active(t, f.sessionA) {
				t.Fatal("organization profile session survived")
			}
			if f.active(t, f.accountSession) {
				t.Fatal("account-login session survived although this organization is the account's only membership")
			}
			if got := f.revision(t, f.orgA); got <= f.revisionA {
				t.Fatalf("organization A security revision = %d, want > %d", got, f.revisionA)
			}
			if f.wholeUserCalls != 1 {
				t.Fatalf("whole-account compat invalidations = %d, want 1", f.wholeUserCalls)
			}
		})
	}
}
