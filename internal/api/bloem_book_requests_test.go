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
	h := handlers.NewBloemBookRequestsHandler(bookrequests.NewService(nil, nil), nil)
	h.SetEbookRequests(bookrequests.NewService(nil, nil))
	mountBloemAdminBookRequests(router, h, nil)
	routes := walkRoutes(t, router)
	for _, pair := range []string{
		"GET /organization/book-requests", "POST /organization/book-requests/{id}/approve", "POST /organization/book-requests/{id}/decline", "POST /organization/book-requests/{id}/fulfil",
		"PUT /organization/audiobook-request-integrations/{id}", "POST /organization/audiobook-request-integrations/{id}/test",
		"POST /organization/ebook-request-integrations/{id}/test", "POST /organization/ebook-requests/{id}/retry", "PUT /organization/ebook-request-policy",
	} {
		if !slices.Contains(routes, pair) {
			t.Fatalf("missing admin route %s: %v", pair, routes)
		}
		method, path := splitRoute(pair)
		if directProfileRouteAllowed(method, NativeAPIPrefix+"/admin"+path) {
			t.Fatalf("direct profile admitted admin action: %s", pair)
		}
	}
	// Ebook requests are open to any user of Bloem, including a session bound
	// to one profile: filing, history and withdrawal act as that profile.
	for _, prefix := range []string{"/book-requests", "/ebook-requests"} {
		for _, pair := range []string{"GET /capability", "GET /mine", "POST ", "GET /{id}", "POST /{id}/cancel"} {
			method, path := splitRoute(pair)
			if !directProfileRouteAllowed(method, NativeAPIPrefix+prefix+path) {
				t.Fatalf("bound profile cannot use owned request route: %s %s%s", method, prefix, path)
			}
		}
		if directProfileRouteAllowed(http.MethodDelete, NativeAPIPrefix+prefix+"/{id}") || directProfileRouteAllowed(http.MethodPost, NativeAPIPrefix+prefix+"/{id}/approve") {
			t.Fatalf("direct-profile %s mutations expanded beyond filing and withdrawal", prefix)
		}
	}
}

func TestBloemBookRequestConnectionAdminCapabilityTracksSetups(t *testing.T) {
	for _, wired := range []bool{false, true} {
		h := handlers.NewBloemBookRequestsHandler(bookrequests.NewService(nil, nil), nil)
		h.SetEbookRequests(bookrequests.NewService(nil, nil))
		if wired {
			h.SetAudiobookRequestSetup(bloemAudiobookSetup{})
			h.SetEbookRequestSetup(bloemEbookSetup{})
		}
		system := handlers.NewBloemSystemHandler(nil)
		router := chi.NewRouter()
		router.Get(NativeAPIPrefix+"/capabilities", system.HandleCapabilities)
		mountBloemAdminBookRequests(router, h, system)
		probe := httptest.NewRecorder()
		router.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
		if strings.Contains(probe.Body.String(), "book_request_connection_admin_v1") != wired {
			t.Fatalf("wired=%v: %s", wired, probe.Body.String())
		}
	}
	// Only the audiobook setup: the ebook test route has nothing behind it.
	h := handlers.NewBloemBookRequestsHandler(bookrequests.NewService(nil, nil), nil)
	h.SetEbookRequests(bookrequests.NewService(nil, nil))
	h.SetAudiobookRequestSetup(bloemAudiobookSetup{})
	if h.ConnectionAdminAvailable() {
		t.Fatal("connection administration advertised without the ebook setup")
	}
}
