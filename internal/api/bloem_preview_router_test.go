package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
)

func TestPreviewPrincipalRouterBoundary(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	admin, err := users.Create(ctx, models.CreateUserInput{Username: "preview-admin", Email: "preview-admin@example.test", Password: "synthetic-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := users.Create(ctx, models.CreateUserInput{Username: "preview-user", Email: "preview-user@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entitlements.NewTemplateStore(pool).ApplyDefaultAccountTemplate(ctx, admin.ID, user.ID, accesspolicy.PreviewTemplateKey, 1, false); err != nil {
		t.Fatal(err)
	}
	if preview, err := auth.NewSessionRepository(pool).AccountPreviewOnly(ctx, user.ID); err != nil || !preview {
		var detail string
		_ = pool.QueryRow(ctx, `SELECT row_to_json(x)::text FROM (SELECT u.id,u.role,m.status,g.managed_template_key FROM users u LEFT JOIN organization_memberships m ON m.account_id=u.id LEFT JOIN access_groups g ON g.id=m.access_group_id WHERE u.id=$1) x`, user.ID).Scan(&detail)
		t.Fatalf("preview assignment did not make a preview principal: preview=%v err=%v %s", preview, err, detail)
	}

	profileID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,is_primary,organization_id,access_group_id) SELECT $1,$2,'Preview',true,organization_id,access_group_id FROM organization_memberships WHERE account_id=$2 AND status='active'`, profileID, user.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	if err := sessions.Create(ctx, models.AuthSession{ID: "preview-session", UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if role, preview, active, err := sessions.ActiveSessionAuthority(ctx, "preview-session"); err != nil || !preview || !active || role != "user" {
		t.Fatalf("session authority: %s preview=%v active=%v err=%v", role, preview, active, err)
	}
	const secret = "preview-router-test-secret"
	jwt := auth.NewJWTService(secret, time.Hour, time.Hour)
	bearer, err := jwt.GenerateAccessToken(user.ID, "user", "preview-session")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := jwt.GeneratePluginAccessToken(user.ID, "user", "preview-session", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.NewAPIKeyRepository(pool).Create(ctx, user.ID, "preview test", nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := browserPluginFixture{dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(fixture.dir, "page.html"), []byte("<html>plugin bytes</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{
		DB:                pool,
		FolderRepo:        catalog.NewFolderRepository(pool),
		FileRepo:          scanner.NewFileRepository(pool),
		SessionMgr:        playback.NewSessionManager(4, 2),
		Config:            &config.Config{Auth: config.AuthConfig{JWTSecret: secret, AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
		UserStoreProvider: pgstore.NewPostgresProvider(pool),
		PluginHTTPProxy:   plugins.NewHTTPProxyWithTypedResolver(fixture, nil),
	})
	if preview, err := sessions.AccountPreviewOnly(ctx, user.ID); err != nil || !preview {
		t.Fatalf("preview principal lost after router wiring: %v %v", preview, err)
	}
	serve := func(method, path, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("X-Profile-Id", profileID)
		switch credential {
		case "bearer":
			r.Header.Set("Authorization", "Bearer "+bearer)
		case "query":
			r.URL.RawQuery = "token=" + bearer
		case "cookie":
			r.AddCookie(&http.Cookie{Name: auth.PluginAccessCookieName, Value: cookie})
		case "api-key":
			r.Header.Set("Authorization", "Bearer "+key.Key)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if preview, err := sessions.AccountPreviewOnly(ctx, user.ID); err != nil || !preview {
			t.Fatalf("preview principal lost after %s %s: %v %v", method, path, preview, err)
		}
		return w
	}

	// Must allow: boot and browse. A 200, 404 for an unknown item, or 204 all
	// prove the boundary let the request through; a 403 does not.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v2/account/me"},
		{"GET", "/api/v2/profiles"},
		{"GET", "/api/v2/user/libraries"},
		{"GET", "/api/v2/home/sections"},
		{"GET", "/api/v2/catalog/items/movie:missing"},
		{"GET", "/api/bloem/v1/capabilities"},
	} {
		if w := serve(tc.method, tc.path, "bearer"); w.Code != http.StatusOK && !(strings.Contains(tc.path, "movie:missing") && w.Code == http.StatusNotFound) {
			t.Errorf("must allow %s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	// Must deny: minting, bytes, writes, recommendations that personalise, Live TV.
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v2/playback/start"},
		{"GET", "/api/v2/recommendations/for-you/main"},
		{"GET", "/api/v2/recommendations/discover"},
		{"POST", "/api/v2/recommendations/taste-seed"},
		{"PUT", "/api/v2/profile/sections"},
		{"POST", "/api/v2/collections"},
		{"GET", "/api/v2/markers/items/movie:missing"},
		{"POST", "/api/bloem/v1/sync/progress"},
		{"GET", "/api/bloem/v1/livetv/capability"},
		{"GET", "/api/v1/direct-download?token=x"},
		{"HEAD", "/api/v1/direct-download?token=x"},
		{"GET", "/api/v1/direct-download-proxy?token=x"},
		{"HEAD", "/api/v1/direct-download-proxy?token=x"},
		{"GET", "/api/v1/downloads/1/file"},
		{"HEAD", "/api/v1/downloads/1/file"},
		{"GET", "/api/v1/downloads/1/file-proxy"},
		{"HEAD", "/api/v1/downloads/1/file-proxy"},
		{"GET", "/api/v1/downloads/1/subtitles/1"},
		{"GET", "/api/v1/playback/transcode/missing/master.m3u8"},
		{"GET", "/api/v1/playback/transcode/missing/segment/1"},
		{"GET", "/api/v1/stream/missing"},
		{"HEAD", "/api/v1/stream/missing"},
		{"GET", "/api/v1/stream/missing/subtitles/1"},
		{"HEAD", "/api/v1/stream/missing/subtitles/1"},
		{"GET", "/api/v1/stream/missing/subtitles/1/fonts"},
		{"HEAD", "/api/v1/ebooks/book/files/1/read"},
		{"GET", "/api/v1/ebooks/book/files/1/read"},
		{"PUT", "/api/v1/ebooks/book/progress"},
	} {
		w := serve(tc.method, tc.path, "bearer")
		if w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != "preview_only" {
			t.Errorf("must deny %s %s: %d %q %s", tc.method, tc.path, w.Code, w.Header().Get("X-Bloem-Denial"), w.Body)
		}
	}
	// API keys are refused outright.
	if w := serve("GET", "/api/v2/account/me", "api-key"); w.Code != http.StatusForbidden {
		t.Errorf("preview API key: %d", w.Code)
	}
	// Plugin content treats every preview credential as anonymous.
	for _, credential := range []string{"bearer", "query", "cookie", "api-key"} {
		for _, path := range []string{"/api/v1/plugins/1/", plugins.ContentPrefix + "/plugins/1/"} {
			if w := serve("GET", path, credential); w.Code == http.StatusOK || w.Code == http.StatusPartialContent {
				t.Errorf("plugin %s via %s served bytes to a preview principal: %d", path, credential, w.Code)
			}
		}
		r := httptest.NewRequest("GET", "/api/v1/plugins/1/", nil)
		switch credential {
		case "bearer":
			r.Header.Set("Authorization", "Bearer "+bearer)
		case "query":
			r.URL.RawQuery = "token=" + bearer
		case "cookie":
			r.AddCookie(&http.Cookie{Name: auth.PluginAccessCookieName, Value: cookie})
		case "api-key":
			r.Header.Set("Authorization", "Bearer "+key.Key)
		}
		if authenticated, _, _, _, _ := resolveOptionalPluginAccessUser(r, jwt, sessions, auth.NewAPIKeyRepository(pool), users); authenticated {
			t.Errorf("plugin resolver authenticated preview %s", credential)
		}
	}
}
