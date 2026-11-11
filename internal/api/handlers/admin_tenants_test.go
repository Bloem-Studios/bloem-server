package handlers_test

// The tenant admin API's wire contract — the exact JSON bloem-park's media
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
	users := auth.NewUserRepository(pool)
	members := tenancy.NewMemberService(pool, auth.NewAccountProvisioner(users, nil), users, auth.NewSessionRepository(pool))
	handler := handlers.NewAdminTenantsHandler(tenancy.NewStore(pool), members)
	r := chi.NewRouter()
	r.Post("/api/bloem/v2/admin/platform/tenants", handler.HandleCreate)
	r.Get("/api/bloem/v2/admin/platform/tenants", handler.HandleList)
	r.Get("/api/bloem/v2/admin/platform/tenants/{id}", handler.HandleGet)
	r.Patch("/api/bloem/v2/admin/platform/tenants/{id}/limits", handler.HandleUpdateLimits)
	r.Post("/api/bloem/v2/admin/platform/tenants/{id}/freeze", handler.HandleFreeze)
	r.Post("/api/bloem/v2/admin/platform/tenants/{id}/thaw", handler.HandleThaw)
	r.Delete("/api/bloem/v2/admin/platform/tenants/{id}", handler.HandleDelete)
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
	status, body := tenantCall(t, server, http.MethodPost, "/api/bloem/v2/admin/platform/tenants", map[string]any{
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
	status, body = tenantCall(t, server, http.MethodPost, "/api/bloem/v2/admin/platform/tenants", map[string]any{
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
	status, body = tenantCall(t, server, http.MethodGet, "/api/bloem/v2/admin/platform/tenants", nil)
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
		"/api/bloem/v2/admin/platform/tenants/"+created.TenantID+"/freeze", nil); status != http.StatusNoContent {
		t.Fatalf("freeze = %d %s", status, body)
	}
	_, body = tenantCall(t, server, http.MethodGet, "/api/bloem/v2/admin/platform/tenants/"+created.TenantID, nil)
	var got struct {
		Frozen bool `json:"frozen"`
	}
	if json.Unmarshal(body, &got) != nil || !got.Frozen {
		t.Fatalf("get after freeze = %s", body)
	}
	if status, _ = tenantCall(t, server, http.MethodPost,
		"/api/bloem/v2/admin/platform/tenants/"+created.TenantID+"/thaw", nil); status != http.StatusNoContent {
		t.Fatalf("thaw = %d", status)
	}

	// Limits in place.
	status, body = tenantCall(t, server, http.MethodPatch,
		"/api/bloem/v2/admin/platform/tenants/"+created.TenantID+"/limits", map[string]int{"slots": 20, "transcodes": 8})
	if status != http.StatusOK {
		t.Fatalf("limits = %d %s", status, body)
	}

	// Unknown id -> 404 (park maps this to its sentinel).
	if status, _ = tenantCall(t, server, http.MethodGet,
		"/api/bloem/v2/admin/platform/tenants/00000000-0000-0000-0000-000000000000", nil); status != http.StatusNotFound {
		t.Fatalf("get unknown = %d, want 404", status)
	}

	// Delete; a second delete is the 404 the adapter maps to its sentinel.
	if status, _ = tenantCall(t, server, http.MethodDelete,
		"/api/bloem/v2/admin/platform/tenants/"+created.TenantID, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _ = tenantCall(t, server, http.MethodDelete,
		"/api/bloem/v2/admin/platform/tenants/"+created.TenantID, nil); status != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", status)
	}
}

// A server built without the member service cannot retire a tenant. It says
// the tenant authority is unavailable, not lifecycle idempotency, which
// Bloem removed in b017bfe84.
func TestTenantRetirementWithoutMemberServiceIsTenantUnavailable(t *testing.T) {
	h := handlers.NewAdminTenantsHandler(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/tenants/"+uuid.NewString(), nil)
	req.SetPathValue("id", strings.TrimPrefix(req.URL.Path, "/admin/tenants/"))
	rec := httptest.NewRecorder()
	h.HandleDelete(rec, req)

	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable || body.Error != "tenant_unavailable" {
		t.Fatalf("status %d error %q; want 503 tenant_unavailable", rec.Code, body.Error)
	}
}
