package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

func platformAdminGateRequest(t *testing.T, gate func(http.Handler) http.Handler, claims *auth.Claims, admin *auth.AdminContextClaims) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/platform/tenants", nil)
	if claims != nil {
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
	}
	if admin != nil {
		req = req.WithContext(middleware.SetAdminContextClaims(req.Context(), *admin))
	}
	reached := false
	rec := httptest.NewRecorder()
	gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)
	return rec, reached
}

func TestRequireBloemPlatformAdminRefusesOtherCredentials(t *testing.T) {
	gate := RequireBloemPlatformAdmin(nil)
	for name, tc := range map[string]struct {
		claims *auth.Claims
		admin  *auth.AdminContextClaims
	}{
		"no credential":        {},
		"organization context": {admin: &auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization}},
		"account session":      {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAccess}},
		"user API key":         {claims: &auth.Claims{UserID: 7, Role: "user", TokenType: auth.TokenTypeAPIKey}},
		"scoped admin API key": {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: []string{auth.ScopeAdminMonitorRead}}},
	} {
		rec, reached := platformAdminGateRequest(t, gate, tc.claims, tc.admin)
		if reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_platform_authority"`) {
			t.Fatalf("%s: reached=%v response=%d %s", name, reached, rec.Code, rec.Body.String())
		}
	}
}

func TestRequireBloemPlatformAdminFailsClosedWithoutDatabase(t *testing.T) {
	gate := RequireBloemPlatformAdmin(nil)
	for name, tc := range map[string]struct {
		claims *auth.Claims
		admin  *auth.AdminContextClaims
	}{
		"platform context":       {admin: &auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}},
		"unscoped admin API key": {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey}},
	} {
		rec, reached := platformAdminGateRequest(t, gate, tc.claims, tc.admin)
		if reached || rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
			t.Fatalf("%s: reached=%v response=%d %s", name, reached, rec.Code, rec.Body.String())
		}
	}
}

func TestRequireBloemPlatformAdminRechecksTheAccount(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool := newCompatAdapterDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var adminID, disabledID, demotedID int
	for _, row := range []struct {
		name    string
		role    string
		enabled bool
		id      *int
	}{{"admin", "admin", true, &adminID}, {"disabled", "admin", false, &disabledID}, {"demoted", "user", true, &demotedID}} {
		if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ($1,$2,'x',$3,$4) RETURNING id`, row.name, row.name+"@example.test", row.role, row.enabled).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	gate := RequireBloemPlatformAdmin(pool)
	key := func(id int) *auth.Claims {
		return &auth.Claims{UserID: id, Role: "admin", TokenType: auth.TokenTypeAPIKey}
	}

	if rec, reached := platformAdminGateRequest(t, gate, key(adminID), nil); !reached || rec.Code != http.StatusNoContent {
		t.Fatalf("enabled admin key: reached=%v response=%d %s", reached, rec.Code, rec.Body.String())
	}
	if rec, reached := platformAdminGateRequest(t, gate, nil, &auth.AdminContextClaims{AccountID: adminID, Scope: auth.AdminScopePlatform}); !reached || rec.Code != http.StatusNoContent {
		t.Fatalf("platform context: reached=%v response=%d %s", reached, rec.Code, rec.Body.String())
	}
	// The key's claims still say admin, but the account no longer is one.
	for name, id := range map[string]int{"disabled": disabledID, "demoted": demotedID, "missing": adminID + demotedID + disabledID + 1000} {
		if rec, reached := platformAdminGateRequest(t, gate, key(id), nil); reached || rec.Code != http.StatusForbidden {
			t.Fatalf("%s account: reached=%v response=%d %s", name, reached, rec.Code, rec.Body.String())
		}
	}
}

func TestBloemPlatformAccountResourcePassesTheAccountAsUser(t *testing.T) {
	router := chi.NewRouter()
	var got string
	router.Get("/admin/platform/accounts/{account_id}/profiles", BloemPlatformAccountResource(func(w http.ResponseWriter, r *http.Request) {
		got = chi.URLParam(r, "user_id")
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/platform/accounts/42/profiles", nil))
	if rec.Code != http.StatusNoContent || got != "42" {
		t.Fatalf("response=%d user_id=%q", rec.Code, got)
	}
}
