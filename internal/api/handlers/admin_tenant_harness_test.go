package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

// tenantAdminHarness serves the tenant organization routes as a platform
// administrator over a migrated test database.
func tenantAdminHarness(t *testing.T) (*pgxpool.Pool, *tenancy.Store, *auth.UserRepository, *models.User, http.Handler) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewUserRepository(pool)
	actor, err := users.Create(ctx, models.CreateUserInput{
		Username: "tenant-org-actor-" + uuid.NewString(), Email: uuid.NewString() + "@tenant-org.test",
		Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = users.Delete(context.Background(), actor.ID) })
	store := tenancy.NewStore(pool)
	members := tenancy.NewMemberService(pool, auth.NewAccountProvisioner(users, nil), users, auth.NewSessionRepository(pool))
	handler := handlers.NewAdminTenantsHandler(store, members)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: actor.ID, AccountIncarnationID: actor.AccountIncarnationID.String(), Role: models.RoleAdmin}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Post("/api/v1/admin/tenants", handler.HandleCreate)
	router.Post("/api/v1/admin/tenants/{id}/freeze", handler.HandleFreeze)
	router.Post("/api/v1/admin/tenants/{id}/thaw", handler.HandleThaw)
	router.Patch("/api/v1/admin/tenants/{id}/limits", handler.HandleUpdateLimits)
	router.Delete("/api/v1/admin/tenants/{id}", handler.HandleDelete)
	return pool, store, users, actor, router
}

func tenantAdminRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
	return recorder
}
