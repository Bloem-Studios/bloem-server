package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// v1RouteSiloContract lists every /api/v1 route a client may rely on.
//
// It is deliberately NOT the same mechanism as the route-surface golden. That
// golden is regenerated whenever the surface changes, which makes it good at
// showing a diff and bad at guaranteeing anything: an addition and a removal
// look alike in it, and regenerating absorbs both.
//
// This file only ever shrinks by someone deleting a line, which is a visible,
// reviewable act with a removal entry to write in docs/architecture/v1-scope.md.
// Adding routes needs no change here at all, because an addition cannot break a
// client that never calls it. The frozen snapshot also contains historical
// Bloem-only Live TV routes: bloem_v1_adjudication_test.go verifies each exact
// native relocation before removing it from this upstream-preservation check.
const v1RouteSiloContract = "testdata/v1_routes_silo_contract.txt"

// A Silo client is a supported caller and must stay one. Removing or renaming a
// v1 route breaks every client already built against it, including ones we do
// not ship and cannot fix, so a removal has to be a decision rather than a
// side effect of editing the router.
func TestV1SiloRouteContractIsNeverNarrowed(t *testing.T) {
	// Use a migrated disposable database so router construction does not fall
	// back from missing settings and tenancy tables.
	pool := newV1TenancyDatabase(t)
	provider := pgstore.NewPostgresProvider(pool)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	router := newChiRouter(Dependencies{
		AppContext: t.Context(),
		DB:         pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "v1-silo-contract-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     provider,
		FileRepo:              scanner.NewFileRepository(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})
	live := map[string]struct{}{}
	for _, route := range walkV1Routes(t, router) {
		live[route] = struct{}{}
	}

	raw, err := os.ReadFile(v1RouteSiloContract)
	if err != nil {
		t.Fatalf("read the Silo route contract: %v", err)
	}
	var missing []string
	for _, route := range bloemAdjudicatedV1Baseline(t, v1RouteSiloContract, raw, router) {
		route = strings.TrimSpace(route)
		if route == "" {
			continue
		}
		if _, ok := live[route]; !ok {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("these /api/v1 routes are in the Silo contract but no longer served:\n  %s\n\n"+
			"Every client already built against them breaks, including ones we do not ship.\n"+
			"If the removal is intended, delete the line from %s and record it in the pre-lock\n"+
			"removals table in docs/architecture/v1-scope.md.",
			strings.Join(missing, "\n  "), v1RouteSiloContract)
	}
}
