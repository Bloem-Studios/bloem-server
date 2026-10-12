package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/go-chi/chi/v5"
)

func TestLibraryLocationsHTTPAdminRoutes(t *testing.T) {
	router := chi.NewRouter()
	router.Route("/api/bloem/v1/admin", func(r chi.Router) {
		mountBloemNativeStorageManagement(r, handlers.NewBloemNativeStorageManagementHandler(nil))
	})
	for _, scope := range []string{"platform", "organization"} {
		for _, op := range []struct{ method, path string }{{"POST", "/library-locations"}, {"GET", "/library-locations/1"}, {"PUT", "/library-locations/1"}, {"GET", "/native-storage/sources/00000000-0000-0000-0000-000000000201/browse"}} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(op.method, "/api/bloem/v1/admin/"+scope+op.path, strings.NewReader(`{"secret":"never-decoded"}`)))
			if w.Code != 401 {
				t.Errorf("%s %s: got %d want 401", op.method, scope+op.path, w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("admin response cacheable")
			}
		}
	}
}

func TestLibraryLocationsHTTPActualAdminMiddleware(t *testing.T) {
	h := (&handlers.BloemLibraryLocationsHandler{}).ForAdminScope(auth.AdminScopePlatform)
	for _, call := range []http.HandlerFunc{h.HandleCreate, h.HandleGet, h.HandleReplace} {
		protected := apimw.NewAdminContextMiddleware(nil, nil, nil, nil, nil).Require(call)
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, httptest.NewRequest("POST", "/api/bloem/v1/admin/platform/library-locations", strings.NewReader(`{"library":{"name":"x"}}`)))
		if w.Code != 401 {
			t.Fatalf("actual admin-context middleware status=%d", w.Code)
		}
	}
}
func TestLibraryLocationsStartupCompositionReadiness(t *testing.T) {
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-composition", 3)))
	if err != nil {
		t.Fatal(err)
	}
	// Readiness needs a configured client, not a reachable server: any address
	// works, and the hook fails the test if composition ever dials.
	client := redis.NewClient(&redis.Options{})
	client.AddHook(noRedisDial{t: t})
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	host := &nativestorage.Host{Registry: &plugins.NativeStorageRegistry{}, Manager: storageplugin.NewManager(storageplugin.Config{})}
	db := &pgxpool.Pool{}
	for _, mode := range []string{"complete", "no-redis", "no-cipher", "no-manager"} {
		t.Run(mode, func(t *testing.T) {
			localHost := &nativestorage.Host{Registry: host.Registry, Manager: host.Manager}
			deps := Dependencies{DB: db, RedisClient: client, SecretCipher: cipher, LibraryIngester: &libraryingest.Executor{}, NativeStorage: localHost}
			switch mode {
			case "no-redis":
				deps.RedisClient = nil
			case "no-cipher":
				deps.SecretCipher = nil
			case "no-manager":
				localHost.Manager = nil
			}
			deps = nativeStorageDependencies(deps)
			c := deps.NativeStorageManagement.Capabilities.(*nativeStorageCapabilities)
			c.reader = true
			h := handlers.NewLibraryHandler(nil, nil, nil, nil, nil)
			locations := nativestorage.NewLibraryLocations(db)
			h.StorageLocations = locations
			attachNativeLibraryLocations(deps, h)
			ready := mode == "complete"
			if c.LibraryLocationBrowsingReady(t.Context()) != ready || c.LibraryLocationEditingReady(t.Context()) != ready {
				t.Fatalf("%s readiness differs", mode)
			}
			// Mixing stays off until mixed ingestion flips the domain's one switch.
			if c.LibraryLocationMixingReady(t.Context()) {
				t.Fatal("mixing advertised before mixed ingestion")
			}
			locations.SetMixedLocationsReady(true)
			if c.LibraryLocationMixingReady(t.Context()) != ready {
				t.Fatalf("%s mixing capability ignores the domain switch", mode)
			}
			if c.NativeAudioReady(t.Context()) {
				t.Fatal("Task3 advertised audio without Task9")
			}
			if deps.NativeStorageManagement.Locations == nil || deps.NativeStorageManagement.Locations.Libraries != h {
				t.Fatal("composition replaced the ordinary creator")
			}
		})
	}
}

// noRedisDial refuses every Redis connection the composition might open.
type noRedisDial struct{ t *testing.T }

func (h noRedisDial) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		h.t.Error("startup composition dialed Redis")
		return nil, errors.New("startup composition must not dial Redis")
	}
}
func (noRedisDial) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (noRedisDial) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
