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
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/go-chi/chi/v5"
)

func TestNativeOnboardingHTTPProtectedRouteRefusal(t *testing.T) {
	key := "00000000-0000-0000-0000-000000000001"
	cases := []struct{ method, suffix string }{
		{"GET", "/capabilities"}, {"GET", "/artifacts"}, {"GET", "/sources"}, {"GET", "/sources/" + key},
		{"POST", "/installations"}, {"PUT", "/sources/" + key + "/configuration"}, {"POST", "/installations/7/disable"},
		{"DELETE", "/installations/7"}, {"GET", "/sources/" + key + "/bindings"}, {"PUT", "/sources/" + key + "/bindings/7"},
		{"POST", "/libraries"}, {"GET", "/libraries/creation/" + key}, {"GET", "/libraries/7"}, {"GET", "/libraries"},
		{"POST", "/libraries/7/initialize"}, {"POST", "/libraries/7/scan"},
	}
	for _, scope := range []string{"platform", "organization"} {
		for _, tt := range cases {
			t.Run(scope+tt.method+tt.suffix, func(t *testing.T) {
				router := chi.NewRouter()
				router.Route("/api/bloem/v1/admin", func(r chi.Router) {
					mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil, nil))
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

func TestNativeOnboardingHTTPRouteRecoveryPriorityAndUnsupportedMethods(t *testing.T) {
	router := chi.NewRouter()
	var matched string
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			matched = chi.RouteContext(r.Context()).RoutePattern()
		})
	})
	router.Route("/api/bloem/v1/admin", func(r chi.Router) {
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil, nil))
	})
	key := "00000000-0000-0000-0000-000000000001"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/bloem/v1/admin/platform/native-storage/libraries/creation/"+key, nil))
	if w.Code != 401 || matched != "/api/bloem/v1/admin/platform/native-storage/libraries/creation/{creation_key}" {
		t.Fatalf("literal recovery path shadowed: status=%d pattern=%s", w.Code, matched)
	}
	for _, request := range []struct {
		method, path string
		status       int
	}{
		{"PATCH", "/libraries/7", 405}, {"DELETE", "/sources/" + key, 405}, {"POST", "/sources/" + key + "/enable", 404},
		{"DELETE", "/sources/" + key + "/bindings/7", 405}, {"POST", "/libraries/7/repair", 404},
	} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(request.method, "/api/bloem/v1/admin/platform/native-storage"+request.path, nil))
		if w.Code != request.status {
			t.Fatalf("unsupported mutation status=%d want=%d", w.Code, request.status)
		}
	}
}

func TestNativeOnboardingHTTPV2UsesNativeProblemTypes(t *testing.T) {
	for _, code := range []string{"native_local_operation_unsupported", "native_library_delete_unsupported", "native_repair_unsupported", "native_storage_unavailable", "artifact_rejected", "revision_conflict"} {
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
	h := handlers.NewBloemNativeStorageManagementHandler(nil, nil)
	protected := apimw.NewAdminContextMiddleware(nil, nil, nil, nil, nil).Require(http.HandlerFunc(h.HandleCreateLibrary))
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"Books"}`))
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
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil, nil))
	})
	for _, tt := range []struct {
		method, path, code string
		status             int
	}{
		{"PATCH", "/libraries/7", "method_not_allowed", 405},
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
		got := nativeStorageOnboardingDependencies(deps)
		if got.NativeStorageManagement != nil {
			t.Fatal("missing stores created management dependencies")
		}
		if attachNativeStorageOnboardingReader(got, nil) {
			t.Fatal("missing reader attached a replacement coordinator")
		}
	}
	h := handlers.NewBloemNativeStorageManagementHandler(nativestorage.NewSourceManagement(nil, nil), nil)
	deps := Dependencies{BloemDependencies: BloemDependencies{NativeStorageManagement: h}}
	prepared := nativeStorageOnboardingDependencies(deps)
	copy := prepared.NativeStorageManagement
	if copy == h || copy.Sources != h.Sources || copy.Libraries != h.Libraries || h.Capabilities != nil {
		t.Fatal("router must copy handler while preserving caller services")
	}
	holder, ok := copy.Capabilities.(*nativeStorageCapabilities)
	if !ok || holder == nil || holder.guard != nil || holder.v2 != nil {
		t.Fatal("router must allocate a fresh unsealed capability holder")
	}
	scoped := copy.ForAdminScope(auth.AdminScopePlatform)
	guard, v2 := &nativeMutationGuard{}, &apiv2.Dependencies{}
	holder.attach(guard, v2)
	if scoped.Capabilities != holder || holder.guard != guard || holder.v2 != v2 {
		t.Fatal("mounted scope did not retain final composition holder")
	}
	next := nativeStorageOnboardingDependencies(prepared)
	fresh, ok := next.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities)
	if !ok || fresh == nil || fresh == holder || fresh.guard != nil || fresh.v2 != nil || copy.Capabilities != holder {
		t.Fatal("reused dependencies inherited or replaced another router witness")
	}
	if attachNativeStorageOnboardingReader(deps, handlers.NewNativeEbookFileService(nil, nil)) {
		t.Fatal("absent native implementation attached a coordinator")
	}
	var missing *nativestorage.Coordinator
	if attachNativeStorageOnboardingReader(deps, handlers.NewNativeEbookFileService(nil, missing)) {
		t.Fatal("typed nil reader attached a coordinator")
	}
	actual := &nativestorage.Coordinator{}
	if !attachNativeStorageOnboardingReader(deps, handlers.NewNativeEbookFileService(nil, actual)) {
		t.Fatal("actual reader coordinator was not attached")
	}
}
