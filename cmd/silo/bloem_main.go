package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatapp"
	"github.com/Silo-Server/silo-server/internal/compatgateway"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/lanadvert"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/Silo-Server/silo-server/internal/nodemetrics"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/internal/serveridentity"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Bloem-owned declarations of package main, moved out of main.go so the Silo
// file carries only the call sites. See contracts/seams.txt.

type reconcilerShutdown interface {
	Stop()
	StopAndWait(context.Context) error
}

type heartbeatShutdown interface {
	Stop()
	StopAndWait(context.Context) error
	CleanupSelf(context.Context) error
}

type sharedWorkerShutdownResult struct {
	reconcilerJoinErr error
	heartbeatJoinErr  error
	cleanupErr        error
}

// shutdownSharedWorkerOwnership fences both writers before joining either one.
// Reconciliation is joined first because it can recreate a session row; the
// shared session and heartbeat rows are deleted only after both joins succeed.
func shutdownSharedWorkerOwnership(
	ctx context.Context,
	reconciler reconcilerShutdown,
	heartbeat heartbeatShutdown,
) sharedWorkerShutdownResult {
	if reconciler != nil {
		reconciler.Stop()
	}
	if heartbeat != nil {
		heartbeat.Stop()
	}

	var result sharedWorkerShutdownResult
	if reconciler != nil {
		result.reconcilerJoinErr = reconciler.StopAndWait(ctx)
	}
	if heartbeat != nil {
		result.heartbeatJoinErr = heartbeat.StopAndWait(ctx)
	}
	if heartbeat != nil && result.reconcilerJoinErr == nil && result.heartbeatJoinErr == nil {
		result.cleanupErr = heartbeat.CleanupSelf(ctx)
	}
	return result
}

// tenancyOwnershipBootstrapper adapts tenancy.Store's ownership state return
// to the auth setup seam, which only needs to know whether activation worked.
type tenancyOwnershipBootstrapper struct {
	store *tenancy.Store
}

func (b tenancyOwnershipBootstrapper) ActivateInitialOwnership(ctx context.Context, accountID int) error {
	_, err := b.store.ActivateInitialOwnership(ctx, accountID)
	return err
}

func (b tenancyOwnershipBootstrapper) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, legacyRole)
	return err
}

func (b tenancyOwnershipBootstrapper) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, legacyRole string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, legacyRole)
	return membership.OrganizationID, membership.ID, err
}

func (b tenancyOwnershipBootstrapper) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, legacyRole string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionMembershipInTransaction(ctx, tx, organizationID, accountID, legacyRole)
	return membership.OrganizationID, membership.ID, err
}

// proxySourceAccess is proxy.SourceAccess's database-backed implementation.
// internal/proxy holds no database handle of its own (see proxy.NewServer) —
// this composes the same tenancy/resourcetenancy authorities the API's
// request-scoped tenant middleware uses, built here where the pool this
// process opened already exists, and wired in with proxy.Server.SetSourceAccess.
//
// A signed proxy artifact (stream token, media grant, download URL) names a
// media file, never its media_folder_id, so this is also where that file's
// current library is resolved before the organization entitlement check
// runs.
type proxySourceAccess struct {
	files     *scanner.FileRepository
	tenants   *tenancy.Resolver
	resources *resourcetenancy.Store
}

func (a proxySourceAccess) AllowMediaSource(ctx context.Context, accountID int, mediaFileID int) error {
	file, err := a.files.GetByID(ctx, mediaFileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proxy.ErrSourceHidden
		}
		return fmt.Errorf("%w: load media file: %w", proxy.ErrAuthorityUnavailable, err)
	}
	// Legacy accounts (and every account a stateless proxy token can name —
	// tokens carry no organization claim) project into the deployment's
	// default organization, mirroring TenantMiddleware.ResolveLegacy.
	tenant, err := a.tenants.Resolve(ctx, accountID, nil, true)
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantNotFoundOrHidden) || errors.Is(err, tenancy.ErrTenantSuspended) {
			return proxy.ErrSourceHidden
		}
		return fmt.Errorf("%w: resolve tenant: %w", proxy.ErrAuthorityUnavailable, err)
	}
	root := resourcetenancy.RootRef{Kind: resourcetenancy.RootMediaFolder, ID: int64(file.MediaFolderID)}
	if _, err := a.resources.RequireAccess(ctx, tenant, root); err != nil {
		if errors.Is(err, resourcetenancy.ErrResourceHidden) {
			return proxy.ErrSourceHidden
		}
		return fmt.Errorf("%w: require media folder access: %w", proxy.ErrAuthorityUnavailable, err)
	}
	return nil
}

// publicMux composes the public listener: Prometheus metrics, the chi API
// router for /api/**, the fixed-path compatibility gateway for the reviewed
// route families, and the Bloem SPA for everything else.
//
// The layering matters and is why this is a named function rather than a few
// lines inside main: only /api/** reaches the chi router, so the gateway's
// families (/System/**, /emby/**, /audiobookshelf/**, /web/**, …) are claimed
// here, ahead of the SPA fallback, or public ingress never reaches them at
// all. TestPublicMuxRoutesEachLayer drives this exact function.
//
// ABS-compat is NOT mounted here — see the "ABS compat listener" block in
// main. It binds its own port so the discovery probes (/ping, /healthcheck,
// /status, /init, /login, /socket.io) own the URL space without colliding
// with the SPA fallback. Mirrors how the Jellyfin compat server is set up at
// :8096.
//
// A nil gateway composes to the plain SPA fallback, keeping deployments
// without the compatibility stack byte-identical to the pre-gateway listener.
// publicServer builds the public listener: the composed handler from
// publicMux plus the timeouts the public port runs with. It exists so the
// thing production serves is a value a test can construct and drive, rather
// than a shape assembled inline in main — TestPublicServerRoutesEachLayer
// exercises this exact function.
//
// It is called from exactly one place, servePublic, and the guard tests in
// main_test.go fail if that stops being true.
func publicServer(addr string, router, frontend, gateway http.Handler) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      publicMux(router, frontend, gateway),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func publicMux(router, frontend, gateway http.Handler) http.Handler {
	return bloemRootHandler(router, frontend, gateway)
}

// publicPort is a bound, serving public listener. Only servePublic can build
// one, and the composed *http.Server never escapes it: what a caller holds is
// a handle that can stop the port and do nothing else. That is deliberate —
// every previous shape of this code handed main a *http.Server, and a
// *http.Server in main's scope is a handler that a later statement can
// replace, discard, or serve a second time.
type publicPort struct {
	srv  *http.Server
	addr string
}

// shutdown stops the public port gracefully, refusing new connections and
// draining the in-flight ones until ctx expires.
func (p *publicPort) shutdown(ctx context.Context) error {
	return p.srv.Shutdown(ctx)
}

// listenPublic binds the canonical public address. It is the only listener
// this package opens: everything else that answers on a port either receives
// an already-bound address from configuration through its own constructor, or
// does not exist. TestConfiguredPublicAddressIsBoundOnlyByTheBlessedListener
// pins that.
func listenPublic(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// servePublic is the composition root of the public port: it composes the
// public handler with publicServer and starts serving it on ln, reporting any
// non-graceful serve failure on errCh exactly as the inline goroutine it
// replaces did.
//
// The gateway is the concrete *compatgateway.Gateway rather than an
// http.Handler on purpose. Every argument here is an http.Handler otherwise,
// so a swapped pair — the frontend where the gateway belongs, or the reverse —
// used to be a silent behavioral change that still type-checked. With one
// concrete parameter the swap is a compile error.
//
// It returns a handle rather than blocking and returning an error, because
// main still owns the ordered shutdown sequence: the public port stops
// accepting first, then the compat listeners, then sessions are cleaned. A
// blocking servePublic would have to either own that ordering or hand back the
// server, and handing back the server is the escape hatch this whole shape
// exists to remove.
func servePublic(
	ln net.Listener,
	router, frontend http.Handler,
	gateway *compatgateway.Gateway,
	errCh chan<- error,
) *publicPort {
	// A typed-nil *Gateway satisfies http.Handler as a non-nil interface,
	// which is not the same composition as "no gateway": publicMux's nil case
	// is the pre-gateway listener byte for byte, and a typed nil would route
	// the owned families into a nil receiver instead.
	var gatewayHandler http.Handler
	if gateway != nil {
		gatewayHandler = gateway
	}

	addr := ln.Addr().String()
	port := &publicPort{srv: publicServer(addr, router, frontend, gatewayHandler), addr: addr}
	go func() {
		slog.Info("HTTP server listening", "addr", port.addr)
		if listenErr := port.srv.Serve(ln); listenErr != nil && listenErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server error: %w", listenErr)
		}
	}()
	return port
}

// serveAux starts an auxiliary listener — a compatibility protocol server on
// its own port, never the public one — and reports a non-graceful failure on
// errCh. It exists so main holds no Serve call of its own: the guard tests
// allow ListenAndServe only in the handful of named functions that own a
// listener, and "somewhere in main()" is precisely the place that must not
// count as one.
func serveAux(name string, srv *http.Server, errCh chan<- error) {
	go func() {
		slog.Info("compat server listening", "server", name, "addr", srv.Addr)
		if listenErr := srv.ListenAndServe(); listenErr != nil && listenErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("%s server error: %w", name, listenErr)
		}
	}()
}

// absCompatServer builds the Audiobookshelf-compat listener: its own port,
// its own root URL space, no SPA fallback. Named rather than inline for the
// same reason as serveAux — main composes no http.Server itself.
func absCompatServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
}

type adminPeopleBackgroundWorker interface {
	Run(context.Context)
}

func startAdminPeopleBackgroundWorker(ctx context.Context, worker adminPeopleBackgroundWorker) {
	go worker.Run(ctx)
}

func newTenantAwareViewerResolver(pool *pgxpool.Pool, viewer policy.AccessScopeResolver) *policy.TenantViewerResolver {
	tenantStore := tenancy.NewStore(pool)
	return policy.NewTenantViewerResolver(
		viewer,
		tenancy.NewSubjectResolver(tenancy.NewResolver(tenantStore), tenantStore),
	)
}

// bloemWireCoreDependencies installs Bloem's API router dependencies on deps:
// admin-people (whose background worker it starts on ctx), administrative
// context tokens, the platform admin authorizer, compatibility applications
// and the tenancy ownership/membership bootstrapper.
func bloemWireCoreDependencies(ctx context.Context, deps *api.Dependencies, pool *pgxpool.Pool, cfg *config.Config) {
	adminPeopleService := adminpeople.NewService(pool, cfg.Auth.JWTSecret)
	adminPeopleWorker := adminpeople.NewWorker(adminPeopleService, adminpeople.WorkerOptions{})
	startAdminPeopleBackgroundWorker(ctx, adminPeopleWorker)

	deps.AdminPeopleService = adminPeopleService
	deps.AdminPeopleWorker = adminPeopleWorker
	deps.AdminContextTokens = auth.NewAdminContextTokenService(cfg.Auth.JWTSecret)
	if pool != nil {
		deps.PlatformAdminAuthorizer = auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(pool))
		// Companion enrollment and revocable service trust, behind the
		// Compatibility Applications admin surface. Without a database the
		// adapter is nil and the surface stays unmounted rather than half
		// wired.
		deps.CompatApplications = handlers.NewCompatApplicationService(compatapp.NewService(pool))
		bootstrapper := tenancyOwnershipBootstrapper{store: tenancy.NewStore(pool)}
		deps.OwnershipBootstrapper = bootstrapper
		deps.MembershipProvisioner = bootstrapper
	}
}

// runBloemCommand runs a Bloem maintenance subcommand named by args[1] and
// reports whether it did; main returns when it has.
func runBloemCommand(args []string) bool {
	if len(args) > 1 && args[1] == "membership-policy" {
		if err := runMembershipPolicyCommand(context.Background(), args[2:]); err != nil {
			log.Fatalf("membership-policy: %v", err)
		}
		return true
	}
	return false
}

// newProxySourceAccess builds the proxy's source-access authority. A signed
// recipe, media grant, or download URL names a source file; none of them
// preserve a library entitlement. The proxy rechecks it against current
// organization ownership before it serves (or relays) any media byte.
func newProxySourceAccess(pool *pgxpool.Pool) proxySourceAccess {
	return proxySourceAccess{
		files:     scanner.NewFileRepository(pool),
		tenants:   tenancy.NewResolver(tenancy.NewStore(pool)),
		resources: resourcetenancy.NewStore(pool),
	}
}

// bloemProfileSessionRevoker drops the compatibility sessions bound to the
// named profiles of an account. compatServer is read at call time: main
// populates it after the router is built.
func bloemProfileSessionRevoker(compatServer **jellycompat.Server) func(ctx context.Context, userID int, profileIDs []string) error {
	return func(ctx context.Context, userID int, profileIDs []string) error {
		if server := *compatServer; server != nil {
			return server.SessionStore().DeleteByUserAndProfileIDs(ctx, userID, profileIDs)
		}
		return nil
	}
}

// bloemWireServices builds the process-level services Bloem adds and installs
// them on deps: the host stats sampler, S-3 seasonal ambience, S-2
// promotions, and the shared Live TV service. It returns the Live TV service
// (nil without a database) so main can register its tasks, hand it to the
// Jellyfin-compatible surface, and close it at shutdown.
func bloemWireServices(
	ctx context.Context,
	deps *api.Dependencies,
	pool *pgxpool.Pool,
	cfg *config.Config,
	nodeIdentity string,
	imageCacher *imagecache.Cacher,
) *livetv.Service {
	// Host CPU/memory/network sampling needs no database — it's a pure /proc
	// reader — so it's wired unconditionally. Start exits its background
	// goroutine on its own once ctx is canceled at shutdown.
	hostStatsSampler := nodemetrics.NewSampler(nodemetrics.Options{Interval: 2 * time.Second})
	hostStatsSampler.Start(ctx)
	deps.HostStatsSource = hostStatsSampler

	// S-3 seasonal ambience packs store their artwork in the catalog assets
	// store, like branding, so uploads work on local artwork storage as well
	// as S3 (on S3 the assets store is the public bucket, so existing keys
	// are unchanged). The public S3 client is the fallback only when no
	// assets store is configured. The nil-interface rule applies: never hand
	// the service a typed-nil store.
	var ambienceStore ambience.AssetStore
	if assets := blobstore.NewBucketAPI(deps.Blobs.Assets); assets != nil {
		ambienceStore = assets
	} else if deps.S3Public != nil {
		ambienceStore = deps.S3Public
	}
	if pool != nil {
		deps.Ambience = ambience.NewService(pool, recipes.RealClock{}, ambienceStore)
		// S-2 promotion cards: dismissals ride on the per-profile user store.
		deps.Promotions = promotions.NewService(pool, recipes.RealClock{}, deps.UserStoreProvider)
	}

	// Live TV is a single shared service used by the native API, Jellyfin
	// compatibility surface, guide sync, DVR processing, and HLS playback.
	if deps.DB == nil {
		return nil
	}
	liveTVSvc := livetv.NewService(deps.DB)
	liveTVSvc.SetXtreamCipher(deps.SecretCipher)
	liveTVSvc.SetClusterOwner(nodeIdentity, nodeidentity.InstanceID())
	liveTVSettings := func(context.Context) livetv.TranscodeSettings {
		current := cfg
		if deps.LiveConfig != nil {
			if live := deps.LiveConfig(); live != nil {
				current = live
			}
		}
		return livetv.TranscodeSettings{
			HWAccel:       current.LiveTV.HWAccel,
			HWDecode:      current.LiveTV.HWDecode,
			EncoderPreset: current.LiveTV.EncoderPreset,
			FrameRateCap:  current.LiveTV.FrameRateCap,
			MaxResolution: current.LiveTV.MaxResolution,
			PlayMethod:    current.LiveTV.PlayMethod,
			MaxTranscodes: current.LiveTV.MaxTranscodes,
		}
	}
	liveTVSvc.SetPlaybackBridge(livetv.NewHLSBridge(livetv.HLSBridgeOptions{
		Root:       cfg.Playback.TranscodeDir,
		FFmpegPath: cfg.Playback.FFmpegPath,
		Settings:   liveTVSettings,
	}))
	dvrPath := cfg.LiveTV.DVRPath
	if dvrPath == "" {
		dvrPath = config.DefaultLiveTVDVRPath
	}
	liveTVSvc.SetRecorder(livetv.NewRecorder(liveTVSvc, dvrPath, cfg.Playback.FFmpegPath))
	if imageCacher != nil && deps.ImageResolver != nil {
		liveTVArtwork := livetv.NewArtworkCache(deps.DB, imageCacher, deps.ImageResolver)
		// Delete through the store the cacher writes to, so local
		// artwork storage is cleaned up too, not only S3.
		if deps.Blobs.Assets != nil {
			liveTVArtwork.SetObjectDeleter(deps.Blobs.Assets)
		}
		liveTVArtwork.SetEnabled(cfg.Metadata.CacheImages)
		liveTVSvc.SetArtworkCache(liveTVArtwork)
		if deps.OnConfigChange != nil {
			deps.OnConfigChange(func(_, updated *config.Config) {
				if updated != nil {
					liveTVArtwork.SetEnabled(updated.Metadata.CacheImages)
				}
			})
		}
	}
	deps.LiveTV = liveTVSvc
	return liveTVSvc
}

// bloemStartListeners starts Bloem's process-level network presence once the
// public port is serving: today the opt-in LAN advertisement
// (lan.advertisement_enabled), which publishes _bloem._tcp on the public port
// so clients on the same broadcast domain can discover this server. The id
// comes from the same resolver surface the identity endpoint answers from; if
// it cannot be resolved — or the host has no multicast-capable interface —
// the advertiser stays quiet rather than announcing something wrong. The
// returned advertiser (nil when not started) must be stopped first at
// shutdown, while its responder loop can still deliver the goodbye packets.
func bloemStartListeners(ctx context.Context, cfg *config.Config, publicServing bool, pool *pgxpool.Pool, brandingSvc *branding.Service) *lanadvert.Advertiser {
	if !cfg.LAN.AdvertisementEnabled || !publicServing {
		return nil
	}
	return lanadvert.Start(ctx, lanadvert.Config{
		Listen: cfg.Server.Listen,
		// The public listener is plain HTTP: TLS terminates at an operator's
		// reverse proxy, whose origin clients reach by manual entry. If this
		// process ever serves TLS itself, this must follow it.
		Scheme: lanadvert.SchemeHTTP,
		// The same row, read the same way, as the v2 listener's and the
		// identity endpoint's ServerIdentity: raw settings, so a SECRET_KEY
		// rotation cannot change who the server is.
		Identity: serveridentity.New(bloemIdentityStore(pool)),
		ServerName: func(ctx context.Context) string {
			if brandingSvc == nil {
				return branding.DefaultServerName
			}
			return brandingSvc.Load(ctx).ServerName
		},
	})
}

// bloemIdentityStore is the raw settings store the deployment identity reads,
// or nil without a database (serveridentity then answers ErrUnavailable).
func bloemIdentityStore(pool *pgxpool.Pool) serveridentity.Store {
	if pool == nil {
		return nil
	}
	return catalog.NewServerSettingsRepo(pool)
}
