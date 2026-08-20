package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

var adminUserResourceRouteContract = []struct {
	method string
	path   string
}{
	{http.MethodDelete, "/api/v1/admin/users/{user_id}/auth-sessions"},
	{http.MethodDelete, "/api/v1/admin/users/{user_id}/auth-sessions/{session_id}"},
	{http.MethodDelete, "/api/v1/admin/users/{user_id}/devices/{device_id}"},
	{http.MethodDelete, "/api/v1/admin/users/{user_id}/profiles/{profile_id}"},
	{http.MethodGet, "/api/v1/admin/users/{user_id}/auth-sessions"},
	{http.MethodGet, "/api/v1/admin/users/{user_id}/devices"},
	{http.MethodGet, "/api/v1/admin/users/{user_id}/profiles"},
	{http.MethodPost, "/api/v1/admin/users/{user_id}/profiles"},
	{http.MethodPut, "/api/v1/admin/users/{user_id}/profiles/{profile_id}"},
}

func TestAdminUserResourceRoutesUseProductionAdminBoundary(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "vondel_admin_user_routes_", true)
	if err := database.RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "admin-user-route-contract-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour,
	}}
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	router := NewRouter(Dependencies{
		DB: pool, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap, MembershipProvisioner: bootstrap,
	})

	routes := walkV1Routes(t, router)
	present := make(map[string]bool, len(routes))
	for _, route := range routes {
		present[route] = true
	}
	for _, route := range adminUserResourceRouteContract {
		pair := route.method + " " + route.path
		if !present[pair] {
			t.Errorf("production router omitted %s", pair)
		}
	}

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"route-admin","email":"route-admin@example.test","password":"correct horse battery staple"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	var login v1LoginEnvelope
	if err := json.Unmarshal(setup.Body.Bytes(), &login); err != nil || login.AccessToken == "" {
		t.Fatalf("decode setup token: token present=%v err=%v", login.AccessToken != "", err)
	}
	createdUser := performJSONRequest(t, router, http.MethodPost, "/api/v1/admin/users", `{
		"username":"route-user","email":"route-user@example.test","password":"correct horse battery staple","role":"user"
	}`, login.AccessToken, nil)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("create non-admin user = %d %s", createdUser.Code, createdUser.Body.String())
	}
	userLoginResponse := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{
		"username":"route-user","password":"correct horse battery staple"
	}`, "", nil)
	if userLoginResponse.Code != http.StatusOK {
		t.Fatalf("login non-admin user = %d %s", userLoginResponse.Code, userLoginResponse.Body.String())
	}
	var userLogin v1LoginEnvelope
	if err := json.Unmarshal(userLoginResponse.Body.Bytes(), &userLogin); err != nil || userLogin.AccessToken == "" {
		t.Fatalf("decode non-admin token: token present=%v err=%v", userLogin.AccessToken != "", err)
	}

	requestPath := func(pattern string) string {
		path := strings.ReplaceAll(pattern, "{user_id}", "999999")
		path = strings.ReplaceAll(path, "{profile_id}", "missing-profile")
		path = strings.ReplaceAll(path, "{device_id}", "missing-device")
		return strings.ReplaceAll(path, "{session_id}", "missing-session")
	}
	for _, route := range adminUserResourceRouteContract {
		path := requestPath(route.path)
		unauthenticated := performJSONRequest(t, router, route.method, path, `{}`, "", nil)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s %s = %d %s, want 401", route.method, path, unauthenticated.Code, unauthenticated.Body.String())
		}
		nonAdmin := performJSONRequest(t, router, route.method, path, `{}`, userLogin.AccessToken, nil)
		if nonAdmin.Code != http.StatusForbidden {
			t.Errorf("non-admin %s %s = %d %s, want 403", route.method, path, nonAdmin.Code, nonAdmin.Body.String())
		}
		authorized := performJSONRequest(t, router, route.method, path, `{}`, login.AccessToken, nil)
		if authorized.Code != http.StatusNotFound || !strings.Contains(authorized.Body.String(), `"error":"not_found"`) {
			t.Errorf("authorized %s %s = %d %s, want handler not_found", route.method, path, authorized.Code, authorized.Body.String())
		}
	}
}
