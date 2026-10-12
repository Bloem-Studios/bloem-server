package api

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// nativeStorageActorPolicy uses the same credential, viewer and resource owners
// as the router. It deliberately has no video resolver or playback admission.
func nativeStorageActorPolicy(deps Dependencies) nativeVideoPolicy {
	var p nativeVideoPolicy
	if deps.DB == nil || deps.UserStoreProvider == nil || deps.CurrentConfig() == nil {
		return p
	}
	users := auth.NewUserRepository(deps.DB)
	logins := auth.NewSessionRepository(deps.DB)
	keys := auth.NewAPIKeyRepository(deps.DB)
	cfg := deps.CurrentConfig()
	p.credentials = middleware.NewAuthMiddleware(auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour), logins, keys, users)
	groups := access.NewTenantGroupStore(deps.DB)
	tokens := access.NewProfileTokenService(cfg.Auth.JWTSecret, 0)
	unrated := config.NewUnratedContentPolicy(catalog.NewEncryptedSettingsRepo(catalog.NewServerSettingsRepo(deps.DB), deps.SecretCipher))
	var viewer middleware.ViewerResolver
	if deps.PolicySystem != nil {
		viewer = policy.NewViewerResolver(users, deps.UserStoreProvider, tokens, deps.PolicySystem.PDP(), groups).WithUnratedContentPolicy(unrated).WithBloemTenancy(resourcetenancy.NewStore(deps.DB))
	} else {
		viewer = access.NewResolver(users, deps.UserStoreProvider, tokens, groups).WithUnratedContentPolicy(unrated)
	}
	p.viewer = middleware.NewViewerAccessMiddleware(bloemOrgRevocationAwareViewer(viewer, deps.DB))
	store := tenancy.NewStore(deps.DB)
	resolver := tenancy.NewResolver(store)
	resources := resourcetenancy.NewStore(deps.DB)
	p.tenants = tenancy.NewSubjectResolver(resolver, store)
	p.authorizeSource = func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		return authorizeNativeStorageSource(ctx, file, source, resolver, resources)
	}
	populateNativeVideoStores(deps, &p)
	return p
}

// ConfigureNativeStorageArtwork is called once after the policy/user stores
// exist and before any listener serves requests. The host is shared by all
// artwork listeners and by the metadata storage-cover source.
func ConfigureNativeStorageArtwork(deps *Dependencies) {
	if deps == nil || deps.NativeStorage == nil || deps.DB == nil {
		return
	}
	s := &nativeStorageArtwork{next: deps.ArtworkResolver, signer: deps.ArtworkSigner, cipher: deps.SecretCipher, actors: nativeStorageActorPolicy(*deps), references: storagesource.NewRepository(deps.DB), registry: deps.NativeStorage.Registry}
	s.authorizeFile = nativeStorageCoverFileAuthority(*deps)
	deps.ArtworkResolver = s
	deps.NativeStorage.SetCoverRequestAuthorizer(s.authorizeRequest)
	deps.NativeStorage.SetCoverURLResolver(s)
}

// Native audiobook covers require the complete recording on every recipe check,
// including cache hits and conditional responses.
func nativeStorageCoverFileAuthority(deps Dependencies) func(context.Context, storagesource.CoverReference) error {
	authorizer := &handlers.MediaFileAuthorizer{FileResolver: deps.FileRepo, ItemAccess: catalog.NewItemRepository(deps.DB), EpisodeLookup: catalog.NewEpisodeRepository(deps.DB), ExtraLookup: catalog.NewExtraRepository(deps.DB)}
	return func(ctx context.Context, ref storagesource.CoverReference) error {
		file, err := authorizer.AuthorizeContext(ctx, ref.MediaFileID, handlers.AccessFilterFromContext(ctx, ""))
		if err != nil {
			return err
		}
		if file == nil || file.ID != ref.MediaFileID || file.ContentID != ref.ContentID || file.MediaFolderID != ref.FolderID || file.FilePath != ref.FilePath {
			return storagesource.ErrReferenceConflict
		}
		if handlers.NativeAudioSupported(file) {
			return nativeAudioPresentation(deps)(ctx, file)
		}
		return nil
	}
}
