package api

import (
	"context"
	"sync/atomic"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/mail"
	"github.com/Silo-Server/silo-server/internal/orgdomains"
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
	// NativeMovieScannerReady witnesses startup assembly of the scan reader,
	// trusted probe and publication coordinator. It is not backend health proof
	// or viewer playback authority, and no request/config setting may change it.
	NativeMovieScannerReady bool
	// NativeSeriesScannerReady witnesses the series lane on the same scanner;
	// TV libraries are admitted only where movie playback readiness is too.
	NativeSeriesScannerReady bool
	NativeAudioScannerReady  bool
	NativeAudioGate          *atomic.Bool
	Ambience                 *ambience.Service   // S-3 seasonal ambience packs (nil when DB unavailable)
	Promotions               *promotions.Service // S-2 promotion cards (nil when DB unavailable)
	HostStatsSource          handlers.HostStatsSource
	LiveTV                   *livetv.Service // shared Live TV / OTA / DVR service (may be nil)

	// OrganizationDomains maps each request's domain to its organization for
	// presentation; OrganizationDomainStore writes domains and branding. Both
	// nil without a database (internal/orgdomains).
	OrganizationDomains     *orgdomains.Directory
	OrganizationDomainStore *orgdomains.Store

	// OnUserProfileSessionsRevoked invalidates sessions bound to the
	// named profiles of one account.
	OnUserProfileSessionsRevoked func(ctx context.Context, userID int, profileIDs []string) error
	// BloemOnUserSessionsRevoked is the account session-revoked hook for
	// Bloem's own callers that fail closed when invalidation fails (tenant
	// members, admin resources, profile revocation). Silo's
	// OnUserSessionsRevoked keeps Silo's type and runs for Silo's callers.
	BloemOnUserSessionsRevoked func(ctx context.Context, userID int) error

	// AdminContextTokens signs the short-lived administrative context JWTs.
	// It is separate from normal account-session token validation.
	AdminContextTokens      auth.AdminContextTokenService
	PlatformAdminAuthorizer auth.PlatformAdminAuthorizer
	AdminPeopleService      *adminpeople.Service
	AdminPeopleWorker       *adminpeople.Worker
	// SupportMail replaces the SMTP sender for the organization support
	// routes' reset emails; nil sends through the server's mail settings.
	SupportMail mail.Sender

	// NativeStorageManagement shares retained domain services and the reader
	// coordinator. Construction does not witness complete mutation readiness.
	NativeStorageManagement *handlers.BloemNativeStorageManagementHandler

	// NativeVideoCompat is the Jellyfin listener's native media host. The
	// router installs native video delivery into it when it builds one.
	NativeVideoCompat *NativeVideoCompat
}
