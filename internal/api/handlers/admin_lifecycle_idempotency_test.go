package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestAdminDeleteUserLifecycleReplayDoesNotDeleteSameNumericReplacement(t *testing.T) {
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
	actor, err := users.Create(ctx, models.CreateUserInput{
		Username: "lifecycle-actor-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test",
		Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	target, err := users.Create(ctx, models.CreateUserInput{
		Username: "lifecycle-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test",
		Password: "test-password", Role: models.RoleUser,
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	for _, accountID := range []int{actor.ID, target.ID} {
		if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active',$3)`, organizationID, accountID, map[bool]string{true: "admin", false: "user"}[accountID == actor.ID]); err != nil {
			t.Fatalf("create membership for %d: %v", accountID, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::integer[])`, []int{actor.ID, target.ID})
	})

	handler := handlers.NewAdminHandler(users, pool, nil)
	secret := []byte("handler-lifecycle-idempotency-test-secret")
	handler.SetLifecycleIdempotency(
		lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)),
		lifecycleidempotency.NewRequestDigester(secret),
	)
	invalidations := 0
	handler.OnUserSessionsRevoked = func(context.Context, int) error {
		invalidations++
		return nil
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Delete("/api/v1/admin/users/{id}", handler.HandleDeleteUser)

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/"+strconv.Itoa(target.ID), nil)
		req.Header.Set("Idempotency-Key", "admin-delete-replay-"+strconv.Itoa(target.ID))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if first := request(); first.Code != http.StatusNoContent {
		t.Fatalf("first delete = %d: %s", first.Code, first.Body.String())
	}

	var replacementIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (id,username,email,password_hash,role)
VALUES ($1,$2,$3,'x','user')
RETURNING account_incarnation_id`, target.ID, "replacement-"+uuid.NewString(), uuid.NewString()+"@lifecycle.test").Scan(&replacementIncarnation); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, organizationID, target.ID); err != nil {
		t.Fatalf("create replacement membership: %v", err)
	}

	if replay := request(); replay.Code != http.StatusNoContent {
		t.Fatalf("replay delete = %d: %s", replay.Code, replay.Body.String())
	}
	var stillPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND account_incarnation_id=$2)`, target.ID, replacementIncarnation).Scan(&stillPresent); err != nil || !stillPresent {
		t.Fatalf("replacement present = %v, error = %v", stillPresent, err)
	}
	if invalidations != 1 {
		t.Fatalf("external invalidations = %d, want 1", invalidations)
	}
}

func TestRequestLimitLifecycleReplayDoesNotUpdateSameNumericReplacement(t *testing.T) {
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
	actor, err := users.Create(ctx, models.CreateUserInput{Username: "limit-actor-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "test-password", Role: models.RoleAdmin})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	target, err := users.Create(ctx, models.CreateUserInput{Username: "limit-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test", Password: "test-password", Role: models.RoleUser})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	for _, accountID := range []int{actor.ID, target.ID} {
		if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active',$3)`, organizationID, accountID, map[bool]string{true: "admin", false: "user"}[accountID == actor.ID]); err != nil {
			t.Fatalf("create membership: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::integer[])`, []int{actor.ID, target.ID})
	})

	handler := handlers.NewRequestsHandler(mediarequests.NewService(mediarequests.NewRepository(pool, nil), nil, nil))
	secret := []byte("request-limit-lifecycle-test-secret")
	handler.SetLifecycleIdempotency(
		lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)),
		lifecycleidempotency.NewRequestDigester(secret),
	)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Put("/api/v1/admin/request-users/{user_id}/limit", handler.HandleUpdateUserLimit)
	key := "request-limit-replay-" + uuid.NewString()
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/request-users/"+strconv.Itoa(target.ID)+"/limit", strings.NewReader(`{"limit_mode":"custom","max_requests":3,"window_days":7,"approval_mode":"manual"}`))
		req.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if first := request(); first.Code != http.StatusOK {
		t.Fatalf("first update = %d: %s", first.Code, first.Body.String())
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, target.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,username,email,password_hash,role) VALUES ($1,$2,$3,'x','user')`, target.ID, "replacement-"+uuid.NewString(), uuid.NewString()+"@lifecycle.test"); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, organizationID, target.ID); err != nil {
		t.Fatalf("create replacement membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO request_user_limits (user_id,limit_mode,max_requests,window_days,approval_mode) VALUES ($1,'custom',99,30,'auto')`, target.ID); err != nil {
		t.Fatalf("create replacement limit: %v", err)
	}
	if replay := request(); replay.Code != http.StatusOK {
		t.Fatalf("replay update = %d: %s", replay.Code, replay.Body.String())
	}
	var maxRequests int
	if err := pool.QueryRow(ctx, `SELECT max_requests FROM request_user_limits WHERE user_id=$1`, target.ID).Scan(&maxRequests); err != nil || maxRequests != 99 {
		t.Fatalf("replacement max requests = %d, error = %v", maxRequests, err)
	}
}

func TestAdminRevokeSessionsLifecycleReplayDoesNotRevokeSameNumericReplacement(t *testing.T) {
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
	actor, err := users.Create(ctx, models.CreateUserInput{
		Username: "lifecycle-session-actor-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test",
		Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','admin')`, organizationID, actor.ID); err != nil {
		t.Fatalf("create actor membership: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor.ID) })

	for _, test := range []struct {
		name string
		path func(int, string) string
	}{
		{name: "one", path: func(id int, sessionID string) string {
			return "/api/v1/admin/users/" + strconv.Itoa(id) + "/auth-sessions/" + sessionID
		}},
		{name: "all", path: func(id int, _ string) string {
			return "/api/v1/admin/users/" + strconv.Itoa(id) + "/auth-sessions"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, createErr := users.Create(ctx, models.CreateUserInput{
				Username: "lifecycle-session-target-" + uuid.NewString(), Email: uuid.NewString() + "@lifecycle.test",
				Password: "test-password", Role: models.RoleUser,
			})
			if createErr != nil {
				t.Fatalf("create target: %v", createErr)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, organizationID, target.ID); err != nil {
				t.Fatalf("create target membership: %v", err)
			}
			sessionID := uuid.NewString()
			if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id,user_id,device_id,expires_at) VALUES ($1,$2,'test',now()+interval '1 hour')`, sessionID, target.ID); err != nil {
				t.Fatalf("create session: %v", err)
			}

			handler := handlers.NewAdminHandler(users, pool, nil)
			secret := []byte("handler-session-lifecycle-test-secret")
			handler.SetLifecycleIdempotency(
				lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)),
				lifecycleidempotency.NewRequestDigester(secret),
			)
			invalidations := 0
			handler.OnUserSessionsRevoked = func(context.Context, int) error {
				invalidations++
				return nil
			}
			router := chi.NewRouter()
			router.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
					next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
				})
			})
			router.Delete("/api/v1/admin/users/{user_id}/auth-sessions/{session_id}", handler.HandleRevokeUserAuthSession)
			router.Delete("/api/v1/admin/users/{user_id}/auth-sessions", handler.HandleRevokeAllUserAuthSessions)
			key := "admin-session-replay-" + uuid.NewString()
			request := func(id int, sid string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodDelete, test.path(id, sid), nil)
				req.Header.Set("Idempotency-Key", key)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				return recorder
			}
			if first := request(target.ID, sessionID); first.Code != http.StatusNoContent {
				t.Fatalf("first revoke = %d: %s", first.Code, first.Body.String())
			}
			if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, target.ID); err != nil {
				t.Fatalf("delete target: %v", err)
			}
			var replacementIncarnation uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO users (id,username,email,password_hash,role) VALUES ($1,$2,$3,'x','user') RETURNING account_incarnation_id`, target.ID, "replacement-"+uuid.NewString(), uuid.NewString()+"@lifecycle.test").Scan(&replacementIncarnation); err != nil {
				t.Fatalf("create replacement: %v", err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, organizationID, target.ID); err != nil {
				t.Fatalf("create replacement membership: %v", err)
			}
			replacementSessionID := uuid.NewString()
			if _, err := pool.Exec(ctx, `INSERT INTO auth_sessions (id,user_id,device_id,expires_at) VALUES ($1,$2,'replacement',now()+interval '1 hour')`, replacementSessionID, target.ID); err != nil {
				t.Fatalf("create replacement session: %v", err)
			}
			if replay := request(target.ID, sessionID); replay.Code != http.StatusNoContent {
				t.Fatalf("replay revoke = %d: %s", replay.Code, replay.Body.String())
			}
			var active bool
			if err := pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM auth_sessions WHERE id=$1`, replacementSessionID).Scan(&active); err != nil || !active {
				t.Fatalf("replacement session active = %v, error = %v", active, err)
			}
			if invalidations != 1 {
				t.Fatalf("external invalidations = %d, want 1", invalidations)
			}
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, target.ID)
		})
	}
}
