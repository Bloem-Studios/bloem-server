package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The Bloem-native surface lives at NativeAPIPrefix. /api/v1 is the
// Silo-compatible projection and /api/v2 is reserved for upstream Silo, whose
// own v2 would otherwise answer the paths this project's clients call. The
// golden below is this surface's drift guard: it never had one while it lived
// on /api/v2, so a native route could be moved or dropped silently.
//
//	BLOEM_UPDATE_BLOEM_ROUTE_GOLDEN=1 go test ./internal/api/ -run TestNativeRouteSurface
const (
	bloemRouteGoldenEnv = "BLOEM_UPDATE_BLOEM_ROUTE_GOLDEN"
	bloemRouteGolden    = "testdata/bloem_routes.txt"
)

func TestNativeSurfaceIsMountedUnderTheBloemPrefix(t *testing.T) {
	router := NewRouter(Dependencies{})

	probe := func(path string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	if got := probe(NativeAPIPrefix + "/capabilities"); got == http.StatusNotFound {
		t.Errorf("GET %s/capabilities = 404, want the capabilities handler", NativeAPIPrefix)
	}
	if got := probe("/api/v2/capabilities"); got != http.StatusNotFound {
		t.Errorf("GET /api/v2/capabilities = %d, want 404; that prefix belongs to upstream", got)
	}
}

func TestNativeRouteSurfaceIsUnchanged(t *testing.T) {
	routes := walkNativeRoutes(t, NewRouter(Dependencies{}))

	if os.Getenv(bloemRouteGoldenEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(bloemRouteGolden), 0o750); err != nil {
			t.Fatalf("create golden directory: %v", err)
		}
		if err := os.WriteFile(bloemRouteGolden, []byte(strings.Join(routes, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", bloemRouteGolden, err)
		}
		t.Logf("wrote %d routes to %s", len(routes), bloemRouteGolden)
		return
	}

	raw, err := os.ReadFile(bloemRouteGolden) //nolint:gosec // fixed test fixture path
	if err != nil {
		t.Fatalf("read %s: %v", bloemRouteGolden, err)
	}
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")

	added, removed := routeDifference(routes, want)
	for _, route := range added {
		t.Errorf("this branch ADDS a native route: %s", route)
	}
	for _, route := range removed {
		t.Errorf("this branch REMOVES a native route: %s", route)
	}
	if len(added) > 0 || len(removed) > 0 {
		t.Fatalf("the native surface changed against %s; regenerate deliberately", bloemRouteGolden)
	}
}

// walkNativeRoutes mirrors walkV1Routes, filtered to the native prefix.
func walkNativeRoutes(t *testing.T, router chi.Router) []string {
	t.Helper()
	var routes []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/*")
		if route == NativeAPIPrefix || strings.HasPrefix(route, NativeAPIPrefix+"/") {
			routes = append(routes, fmt.Sprintf("%s %s", method, route))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no native routes were walked; the walk itself is broken")
	}
	sort.Strings(routes)
	return routes
}
