package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// The Silo-compatible projection at /api/v1 is frozen against upstream Silo:
// this repository's native surface is /api/bloem/v1, and a route added, removed or
// renamed under /api/v1 breaks clients this project does not ship. The golden
// files below were generated from origin/main, so any drift — in either
// direction — fails here with the exact route named.
//
// Deliberate, reviewed exceptions are pinned in the golden files: direct
// profile login, direct profile lookup, the nine account-administration
// profile/device/session methods in adminUserResourceRouteContract, and the
// 17 tenant-member lifecycle/resource methods in
// adminTenantMemberRouteContract. Regenerate deliberately, never merely to
// make a failure go away:
//
//	BLOEM_UPDATE_V1_ROUTE_GOLDEN=1 go test ./internal/api/ -run TestV1RouteSurface
const (
	v1RouteGoldenEnv      = "BLOEM_UPDATE_V1_ROUTE_GOLDEN"
	v1RouteGoldenMinimal  = "testdata/v1_routes_no_dependencies.txt"
	v1RouteGoldenDatabase = "testdata/v1_routes_with_database.txt"
)

// TestV1RouteSurfaceIsUnchanged walks the routes a dependency-free router
// mounts. These are the routes that exist on every deployment, healthy or not —
// health, readiness, and the public probes — so a new unconditional /api/v1
// route lands here.
func TestV1RouteSurfaceIsUnchanged(t *testing.T) {
	assertV1RouteSurface(t, v1RouteGoldenMinimal, NewRouter(Dependencies{}))
}

// TestV1RouteSurfaceWithADatabaseIsUnchanged walks the far larger surface a
// router with a real database mounts: almost every /api/v1 route is behind a
// `handler != nil` guard that a dependency-free router leaves unmounted, so
// without this the guard above would miss most of the API.
func TestV1RouteSurfaceWithADatabaseIsUnchanged(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "bloem_v1_routes_", false)
	provider := pgstore.NewPostgresProvider(pool)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	// A fixed set of dependencies: the golden describes what these mount, so
	// adding one here changes the expected surface and must be regenerated on
	// origin/main first.
	router := NewRouter(Dependencies{
		DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "v1-route-surface-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     provider,
		FileRepo:              scanner.NewFileRepository(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})
	assertV1RouteSurface(t, v1RouteGoldenDatabase, router)
}

func assertV1RouteSurface(t *testing.T, golden string, router chi.Router) {
	t.Helper()
	routes := walkV1Routes(t, router)

	if os.Getenv(v1RouteGoldenEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatalf("create golden directory: %v", err)
		}
		if err := os.WriteFile(golden, []byte(strings.Join(routes, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", golden, err)
		}
		t.Logf("wrote %d routes to %s", len(routes), golden)
		return
	}

	raw, err := os.ReadFile(golden) //nolint:gosec // fixed test fixture path
	if err != nil {
		t.Fatalf("read %s: %v", golden, err)
	}
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")

	added, removed := routeDifference(routes, want)
	for _, route := range added {
		t.Errorf("this branch ADDS an /api/v1 route: %s", route)
	}
	for _, route := range removed {
		t.Errorf("this branch REMOVES an /api/v1 route: %s", route)
	}
	if len(added) > 0 || len(removed) > 0 {
		t.Fatalf("the /api/v1 surface changed against %s; native routes belong on /api/bloem/v1", golden)
	}
}

// walkV1Routes returns the mounted /api/v1 routes as sorted "METHOD /path"
// lines. chi reports a route once per method, which is what makes a method
// added to an existing path visible too.
func walkV1Routes(t *testing.T, router chi.Router) []string {
	t.Helper()
	var routes []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi renders a mounted subtree's root as "/api/v1/*"; the trailing
		// wildcard is noise once the subtree's own routes are listed.
		route = strings.TrimSuffix(route, "/*")
		if route == "" {
			route = "/"
		}
		if route == "/api/v1" || strings.HasPrefix(route, "/api/v1/") {
			routes = append(routes, fmt.Sprintf("%s %s", method, route))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no /api/v1 routes were walked; the walk itself is broken")
	}
	sort.Strings(routes)
	return routes
}

func routeDifference(got, want []string) (added, removed []string) {
	present := make(map[string]bool, len(want))
	for _, route := range want {
		present[route] = true
	}
	seen := make(map[string]bool, len(got))
	for _, route := range got {
		seen[route] = true
		if !present[route] {
			added = append(added, route)
		}
	}
	for _, route := range want {
		if !seen[route] {
			removed = append(removed, route)
		}
	}
	return added, removed
}
