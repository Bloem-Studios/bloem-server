package api

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The capability must agree with the route table under every wiring: a client
// that trusts discovery must never be told about a login route the router did
// not mount, nor denied one it did.
func TestDirectProfileCapabilityMatchesRouteWiring(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret:          "capability-wiring-secret",
		AccessTokenExpiry:  time.Hour,
		RefreshTokenExpiry: 24 * time.Hour,
	}}

	var pool *pgxpool.Pool
	withDB := func() *pgxpool.Pool {
		if pool == nil {
			pool = newV1TenancyDatabase(t)
		}
		return pool
	}

	testCases := map[string]struct {
		requiresDatabase bool
		dependencies     func() Dependencies
	}{
		"db and config": {requiresDatabase: true, dependencies: func() Dependencies {
			p := withDB()
			b := v1TenancyBootstrap{store: tenancy.NewStore(p)}
			return Dependencies{DB: p, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(p),
				OwnershipBootstrapper: b}
		}},
		"db without config": {requiresDatabase: true, dependencies: func() Dependencies {
			return Dependencies{DB: withDB()}
		}},
		"db and empty config": {requiresDatabase: true, dependencies: func() Dependencies {
			return Dependencies{DB: withDB(), Config: &config.Config{}}
		}},
		"config without db": {dependencies: func() Dependencies {
			return Dependencies{Config: cfg}
		}},
		"neither": {dependencies: func() Dependencies {
			return Dependencies{}
		}},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			if testCase.requiresDatabase && strings.TrimSpace(os.Getenv("SILO_TEST_DATABASE_URL")) == "" {
				if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
					t.Fatal("SILO_TEST_DATABASE_URL is required")
				}
				t.Skip("SILO_TEST_DATABASE_URL is not set")
			}
			deps := testCase.dependencies()
			router := NewRouter(deps)

			requestStatus := performJSONRequest(t, router, http.MethodGet, apiv2.Prefix+"/requests/status", "", "", nil)
			if requestStatus.Code < http.StatusBadRequest {
				t.Fatalf("unauthenticated request policy route succeeded: %d %s", requestStatus.Code, requestStatus.Body)
			}
			if deps.Config == nil {
				if requestStatus.Code != http.StatusServiceUnavailable {
					t.Fatalf("router without authentication exposed native request policy: %d %s", requestStatus.Code, requestStatus.Body)
				}
				legacyStatus := performJSONRequest(t, router, http.MethodGet, "/api/v1/requests/status", "", "", nil)
				if legacyStatus.Code != http.StatusNotFound {
					t.Fatalf("router without authentication mounted legacy request policy: %d %s", legacyStatus.Code, legacyStatus.Body)
				}
			}

			login := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
				`{"email":"probe@example.test","password":"probe","device_id":"probe"}`, "", nil)
			routeMounted := login.Code != http.StatusNotFound

			capabilities := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/capabilities", "", "", nil)
			if capabilities.Code == http.StatusNotFound {
				// No v2 surface at all: nothing is advertised, so nothing can
				// disagree — but then the login route must be absent too.
				if routeMounted {
					t.Fatalf("login route mounted with no capability surface")
				}
				return
			}
			advertised := strings.Contains(capabilities.Body.String(), `"direct_profile_login":true`)
			if advertised != routeMounted {
				t.Fatalf("capability advertises %v while route mounted is %v (login=%d, capabilities=%s)",
					advertised, routeMounted, login.Code, capabilities.Body.String())
			}
		})
	}
}

func TestBloemAuthenticatedRouterRequiresRequestPolicyDependencies(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	for _, missing := range []string{"users", "groups"} {
		t.Run(missing, func(t *testing.T) {
			service := mediarequests.NewService(mediarequests.NewRepository(pool, nil), nil, nil)
			if missing != "users" {
				service.SetUserRepository(auth.NewUserRepository(pool))
			}
			if missing != "groups" {
				service.SetGroupPolicyProvider(access.NewTenantGroupStore(pool))
			}
			handler := handlers.NewRequestsHandler(service)
			jwt := auth.NewJWTService("request-composition-test-secret", time.Hour, time.Hour)
			authMW := apimw.NewAuthMiddleware(jwt, auth.NewSessionRepository(pool), nil, nil)
			defer func() {
				failure := recover()
				if failure == nil {
					t.Fatal("authenticated router accepted a request service without required policy authority")
				}
				err, ok := failure.(error)
				if !ok || !strings.Contains(err.Error(), "compose Bloem request policy:") {
					t.Fatalf("unexpected composition panic: %v", failure)
				}
			}()
			// No lifecycle config: authentication presence, rather than JWT or
			// lifecycle configuration, controls whether policy is mandatory.
			wireBloemRouter(chi.NewRouter(), Dependencies{DB: pool}, nil, handler, nil, authMW, nil, nil)
		})
	}
}
