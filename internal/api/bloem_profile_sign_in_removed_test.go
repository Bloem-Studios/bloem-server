package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Profile sign-in was removed: no route signs a profile in or manages its
// credentials, and the capability probe names none of it.
func TestProfileSignInRoutesAreGone(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	router := newChiRouter(Dependencies{
		DB:                    pool,
		Config:                &config.Config{Auth: config.AuthConfig{JWTSecret: "profile-sign-in-removed", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: v1TenancyBootstrap{store: tenancy.NewStore(pool)},
		SessionMgr:            playback.NewSessionManager(4, 2),
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
	})

	for _, pair := range walkRoutes(t, router) {
		if strings.Contains(pair, "profile-credentials") || strings.Contains(pair, "profile-login") {
			t.Errorf("profile sign-in route is registered: %s", pair)
		}
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, NativeAPIPrefix + "/profile-credentials/profile-1"},
		{http.MethodPut, NativeAPIPrefix + "/profile-credentials/profile-1"},
		{http.MethodDelete, NativeAPIPrefix + "/profile-credentials/profile-1"},
		{http.MethodPost, NativeAPIPrefix + "/auth/profile-login"},
		{http.MethodPost, "/api/v2/auth/profile-login"},
	} {
		if router.Match(chi.NewRouteContext(), request.method, request.path) {
			t.Errorf("%s %s matches a route", request.method, request.path)
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d", rec.Code)
	}
	for _, token := range []string{"profile_credential_management_v1", "ebook_requests_direct_profile_v1", `"direct_profile_login":true`} {
		if strings.Contains(rec.Body.String(), token) {
			t.Errorf("capabilities advertise %s: %s", token, rec.Body.String())
		}
	}
}

// walkRoutes returns every registered route as a sorted "METHOD /pattern" pair.
func walkRoutes(t *testing.T, routes chi.Routes) []string {
	t.Helper()
	seen := map[string]bool{}
	if err := chi.Walk(routes, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	pairs := make([]string, 0, len(seen))
	for pair := range seen {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	return slices.Clip(pairs)
}
