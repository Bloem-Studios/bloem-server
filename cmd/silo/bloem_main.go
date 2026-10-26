package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/lanadvert"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/metadata"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/nodeidentity"
	"github.com/Silo-Server/silo-server/internal/nodemetrics"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/internal/serveridentity"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/worker"
)

// Bloem-owned declarations of package main, moved out of main.go so the Silo
// file carries only the call sites. See contracts/seams.txt.

// bloemShutdownSharedWorkerOwnership fences and joins every writer before
// removing this node's shared ownership. If either join fails, leave both rows
// for TTL cleanup: deleting
// first would allow a late reconciliation or heartbeat to recreate them.
func bloemShutdownSharedWorkerOwnership(
	ctx context.Context,
	reconciler *worker.Reconciler,
	heartbeat *worker.HeartbeatWriter,
) {
	var reconcilerLifecycle reconcilerShutdown
	if reconciler != nil {
		reconcilerLifecycle = reconciler
	}
	var heartbeatLifecycle heartbeatShutdown
	if heartbeat != nil {
		heartbeatLifecycle = heartbeat
	}
	workerShutdown := shutdownSharedWorkerOwnership(ctx, reconcilerLifecycle, heartbeatLifecycle)
	if workerShutdown.reconcilerJoinErr != nil {
		slog.ErrorContext(ctx, "reconciler shutdown error; skipping shared ownership cleanup", "error", workerShutdown.reconcilerJoinErr)
	}
	if workerShutdown.heartbeatJoinErr != nil {
		slog.ErrorContext(ctx, "heartbeat shutdown error; skipping shared ownership cleanup", "error", workerShutdown.heartbeatJoinErr)
	}
	if workerShutdown.cleanupErr != nil {
		slog.ErrorContext(ctx, "shared ownership cleanup error", "error", workerShutdown.cleanupErr)
	}
}

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
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, scanner.ErrFileNotFound) {
			return proxy.ErrSourceHidden
		}
		return fmt.Errorf("%w: load media file: %w", proxy.ErrAuthorityUnavailable, err)
	}
	if file == nil {
		return proxy.ErrSourceHidden
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
	// Recheck the current catalog location, not only the signed token path:
	// retained remote-artifact tokens may carry an empty MediaPath.
	if storagesource.IsNativeLocation(file.FilePath) {
		return proxy.ErrSourceHidden
	}
	return nil
}

type adminPeopleBackgroundWorker interface {
	Run(context.Context)
}

func startAdminPeopleBackgroundWorker(ctx context.Context, worker adminPeopleBackgroundWorker) {
	go worker.Run(ctx)
}

// newTenantAwareViewerResolver turns on Bloem tenancy for a Silo viewer
// resolver and resolves each account's tenant for callers without a request
// tenant (background jobs).
func newTenantAwareViewerResolver(pool *pgxpool.Pool, viewer *policy.ViewerResolver) *policy.TenantViewerResolver {
	tenantStore := tenancy.NewStore(pool)
	return policy.NewTenantViewerResolver(
		viewer.WithBloemTenancy(resourcetenancy.NewStore(pool)),
		tenancy.NewSubjectResolver(tenancy.NewResolver(tenantStore), tenantStore),
	)
}

// bloemWireCoreDependencies installs Bloem's API router dependencies on deps:
// admin-people (whose background worker it starts on ctx), administrative
// context tokens and the platform admin authorizer
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
		bootstrapper := tenancyOwnershipBootstrapper{store: tenancy.NewStore(pool)}
		deps.OwnershipBootstrapper = bootstrapper
		deps.MembershipProvisioner = bootstrapper
	}
}

// runBloemCommand runs a Bloem maintenance subcommand named by args[1] and
// reports whether it did; main returns when it has.
func runBloemCommand(args []string) bool {
	if len(args) > 1 && args[1] == "silo-switching" {
		if err := runSiloSwitchingCommand(context.Background(), args[2:]); err != nil {
			log.Fatalf("silo-switching: %v", err)
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

// bloemWireServices builds the process-level services Bloem adds and installs
// them on deps: the host stats sampler, S-3 seasonal ambience, S-2
// promotions, and the shared Live TV service. It returns the Live TV service
// (nil without a database) so main can register its tasks and close it at shutdown.
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

	// Live TV is a single shared service used by the native API, guide sync,
	// DVR processing, and HLS playback.
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

// storageCovers is the artwork routes' reader for storage covers, or nil when
// no storage runtime is configured. A nil host must not become a non-nil
// interface.
func storageCovers(deps api.Dependencies) apiv2.StorageCoverService {
	if deps.NativeStorage == nil {
		return nil
	}
	return deps.NativeStorage
}

// storageCoverCacheTTL bounds how long a cover outlives its last write. The
// cache's own memory limit and LRU policy decide what stays sooner.
const storageCoverCacheTTL = 30 * 24 * time.Hour

// configureStorageCoverCache shares storage covers between requests and nodes
// through a dedicated Redis named by BLOEM_STORAGE_COVER_CACHE_URL. That Redis
// must have a memory limit and an LRU eviction policy; the server's main Redis
// must not be used. Without it covers are read from their source each time. An
// unreachable cache is logged and skipped: covers still work without it.
func configureStorageCoverCache(ctx context.Context, host *nativestorage.Host, url string) func() {
	if url == "" {
		return nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		slog.WarnContext(ctx, "storage cover cache disabled: invalid BLOEM_STORAGE_COVER_CACHE_URL", "component", "app", "error", err)
		return nil
	}
	// A cache that is down must cost a cover request milliseconds, not the
	// client's default multi-second dial and retries.
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 250*time.Millisecond, 500*time.Millisecond, 500*time.Millisecond
	opts.MaxRetries = -1
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		slog.WarnContext(ctx, "storage cover cache unreachable at startup; reads go to the source until it answers", "component", "app", "error", err)
	}
	host.SetCoverCache(nativestorage.NewRedisCoverCache(client, storageCoverCacheTTL))
	slog.InfoContext(ctx, "storage cover cache enabled", "component", "app")
	return func() { _ = client.Close() }
}

// bloemImageResolverSources are the image resolver sources Bloem registers
// beside the plugins' on every reload: storage covers, whose URLs are signed
// for the server artwork route.
var bloemImageResolverSources []metadata.PluginImageResolverSourceRegistration

func bloemImageSources(deps api.Dependencies) []metadata.PluginImageResolverSourceRegistration {
	if deps.Blobs.Assets == nil || deps.ArtworkSigner == nil {
		return nil
	}
	source := metadata.NewStorageCoverSource(deps.NativeStorage)
	return []metadata.PluginImageResolverSourceRegistration{metadata.StorageCoverRegistration(source)}
}
