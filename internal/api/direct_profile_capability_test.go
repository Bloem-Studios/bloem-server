package api

import (
	"net/http"
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

	for name, deps := range map[string]func() Dependencies{
		"db and config": func() Dependencies {
			p := withDB()
			b := v1TenancyBootstrap{store: tenancy.NewStore(p)}
			return Dependencies{DB: p, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(p),
				OwnershipBootstrapper: b, MembershipProvisioner: b}
		},
		"db without config": func() Dependencies {
			return Dependencies{DB: withDB()}
		},
		"config without db": func() Dependencies {
			return Dependencies{Config: cfg}
		},
		"neither": func() Dependencies {
			return Dependencies{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			router := NewRouter(deps())

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
