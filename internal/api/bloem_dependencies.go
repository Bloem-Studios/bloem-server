package api

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
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
	PastimeListOrigin string
	ListCollections   *ListCollectionRuntime
	// NativeMovieScannerReady witnesses startup assembly of the scan reader,
	// trusted probe and publication coordinator. It is not backend health proof
	// or viewer playback authority, and no request/config setting may change it.
	NativeMovieScannerReady bool
	NativeAudioScannerReady bool
	NativeAudioGate         *atomic.Bool
	Ambience                *ambience.Service   // S-3 seasonal ambience packs (nil when DB unavailable)
	Promotions              *promotions.Service // S-2 promotion cards (nil when DB unavailable)
	HostStatsSource         handlers.HostStatsSource
	LiveTV                  *livetv.Service // shared Live TV / OTA / DVR service (may be nil)

	// OnUserProfileSessionsRevoked invalidates sessions bound to the
	// named profiles of one account.
	OnUserProfileSessionsRevoked func(ctx context.Context, userID int, profileIDs []string) error

	// AdminContextTokens signs the short-lived administrative context JWTs.
	// It is separate from normal account-session token validation.
	AdminContextTokens      auth.AdminContextTokenService
	PlatformAdminAuthorizer auth.PlatformAdminAuthorizer
	AdminPeopleService      *adminpeople.Service
	AdminPeopleWorker       *adminpeople.Worker

	// NativeStorageManagement shares retained domain services and the reader
	// coordinator. Construction does not witness complete mutation readiness.
	NativeStorageManagement *handlers.BloemNativeStorageManagementHandler
}

// Upstream notification hooks have no error result. Keep Bloem's detached
// invalidation boundary and report a failure after the committed mutation.
func bloemSessionRevocationNotification(revoke func(context.Context, int) error) func(context.Context, int) {
	if revoke == nil {
		return nil
	}
	return func(ctx context.Context, userID int) {
		if err := sessioninvalidation.Run(ctx, func(callCtx context.Context) error { return revoke(callCtx, userID) }); err != nil {
			slog.ErrorContext(ctx, "session invalidation failed", "user_id", userID, "error", err)
		}
	}
}
