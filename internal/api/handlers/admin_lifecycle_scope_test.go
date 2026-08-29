package handlers

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
	"github.com/Silo-Server/silo-server/migrations"
)

func TestLifecycleSessionRevocationPreservesOrganizationScope(t *testing.T) {
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
	users := auth.NewUserRepository(pool)
	actor, err := users.Create(ctx, models.CreateUserInput{Username: "scope-actor-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "test-password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	target, err := users.Create(ctx, models.CreateUserInput{Username: "scope-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "test-password", Role: models.RoleUser})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	organizationA, organizationB := uuid.New(), uuid.New()
	groupIDs := make(map[uuid.UUID]int64, 2)
	for _, organizationID := range []uuid.UUID{organizationA, organizationB} {
		if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,name,slug,status,owner_account_id) VALUES ($1,$2,$3,'active',$4)`, organizationID, "Scope "+organizationID.String(), "scope-"+organizationID.String(), actor.ID); err != nil {
			t.Fatalf("create organization: %v", err)
		}
		var groupID int64
		if err := pool.QueryRow(ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,'Default',true) RETURNING id`, organizationID).Scan(&groupID); err != nil {
			t.Fatalf("create default access group: %v", err)
		}
		groupIDs[organizationID] = groupID
		for _, accountID := range []int{actor.ID, target.ID} {
			if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active',$3)`, organizationID, accountID, map[bool]string{true: "admin", false: "user"}[accountID == actor.ID]); err != nil {
				t.Fatalf("create membership: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=ANY($1::uuid[])`, []uuid.UUID{organizationA, organizationB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::integer[])`, []int{actor.ID, target.ID})
	})
	profileA, profileB := uuid.NewString(), uuid.NewString()
	for profileID, organizationID := range map[string]uuid.UUID{profileA: organizationA, profileB: organizationB} {
		if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,$3,$4,$5)`, profileID, target.ID, profileID, organizationID, groupIDs[organizationID]); err != nil {
			t.Fatalf("create profile: %v", err)
		}
	}
	sessionA, sessionB, accountSession := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for sessionID, profileID := range map[string]*string{sessionA: &profileA, sessionB: &profileB, accountSession: nil} {
		if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id,user_id,device_id,expires_at,profile_id,profile_credential_revision,auth_method) VALUES ($1,$2,'test',now()+interval '1 hour',$3,CASE WHEN $3::text IS NULL THEN NULL ELSE 1 END,CASE WHEN $3::text IS NULL THEN 'account' ELSE 'direct_profile' END)`, sessionID, target.ID, profileID); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}

	handler := NewAdminHandler(users, pool, nil)
	secret := []byte("organization-session-lifecycle-secret")
	handler.SetLifecycleIdempotency(lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)), lifecycleidempotency.NewRequestDigester(secret))
	var invalidated []string
	handler.OnUserProfileSessionsRevoked = func(_ context.Context, _ int, profileIDs []string) error {
		invalidated = append(invalidated, profileIDs...)
		return nil
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			requestCtx := apimw.SetClaims(r.Context(), claims)
			requestCtx = withAdminResourceOrganization(requestCtx, organizationA)
			next.ServeHTTP(w, r.WithContext(requestCtx))
		})
	})
	router.Delete("/users/{user_id}/auth-sessions/{session_id}", handler.HandleRevokeUserAuthSession)
	router.Delete("/users/{user_id}/auth-sessions", handler.HandleRevokeAllUserAuthSessions)

	foreignReq := httptest.NewRequest(http.MethodDelete, "/users/"+strconv.Itoa(target.ID)+"/auth-sessions/"+sessionB, nil)
	foreignReq.Header.Set("Idempotency-Key", "scoped-foreign-"+uuid.NewString())
	foreign := httptest.NewRecorder()
	router.ServeHTTP(foreign, foreignReq)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign profile session revoke = %d: %s", foreign.Code, foreign.Body.String())
	}

	allReq := httptest.NewRequest(http.MethodDelete, "/users/"+strconv.Itoa(target.ID)+"/auth-sessions", nil)
	allReq.Header.Set("Idempotency-Key", "scoped-all-"+uuid.NewString())
	all := httptest.NewRecorder()
	router.ServeHTTP(all, allReq)
	if all.Code != http.StatusNoContent {
		t.Fatalf("scoped revoke all = %d: %s", all.Code, all.Body.String())
	}
	for sessionID, wantActive := range map[string]bool{sessionA: false, sessionB: true, accountSession: true} {
		var active bool
		if err := pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM auth_sessions WHERE id=$1`, sessionID).Scan(&active); err != nil || active != wantActive {
			t.Fatalf("session %s active = %v, want %v, error = %v", sessionID, active, wantActive, err)
		}
	}
	if len(invalidated) != 1 || invalidated[0] != profileA {
		t.Fatalf("profile invalidations = %v, want [%s]", invalidated, profileA)
	}
}
