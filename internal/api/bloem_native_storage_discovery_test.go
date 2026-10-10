package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func discoveryCapabilitiesResponse(t *testing.T, deps Dependencies, ctx context.Context) map[string]any {
	t.Helper()
	h := deps.NativeStorageManagement.ForAdminScope(auth.AdminScopePlatform)
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopePlatform}
	r := httptest.NewRequest("GET", "/capabilities", nil).WithContext(apimw.SetAdminContextClaims(ctx, actor))
	w := httptest.NewRecorder()
	h.HandleCapabilities(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("capability response status=%d headers=%v", w.Code, w.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestNativeStorageDiscoveryCapabilityUsesActualComposition(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		reader, manager, matching, canceled bool
		ready                               bool
	}{
		{"ready", true, true, true, false, true},
		{"reader_absent", false, true, true, false, false},
		{"manager_absent", true, false, true, false, false},
		{"source_registry_mismatch", true, true, false, false, false},
		{"canceled", true, true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &pgxpool.Pool{}
			registry := &plugins.NativeStorageRegistry{}
			host := &nativestorage.Host{Registry: registry}
			if tc.manager {
				host.Manager = &storageplugin.Manager{}
			}
			deps := Dependencies{DB: pool, LibraryIngester: &libraryingest.Executor{}, NativeStorage: host}
			if !tc.matching {
				h := handlers.NewBloemNativeStorageManagementHandler(nativestorage.NewSourceManagement(pool, &plugins.NativeStorageRegistry{}))
				h.Registry = registry
				deps.NativeStorageManagement = h
			}
			deps = nativeStorageDependencies(deps)
			if tc.reader {
				attachNativeStorageReader(deps, handlers.NewNativeEbookFileService(nil, &nativestorage.Coordinator{}))
			}
			ctx := t.Context()
			if tc.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			body := discoveryCapabilitiesResponse(t, deps, ctx)
			if body["s3_initial_root_discovery"] != tc.ready {
				t.Fatalf("discovery ready=%v want=%v", body["s3_initial_root_discovery"], tc.ready)
			}
			if body["backend_verified"] != false || body["schema"] != float64(2) {
				t.Fatal("discovery certified a backend or changed management schema")
			}
		})
	}
}

func TestNativeStorageDiscoveryActualAdminMiddlewareFailClosed(t *testing.T) {
	h := handlers.NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopePlatform)
	protected := apimw.NewAdminContextMiddleware(nil, nil, nil, nil, nil).Require(http.HandlerFunc(h.HandleDiscoverInstall))
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, httptest.NewRequest("POST", "/installations/discover", nil))
	if w.Code != 401 {
		t.Fatalf("actual middleware refusal=%d want401", w.Code)
	}
}
