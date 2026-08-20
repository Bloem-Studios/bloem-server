package api

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
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
				OwnershipBootstrapper: b, MembershipProvisioner: b}
		}},
		"db without config": {requiresDatabase: true, dependencies: func() Dependencies {
			return Dependencies{DB: withDB()}
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
			router := NewRouter(testCase.dependencies())

			login := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
				`{"email":"probe@example.test","password":"probe","device_id":"probe"}`, "", nil)
			routeMounted := login.Code != http.StatusNotFound

			capabilities := performJSONRequest(t, router, http.MethodGet, "/api/v2/capabilities", "", "", nil)
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
