package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/usercollections"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
)

// compatAppsStub satisfies the narrow lifecycle interface so the admin
// surface mounts; the routes, not the behavior, are under test here.
type compatAppsStub struct{}

func (compatAppsStub) ListApplications(context.Context) ([]handlers.CompatibilityApplication, error) {
	return nil, nil
}

func (compatAppsStub) CreateEnrollment(context.Context, string, []string) (handlers.CompatibilityEnrollment, error) {
	return handlers.CompatibilityEnrollment{}, nil
}

func (compatAppsStub) SetApplicationEnabled(context.Context, string, bool, int64) (handlers.CompatibilityApplication, error) {
	return handlers.CompatibilityApplication{}, nil
}

func (compatAppsStub) RotateApplicationCredential(context.Context, string, int64) (handlers.CompatibilityCredential, handlers.CompatibilityApplication, error) {
	return handlers.CompatibilityCredential{}, handlers.CompatibilityApplication{}, nil
}

func (compatAppsStub) RevokeApplication(context.Context, string, int64) (handlers.CompatibilityApplication, error) {
	return handlers.CompatibilityApplication{}, nil
}

// gatewayStub marks routes owned by the gateway so route resolution can be
// distinguished from native handlers.
type gatewayStub struct{}

func (gatewayStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
}

func newCompatWiredRouter(t *testing.T) chi.Routes {
	t.Helper()
	pool := newV1TenancyDatabase(t)
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	// These dependencies mirror newRouteInventoryRouter exactly — same
	// conditional handlers mounted — so the only route-table difference
	// between the two fixtures is the compat wiring. PublicURL stays empty on
	// purpose: setting it would additionally mount the OAuth callback routes.
	router := NewRouter(Dependencies{
		DB:                    pool,
		Config:                &config.Config{Auth: config.AuthConfig{JWTSecret: "compat-overlap", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
		SessionMgr:            playback.NewSessionManager(4, 2),
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
		UserCollectionSync: usercollections.NewService(
			pgstore.NewPostgresProvider(pool),
			catalog.NewItemRepository(pool),
			catalog.NewLibraryItemRepository(pool),
			nil,
			nil,
		),
		CompatApplications: compatAppsStub{},
		CompatGateway:      gatewayStub{},
	})
	routes, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("router does not expose its route table")
	}
	return routes
}

// No native route may sit inside a gateway-owned path family. This walks the
// full production router (gateway absent, so every registered route is
// native) and holds each pattern against the static route table.
func TestCompatGatewayRoutesDoNotOverlapNativeRoutes(t *testing.T) {
	routes := newRouteInventoryRouter(t)
	for _, pair := range walkRoutes(t, routes) {
		_, pattern := splitRoute(pair)
		if route, owned := compatgateway.MatchPath(pattern); owned {
			t.Fatalf("native route %q falls inside the %s gateway family %q", pair, route.App, route.Prefix)
		}
	}
}

// Mounting the gateway is purely additive: every route it adds is owned by
// the static table, and every pre-existing native route survives unchanged.
func TestCompatGatewayMountIsAdditive(t *testing.T) {
	native := map[string]bool{}
	for _, pair := range walkRoutes(t, newRouteInventoryRouter(t)) {
		native[pair] = true
	}

	wired := newCompatWiredRouter(t)
	for _, pair := range walkRoutes(t, wired) {
		if native[pair] {
			delete(native, pair)
			continue
		}
		_, pattern := splitRoute(pair)
		if _, owned := compatgateway.MatchPath(pattern); owned {
			continue
		}
		// The admin surface mounts with the lifecycle service; it lives under
		// the native /api/v2 admin tree, never on a gateway path.
		if strings.HasPrefix(pattern, "/api/v2/admin/platform/compatibility/") {
			continue
		}
		t.Fatalf("route %q is neither native, gateway-owned, nor the compatibility admin surface", pair)
	}
	// The inventory fixture and the wired fixture differ only in the compat
	// dependencies, so every native route must still be present.
	for pair := range native {
		t.Fatalf("native route %q disappeared when the gateway was mounted", pair)
	}
}

// The gateway may not replace native authentication: with the gateway
// mounted, the native auth and profile routes must still resolve to native
// /api patterns, and requests to them must not answer with the gateway stub.
func TestCompatGatewayDoesNotReplaceNativeAuthRoutes(t *testing.T) {
	wired := newCompatWiredRouter(t)
	probes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, "/api/v2/admin/session"},
	}
	for _, probe := range probes {
		rctx := chi.NewRouteContext()
		if !wired.Match(rctx, probe.method, probe.path) {
			t.Fatalf("native auth route %s %s no longer resolves", probe.method, probe.path)
		}
		pattern := rctx.RoutePattern()
		if !strings.HasPrefix(pattern, "/api/") {
			t.Fatalf("native auth route %s %s resolved to non-native pattern %q", probe.method, probe.path, pattern)
		}
		if _, owned := compatgateway.MatchPath(pattern); owned {
			t.Fatalf("native auth route %s %s is owned by the gateway", probe.method, probe.path)
		}
	}
}

// The full fixed table is registered — each owned family answers through the
// gateway handler rather than 404ing into the native fallback.
func TestCompatGatewayOwnsItsFixedFamilies(t *testing.T) {
	wired := newCompatWiredRouter(t)
	mux, ok := wired.(chi.Router)
	if !ok {
		t.Fatal("router is not a chi.Router")
	}
	for _, route := range compatgateway.RouteTable() {
		for _, path := range []string{route.Prefix, route.Prefix + "/probe"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("gateway path %q answered %d, want the gateway stub's 503", path, rec.Code)
			}
		}
	}
}

// The Compatibility Applications admin surface mounts under the v2 admin
// tree only when the lifecycle service is wired.
func TestCompatibilityAdminSurfaceMountsWithTheService(t *testing.T) {
	wired := walkRoutes(t, newCompatWiredRouter(t))
	registered := map[string]bool{}
	for _, pair := range wired {
		registered[pair] = true
	}
	for _, required := range []string{
		"GET /api/v2/admin/platform/compatibility/applications",
		"POST /api/v2/admin/platform/compatibility/enrollments",
		"POST /api/v2/admin/platform/compatibility/applications/{instance_id}/enable",
		"POST /api/v2/admin/platform/compatibility/applications/{instance_id}/disable",
		"POST /api/v2/admin/platform/compatibility/applications/{instance_id}/rotate-credential",
		"POST /api/v2/admin/platform/compatibility/applications/{instance_id}/revoke",
	} {
		if !registered[required] {
			t.Fatalf("admin surface route %q is not registered", required)
		}
	}

	for _, pair := range walkRoutes(t, newRouteInventoryRouter(t)) {
		if strings.Contains(pair, "/compatibility/") {
			t.Fatalf("admin surface route %q mounted without the lifecycle service", pair)
		}
	}
}

// The admin surface is account/admin-scoped: it must never join the
// direct-profile allowlist, whose sessions are typed into third-party
// clients.
func TestCompatibilityAdminSurfaceIsOutsideTheDirectProfileSurface(t *testing.T) {
	for _, pair := range walkRoutes(t, newCompatWiredRouter(t)) {
		method, pattern := splitRoute(pair)
		if !strings.Contains(pattern, "/compatibility/") {
			continue
		}
		if directProfileRouteAllowed(method, pattern) {
			t.Fatalf("compatibility admin route %q must not be on the direct-profile surface", pair)
		}
	}
}
