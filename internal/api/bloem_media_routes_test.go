package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

func TestBloemAdditionalMediaRoutesRemainProvisional(t *testing.T) {
	base := map[string]bool{}
	for _, route := range nativeMediaRoutes {
		base[route.Method+" "+route.Pattern] = true
	}
	for _, route := range declaredNativeMediaRoutes() {
		if base[route.Method+" "+route.Pattern] {
			continue
		}
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
			t.Run(route.Method+" "+route.Pattern+" "+http.StatusText(status), func(t *testing.T) {
				cfg := streamtelemetry.DefaultConfig("test")
				cfg.Enabled = true
				registry := streamtelemetry.NewRegistry(cfg, streamtelemetry.NewLocalStore(), nil)
				handler := registry.Observe(route)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(route.Method, "/", nil))
				snapshot := registry.Sweep()
				if len(snapshot.Sessions) != 0 || len(snapshot.Transfers) != 0 {
					t.Fatalf("status %d created logical activity: %+v", status, snapshot)
				}
			})
		}
	}
}
