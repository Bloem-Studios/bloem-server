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
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func tenantOrganizationLifecycleHarness(t *testing.T) (*pgxpool.Pool, *tenancy.Store, *auth.UserRepository, *models.User, http.Handler) {
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
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership a new account is given.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
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
	handler := handlers.NewAdminTenantsHandler(store, users)
	secret := []byte("tenant-organization-lifecycle-test")
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
	router.Post("/api/v1/admin/tenants", handler.HandleCreate)
	router.Post("/api/v1/admin/tenants/{id}/freeze", handler.HandleFreeze)
	router.Post("/api/v1/admin/tenants/{id}/thaw", handler.HandleThaw)
	router.Patch("/api/v1/admin/tenants/{id}/limits", handler.HandleUpdateLimits)
	router.Delete("/api/v1/admin/tenants/{id}", handler.HandleDelete)
	return pool, store, users, actor, router
}

func lifecycleTenantRequest(t *testing.T, router http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestTenantOrganizationLifecycleReplayPreservesLaterStateAndReplacement(t *testing.T) {
	pool, store, users, _, router := tenantOrganizationLifecycleHarness(t)
	ctx := context.Background()
	tenant, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Replay tenant", ExternalServiceID: "tenant-replay-" + uuid.NewString(), Slots: 2, Transcodes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := users.Create(ctx, models.CreateUserInput{
		Username: "tenant-org-member-" + uuid.NewString(), Email: uuid.NewString() + "@tenant-org.test",
		Password: "test-password", Role: models.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProvisionTenantMembership(ctx, tenant.ID, member.ID, "user"); err != nil {
		t.Fatal(err)
	}

	freezePath := "/api/v1/admin/tenants/" + tenant.ID.String() + "/freeze"
	freezeKey := "tenant-freeze-" + uuid.NewString()
	if got := lifecycleTenantRequest(t, router, http.MethodPost, freezePath, freezeKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("freeze = %d: %s", got.Code, got.Body.String())
	}
	if _, err := store.SetTenantOrganizationFrozen(ctx, tenant.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := lifecycleTenantRequest(t, router, http.MethodPost, freezePath, freezeKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("freeze replay = %d: %s", got.Code, got.Body.String())
	}
	if current, err := store.GetTenantOrganization(ctx, tenant.ID); err != nil || current.Frozen {
		t.Fatalf("freeze replay changed later state: %+v, %v", current, err)
	}

	limitsPath := "/api/v1/admin/tenants/" + tenant.ID.String() + "/limits"
	limitsKey := "tenant-limits-" + uuid.NewString()
	firstLimits := lifecycleTenantRequest(t, router, http.MethodPatch, limitsPath, limitsKey, map[string]int{"slots": 3, "transcodes": 2})
	if firstLimits.Code != http.StatusOK {
		t.Fatalf("limits = %d: %s", firstLimits.Code, firstLimits.Body.String())
	}
	if _, err := store.UpdateTenantOrganizationLimits(ctx, tenant.ID, 4, 3); err != nil {
		t.Fatal(err)
	}
	replayedLimits := lifecycleTenantRequest(t, router, http.MethodPatch, limitsPath, limitsKey, map[string]int{"slots": 3, "transcodes": 2})
	if replayedLimits.Code != firstLimits.Code || replayedLimits.Body.String() != firstLimits.Body.String() {
		t.Fatalf("limits replay = %d %q, want %d %q", replayedLimits.Code, replayedLimits.Body.String(), firstLimits.Code, firstLimits.Body.String())
	}
	if current, err := store.GetTenantOrganization(ctx, tenant.ID); err != nil || current.Slots != 4 || current.Transcodes != 3 {
		t.Fatalf("limits replay changed later state: %+v, %v", current, err)
	}

	deletePath := "/api/v1/admin/tenants/" + tenant.ID.String()
	deleteKey := "tenant-delete-" + uuid.NewString()
	if got := lifecycleTenantRequest(t, router, http.MethodDelete, deletePath, deleteKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", got.Code, got.Body.String())
	}
	var replacementIncarnation uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO users (id,username,email,password_hash,role)
VALUES ($1,$2,$3,'x','user') RETURNING account_incarnation_id`, member.ID,
		"replacement-"+uuid.NewString(), uuid.NewString()+"@tenant-org.test").Scan(&replacementIncarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE organizations SET slug=$2,external_service_id=$3,slots=2,transcodes=1,status='initializing'
WHERE id=$1`, tenant.ID, "replacement-"+uuid.NewString(), "replacement-service-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT $1,$2,'active','user'
WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
ON CONFLICT (organization_id, account_id) DO UPDATE
SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, tenant.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if got := lifecycleTenantRequest(t, router, http.MethodDelete, deletePath, deleteKey, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete replay = %d: %s", got.Code, got.Body.String())
	}
	var replacementPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND account_incarnation_id=$2)`, member.ID, replacementIncarnation).Scan(&replacementPresent); err != nil || !replacementPresent {
		t.Fatalf("replacement present=%v, err=%v", replacementPresent, err)
	}
	_ = store.DeleteTenantOrganization(ctx, tenant.ID)
	_ = users.Delete(ctx, member.ID)
}

func TestTenantOrganizationLifecycleUnavailableLeavesNoReceipt(t *testing.T) {
	pool, store, _, _, router := tenantOrganizationLifecycleHarness(t)
	ctx := context.Background()
	tenant, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Empty tenant", ExternalServiceID: "empty-" + uuid.NewString(), Slots: 1, Transcodes: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteTenantOrganization(context.Background(), tenant.ID) }()
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	key := "empty-tenant-freeze-" + uuid.NewString()
	got := lifecycleTenantRequest(t, router, http.MethodPost, "/api/v1/admin/tenants/"+tenant.ID.String()+"/freeze", key, nil)
	if got.Code != http.StatusServiceUnavailable || got.Header().Get("Retry-After") != "1" {
		t.Fatalf("empty freeze = %d headers=%v body=%s", got.Code, got.Header(), got.Body.String())
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_request_receipts`).Scan(&after); err != nil || after != before {
		t.Fatalf("receipt count after failed mutation=%d, before=%d, err=%v", after, before, err)
	}

	serviceID := "keyed-create-" + uuid.NewString()
	got = lifecycleTenantRequest(t, router, http.MethodPost, "/api/v1/admin/tenants", "tenant-create-"+uuid.NewString(), map[string]any{
		"name": "Keyed create", "external_ref": map[string]string{"service_id": serviceID},
		"limits": map[string]int{"slots": 1, "transcodes": 0},
	})
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("keyed create = %d: %s", got.Code, got.Body.String())
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE external_service_id=$1)`, serviceID).Scan(&exists); err != nil || exists {
		t.Fatalf("keyed create residue exists=%v, err=%v", exists, err)
	}
}
