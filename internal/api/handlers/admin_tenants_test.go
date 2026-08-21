package handlers_test

// The tenant admin API's wire contract — the exact JSON vondel-park's media
// adapter speaks. Field names here are pinned on BOTH sides (the adapter's
// own test fakes this shape); changing one without the other strands
// provisioning.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func tenantTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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
	handler := handlers.NewAdminTenantsHandler(tenancy.NewStore(pool), auth.NewUserRepository(pool))
	r := chi.NewRouter()
	r.Post("/api/v1/admin/tenants", handler.HandleCreate)
	r.Get("/api/v1/admin/tenants", handler.HandleList)
	r.Get("/api/v1/admin/tenants/{id}", handler.HandleGet)
	r.Patch("/api/v1/admin/tenants/{id}/limits", handler.HandleUpdateLimits)
	r.Post("/api/v1/admin/tenants/{id}/freeze", handler.HandleFreeze)
	r.Post("/api/v1/admin/tenants/{id}/thaw", handler.HandleThaw)
	r.Delete("/api/v1/admin/tenants/{id}", handler.HandleDelete)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return server, pool
}

func tenantCall(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

func TestTenantAdminAPIWireContract(t *testing.T) {
	server, pool := tenantTestServer(t)
	template, err := entitlements.NewTemplateStore(pool).Create(context.Background(), entitlements.CreateTemplateInput{
		Key: "tenant-wire-" + strings.ToLower(uuid.NewString()[:8]), Name: "Tenant wire " + uuid.NewString(), Enabled: true,
		Policy: entitlements.Policy{LibraryIDs: []int{}, PlaybackAllowed: true, MaxStreams: 2, MaxProfiles: 3, TranscodeAllowed: true, MaxTranscodes: 1, DownloadAllowed: true, MaxPlaybackQuality: "1080p", RequestsAllowed: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create: the park adapter's exact request shape; tenant_id comes back.
	status, body := tenantCall(t, server, http.MethodPost, "/api/v1/admin/tenants", map[string]any{
		"name":                          "acme@example.test",
		"external_ref":                  map[string]string{"operator_id": "op-1", "service_id": "order-" + t.Name()},
		"limits":                        map[string]int{"slots": 10, "transcodes": 4},
		"entitlement_template_key":      template.Key,
		"entitlement_template_revision": template.Revision,
	})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %s", status, body)
	}
	var created struct {
		TenantID string `json:"tenant_id"`
		Limits   struct {
			Slots      int `json:"slots"`
			Transcodes int `json:"transcodes"`
		} `json:"limits"`
		Frozen                     bool  `json:"frozen"`
		AppliedEntitlementRevision int64 `json:"applied_entitlement_revision"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.TenantID == "" ||
		created.Limits.Slots != 10 || created.Limits.Transcodes != 4 || created.AppliedEntitlementRevision != template.Revision {
		t.Fatalf("create body = %s (%v)", body, err)
	}

	// Idempotent on the park claim: a replayed create returns the SAME tenant.
	status, body = tenantCall(t, server, http.MethodPost, "/api/v1/admin/tenants", map[string]any{
		"name":                          "acme replay",
		"external_ref":                  map[string]string{"operator_id": "op-1", "service_id": "order-" + t.Name()},
		"limits":                        map[string]int{"slots": 99, "transcodes": 99},
		"entitlement_template_key":      template.Key,
		"entitlement_template_revision": template.Revision,
	})
	var replayed struct {
		TenantID string `json:"tenant_id"`
		Limits   struct {
			Slots int `json:"slots"`
		} `json:"limits"`
		AppliedEntitlementRevision int64 `json:"applied_entitlement_revision"`
	}
	if status != http.StatusCreated || json.Unmarshal(body, &replayed) != nil ||
		replayed.TenantID != created.TenantID || replayed.Limits.Slots != 10 || replayed.AppliedEntitlementRevision != template.Revision {
		t.Fatalf("replayed create = %d %s, want the original tenant unchanged", status, body)
	}

	// List answers a BARE array (the adapter decodes []tenantBody).
	status, body = tenantCall(t, server, http.MethodGet, "/api/v1/admin/tenants", nil)
	var listed []struct {
		TenantID string `json:"tenant_id"`
		Frozen   bool   `json:"frozen"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &listed) != nil {
		t.Fatalf("list = %d %s", status, body)
	}
	found := false
	for _, item := range listed {
		if item.TenantID == created.TenantID {
			found = true
		}
	}
	if !found {
		t.Fatalf("list = %s, want to find %s", body, created.TenantID)
	}

	// Freeze -> listed frozen; thaw -> back.
	if status, body = tenantCall(t, server, http.MethodPost,
		"/api/v1/admin/tenants/"+created.TenantID+"/freeze", nil); status != http.StatusNoContent {
		t.Fatalf("freeze = %d %s", status, body)
	}
	_, body = tenantCall(t, server, http.MethodGet, "/api/v1/admin/tenants/"+created.TenantID, nil)
	var got struct {
		Frozen bool `json:"frozen"`
	}
	if json.Unmarshal(body, &got) != nil || !got.Frozen {
		t.Fatalf("get after freeze = %s", body)
	}
	if status, _ = tenantCall(t, server, http.MethodPost,
		"/api/v1/admin/tenants/"+created.TenantID+"/thaw", nil); status != http.StatusNoContent {
		t.Fatalf("thaw = %d", status)
	}

	// Limits in place.
	status, body = tenantCall(t, server, http.MethodPatch,
		"/api/v1/admin/tenants/"+created.TenantID+"/limits", map[string]int{"slots": 20, "transcodes": 8})
	if status != http.StatusOK {
		t.Fatalf("limits = %d %s", status, body)
	}

	// Unknown id -> 404 (park maps this to its sentinel).
	if status, _ = tenantCall(t, server, http.MethodGet,
		"/api/v1/admin/tenants/00000000-0000-0000-0000-000000000000", nil); status != http.StatusNotFound {
		t.Fatalf("get unknown = %d, want 404", status)
	}

	// Delete; a second delete is the 404 the adapter maps to its sentinel.
	if status, _ = tenantCall(t, server, http.MethodDelete,
		"/api/v1/admin/tenants/"+created.TenantID, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _ = tenantCall(t, server, http.MethodDelete,
		"/api/v1/admin/tenants/"+created.TenantID, nil); status != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", status)
	}
}

// TestAdminHandlerCreateUserEnforcesTenantSlotQuota exercises the OTHER
// chokepoint the wire contract test above does not: POST /admin/users with
// organization_id set, gated by the same tenant slot quota
// TestTenantOrganizationLifecycle (internal/tenancy) already proves at the
// store level — this proves the HTTP handler actually wires it.
func TestAdminHandlerCreateUserEnforcesTenantSlotQuota(t *testing.T) {
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

	tenantStore := tenancy.NewStore(pool)
	tenant, err := tenantStore.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Slot Quota Co", ExternalOperatorID: "op-1", ExternalServiceID: "order-" + t.Name(),
		Slots: 1, Transcodes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Explicit ordering, not t.Cleanup's LIFO: the organization must retire
	// BEFORE the owner account is deleted (organizations.owner_account_id is
	// RESTRICT), the same order the admin handler's own delete flow follows.
	defer func() {
		_ = tenantStore.DeleteTenantOrganization(context.Background(), tenant.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email LIKE '%@tenant-quota.test'`)
	}()

	adminHandler := handlers.NewAdminHandler(auth.NewUserRepository(pool), pool, nil)
	adminHandler.SetTenantStore(tenantStore)
	r := chi.NewRouter()
	r.Post("/api/v1/admin/users", adminHandler.HandleCreateUser)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)

	createUser := func(username string) (int, []byte) {
		return tenantCall(t, server, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"username":        username,
			"email":           username + "@tenant-quota.test",
			"password":        "x-password-1",
			"role":            "admin",
			"organization_id": tenant.ID.String(),
		})
	}

	// First account: the tenant's sole slot, and it becomes the owner.
	status, body := createUser("owner")
	if status != http.StatusCreated {
		t.Fatalf("create first tenant user = %d %s", status, body)
	}

	// Second account: the tenant has one slot and it is already spent.
	status, body = createUser("overflow")
	if status != http.StatusConflict {
		t.Fatalf("create over-quota tenant user = %d %s, want 409", status, body)
	}
	var conflict struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &conflict) != nil || conflict.Error != "tenant_slots_exhausted" {
		t.Fatalf("over-quota error body = %s, want error=tenant_slots_exhausted", body)
	}

	// A tenant id that does not exist is a validation error, not a crash.
	status, body = tenantCall(t, server, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username":        "ghost",
		"email":           "ghost@tenant-quota.test",
		"password":        "x-password-1",
		"role":            "user",
		"organization_id": "00000000-0000-0000-0000-000000000000",
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("create against unknown tenant = %d %s, want 422", status, body)
	}
}
