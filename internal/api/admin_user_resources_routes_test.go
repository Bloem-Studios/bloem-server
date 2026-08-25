package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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
	pool := newDisposableAPIDatabase(t, "bloem_admin_user_routes_", true)
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

func TestProfileRoutesWithoutS3PreserveNilAvatarStore(t *testing.T) {
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_no_s3_profiles_", true)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "no-s3-profile-lifecycle-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour,
	}}
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	// S3Private is deliberately nil: this is the production configuration for
	// servers that do not enable uploaded-avatar object storage.
	router := NewRouter(Dependencies{
		DB: pool, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap, MembershipProvisioner: bootstrap,
	})
	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"no-s3-admin","email":"no-s3-admin@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	adminToken := decodeLogin(t, setup).AccessToken
	var userID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'no-s3-admin'`).Scan(&userID); err != nil {
		t.Fatalf("load account id: %v", err)
	}
	var primaryProfileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id=$1 AND is_primary`, userID).Scan(&primaryProfileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}

	createProfile := func(name string) string {
		t.Helper()
		created := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/", `{"name":"`+name+`"}`, adminToken, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("create profile %q = %d %s", name, created.Code, created.Body.String())
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.ID == "" {
			t.Fatalf("decode created profile %q: id=%q err=%v", name, body.ID, err)
		}
		return body.ID
	}
	setUploadedAvatar := func(profileID string) {
		t.Helper()
		ref := "upload:profile-avatars/" + strconv.Itoa(userID) + "/" + profileID + "/original.webp"
		if _, err := pool.Exec(ctx, `UPDATE user_profiles SET avatar=$1 WHERE user_id=$2 AND id=$3`, ref, userID, profileID); err != nil {
			t.Fatalf("persist upload avatar for %q: %v", profileID, err)
		}
	}
	assertProfileAvatar := func(profileID, want string) {
		t.Helper()
		var got string
		if err := pool.QueryRow(ctx, `SELECT avatar FROM user_profiles WHERE user_id=$1 AND id=$2`, userID, profileID).Scan(&got); err != nil {
			t.Fatalf("load avatar for %q: %v", profileID, err)
		}
		if got != want {
			t.Fatalf("avatar for %q = %q, want %q", profileID, got, want)
		}
	}
	assertProfileDeleted := func(profileID string) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_profiles WHERE user_id=$1 AND id=$2`, userID, profileID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("profile %q rows = %d (%v), want deleted", profileID, count, err)
		}
	}

	setUploadedAvatar(primaryProfileID)
	nativeDeleteID := createProfile("Native Delete")
	adminUpdateID := createProfile("Admin Update")
	adminDeleteID := createProfile("Admin Delete")
	setUploadedAvatar(nativeDeleteID)
	setUploadedAvatar(adminUpdateID)
	setUploadedAvatar(adminDeleteID)

	t.Run("native list omits upload URL and reports uploads disabled", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, "/api/v1/profiles/", "", adminToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("native list = %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), `"avatar_url"`) || !strings.Contains(response.Body.String(), `"avatar_upload_enabled":false`) {
			t.Fatalf("native list exposed no-S3 avatar capability incorrectly: %s", response.Body.String())
		}
	})

	t.Run("admin list omits upload URL", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet,
			"/api/v1/admin/users/"+strconv.Itoa(userID)+"/profiles", "", adminToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("admin list = %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), `"avatar_url"`) {
			t.Fatalf("admin list exposed an upload URL without S3: %s", response.Body.String())
		}
	})

	t.Run("native update treats upload cleanup as a no-op", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodPut, "/api/v1/profiles/"+primaryProfileID,
			`{"avatar":"avatar-1"}`, adminToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("native update = %d %s", response.Code, response.Body.String())
		}
		assertProfileAvatar(primaryProfileID, "preset:avatar-1")
	})

	t.Run("admin update treats upload cleanup as a no-op", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodPut,
			"/api/v1/admin/users/"+strconv.Itoa(userID)+"/profiles/"+adminUpdateID,
			`{"avatar":"avatar-1"}`, adminToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("admin update = %d %s", response.Code, response.Body.String())
		}
		assertProfileAvatar(adminUpdateID, "preset:avatar-1")
	})

	t.Run("native delete treats upload cleanup as a no-op", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodDelete, "/api/v1/profiles/"+nativeDeleteID, "", adminToken, nil)
		if response.Code != http.StatusNoContent {
			t.Fatalf("native delete = %d %s", response.Code, response.Body.String())
		}
		assertProfileDeleted(nativeDeleteID)
	})

	t.Run("admin delete treats upload cleanup as a no-op", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodDelete,
			"/api/v1/admin/users/"+strconv.Itoa(userID)+"/profiles/"+adminDeleteID, "", adminToken, nil)
		if response.Code != http.StatusNoContent {
			t.Fatalf("admin delete = %d %s", response.Code, response.Body.String())
		}
		assertProfileDeleted(adminDeleteID)
	})
}
