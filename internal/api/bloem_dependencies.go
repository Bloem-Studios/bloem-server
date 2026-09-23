package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/compatapi"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// BloemDependencies is every dependency Bloem adds to Silo's router
// Dependencies. It is embedded in Dependencies so Silo's struct carries one
// Bloem line instead of a field per feature: reads and assignments through the
// promoted fields (deps.LiveTV, deps.Ambience = ...) work unchanged, while a
// composite literal names the block explicitly:
//
//	api.Dependencies{DB: pool, BloemDependencies: api.BloemDependencies{LiveTV: svc}}
type BloemDependencies struct {
	Ambience        *ambience.Service   // S-3 seasonal ambience packs (nil when DB unavailable)
	Promotions      *promotions.Service // S-2 promotion cards (nil when DB unavailable)
	HostStatsSource handlers.HostStatsSource
	LiveTV          *livetv.Service // shared Live TV / OTA / DVR service (may be nil)

	// OnUserProfileSessionsRevoked drops compatibility sessions bound to the
	// named profiles of one account.
	OnUserProfileSessionsRevoked func(ctx context.Context, userID int, profileIDs []string) error

	// CompatAPIV1 is the private Compatibility Service API v1 handler
	// (internal/compatapi), consumed exclusively by enrolled compatibility
	// applications. May be nil; no compat routes are registered in that
	// case, so the surface fails closed until the trust stack is wired.
	CompatAPIV1 *compatapi.Handler
	// AdminContextTokens signs the short-lived administrative context JWTs.
	// It is separate from normal account-session token validation.
	AdminContextTokens      auth.AdminContextTokenService
	PlatformAdminAuthorizer auth.PlatformAdminAuthorizer
	AdminPeopleService      *adminpeople.Service
	AdminPeopleWorker       *adminpeople.Worker

	// CompatApplications is the application lifecycle service behind the
	// Compatibility Applications admin surface (may be nil; the surface is
	// not mounted). Handlers call this service to list, enroll, enable,
	// disable, rotate, and revoke; they never write application state
	// themselves.
	CompatApplications handlers.CompatibilityApplicationService

	// The compatibility edge gateway is deliberately absent from this
	// router. The public listener hands only /api/** here, so the gateway's
	// families (/System/**, /emby/**, /audiobookshelf/**, /web/**, …) are
	// claimed one layer up, in cmd/silo's publicMux, ahead of the SPA
	// fallback. Mounting it here as well would be dead code that only tests
	// could reach. What this router still owes the gateway is the negative
	// guarantee: no native route may fall inside an owned family, which
	// TestCompatGatewayRoutesDoNotOverlapNativeRoutes pins.
}
