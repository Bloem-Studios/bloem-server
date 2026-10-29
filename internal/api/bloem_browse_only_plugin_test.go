package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestBrowseOnlyPluginRouterPostgres(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	users := auth.NewUserRepository(pool)
	user, err := users.Create(t.Context(), models.CreateUserInput{Username: "browse-plugin", Email: "browse-plugin@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	if err := sessions.Create(t.Context(), models.AuthSession{ID: "browse-session", UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	jwt := auth.NewJWTService("browse-plugin-test-secret", time.Hour, time.Hour)
	bearer, err := jwt.GenerateAccessToken(user.ID, "user", "browse-session")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := jwt.GeneratePluginAccessToken(user.ID, "user", "browse-session", "", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.NewAPIKeyRepository(pool).Create(t.Context(), user.ID, "browse test", nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := browserPluginFixture{dir: t.TempDir()}
	for name, body := range map[string]string{"page.html": "<html>plugin bytes</html>", "app.js": "window.pluginLoaded = true;"} {
		if err := os.WriteFile(filepath.Join(fixture.dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(Dependencies{DB: pool, Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "browse-plugin-test-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}}, UserStoreProvider: pgstore.NewPostgresProvider(pool), PluginHTTPProxy: plugins.NewHTTPProxyWithTypedResolver(fixture, nil)})
	paths := []string{"/api/v1/plugins/1/", "/api/v1/plugin-assets/1/app.js", plugins.ContentPrefix + "/plugins/1/", plugins.ContentPrefix + "/plugin-assets/1/app.js"}
	setPlayback := func(allowed bool) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE access_groups SET playback_allowed=$1 WHERE is_default", allowed); err != nil {
			t.Fatal(err)
		}
	}
	// Reuse the same credentials across policy revocation and restoration.
	for _, allowed := range []bool{true, false, true} {
		setPlayback(allowed)
		for _, credential := range []string{"bearer", "query", "cookie", "api-key"} {
			for _, path := range paths {
				r := httptest.NewRequest("GET", path, nil)
				switch credential {
				case "bearer":
					r.Header.Set("Authorization", "Bearer "+bearer)
				case "query":
					r.URL.RawQuery = url.Values{"token": {bearer}}.Encode()
				case "cookie":
					r.AddCookie(&http.Cookie{Name: auth.PluginAccessCookieName, Value: cookie})
				case "api-key":
					r.Header.Set("Authorization", "Bearer "+key.Key)
				}
				r.Header.Set("Range", "bytes=2-4")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if credential == "api-key" && strings.Contains(path, "/plugin-assets/") {
					// Silo's plugin assets accept login tokens only (#3021).
					if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "ndo") {
						t.Fatalf("api-key asset %s: %d %s", path, w.Code, w.Body)
					}
					continue
				}
				if allowed {
					if w.Code != 206 {
						t.Fatalf("allowed %s %s: %d %s", credential, path, w.Code, w.Body)
					}
				} else if w.Code != 403 || strings.Contains(w.Body.String(), "ndo") {
					t.Fatalf("denied %s %s: %d %s", credential, path, w.Code, w.Body)
				}
			}
		}
	}
}
