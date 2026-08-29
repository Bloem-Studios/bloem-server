package handlers_test

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

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
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
