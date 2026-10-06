package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type nativeGuardRegistry struct {
	native bool
	err    error
}

func (s nativeGuardRegistry) NativeStorageIDs(context.Context, []int) (map[int]bool, error) {
	return map[int]bool{12: s.native}, s.err
}
func TestNativeStorageMutationDispatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		registry nativeGuardRegistry
		want     int
	}{{"native", nativeGuardRegistry{native: true}, http.StatusConflict}, {"ordinary", nativeGuardRegistry{}, http.StatusNoContent}, {"unavailable", nativeGuardRegistry{err: errors.New("db unavailable")}, http.StatusServiceUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			registerNativeStorageInstallationRoutes(router, tc.registry, nativeStorageAdminDispatch{Update: handler, ApplyUpdate: handler, TestConfig: handler, PutConfig: handler, PutAuthBinding: handler, PutTaskBinding: handler, Delete: handler})
			for _, path := range []struct{ method, path string }{{"PUT", "/installations/12/config"}, {"DELETE", "/installations/12"}, {"POST", "/installations/12/update"}, {"PUT", "/installations/12"}, {"POST", "/installations/12/config/test"}, {"PUT", "/installations/12/auth-binding"}, {"PUT", "/installations/12/task-bindings/cap"}} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(path.method, path.path, nil))
				if w.Code != tc.want {
					t.Fatalf("%s %s=%d want %d", path.method, path.path, w.Code, tc.want)
				}
			}
		})
	}
}
