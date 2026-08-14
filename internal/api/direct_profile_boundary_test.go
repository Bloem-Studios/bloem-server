package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// TestDirectProfileSessionBoundary exercises the real router. A direct-profile
// session is one profile and nothing more: it may act as itself, and every
// account-, household-, and sibling-scoped surface must refuse it. Middleware
// unit tests prove the helpers; only the wired router proves the routes
// actually use them.
func TestDirectProfileSessionBoundary(t *testing.T) {
	ctx := context.Background()
	pool := newV1TenancyDatabase(t)
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          "direct-profile-boundary-secret",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}
	router := NewRouter(Dependencies{
		DB:                    pool,
		Config:                cfg,
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
		SessionMgr:            playback.NewSessionManager(4, 2),
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
		// Mounted only so the proxy route exists. A direct-profile token is
		// refused before the proxy is ever asked to serve anything, which is
		// the property under test.
		PluginHTTPProxy: plugins.NewHTTPProxy(nil, nil),
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"owner","email":"owner@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	accountToken := decodeLogin(t, setup).AccessToken

	var accountID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&accountID); err != nil {
		t.Fatalf("load owner: %v", err)
	}
	var primaryProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, accountID).Scan(&primaryProfileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}

	created := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/", `{"name":"Reader"}`, accountToken, nil)
	if created.Code != http.StatusCreated && created.Code != http.StatusOK {
		t.Fatalf("create reader profile = %d %s", created.Code, created.Body.String())
	}
	var readerProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND name = 'Reader'`, accountID).Scan(&readerProfileID); err != nil {
		t.Fatalf("load reader profile: %v", err)
	}

	credentials := auth.NewProfileCredentialService(pool)
	if err := credentials.Set(ctx, accountID, readerProfileID, "reader@example.test", "reader-password"); err != nil {
		t.Fatalf("set reader credential: %v", err)
	}

	login := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
		`{"email":"reader@example.test","password":"reader-password","device_id":"reader-tablet"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("profile login = %d %s", login.Code, login.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
		ProfileID   string `json:"profile_id"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode profile login: %v", err)
	}
	if loginBody.ProfileID != readerProfileID {
		t.Fatalf("profile login bound %q, want %q", loginBody.ProfileID, readerProfileID)
	}
	directToken := loginBody.AccessToken

	// Every account, household, and sibling surface refuses the direct session.
	refused := []struct {
		name, method, path, body string
	}{
		{"account identity", http.MethodGet, "/api/v1/auth/me", ""},
		{"session list", http.MethodGet, "/api/v1/auth/sessions", ""},
		{"session revocation", http.MethodDelete, "/api/v1/auth/sessions/some-session", ""},
		{"device pairing approval", http.MethodPost, "/api/v1/auth/device/approve", `{"code":"ABCD"}`},
		{"device pairing denial", http.MethodPost, "/api/v1/auth/device/deny", `{"code":"ABCD"}`},
		{"account api key creation", http.MethodPost, "/api/v1/api-keys/", `{"name":"escape"}`},
		{"account api key list", http.MethodGet, "/api/v1/api-keys/", ""},
		{"profile directory", http.MethodGet, "/api/v1/profiles/", ""},
		{"profile creation", http.MethodPost, "/api/v1/profiles/", `{"name":"Extra"}`},
		{"household sessions", http.MethodGet, "/api/v1/profiles/household/sessions", ""},
		{"sibling profile update", http.MethodPut, "/api/v1/profiles/" + primaryProfileID, `{"name":"Hijacked"}`},
		{"sibling profile deletion", http.MethodDelete, "/api/v1/profiles/" + readerProfileID, ""},
		{"sibling pin verification", http.MethodPost, "/api/v1/profiles/" + primaryProfileID + "/verify-pin", `{"pin":"2468"}`},
		{"account diagnostics", http.MethodGet, "/api/v1/diagnostics/status", ""},
		{"account settings list", http.MethodGet, "/api/v1/settings/", ""},
		{"account setting read", http.MethodGet, "/api/v1/settings/ui.theme", ""},
		{"account setting write", http.MethodPut, "/api/v1/settings/ui.theme", `{"value":"dark"}`},
		{"compat connect info", http.MethodGet, "/api/v1/compat/connect-info", ""},
		{"history import sources", http.MethodGet, "/api/v1/history-imports/sources", ""},
		{"plex sync connections", http.MethodGet, "/api/v1/plex-sync/connections", ""},
		{"webhook sync connections", http.MethodGet, "/api/v1/webhook-sync/connections", ""},
		{"plugin launch", http.MethodPost, "/api/v1/auth/plugin-launch", `{"installation_id":1}`},
		{"requests", http.MethodGet, "/api/v1/requests/", ""},
		{"admin users", http.MethodGet, "/api/v1/admin/users", ""},
		// The plugin proxy authenticates itself and never reaches the
		// allowlist, so it refuses profile-bound tokens on its own.
		{"plugin proxy", http.MethodGet, "/api/v1/plugins/1/anything", ""},
		// The Discord link routes carry the same guard, but they are only
		// mounted when a notification service is wired, which this fixture
		// deliberately does not do.
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			response := performJSONRequest(t, router, tc.method, tc.path, tc.body, directToken, nil)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d %s, want %d",
					tc.method, tc.path, response.Code, response.Body.String(), http.StatusForbidden)
			}
		})
	}

	// A sibling named in the header is refused rather than honored.
	t.Run("sibling header on a profile-scoped route", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet,
			"/api/v1/settings/effective?keys=player.playback_speed", "", directToken,
			map[string]string{"X-Profile-Id": primaryProfileID})
		if response.Code != http.StatusForbidden {
			t.Fatalf("sibling header = %d %s, want %d", response.Code, response.Body.String(), http.StatusForbidden)
		}
	})

	// The session still works as itself.
	t.Run("own profile update is allowed", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodPut,
			"/api/v1/profiles/"+readerProfileID, `{"quality_preference":"1080p"}`, directToken, nil)
		if response.Code == http.StatusForbidden {
			t.Fatalf("own profile update = %d %s, want the direct session to act as itself",
				response.Code, response.Body.String())
		}
	})

	// The refusal is real, not just a status code: the sibling is unchanged.
	t.Run("a refused sibling mutation changes nothing", func(t *testing.T) {
		var before string
		if err := pool.QueryRow(ctx, `SELECT name FROM user_profiles WHERE user_id = $1 AND id = $2`,
			accountID, primaryProfileID).Scan(&before); err != nil {
			t.Fatalf("read sibling name: %v", err)
		}
		response := performJSONRequest(t, router, http.MethodPut,
			"/api/v1/profiles/"+primaryProfileID, `{"name":"Hijacked"}`, directToken, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("sibling mutation = %d %s, want %d", response.Code, response.Body.String(), http.StatusForbidden)
		}
		var after string
		if err := pool.QueryRow(ctx, `SELECT name FROM user_profiles WHERE user_id = $1 AND id = $2`,
			accountID, primaryProfileID).Scan(&after); err != nil {
			t.Fatalf("re-read sibling name: %v", err)
		}
		if after != before {
			t.Fatalf("sibling name = %q, want it unchanged at %q", after, before)
		}
	})

	// The profile surface the session is entitled to still works. These assert
	// only that the boundary lets the request through: without a real media
	// file the handlers answer 4xx/5xx for their own reasons, and it is the
	// 403 refusal that would mean the allowlist had locked a bound profile out
	// of watching anything.
	t.Run("own profile surfaces still work", func(t *testing.T) {
		for _, probe := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/v1/settings/values", ""},
			{http.MethodGet, "/api/v1/settings/effective?keys=player.playback_speed", ""},
			{http.MethodGet, "/api/v1/home/", ""},
			{http.MethodGet, "/api/v1/user/libraries", ""},
			{http.MethodGet, "/api/v1/playback/capability", ""},
			{http.MethodPost, "/api/v1/playback/start", `{"item_id":"missing"}`},
			{http.MethodPost, "/api/v1/playback/session-that-does-not-exist/progress", `{"position_seconds":12}`},
			{http.MethodDelete, "/api/v1/playback/session-that-does-not-exist", ""},
			{http.MethodGet, "/api/v1/stream/session-that-does-not-exist", ""},
		} {
			response := performJSONRequest(t, router, probe.method, probe.path, probe.body, directToken, nil)
			if response.Code == http.StatusForbidden {
				t.Errorf("%s %s = 403 %s, want the direct profile surface to remain usable",
					probe.method, probe.path, response.Body.String())
			}
		}
	})

	// Legacy account sessions are untouched by any of this.
	t.Run("account session keeps the household surfaces", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", accountToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("account profile directory = %d %s, want %d",
				response.Code, response.Body.String(), http.StatusOK)
		}
	})
}

// A direct-profile session must not be able to widen into household
// management by being the household primary.
func TestDirectProfileSessionOnPrimaryProfileCannotManageHousehold(t *testing.T) {
	ctx := context.Background()
	pool := newV1TenancyDatabase(t)
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          "direct-profile-primary-secret",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}
	router := NewRouter(Dependencies{
		DB:                    pool,
		Config:                cfg,
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"parent","email":"parent@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Parent"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	accountToken := decodeLogin(t, setup).AccessToken

	var accountID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'parent'`).Scan(&accountID); err != nil {
		t.Fatalf("load parent account: %v", err)
	}
	var primaryProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, accountID).Scan(&primaryProfileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}
	if created := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/", `{"name":"Child"}`, accountToken, nil); created.Code >= http.StatusBadRequest {
		t.Fatalf("create child profile = %d %s", created.Code, created.Body.String())
	}
	var childProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND name = 'Child'`, accountID).Scan(&childProfileID); err != nil {
		t.Fatalf("load child profile: %v", err)
	}

	credentials := auth.NewProfileCredentialService(pool)
	if err := credentials.Set(ctx, accountID, primaryProfileID, "parent-profile@example.test", "parent-password"); err != nil {
		t.Fatalf("set primary profile credential: %v", err)
	}
	login := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
		`{"email":"parent-profile@example.test","password":"parent-password","device_id":"parent-tablet"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("primary profile login = %d %s", login.Code, login.Body.String())
	}
	directToken := decodeLogin(t, login).AccessToken

	update := performJSONRequest(t, router, http.MethodPut,
		"/api/v1/profiles/"+childProfileID, `{"name":"Renamed"}`, directToken, nil)
	if update.Code != http.StatusForbidden {
		t.Fatalf("primary direct session updating a child = %d %s, want %d",
			update.Code, update.Body.String(), http.StatusForbidden)
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM user_profiles WHERE user_id = $1 AND id = $2`, accountID, childProfileID).Scan(&name); err != nil {
		t.Fatalf("reload child profile: %v", err)
	}
	if name != "Child" {
		t.Fatalf("child profile name = %q, want it unchanged", name)
	}
}
