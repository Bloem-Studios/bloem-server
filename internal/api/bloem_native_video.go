package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type nativeVideoRequestKey struct{}

func nativeVideoSessionDenied(client *redis.Client, deny *playback.StreamDeny) func(context.Context, string) bool {
	if client == nil {
		return deny.Denied
	}
	return func(ctx context.Context, sessionID string) bool {
		if sessionID == "" {
			return true
		}
		check, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		count, err := client.Exists(check, playback.StreamDenyKey(sessionID)).Result()
		return err != nil || count != 0
	}
}

// This value is request memory only. Never put it in a session, grant, binding,
// log, or durable descriptor. V2 legacy projections preserve the context while
// replacing headers and path; authorization must use the original credential.
type nativeVideoRequest struct{ authorization, profileToken, method, path, streamToken string }

func captureNativeVideoRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(nativeVideoRequestKey{}).(nativeVideoRequest); ok {
			next.ServeHTTP(w, r)
			return
		}
		input := nativeVideoRequest{authorization: r.Header.Get("Authorization"), profileToken: r.Header.Get("X-Profile-Token"), method: r.Method, path: r.URL.Path, streamToken: r.URL.Query().Get(streamtoken.QueryParam)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nativeVideoRequestKey{}, input)))
	})
}

type nativeVideoCredentials interface {
	RevalidateCurrent(context.Context, string, *auth.Claims, string, string) error
}
type nativeVideoViewer interface {
	ResolveCurrent(context.Context, access.ResolveInput) (access.Scope, error)
}
type nativeVideoTenants interface {
	ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error)
}
type nativeVideoPolicy struct {
	loadBinding        func(context.Context, string, int) (handlers.NativeVideoBinding, error)
	allowRecovery      bool
	loadUser           func(context.Context, int) (*models.User, error)
	loadLogin          func(context.Context, string) (*models.AuthSession, error)
	loginActive        func(context.Context, string) (string, bool, error)
	loadProfile        func(context.Context, int, string) (*userstore.Profile, error)
	loadAPIKeyMetadata func(context.Context, int64) (*models.APIKeyMetadata, error)
	credentials        nativeVideoCredentials
	viewer             nativeVideoViewer
	tenants            nativeVideoTenants
	secret             func() string
	getSession         func(string) (*playback.Session, error)
	denied             func(context.Context, string) bool
	resolveFile        func(context.Context, int) (*models.MediaFile, error)
	authorizeSource    func(context.Context, *models.MediaFile, storagesource.SourceConfig) error
}

type nativeVideoSubject struct {
	delegated *handlers.NativeVideoAuthority
	binding   *handlers.NativeVideoBinding
	policy    nativeVideoPolicy
	request   nativeVideoRequest
	claims    *auth.Claims
	stream    *streamtoken.Claims
	userID    int
	profileID string
	fileID    int
	skipPIN   bool
	tenant    *tenancy.Context
}

func nativeVideoPlayback(deps Dependencies, coordinator *nativestorage.Coordinator, h *handlers.PlaybackHandler, sh *handlers.StreamHandler, authMiddleware *middleware.AuthMiddleware, viewer *middleware.ViewerAccessMiddleware) {
	if deps.DB == nil || deps.FileRepo == nil || deps.SessionMgr == nil || coordinator == nil || h == nil || sh == nil || authMiddleware == nil || viewer == nil {
		return
	}
	tenantStore := tenancy.NewStore(deps.DB)
	tenantResolver := tenancy.NewResolver(tenantStore)
	resources := resourcetenancy.NewStore(deps.DB)
	p := nativeVideoPolicy{credentials: authMiddleware, viewer: viewer, tenants: tenancy.NewSubjectResolver(tenantResolver, tenantStore), secret: func() string {
		cfg := deps.CurrentConfig()
		if cfg == nil {
			return ""
		}
		return cfg.Auth.JWTSecret
	}, getSession: deps.SessionMgr.GetSession, denied: nativeVideoSessionDenied(deps.RedisClient, h.StreamDeny), resolveFile: h.ResolveNativeVideoFile, authorizeSource: func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		return authorizeNativeStorageSource(ctx, file, source, tenantResolver, resources)
	}}
	service := &handlers.NativeVideoService{Presentation: nativeAudioPresentation(deps), Files: deps.FileRepo, Bindings: handlers.NewNativeVideoBindingStore(deps.RedisClient), WorkerGrants: handlers.NewNativeVideoWorkerGrantStore(deps.RedisClient), Sessions: deps.SessionMgr, Deny: h.StreamDeny}
	service.Open = func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
		if file == nil {
			return nil, catalog.ErrItemNotFound
		}
		authority, err := nativeVideoAuthority(ctx, file.ID, p)
		if err != nil {
			return nil, err
		}
		opened, err := coordinator.OpenAuthorizedPlayback(ctx, file, authority)
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
	configureNativeVideoDelegates(deps, coordinator, service, p)
	h.NativeVideo = service
	sh.NativeVideo = service
}

func nativeVideoCapture(ctx context.Context, p nativeVideoPolicy) (*nativeVideoSubject, error) {

	if ctx == nil || p.credentials == nil || p.viewer == nil || p.tenants == nil || p.resolveFile == nil || p.authorizeSource == nil {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	request, ok := ctx.Value(nativeVideoRequestKey{}).(nativeVideoRequest)
	scope, hasScope := access.GetScope(ctx)
	if !ok || request.method == "" || request.path == "" || !hasScope || scope.UserID <= 0 || !scope.PlaybackAllowed {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	subject := &nativeVideoSubject{policy: p, request: request, userID: scope.UserID, profileID: scope.ProfileID, fileID: 0}
	if profile := middleware.GetProfileID(ctx); profile != "" && profile != scope.ProfileID {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	if middleware.IsStreamTokenAuthorized(ctx) {
		claims, ok := middleware.StreamTokenClaims(ctx)
		if !ok || claims.UserID != scope.UserID || claims.ProfileID != scope.ProfileID || claims.MediaFileID <= 0 || claims.SessionID == "" || request.streamToken == "" || p.secret == nil || p.getSession == nil || p.denied == nil {
			return nil, middleware.ErrCurrentCredentialInvalid
		}
		frozen := *claims
		subject.stream = &frozen
		subject.fileID = claims.MediaFileID
		subject.skipPIN = true
	} else {
		claims := middleware.GetClaims(ctx)
		if claims == nil || claims.UserID != scope.UserID || request.authorization == "" || claims.ProfileID != "" && claims.ProfileID != scope.ProfileID {
			return nil, middleware.ErrCurrentCredentialInvalid
		}
		frozen := *claims
		frozen.APIKeyScopes = append([]string(nil), claims.APIKeyScopes...)
		subject.claims = &frozen
		subject.skipPIN = claims.TokenType == auth.TokenTypeAPIKey || claims.TokenType == auth.TokenTypeApplePushDisplay || claims.TokenType == auth.TokenTypeDirectDownloadLink || claims.AuthMethod == auth.AuthMethodDirectProfile
	}
	capturedTenant, hasTenant := tenancy.FromContext(ctx)
	if hasTenant {
		if capturedTenant.AccountID != scope.UserID || capturedTenant.OrganizationID == uuid.Nil || capturedTenant.MembershipID == uuid.Nil {
			return nil, middleware.ErrCurrentCredentialInvalid
		}
		subject.tenant = &capturedTenant
	} else if subject.stream == nil {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	freshCtx, err := subject.refresh(ctx)
	if err != nil {
		return nil, err
	}
	initialTenant, _ := tenancy.FromContext(freshCtx)
	subject.tenant = &initialTenant
	return subject, nil
}

func nativeVideoAuthority(ctx context.Context, fileID int, p nativeVideoPolicy) (nativestorage.PlaybackAuthority, error) {
	if fileID <= 0 {
		return nativestorage.PlaybackAuthority{}, middleware.ErrCurrentCredentialInvalid
	}
	subject, err := nativeVideoCapture(ctx, p)
	if err != nil {
		return nativestorage.PlaybackAuthority{}, err
	}
	if subject.stream != nil && subject.fileID != fileID {
		return nativestorage.PlaybackAuthority{}, middleware.ErrCurrentCredentialInvalid
	}
	subject.fileID = fileID
	return nativeVideoCallbacks(subject), nil
}
func nativeVideoCallbacks(subject *nativeVideoSubject) nativestorage.PlaybackAuthority {
	fileID := subject.fileID
	p := subject.policy
	return nativestorage.PlaybackAuthority{
		AuthorizeFile: func(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
			if file == nil || file.ID != fileID {
				return nil, catalog.ErrItemNotFound
			}
			fresh, err := subject.refresh(ctx)
			if err != nil {
				return nil, err
			}
			return subject.resolve(fresh)
		},
		AuthorizeSource: func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
			if file == nil || file.ID != fileID {
				return catalog.ErrItemNotFound
			}
			fresh, err := subject.refresh(ctx)
			if err != nil {
				return err
			}
			actual, err := subject.resolve(fresh)
			if err != nil {
				return err
			}
			if actual.ContentID != file.ContentID || actual.EpisodeID != file.EpisodeID || actual.ExtraID != file.ExtraID || actual.MediaFolderID != file.MediaFolderID || actual.FilePath != file.FilePath {
				return storagesource.ErrReferenceConflict
			}
			return p.authorizeSource(fresh, actual, source)
		},
	}
}

func (s *nativeVideoSubject) resolve(ctx context.Context) (*models.MediaFile, error) {
	actual, err := s.policy.resolveFile(ctx, s.fileID)
	if err != nil {
		return nil, err
	}
	// The owning handler also enforces item/library and supported-container
	// policy. Missing video metadata cannot accidentally admit ebooks or audio.
	if actual == nil || actual.ID != s.fileID || !handlers.NativeVideoSupported(actual) || (!handlers.NativeAudioSupported(actual) && actual.CodecVideo == "" && len(actual.VideoTracks) == 0) {
		return nil, catalog.ErrItemNotFound
	}
	return actual, nil
}

func (s *nativeVideoSubject) refresh(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := access.ResolveInput{UserID: s.userID, ProfileID: s.profileID, SkipPINVerification: s.skipPIN}
	if s.delegated != nil {
		if err := s.verifyDelegated(ctx); err != nil {
			return nil, err
		}
		input.SessionID = s.delegated.LoginSessionID
		ctx = middleware.SetClaims(ctx, &auth.Claims{UserID: s.userID, ProfileID: s.profileID})
	} else if s.stream != nil {
		if err := s.verifyStream(ctx); err != nil {
			return nil, err
		}
		// Catalog filters require the actor ID in claims as well as viewer scope.
		// These internal lookup claims confer no admin or bearer authority.
		ctx = middleware.SetClaims(ctx, &auth.Claims{UserID: s.userID, ProfileID: s.profileID})
	} else {
		claims := *s.claims
		if err := s.policy.credentials.RevalidateCurrent(ctx, s.request.authorization, &claims, s.request.method, s.request.path); err != nil {
			return nil, err
		}
		input.SessionID = claims.SessionID
		input.ProfileToken = s.request.profileToken
		ctx = middleware.SetClaims(ctx, &claims)
	}
	tenant, err := s.policy.tenants.ResolveSubjectTenant(ctx, s.userID, s.profileID)
	if err != nil {
		return nil, err
	}
	if tenant.AccountID != s.userID || tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil {
		return nil, middleware.ErrCurrentCredentialInvalid
	}
	if s.tenant != nil {
		// Legacy is the original request's projection mode. ResolveProfile uses
		// default-org compatibility mode; preserve the captured mode, then compare
		// all current authorization evidence. Source policy re-resolves that mode.
		tenant.Legacy = s.tenant.Legacy
		if tenant != *s.tenant {
			return nil, middleware.ErrCurrentCredentialInvalid
		}
	}
	ctx = tenancy.WithContext(ctx, tenant)
	scope, err := s.policy.viewer.ResolveCurrent(ctx, input)
	if err != nil {
		return nil, err
	}
	if scope.UserID != s.userID || scope.ProfileID != s.profileID || !scope.PlaybackAllowed {
		return nil, middleware.ErrCurrentCredentialForbidden
	}
	ctx = access.SetScope(ctx, scope)
	ctx = middleware.SetProfileID(ctx, s.profileID)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (s *nativeVideoSubject) verifyStream(ctx context.Context) error {
	if s.request.method != http.MethodGet && s.request.method != http.MethodHead {
		return middleware.ErrCurrentCredentialForbidden
	}
	secret := s.policy.secret()
	if secret == "" {
		return middleware.ErrCurrentCredentialInvalid
	}
	current, err := streamtoken.Verify(s.request.streamToken, secret)
	if err != nil || current == nil || current.ExpiresAt == nil || current.SessionID != s.stream.SessionID || current.UserID != s.userID || current.ProfileID != s.profileID || current.MediaFileID != s.fileID {
		return middleware.ErrCurrentCredentialInvalid
	}
	// Tokens are valid only for their captured delivery path. Do not widen the
	// authority to a nested endpoint or a mutation merely because it has a sid.
	if !nativeVideoTokenPath(s.request.path, current.SessionID) {
		return middleware.ErrCurrentCredentialForbidden
	}
	if s.request.method != http.MethodGet && strings.HasSuffix(s.request.path, "/fonts") {
		return middleware.ErrCurrentCredentialForbidden
	}
	if s.policy.denied(ctx, current.SessionID) {
		return middleware.ErrCurrentCredentialForbidden
	}
	session, err := s.policy.getSession(current.SessionID)
	if err != nil {
		return err
	}
	if session == nil || session.ID != current.SessionID || session.UserID != s.userID || session.ProfileID != s.profileID || session.MediaFileID != s.fileID {
		return middleware.ErrCurrentCredentialForbidden
	}
	return nil
}
func nativeVideoTokenPath(path, sessionID string) bool {
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		if path == prefix+"/stream/"+sessionID {
			return true
		}
		subtitles := prefix + "/stream/" + sessionID + "/subtitles/"
		if strings.HasPrefix(path, subtitles) {
			track, suffix, nested := strings.Cut(strings.TrimPrefix(path, subtitles), "/")
			if nested && suffix != "fonts" {
				return false
			}
			_, format, err := playback.ParseSubtitleTrackParam(track)
			if err != nil {
				return false
			}
			switch format {
			case "", "vtt", "ass", "ssa", "srt", "sup":
				return true
			default:
				return false
			}
		}
		root := prefix + "/playback/transcode/" + sessionID + "/"
		if strings.HasPrefix(path, root) {
			tail := strings.TrimPrefix(path, root)
			if tail == "master.m3u8" || tail == "media.m3u8" {
				return true
			}
			if strings.HasPrefix(tail, "segment/") {
				name := strings.TrimPrefix(tail, "segment/")
				return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
			}
		}
	}
	return false
}

// populateNativeVideoStores installs metadata-only credential lookups. In
// particular, delegated API-key checks never load the full key value.
func populateNativeVideoStores(deps Dependencies, p *nativeVideoPolicy) {
	users := auth.NewUserRepository(deps.DB)
	logins := auth.NewSessionRepository(deps.DB)
	keys := auth.NewAPIKeyRepository(deps.DB)
	p.loadUser = users.GetByID
	p.loadLogin = logins.GetByID
	p.loginActive = logins.ActiveSessionRole
	p.loadAPIKeyMetadata = keys.GetMetadataByID
	if deps.UserStoreProvider != nil {
		p.loadProfile = func(ctx context.Context, uid int, pid string) (*userstore.Profile, error) {
			store, err := deps.UserStoreProvider.ForUser(ctx, uid)
			if err != nil {
				return nil, err
			}
			return store.GetProfile(ctx, pid)
		}
	}
	p.allowRecovery = deps.RedisClient != nil
}
func configureNativeVideoDelegates(deps Dependencies, coordinator *nativestorage.Coordinator, service *handlers.NativeVideoService, p nativeVideoPolicy) {
	populateNativeVideoStores(deps, &p)
	p.loadBinding = service.Bindings.Get
	configureNativeVideoBoundAuthority(service, p)
	service.Delegation = func(ctx context.Context) (handlers.NativeVideoAuthority, error) { return nativeVideoDelegation(ctx, p) }
	service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, binding handlers.NativeVideoBinding) (handlers.NativeVideoFile, error) {
		authority, err := nativeVideoDelegatedAuthority(ctx, file, binding, p)
		if err != nil {
			return nil, err
		}
		opened, err := coordinator.OpenAuthorizedPlayback(ctx, file, authority)
		if err != nil {
			return nil, err
		}
		if opened.Identity() != binding.Identity {
			opened.Close()
			return nil, storagesource.ErrReferenceConflict
		}
		return opened, nil
	}
}

func configureNativeVideoBoundAuthority(service *handlers.NativeVideoService, p nativeVideoPolicy) {
	service.AuthorizeBound = func(ctx context.Context, binding handlers.NativeVideoBinding) error {
		identity := binding.Identity
		file := &models.MediaFile{ID: identity.FileID, ContentID: identity.ContentID, MediaFolderID: identity.MediaFolderID, FilePath: identity.FilePath}
		authority, err := nativeVideoDelegatedAuthority(ctx, file, binding, p)
		if err != nil {
			return err
		}
		current, err := authority.AuthorizeFile(ctx, file)
		if err != nil {
			return err
		}
		if current.ID != identity.FileID || current.ContentID != identity.ContentID || current.MediaFolderID != identity.MediaFolderID || current.FilePath != identity.FilePath {
			return storagesource.ErrReferenceConflict
		}
		return nil
	}
}

// NewNativeVideoDelegateService composes worker reads from server-owned durable
// bindings. It never accepts a captured HTTP credential, raw path or worker
// body as authority and does not create sessions or grant transcode admission.
func NewNativeVideoDelegateService(deps Dependencies, coordinator *nativestorage.Coordinator) (*handlers.NativeVideoService, error) {
	if deps.DB == nil || deps.FileRepo == nil || deps.UserStoreProvider == nil || coordinator == nil || coordinator.AcquireOpen == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	tenants := tenancy.NewStore(deps.DB)
	tenantResolver := tenancy.NewResolver(tenants)
	resources := resourcetenancy.NewStore(deps.DB)
	users := auth.NewUserRepository(deps.DB)
	groups := access.NewTenantGroupStore(deps.DB)
	unrated := config.NewUnratedContentPolicy(catalog.NewServerSettingsRepo(deps.DB))
	var resolver middleware.ViewerResolver
	if deps.PolicySystem != nil {
		resolver = policy.NewViewerResolver(users, deps.UserStoreProvider, nil, deps.PolicySystem.PDP(), groups).WithUnratedContentPolicy(unrated).WithAdminDefaults(deps.AdminDefaults).WithBloemTenancy(resources)
	} else {
		resolver = access.NewResolver(users, deps.UserStoreProvider, nil, groups).WithUnratedContentPolicy(unrated)
	}
	viewer := middleware.NewViewerAccessMiddleware(bloemOrgRevocationAwareViewer(resolver, deps.DB))
	h := handlers.NewPlaybackHandler(deps.SessionMgr, deps.FileRepo)
	h.ItemAccess = catalog.NewItemRepository(deps.DB)
	h.EpisodeLookup = catalog.NewEpisodeRepository(deps.DB)
	h.ExtraLookup = catalog.NewExtraRepository(deps.DB)
	deny := playback.NewStreamDeny(deps.RedisClient)
	getSession := func(string) (*playback.Session, error) { return nil, playback.ErrSessionNotFound }
	if deps.SessionMgr != nil {
		getSession = deps.SessionMgr.GetSession
	}
	p := nativeVideoPolicy{viewer: viewer, tenants: tenancy.NewSubjectResolver(tenantResolver, tenants), getSession: getSession, denied: nativeVideoSessionDenied(deps.RedisClient, deny), resolveFile: h.ResolveNativeVideoFile, authorizeSource: func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		return authorizeNativeStorageSource(ctx, file, source, tenantResolver, resources)
	}}
	service := &handlers.NativeVideoService{Presentation: nativeAudioPresentation(deps), Files: deps.FileRepo, Bindings: handlers.NewNativeVideoBindingStore(deps.RedisClient), WorkerGrants: handlers.NewNativeVideoWorkerGrantStore(deps.RedisClient), Sessions: deps.SessionMgr, Deny: deny}
	h.NativeVideo = service
	configureNativeVideoDelegates(deps, coordinator, service, p)
	return service, nil
}

func nativeVideoDelegation(ctx context.Context, p nativeVideoPolicy) (handlers.NativeVideoAuthority, error) {
	empty := handlers.NativeVideoAuthority{}
	subject, err := nativeVideoCapture(ctx, p)
	if err != nil {
		return empty, err
	}
	d := handlers.NativeVideoAuthority{UserID: subject.userID, ProfileID: subject.profileID, PINVerificationSkipped: subject.skipPIN, Tenant: *subject.tenant, OriginalMethod: subject.request.method, OriginalPath: subject.request.path}
	switch {
	case subject.stream != nil:
		if subject.stream.ExpiresAt == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		d.TokenType = "stream"
		d.PlaybackSessionID = subject.stream.SessionID
		d.ExpiresAt = subject.stream.ExpiresAt.UTC()
	case subject.claims.TokenType == auth.TokenTypeAccess:
		if subject.claims.ExpiresAt == nil || p.loadLogin == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		d.TokenType = auth.TokenTypeAccess
		d.LoginSessionID = subject.claims.SessionID
		d.AuthMethod = subject.claims.AuthMethod
		d.ExpiresAt = subject.claims.ExpiresAt.UTC()
		login, err := p.loadLogin(ctx, d.LoginSessionID)
		if err != nil {
			return empty, err
		}
		if login == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		if login.Purpose == models.SessionPurposeRemotePlayback && login.AbsoluteExpiresAt != nil && login.AbsoluteExpiresAt.Before(d.ExpiresAt) {
			d.ExpiresAt = login.AbsoluteExpiresAt.UTC()
		}
		if login.ExpiresAt.Before(d.ExpiresAt) {
			d.ExpiresAt = login.ExpiresAt.UTC()
		}
	case subject.claims.TokenType == auth.TokenTypeAPIKey:
		if p.loadAPIKeyMetadata == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		d.TokenType = auth.TokenTypeAPIKey
		d.APIKeyID = subject.claims.APIKeyID
		d.ExpiresAt = time.Now().UTC().Add(playback.MaxTokenTTL)
		key, err := p.loadAPIKeyMetadata(ctx, d.APIKeyID)
		if err != nil {
			return empty, err
		}
		if key == nil || key.ID != d.APIKeyID || key.UserID != d.UserID || key.Revision <= 0 || len(key.Scopes) != 0 {
			return empty, middleware.ErrCurrentCredentialForbidden
		}
		d.APIKeyRevision = key.Revision
	default:
		return empty, middleware.ErrCurrentCredentialForbidden
	}
	if d.ProfileID != "" {
		if p.loadProfile == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		profile, err := p.loadProfile(ctx, d.UserID, d.ProfileID)
		if err != nil {
			return empty, err
		}
		if profile == nil || profile.ID != d.ProfileID || profile.OrganizationID != d.Tenant.OrganizationID.String() {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		d.ProfilePINRevision = profile.PINRevision
		d.ProfileCredentialRevision = profile.CredentialRevision
	}
	// Repeat exact local credential/PIN validation after loading metadata. A PIN
	// rotated while sealing cannot turn the previous proof into a new attestation.
	if _, err = subject.refresh(ctx); err != nil {
		return empty, err
	}
	if err = verifyNativeVideoActor(ctx, d, p); err != nil {
		return empty, err
	}
	return d, nil
}

func nativeVideoDelegatedAuthority(ctx context.Context, file *models.MediaFile, binding handlers.NativeVideoBinding, p nativeVideoPolicy) (nativestorage.PlaybackAuthority, error) {
	empty := nativestorage.PlaybackAuthority{}
	d := binding.Authority
	if file == nil || binding.SessionID == "" || binding.UserID != d.UserID || binding.ProfileID != d.ProfileID || binding.Identity.FileID != file.ID || binding.Identity.ContentID != file.ContentID || binding.Identity.MediaFolderID != file.MediaFolderID || binding.Identity.FilePath != file.FilePath || p.loadBinding == nil || p.viewer == nil || p.tenants == nil || p.resolveFile == nil || p.authorizeSource == nil || p.getSession == nil || p.denied == nil {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	if d.PlaybackSessionID != "" && d.PlaybackSessionID != binding.SessionID {
		return empty, middleware.ErrCurrentCredentialForbidden
	}
	frozen := binding
	subject := &nativeVideoSubject{policy: p, delegated: &d, binding: &frozen, userID: d.UserID, profileID: d.ProfileID, fileID: file.ID, skipPIN: true, tenant: &d.Tenant}
	if _, err := subject.refresh(ctx); err != nil {
		return empty, err
	}
	return nativeVideoCallbacks(subject), nil
}
func (s *nativeVideoSubject) verifyDelegated(ctx context.Context) error {
	live, err := s.policy.loadBinding(ctx, s.binding.SessionID, s.fileID)
	if err != nil {
		return err
	}
	if !sameNativeVideoBinding(live, *s.binding) {
		return storagesource.ErrReferenceConflict
	}
	if s.policy.denied(ctx, s.binding.SessionID) {
		return middleware.ErrCurrentCredentialForbidden
	}
	session, err := s.policy.getSession(s.binding.SessionID)
	if err != nil {
		// A read can restore its frozen source on a new worker only from a matching
		// Redis binding and current host policy. It never reconstructs a session.
		if !s.policy.allowRecovery || !errors.Is(err, playback.ErrSessionNotFound) {
			return err
		}
	} else if session == nil || session.ID != s.binding.SessionID || session.UserID != s.userID || session.ProfileID != s.profileID || session.MediaFileID != s.fileID {
		return middleware.ErrCurrentCredentialForbidden
	}
	return verifyNativeVideoActor(ctx, *s.delegated, s.policy)
}
func sameNativeVideoBinding(a, b handlers.NativeVideoBinding) bool {
	da, db := a.Authority, b.Authority
	if !da.ExpiresAt.Equal(db.ExpiresAt) {
		return false
	}
	da.ExpiresAt = time.Time{}
	db.ExpiresAt = time.Time{}
	return a.SessionID == b.SessionID && a.UserID == b.UserID && a.ProfileID == b.ProfileID && a.Identity == b.Identity && a.Format == b.Format && a.Info.Name == b.Info.Name && a.Info.LogicalPath == b.Info.LogicalPath && a.Info.Revision == b.Info.Revision && a.Info.Size == b.Info.Size && a.Info.ModifiedAt.Equal(b.Info.ModifiedAt) && da == db
}
func verifyNativeVideoActor(ctx context.Context, d handlers.NativeVideoAuthority, p nativeVideoPolicy) error {
	if d.UserID <= 0 || d.Tenant.AccountID != d.UserID || d.Tenant.OrganizationID == uuid.Nil || d.Tenant.MembershipID == uuid.Nil || d.ExpiresAt.IsZero() || !time.Now().Before(d.ExpiresAt) || p.loadUser == nil {
		return middleware.ErrCurrentCredentialInvalid
	}
	user, err := p.loadUser(ctx, d.UserID)
	if err != nil {
		return err
	}
	if user == nil || user.ID != d.UserID || !user.Enabled {
		return middleware.ErrCurrentCredentialInvalid
	}
	var profile *userstore.Profile
	if d.ProfileID != "" {
		if p.loadProfile == nil {
			return middleware.ErrCurrentCredentialInvalid
		}
		profile, err = p.loadProfile(ctx, d.UserID, d.ProfileID)
		if err != nil {
			return err
		}
		if profile == nil || profile.ID != d.ProfileID || profile.OrganizationID != d.Tenant.OrganizationID.String() || profile.PINRevision != d.ProfilePINRevision || profile.CredentialRevision != d.ProfileCredentialRevision {
			return middleware.ErrCurrentCredentialInvalid
		}
	}
	switch d.TokenType {
	case auth.TokenTypeAccess:
		if d.LoginSessionID == "" || p.loadLogin == nil || p.loginActive == nil {
			return middleware.ErrCurrentCredentialInvalid
		}
		login, err := p.loadLogin(ctx, d.LoginSessionID)
		if err != nil {
			return err
		}
		if login == nil || login.ID != d.LoginSessionID || login.UserID != d.UserID || login.RevokedAt != nil || !time.Now().Before(login.ExpiresAt) || d.ExpiresAt.After(login.ExpiresAt) {
			return middleware.ErrCurrentCredentialInvalid
		}
		if login.Purpose == models.SessionPurposeRemotePlayback &&
			(login.BoundProfileID != d.ProfileID || login.AbsoluteExpiresAt != nil &&
				(!time.Now().Before(*login.AbsoluteExpiresAt) || d.ExpiresAt.After(*login.AbsoluteExpiresAt))) {
			return middleware.ErrCurrentCredentialInvalid
		}
		_, active, err := p.loginActive(ctx, d.LoginSessionID)
		if err != nil {
			return err
		}
		if !active {
			return middleware.ErrCurrentCredentialInvalid
		}
		if login.ProfileID != nil || d.AuthMethod == auth.AuthMethodDirectProfile {
			if d.AuthMethod != auth.AuthMethodDirectProfile || login.AuthMethod != auth.AuthMethodDirectProfile || login.ProfileID == nil || *login.ProfileID != d.ProfileID || login.ProfileCredentialRevision == nil || profile == nil || *login.ProfileCredentialRevision != profile.CredentialRevision {
				return middleware.ErrCurrentCredentialInvalid
			}
		}
	case auth.TokenTypeAPIKey:
		if d.APIKeyID <= 0 || d.APIKeyRevision <= 0 || p.loadAPIKeyMetadata == nil {
			return middleware.ErrCurrentCredentialInvalid
		}
		key, err := p.loadAPIKeyMetadata(ctx, d.APIKeyID)
		if err != nil {
			return err
		}
		// No playback route is admitted by today's scoped-key allowlist. Preserve
		// that restriction without loading its secret or duplicating route policy.
		if key == nil || key.ID != d.APIKeyID || key.UserID != d.UserID || key.Revision != d.APIKeyRevision || len(key.Scopes) != 0 {
			return middleware.ErrCurrentCredentialForbidden
		}
	case "stream":
		if d.PlaybackSessionID == "" {
			return middleware.ErrCurrentCredentialInvalid
		}
	default:
		return middleware.ErrCurrentCredentialForbidden
	}
	return nil
}
