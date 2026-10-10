package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
)

func TestBloemBookRequestMountRequiresAuthorityResolversAndCapabilityIsTruthful(t *testing.T) {
	h := handlers.NewBloemBookRequestsHandler(bookrequests.NewService(nil, nil), seasonalMountProfiles{})
	for _, missing := range []string{"none", "handler", "auth", "tenant", "viewer"} {
		t.Run(missing, func(t *testing.T) {
			handler := h
			client := bloemClientSurface{
				auth:   apimw.NewAuthMiddleware(seasonalMountTokens{}, seasonalMountSessions{}, nil, nil),
				tenant: apimw.NewTenantMiddleware(seasonalMountTenant{}),
				viewer: apimw.NewViewerAccessMiddleware(seasonalMountViewer{}),
			}
			switch missing {
			case "handler":
				handler = nil
			case "auth":
				client.auth = nil
			case "tenant":
				client.tenant = nil
			case "viewer":
				client.viewer = nil
			}
			system := handlers.NewBloemSystemHandler(nil)
			router := chi.NewRouter()
			mountBloemRoutes(router, system, nil, client.auth, nil, bloemRouteSurfaces{Client: client, BookRequests: handler})
			probe := httptest.NewRecorder()
			router.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
			available := missing == "none"
			if strings.Contains(probe.Body.String(), "audiobook_requests_v1") != available {
				t.Fatalf("untruthful capability: %s", probe.Body.String())
			}
			want := http.StatusNotFound
			if available {
				want = http.StatusUnauthorized
			}
			for _, route := range []struct{ method, path string }{{"GET", "/book-requests/capability"}, {"GET", "/book-requests/mine"}, {"POST", "/book-requests"}, {"GET", "/book-requests/fixture"}, {"POST", "/book-requests/fixture/cancel"}} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(route.method, NativeAPIPrefix+route.path, nil))
				if w.Code != want {
					t.Fatalf("%s %s status=%d want=%d", route.method, route.path, w.Code, want)
				}
			}
		})
	}
}

func TestBloemBookRequestAdminPatternsAndDirectProfileBoundary(t *testing.T) {
	router := chi.NewRouter()
	mountBloemAdminBookRequests(router, handlers.NewBloemBookRequestsHandler(bookrequests.NewService(nil, nil), nil))
	routes := walkRoutes(t, router)
	for _, pair := range []string{"GET /organization/book-requests", "POST /organization/book-requests/{id}/approve", "POST /organization/book-requests/{id}/decline", "POST /organization/book-requests/{id}/fulfil"} {
		if !slices.Contains(routes, pair) {
			t.Fatalf("missing admin route %s: %v", pair, routes)
		}
		method, path := splitRoute(pair)
		if directProfileRouteAllowed(method, NativeAPIPrefix+"/admin"+path) {
			t.Fatalf("direct profile admitted admin action: %s", pair)
		}
	}
	for _, pair := range []string{"GET /book-requests/capability", "GET /book-requests/mine", "POST /book-requests", "GET /book-requests/{id}", "POST /book-requests/{id}/cancel"} {
		method, path := splitRoute(pair)
		if !directProfileRouteAllowed(method, NativeAPIPrefix+path) {
			t.Fatalf("bound profile cannot use owned request route: %s", pair)
		}
	}
	if directProfileRouteAllowed(http.MethodDelete, NativeAPIPrefix+"/book-requests/{id}") || directProfileRouteAllowed(http.MethodPost, NativeAPIPrefix+"/book-requests/{id}/approve") {
		t.Fatal("direct-profile request mutations expanded beyond filing and withdrawal")
	}
}
