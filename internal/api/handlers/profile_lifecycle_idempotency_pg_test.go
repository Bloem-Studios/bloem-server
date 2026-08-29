package handlers

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

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestDirectProfileLifecycleReplaysAndRollsBack(t *testing.T) {
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
	account, err := users.Create(ctx, models.CreateUserInput{
		Username: "profile-lifecycle-" + uuid.NewString(), Email: uuid.NewString() + "@profile-lifecycle.test",
		Password: "test-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = users.Delete(context.Background(), account.ID) })
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT id,$1,'active','admin' FROM organizations WHERE is_default`, account.ID); err != nil {
		t.Fatal(err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	store, _ := provider.ForUser(ctx, account.ID)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "primary-" + uuid.NewString(), Name: "Primary"}); err != nil {
		t.Fatal(err)
	}
	secondaryID := "secondary-" + uuid.NewString()
	if err := store.CreateProfile(ctx, userstore.Profile{ID: secondaryID, Name: "Secondary"}); err != nil {
		t.Fatal(err)
	}
	handler := NewProfileHandler(provider)
	handler.UserRepo = users
	secret := []byte("direct-profile-lifecycle-test")
	handler.SetLifecycleIdempotency(
		lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(pool), lifecycleidempotency.NewHMACKeyDigester(secret)),
		lifecycleidempotency.NewRequestDigester(secret),
	)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &auth.Claims{UserID: account.ID, AccountIncarnationID: account.AccountIncarnationID.String(), Role: models.RoleAdmin}
			next.ServeHTTP(w, r.WithContext(apimw.SetClaims(r.Context(), claims)))
		})
	})
	router.Post("/api/v1/profiles/", handler.HandleCreateProfile)
	router.Put("/api/v1/profiles/{id}", handler.HandleUpdateProfile)
	router.Delete("/api/v1/profiles/{id}", handler.HandleDeleteProfile)
	call := func(method, path, key string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	updateKey := "profile-update-" + uuid.NewString()
	updatePath := "/api/v1/profiles/" + secondaryID
	firstUpdate := call(http.MethodPut, updatePath, updateKey, map[string]string{"name": "First"})
	if firstUpdate.Code != http.StatusOK {
		t.Fatalf("first update=%d: %s", firstUpdate.Code, firstUpdate.Body.String())
	}
	later := "Later"
	if err := store.UpdateProfile(ctx, secondaryID, userstore.UpdateProfileInput{Name: &later}); err != nil {
		t.Fatal(err)
	}
	replayUpdate := call(http.MethodPut, updatePath, updateKey, map[string]string{"name": "First"})
	if replayUpdate.Code != http.StatusOK || replayUpdate.Body.String() != firstUpdate.Body.String() {
		t.Fatalf("update replay=%d %s, first=%s", replayUpdate.Code, replayUpdate.Body.String(), firstUpdate.Body.String())
	}
	if got, _ := store.GetProfile(ctx, secondaryID); got == nil || got.Name != later {
		t.Fatalf("update replay remutated profile: %+v", got)
	}

	deleteKey := "profile-delete-" + uuid.NewString()
	if got := call(http.MethodDelete, updatePath, deleteKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("first delete=%d: %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodDelete, updatePath, deleteKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete replay without target=%d: %s", got.Code, got.Body.String())
	}
	if err := store.CreateProfile(ctx, userstore.Profile{ID: secondaryID, Name: "Replacement"}); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodDelete, updatePath, deleteKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete replay=%d: %s", got.Code, got.Body.String())
	}
	if replacement, _ := store.GetProfile(ctx, secondaryID); replacement == nil || replacement.Name != "Replacement" {
		t.Fatalf("delete replay removed replacement: %+v", replacement)
	}

	createKey := "profile-create-" + uuid.NewString()
	firstCreate := call(http.MethodPost, "/api/v1/profiles/", createKey, map[string]string{"name": "Created"})
	if firstCreate.Code != http.StatusCreated {
		t.Fatalf("first create=%d: %s", firstCreate.Code, firstCreate.Body.String())
	}
	var created profileResponse
	if err := json.Unmarshal(firstCreate.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("created response=%s, err=%v", firstCreate.Body.String(), err)
	}
	if err := store.DeleteProfile(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProfile(ctx, userstore.Profile{ID: created.ID, Name: "Create replacement"}); err != nil {
		t.Fatal(err)
	}
	replayCreate := call(http.MethodPost, "/api/v1/profiles/", createKey, map[string]string{"name": "Created"})
	if replayCreate.Code != http.StatusCreated || replayCreate.Body.String() != firstCreate.Body.String() {
		t.Fatalf("create replay=%d %s, first=%s", replayCreate.Code, replayCreate.Body.String(), firstCreate.Body.String())
	}
	if replacement, _ := store.GetProfile(ctx, created.ID); replacement == nil || replacement.Name != "Create replacement" {
		t.Fatalf("create replay remutated replacement: %+v", replacement)
	}

	var receiptsBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts`).Scan(&receiptsBefore)
	fencedName := "Fenced " + uuid.NewString()
	fenced := call(http.MethodPost, "/api/v1/profiles/", "profile-fenced-"+uuid.NewString(), map[string]string{"name": fencedName, "pin": "1234"})
	if fenced.Code != http.StatusServiceUnavailable || fenced.Header().Get("Retry-After") != "1" {
		t.Fatalf("fenced create=%d headers=%v body=%s", fenced.Code, fenced.Header(), fenced.Body.String())
	}
	var profileExists bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id=$1 AND name=$2)`, account.ID, fencedName).Scan(&profileExists)
	var receiptsAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts`).Scan(&receiptsAfter)
	if profileExists || receiptsAfter != receiptsBefore {
		t.Fatalf("fenced mutation residue profile=%v receipts=%d->%d", profileExists, receiptsBefore, receiptsAfter)
	}
}
