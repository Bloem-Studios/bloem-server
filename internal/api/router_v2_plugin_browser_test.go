package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

type browserPluginFixture struct{ dir string }

func (browserPluginFixture) RouteDescriptors(context.Context, int) ([]*pluginv1.HttpRouteDescriptor, error) {
	return []*pluginv1.HttpRouteDescriptor{
		{Method: "GET", Path: "/", Access: "authenticated", StaticAsset: true},
		{Method: "GET", Path: "/app.js", Access: "authenticated", StaticAsset: true},
		{Method: "GET", Path: "/admin", Access: "admin", StaticAsset: true},
	}, nil
}
func (f browserPluginFixture) ResolveAssetPath(_ context.Context, _ int, path string) (string, error) {
	if path == "" || path == "admin" {
		path = "page.html"
	}
	return filepath.Join(f.dir, path), nil
}
func (browserPluginFixture) HTTPRoutesClient(context.Context, int, string) (*pluginhost.HTTPRoutesClient, error) {
	return nil, fmt.Errorf("fixture serves only static content")
}

// Follow the launch receipt with a real cookie jar, route/asset proxy, signed
// JWT and session repository. Plugin-provided absolute links are still owned
// by each plugin; this fixture exercises browser-relative page assets.
func TestV2PluginBrowserLaunchPostgres(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "bloem_plugin_browser_", true)
	if err := database.RunMigrations(t.Context(), pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewUserRepository(pool).Create(t.Context(), models.CreateUserInput{
		Username: "plugin-browser", Email: "plugin-browser@example.test", Password: "correct horse battery", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	if err := sessions.Create(t.Context(), models.AuthSession{
		ID: "browser-session", UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	jwt := auth.NewJWTService("synthetic-plugin-browser-test-key", time.Minute, time.Hour)
	fixture := browserPluginFixture{dir: t.TempDir()}
	for name, body := range map[string]string{"page.html": `<script src="app.js"></script>`, "app.js": "window.pluginLoaded = true;"} {
		if err := os.WriteFile(filepath.Join(fixture.dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	proxy := plugins.NewHTTPProxyWithTypedResolver(fixture, nil)
	content := plugins.NewContentHandler(proxy, func(r *http.Request) plugins.ContentAccess {
		authenticated, admin, userID, profileID, profileBound := resolveOptionalPluginAccessUser(r, jwt, sessions, nil, nil)
		return plugins.ContentAccess{Authenticated: authenticated, Admin: admin, UserID: userID, ProfileID: profileID, ProfileBound: profileBound}
	}, func(r *http.Request) plugins.ContentAccess {
		authenticated, admin, _, _, profileBound := resolveOptionalPluginAccessUser(r, jwt, sessions, nil, nil)
		return plugins.ContentAccess{Authenticated: authenticated, Admin: admin, ProfileBound: profileBound}
	})
	server := httptest.NewTLSServer(apiv2.NewHandler(apiv2.Dependencies{
		Auth:          apimw.NewAuthMiddleware(jwt, sessions, nil, nil),
		ViewerAccess:  apimw.NewViewerAccessMiddleware(v2WiringViewer{}),
		PluginLaunch:  handlers.NewAuthHandler(nil, jwt, nil),
		PluginContent: content,
	}))
	defer server.Close()
	client := server.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar
	request := func(method, path, bearer string, status int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.StatusCode, status, body)
		}
		return string(body)
	}
	page := plugins.ContentPrefix + "/plugins/1/"
	asset := plugins.ContentPrefix + "/plugin-assets/1/app.js"
	request(http.MethodGet, page, "", http.StatusUnauthorized)
	request(http.MethodGet, asset, "", http.StatusUnauthorized)
	member, err := jwt.GenerateAccessToken(user.ID, "user", "browser-session")
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/v2/auth/plugin-launch", member, http.StatusOK)
	if body := request(http.MethodGet, page, "", http.StatusOK); body != `<script src="app.js"></script>` {
		t.Fatal("plugin HTML changed", body)
	}
	for _, path := range []string{page + "app.js", asset} {
		if body := request(http.MethodGet, path, "", http.StatusOK); body != "window.pluginLoaded = true;" {
			t.Fatal("asset bytes changed", body)
		}
	}
	request(http.MethodGet, page+"admin", "", http.StatusForbidden)
	for _, path := range []string{"/api/v2/settings", "/api/v1/plugins/1/", "/"} {
		u, _ := url.Parse(server.URL + path)
		if len(jar.Cookies(u)) != 0 {
			t.Fatal("launch cookie escaped plugin content", path)
		}
	}
	setRole := func(role string) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `UPDATE users SET role=$1 WHERE id=$2`, role, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	// A promotion keeps the session; the member's access token must be
	// refreshed before it can launch again.
	setRole("admin")
	if body := request(http.MethodPost, "/api/v2/auth/plugin-launch", member, http.StatusUnauthorized); !strings.Contains(body, "token_refresh_required") {
		t.Fatal("stale access token was not told to refresh", body)
	}
	admin, err := jwt.GenerateAccessToken(user.ID, "admin", "browser-session")
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/v2/auth/plugin-launch", admin, http.StatusOK)
	request(http.MethodGet, page+"admin", "", http.StatusOK)
	// The launch cookie carries the admin role it was minted with, but admin
	// access follows the account's current role.
	setRole("user")
	request(http.MethodGet, page+"admin", "", http.StatusForbidden)
	request(http.MethodGet, page, "", http.StatusOK)
	if err := sessions.Revoke(t.Context(), "browser-session"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, page, "", http.StatusUnauthorized)
	request(http.MethodGet, asset, "", http.StatusUnauthorized)
}
