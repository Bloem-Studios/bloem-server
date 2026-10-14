package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/apiv2"
)

func TestPreviewRouteAllowedIsAnExactAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, pattern string
		want            bool
	}{
		{http.MethodGet, "/api/bloem/v1/capabilities", true},
		{http.MethodGet, "/api/bloem/v1/persons/{person_id}", true},
		{http.MethodGet, "/api/bloem/v1/watch/items/{content_id}", true},
		{http.MethodPost, "/api/bloem/v1/sync/progress", false},
		{http.MethodGet, "/api/bloem/v1/livetv/capability", false},
		{http.MethodPost, "/api/bloem/v1/persons/{person_id}", false},
		{http.MethodGet, "/api/v1/direct-download", false},
		{http.MethodGet, "", false},
	} {
		if got := previewRouteAllowed(tc.method, tc.pattern); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.method, tc.pattern, got, tc.want)
		}
	}
}

func TestPreviewRouteGuardDefersOnlyDelegatedV2(t *testing.T) {
	root := chi.NewRouter()
	root.Handle(apiv2.DelegationPattern, http.NotFoundHandler())
	root.Get("/api/v2/stream/{id}", http.NotFoundHandler().ServeHTTP)
	root.Get("/api/bloem/v1/capabilities", http.NotFoundHandler().ServeHTTP)
	guard := newPreviewRouteGuard(root.Match)
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/api/v2/catalog/items/x", true},  // delegated: the v2 preview gate decides
		{"/api/v2/stream/42", false},       // chi-served under /api/v2: judged here
		{"/api/bloem/v1/capabilities", true},
	} {
		if got := guard(httptest.NewRequest(http.MethodGet, tc.path, nil)); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.path, got, tc.want)
		}
	}
	if newPreviewRouteGuard(nil)(httptest.NewRequest(http.MethodGet, "/api/bloem/v1/capabilities", nil)) {
		t.Fatal("a guard without a router allowed a request")
	}
}

// Every allowlisted route must exist: a typo would silently deny a page.
func TestPreviewAllowlistNamesRealRoutes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "api", "v2", "route-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Routes []struct{ Method, Path string } `json:"routes"`
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, route := range inventory.Routes {
		known[route.Method+" "+route.Path] = true
	}
	for pattern, methods := range previewAllowedRoutes {
		for _, method := range methods {
			if !known[method+" "+pattern] {
				t.Errorf("preview allowlist names %s %s, which is not in the route inventory", method, pattern)
			}
		}
	}
}
