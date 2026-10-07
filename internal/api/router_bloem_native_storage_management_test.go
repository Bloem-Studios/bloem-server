package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/go-chi/chi/v5"
)

func TestNativeOnboardingHTTPProtectedRouteRefusal(t *testing.T) {
	key := "00000000-0000-0000-0000-000000000001"
	cases := []struct{ method, suffix string }{
		{"GET", "/capabilities"}, {"GET", "/artifacts"}, {"GET", "/sources"}, {"GET", "/sources/" + key},
		{"POST", "/installations"}, {"PUT", "/sources/" + key + "/configuration"}, {"POST", "/installations/7/disable"},
		{"DELETE", "/installations/7"},
	}
	for _, scope := range []string{"platform", "organization"} {
		for _, tt := range cases {
			t.Run(scope+tt.method+tt.suffix, func(t *testing.T) {
				router := chi.NewRouter()
				router.Route("/api/bloem/v1/admin", func(r chi.Router) {
					mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil))
				})
				r := httptest.NewRequest(tt.method, "/api/bloem/v1/admin/"+scope+"/native-storage"+tt.suffix, strings.NewReader(`{"secret":"never-decoded"}`))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != 401 {
					t.Fatalf("owned route refusal=%d want401", w.Code)
				}
				var body map[string]any
				if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] != "tenant_session_required" {
					t.Fatal("actual admin middleware refusal lost")
				}
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("input disclosed")
				}
			})
		}
	}
}

func TestNativeOnboardingHTTPRemovedLibraryRoutesAreGone(t *testing.T) {
	router := chi.NewRouter()
	router.Route("/api/bloem/v1/admin", func(r chi.Router) {
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil))
	})
	key := "00000000-0000-0000-0000-000000000001"
	// Libraries use storage sources through the ordinary library API.
	for _, request := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/libraries", 404}, {"GET", "/libraries/7", 404}, {"POST", "/libraries/7/scan", 404},
		{"GET", "/sources/" + key + "/bindings", 404}, {"DELETE", "/sources/" + key, 405}, {"POST", "/sources/" + key + "/enable", 404},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(request.method, "/api/bloem/v1/admin/platform/native-storage"+request.path, nil))
		if w.Code != request.status {
			t.Fatalf("%s %s status=%d want=%d", request.method, request.path, w.Code, request.status)
		}
	}
}

func TestNativeOnboardingHTTPV2UsesNativeProblemTypes(t *testing.T) {
	for _, code := range []string{"native_storage_unavailable", "artifact_rejected", "revision_conflict"} {
		problem := nativeStorageProblem(&catalog.NativeOnboardingError{Code: code, Cause: errors.New("private-secret")})
		var typed *apiv2.Problem
		if !errors.As(problem, &typed) || typed == nil {
			t.Fatal("v2 mapping lost direct Problem")
		}
		if typed.Type != "https://siloserver.org/docs/api/v2/problems/"+code {
			t.Fatal("native-specific problem type lost")
		}
		if strings.Contains(typed.Detail, "private-secret") {
			t.Fatal("v2 cause exposed")
		}
		if typed.GetHeaders().Get("Cache-Control") != "no-store" {
			t.Fatal("v2 response may cache management error")
		}
	}
}

func TestNativeOnboardingHTTPActualAdminMiddlewareFailClosed(t *testing.T) {
	h := handlers.NewBloemNativeStorageManagementHandler(nil)
	protected := apimw.NewAdminContextMiddleware(nil, nil, nil, nil, nil).Require(http.HandlerFunc(h.HandleInstall))
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"artifact_key":"fixture"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("actual missing-token refusal=%d want401", w.Code)
	}
}

func TestNativeOnboardingHTTPRouteFallbacksKeepPrivacyHeaders(t *testing.T) {
	router := chi.NewRouter()
	router.Route("/api/bloem/v1/admin", func(r chi.Router) {
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil))
	})
	for _, tt := range []struct {
		method, path, code string
		status             int
	}{
		{"PATCH", "/installations/7", "method_not_allowed", 405},
		{"POST", "/libraries/7/repair", "not_found", 404},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tt.method, "/api/bloem/v1/admin/platform/native-storage"+tt.path, nil))
		if w.Code != tt.status {
			t.Fatalf("status=%d want=%d", w.Code, tt.status)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("management fallback bypassed JSON/no-store response contract")
		}
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] != tt.code {
			t.Fatal("fallback lost fixed error code")
		}
	}
}

func TestNativeOnboardingHTTPWiringUnavailable(t *testing.T) {
	for _, deps := range []Dependencies{{}, {NativeStorage: &nativestorage.Host{}}} {
		got := nativeStorageDependencies(deps)
		if got.NativeStorageManagement != nil {
			t.Fatal("missing stores created management dependencies")
		}
	}
	h := handlers.NewBloemNativeStorageManagementHandler(nativestorage.NewSourceManagement(nil, nil))
	deps := Dependencies{BloemDependencies: BloemDependencies{NativeStorageManagement: h}}
	prepared := nativeStorageDependencies(deps)
	copy := prepared.NativeStorageManagement
	capabilities, _ := copy.Capabilities.(*nativeStorageCapabilities)
	if copy == h || copy.Sources != h.Sources || h.Capabilities != nil {
		t.Fatal("router must copy handler while preserving caller services")
	}
	if capabilities == nil || capabilities.reader {
		t.Fatal("router must allocate a fresh, unattached capability witness")
	}
	if capabilities.NativeStorageReady(t.Context()) {
		t.Fatal("unattached router advertised storage readiness")
	}
	attachNativeStorageReader(prepared, handlers.NewNativeEbookFileService(nil, nil))
	var missing *nativestorage.Coordinator
	attachNativeStorageReader(prepared, handlers.NewNativeEbookFileService(nil, missing))
	if capabilities.reader {
		t.Fatal("absent reader coordinator attached")
	}
	attachNativeStorageReader(prepared, handlers.NewNativeEbookFileService(nil, &nativestorage.Coordinator{}))
	if !capabilities.reader {
		t.Fatal("actual reader coordinator was not attached")
	}
}
