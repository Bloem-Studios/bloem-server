package api

import (
	"context"
	"net/http"
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

// The gateway may not replace native authentication. The composed listener
// hands every /api/** request to this router before the gateway is ever
// consulted (cmd/silo publicMux, pinned by TestPublicMuxRoutesEachLayer), so
// what this router owes is that the native auth routes resolve to native
// patterns no owned family covers.
func TestCompatGatewayDoesNotReplaceNativeAuthRoutes(t *testing.T) {
	wired := newCompatWiredRouter(t)
	probes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodPost, NativeAPIPrefix + "/admin/session"},
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

// The Compatibility Applications admin surface mounts under the v2 admin
// tree only when the lifecycle service is wired.
func TestCompatibilityAdminSurfaceMountsWithTheService(t *testing.T) {
	wired := walkRoutes(t, newCompatWiredRouter(t))
	registered := map[string]bool{}
	for _, pair := range wired {
		registered[pair] = true
	}
	for _, required := range []string{
		"GET /api/bloem/v1/admin/platform/compatibility/applications",
		"POST /api/bloem/v1/admin/platform/compatibility/enrollments",
		"POST /api/bloem/v1/admin/platform/compatibility/applications/{instance_id}/enable",
		"POST /api/bloem/v1/admin/platform/compatibility/applications/{instance_id}/disable",
		"POST /api/bloem/v1/admin/platform/compatibility/applications/{instance_id}/rotate-credential",
		"POST /api/bloem/v1/admin/platform/compatibility/applications/{instance_id}/revoke",
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
