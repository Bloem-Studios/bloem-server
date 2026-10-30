package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/migrations"
)

func upgradePromptRequest(t *testing.T, h *BloemUpgradePromptHandler, method, body string, claims *auth.Claims, admin *auth.AdminContextClaims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, NativeAPIPrefix+"/admin/platform/upgrade-prompt", bytes.NewBufferString(body))
	if claims != nil {
		req = req.WithContext(middleware.SetClaims(req.Context(), claims))
	}
	if admin != nil {
		req = req.WithContext(middleware.SetAdminContextClaims(req.Context(), *admin))
	}
	rec := httptest.NewRecorder()
	if method == http.MethodGet {
		h.HandleGet(rec, req)
	} else {
		h.HandlePut(rec, req)
	}
	return rec
}

func TestBloemUpgradePromptRefusesOtherCredentials(t *testing.T) {
	h := NewBloemUpgradePromptHandler(nil)
	for name, tc := range map[string]struct {
		claims *auth.Claims
		admin  *auth.AdminContextClaims
	}{
		"no credential":        {},
		"organization context": {admin: &auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization}},
		"user API key":         {claims: &auth.Claims{UserID: 7, Role: "user", TokenType: auth.TokenTypeAPIKey}},
		"scoped admin API key": {claims: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: []string{auth.ScopeAdminMonitorRead}}},
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			rec := upgradePromptRequest(t, h, method, `{}`, tc.claims, tc.admin)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_platform_authority"`) {
				t.Fatalf("%s %s: response=%d %s", name, method, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestBloemUpgradePromptSavesAndReadsBack(t *testing.T) {
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
	var adminID int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled) VALUES ('admin','admin@example.test','x','admin',true) RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	h := NewBloemUpgradePromptHandler(pool)
	key := &auth.Claims{UserID: adminID, Role: "admin", TokenType: auth.TokenTypeAPIKey}

	bad := upgradePromptRequest(t, h, http.MethodPut, `{"enabled":true,"title":"More screens","cta_label":"See plans","cta_url":"http://shop.example.test"}`, key, nil)
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), "https") {
		t.Fatalf("plain http = %d %s", bad.Code, bad.Body.String())
	}
	saved := upgradePromptRequest(t, h, http.MethodPut, `{"enabled":true,"title":"More screens","body":"Your plan is in use on every screen it allows.","cta_label":"See plans","cta_url":"https://shop.example.test/services"}`, key, nil)
	if saved.Code != http.StatusOK || saved.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("save = %d %s", saved.Code, saved.Body.String())
	}
	read := upgradePromptRequest(t, h, http.MethodGet, ``, key, nil)
	var got notifications.UpgradePrompt
	if err := json.Unmarshal(read.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if read.Code != http.StatusOK || !got.Enabled || got.CTAURL != "https://shop.example.test/services" {
		t.Fatalf("read = %d %+v", read.Code, got)
	}
}
