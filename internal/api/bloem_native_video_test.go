package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type nativeVideoSubjectFixture struct {
	tenant tenancy.Context
	err    error
}

func (s *nativeVideoSubjectFixture) ResolveSubjectTenant(_ context.Context, uid int, pid string) (tenancy.Context, error) {
	if uid != 7 || pid != "profile" {
		return tenancy.Context{}, errors.New("wrong subject")
	}
	return s.tenant, s.err
}

type nativeVideoViewerFixture struct {
	scope  access.Scope
	err    error
	last   access.ResolveInput
	tenant tenancy.Context
}

func (v *nativeVideoViewerFixture) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	v.last = input
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant != v.tenant {
		return access.Scope{}, errors.New("stale viewer tenant")
	}
	return v.scope, v.err
}

type nativeVideoLoginFixture struct{ enabled, active bool }

func (l *nativeVideoLoginFixture) ActiveSessionRole(context.Context, string) (string, bool, error) {
	return "user", l.active, nil
}
func (l *nativeVideoLoginFixture) GetByID(_ context.Context, id int) (*models.User, error) {
	return &models.User{ID: id, Enabled: l.enabled}, nil
}

type nativeVideoPolicyFixture struct {
	policy    nativeVideoPolicy
	tenant    *nativeVideoSubjectFixture
	viewer    *nativeVideoViewerFixture
	login     *nativeVideoLoginFixture
	auth      *middleware.AuthMiddleware
	claims    *auth.Claims
	bearer    string
	file      *models.MediaFile
	source    storagesource.SourceConfig
	denied    bool
	session   *playback.Session
	secret    string
	fileErr   error
	sourceErr error
}

func newNativeVideoPolicyFixture(t *testing.T) *nativeVideoPolicyFixture {
	t.Helper()
	tenant := tenancy.Context{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 7, PolicyRevision: 1, SecurityRevision: 1}
	f := &nativeVideoPolicyFixture{tenant: &nativeVideoSubjectFixture{tenant: tenant}, viewer: &nativeVideoViewerFixture{scope: access.Scope{UserID: 7, ProfileID: "profile", PlaybackAllowed: true, ProfileVerified: true}, tenant: tenant}, login: &nativeVideoLoginFixture{enabled: true, active: true}, secret: "native-test-secret"}
	jwt := auth.NewJWTService("bearer-test-secret", time.Hour, time.Hour)
	token, err := jwt.GenerateAccessToken(7, "user", "login-session")
	if err != nil {
		t.Fatal(err)
	}
	f.claims, err = jwt.ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	f.bearer = "Bearer " + token
	f.auth = middleware.NewAuthMiddleware(jwt, f.login, nil, f.login)
	location, err := storagesource.CatalogLocation(uuid.New(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	f.file = &models.MediaFile{ID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: location, CodecVideo: "h264", Container: "mp4"}
	id := int64(3)
	f.source = storagesource.SourceConfig{InstallationID: &id}
	f.session = &playback.Session{ID: "play-session", UserID: 7, ProfileID: "profile", MediaFileID: 11, PlayMethod: playback.PlayDirect}
	f.policy = nativeVideoPolicy{credentials: f.auth, viewer: middleware.NewViewerAccessMiddleware(f.viewer), tenants: f.tenant, secret: func() string { return f.secret }, getSession: func(id string) (*playback.Session, error) {
		if id != "play-session" {
			return nil, playback.ErrSessionNotFound
		}
		return f.session, nil
	}, denied: func(context.Context, string) bool { return f.denied }, resolveFile: func(ctx context.Context, id int) (*models.MediaFile, error) {
		scope, ok := access.GetScope(ctx)
		tenant, hasTenant := tenancy.FromContext(ctx)
		if !ok || !hasTenant || middleware.GetUserID(ctx) != 7 || scope.UserID != 7 || scope.ProfileID != "profile" || tenant != f.tenant.tenant || id != 11 {
			return nil, errors.New("catalog context stale")
		}
		return f.file, f.fileErr
	}, authorizeSource: func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		scope, ok := access.GetScope(ctx)
		if !ok || !scope.PlaybackAllowed || file.ID != 11 {
			return errors.New("source context stale")
		}
		return f.sourceErr
	}}
	return f
}
func (f *nativeVideoPolicyFixture) requestContext(t *testing.T, stream bool) context.Context {
	t.Helper()
	path := "/api/v2/playback/plans"
	if stream {
		path = "/api/v2/stream/play-session"
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := access.SetScope(req.Context(), f.viewer.scope)
	ctx = middleware.SetProfileID(ctx, "profile")
	if !stream {
		req.Header.Set("Authorization", f.bearer)
		req.Header.Set("X-Profile-Token", "pin-proof")
		ctx = middleware.SetClaims(ctx, f.claims)
		ctx = tenancy.WithContext(ctx, f.tenant.tenant)
	} else {
		token, err := streamtoken.Sign(streamtoken.Claims{SessionID: "play-session", UserID: 7, ProfileID: "profile", MediaFileID: 11, MediaPath: f.file.FilePath, PlayMethod: "direct"}, f.secret, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req.URL.RawQuery = "st=" + token
	}
	req = req.WithContext(ctx)
	var captured context.Context
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r.Context() })
	if stream {
		handler = f.auth.StreamTokenAuth(f.secret)(handler).(http.HandlerFunc)
	}
	captureNativeVideoRequest(handler).ServeHTTP(httptest.NewRecorder(), req)
	return captured
}

func TestNativeVideoReauthorizesBearerWithFreshScopeAndTenant(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	authority, err := nativeVideoAuthority(f.requestContext(t, false), 11, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := authority.AuthorizeFile(t.Context(), f.file)
	if err != nil || actual.ID != 11 {
		t.Fatalf("catalog: %v %v", actual, err)
	}
	if err = authority.AuthorizeSource(t.Context(), actual, f.source); err != nil {
		t.Fatal(err)
	}
	if f.viewer.last.ProfileToken != "pin-proof" || f.viewer.last.SessionID != "login-session" || f.viewer.last.SkipPINVerification {
		t.Fatalf("viewer input=%+v", f.viewer.last)
	}
	f.viewer.scope.PlaybackAllowed = false
	if _, err = authority.AuthorizeFile(t.Context(), f.file); err == nil {
		t.Fatal("playback revocation survived")
	}
}
func TestNativeVideoRejectsChangedBearerAuthority(t *testing.T) {
	for _, kind := range []string{"login", "account", "tenant", "viewer", "catalog", "source"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeVideoPolicyFixture(t)
			authority, err := nativeVideoAuthority(f.requestContext(t, false), 11, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "login":
				f.login.active = false
			case "account":
				f.login.enabled = false
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "viewer":
				f.viewer.scope.ProfileID = "other"
			case "catalog":
				f.fileErr = errors.New("hidden")
			case "source":
				f.sourceErr = errors.New("owner revoked")
			}
			err = authority.AuthorizeSource(t.Context(), f.file, f.source)
			if err == nil {
				t.Fatal("revocation survived")
			}
		})
	}
}
func TestNativeVideoReauthorizesStreamCredentialAndSession(t *testing.T) {
	for _, kind := range []string{"allowed", "secret", "deny", "session", "user", "profile", "file", "tenant", "policy"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeVideoPolicyFixture(t)
			authority, err := nativeVideoAuthority(f.requestContext(t, true), 11, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "secret":
				f.secret = "rotated"
			case "deny":
				f.denied = true
			case "session":
				f.session = nil
			case "user":
				f.session.UserID = 8
			case "profile":
				f.session.ProfileID = "other"
			case "file":
				f.session.MediaFileID = 12
			case "tenant":
				f.tenant.tenant.PolicyRevision++
			case "policy":
				f.viewer.scope.PlaybackAllowed = false
			}
			_, err = authority.AuthorizeFile(t.Context(), f.file)
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("result=%v", err)
			}
			if kind == "allowed" && (!f.viewer.last.SkipPINVerification || f.viewer.last.SessionID != "" || f.viewer.last.ProfileToken != "") {
				t.Fatalf("stream viewer=%+v", f.viewer.last)
			}
		})
	}
}
func TestNativeVideoCapturePreservesOriginalRequestAcrossProjection(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/playback/plans", nil)
	req.Header.Set("Authorization", f.bearer)
	req.Header.Set("X-Profile-Token", "pin-proof")
	req = req.WithContext(tenancy.WithContext(access.SetScope(middleware.SetClaims(req.Context(), f.claims), f.viewer.scope), f.tenant.tenant))
	captureNativeVideoRequest(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		synthetic := httptest.NewRequest(http.MethodGet, "/api/v1/playback/start", nil).WithContext(r.Context())
		captureNativeVideoRequest(http.HandlerFunc(func(_ http.ResponseWriter, s *http.Request) {
			inputs, ok := s.Context().Value(nativeVideoRequestKey{}).(nativeVideoRequest)
			if !ok || inputs.method != http.MethodPost || inputs.path != "/api/v2/playback/plans" || inputs.authorization != f.bearer || inputs.profileToken != "pin-proof" {
				t.Fatal("projection replaced original credential")
			}
			if _, err := nativeVideoAuthority(s.Context(), 11, f.policy); err != nil {
				t.Fatal(err)
			}
		})).ServeHTTP(httptest.NewRecorder(), synthetic)
	})).ServeHTTP(httptest.NewRecorder(), req)
}
func TestNativeVideoRefusesMissingAuthorityInputs(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	for _, kind := range []string{"capture", "scope", "claims", "credential", "tenant", "playback", "nonnative"} {
		t.Run(kind, func(t *testing.T) {
			ctx := f.requestContext(t, false)
			switch kind {
			case "capture":
				ctx = access.SetScope(tenancy.WithContext(middleware.SetClaims(t.Context(), f.claims), f.tenant.tenant), f.viewer.scope)
			case "scope":
				ctx = middleware.SetClaims(context.WithValue(t.Context(), nativeVideoRequestKey{}, nativeVideoRequest{authorization: f.bearer, method: "GET", path: "/api/v2/stream/play-session"}), f.claims)
			case "claims":
				ctx = middleware.SetClaims(ctx, nil)
			case "credential":
				ctx = context.WithValue(ctx, nativeVideoRequestKey{}, nativeVideoRequest{method: "GET", path: "/api/v2/stream/play-session"})
			case "tenant":
				ctx = tenancy.WithContext(ctx, tenancy.Context{})
			case "playback":
				scope := f.viewer.scope
				scope.PlaybackAllowed = false
				ctx = access.SetScope(ctx, scope)
			case "nonnative":
				copy := *f.file
				copy.FilePath = "/local/movie.mp4"
				f.file = &copy
			}
			authority, err := nativeVideoAuthority(ctx, 11, f.policy)
			if err == nil {
				_, err = authority.AuthorizeFile(t.Context(), f.file)
			}
			if err == nil {
				t.Fatal("absent authority admitted")
			}
		})
	}
}

type nativeVideoAPIKeyFixture struct{ key *models.APIKey }

func (k *nativeVideoAPIKeyFixture) GetByKey(_ context.Context, value string) (*models.APIKey, error) {
	if k.key != nil && k.key.Key == value {
		return k.key, nil
	}
	return nil, auth.ErrAPIKeyNotFound
}
func (*nativeVideoAPIKeyFixture) UpdateLastUsed(context.Context, int64) error { return nil }
func TestNativeVideoAPIKeyRechecksCurrentScopesAndPINException(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	keys := &nativeVideoAPIKeyFixture{key: &models.APIKey{ID: 99, UserID: 7, Key: "sa_native_fixture"}}
	f.auth = middleware.NewAuthMiddleware(nil, nil, keys, f.login)
	f.policy.credentials = f.auth
	f.bearer = "Bearer sa_native_fixture"
	f.claims = &auth.Claims{TokenType: auth.TokenTypeAPIKey, APIKeyID: 99, UserID: 7}
	authority, err := nativeVideoAuthority(f.requestContext(t, false), 11, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authority.AuthorizeFile(t.Context(), f.file); err != nil {
		t.Fatal(err)
	}
	if !f.viewer.last.SkipPINVerification {
		t.Fatal("API key lost middleware PIN exception")
	}
	// A key narrowed to library discovery must stop an already-open playback.
	keys.key.Scopes = []string{auth.ScopeLibrariesRead}
	if _, err = authority.AuthorizeFile(t.Context(), f.file); !errors.Is(err, middleware.ErrCurrentCredentialForbidden) {
		t.Fatalf("scope revocation=%v", err)
	}
}

func TestNativeVideoExpiredStreamTokenNeverReachesCatalog(t *testing.T) {
	f := newNativeVideoPolicyFixture(t)
	ctx := f.requestContext(t, true)
	request := ctx.Value(nativeVideoRequestKey{}).(nativeVideoRequest)
	expired, err := streamtoken.Sign(streamtoken.Claims{SessionID: "play-session", UserID: 7, ProfileID: "profile", MediaFileID: 11, MediaPath: f.file.FilePath, PlayMethod: "direct"}, f.secret, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request.streamToken = expired
	ctx = context.WithValue(ctx, nativeVideoRequestKey{}, request)
	if _, err = nativeVideoAuthority(ctx, 11, f.policy); err == nil {
		t.Fatal("expired exact credential admitted")
	}
}

type nativeVideoDelegationFixture struct {
	base    *nativeVideoPolicyFixture
	profile *userstore.Profile
	login   *models.AuthSession
	key     *models.APIKeyMetadata
}

func newNativeVideoDelegationFixture(t *testing.T) *nativeVideoDelegationFixture {
	f := newNativeVideoPolicyFixture(t)
	d := &nativeVideoDelegationFixture{base: f, profile: &userstore.Profile{ID: "profile", OrganizationID: f.tenant.tenant.OrganizationID.String(), PINRevision: 1, CredentialRevision: 2}, login: &models.AuthSession{ID: "login-session", UserID: 7, ExpiresAt: time.Now().Add(time.Hour)}, key: &models.APIKeyMetadata{ID: 99, UserID: 7, Revision: 1}}
	f.policy.loadUser = f.login.GetByID
	f.policy.loadLogin = func(context.Context, string) (*models.AuthSession, error) { return d.login, nil }
	f.policy.loginActive = f.login.ActiveSessionRole
	f.policy.loadProfile = func(_ context.Context, uid int, pid string) (*userstore.Profile, error) {
		if uid != 7 || pid != "profile" {
			return nil, errors.New("wrong profile owner")
		}
		return d.profile, nil
	}
	f.policy.loadAPIKeyMetadata = func(context.Context, int64) (*models.APIKeyMetadata, error) { return d.key, nil }
	return d
}
func (d *nativeVideoDelegationFixture) binding(t *testing.T) handlers.NativeVideoBinding {
	t.Helper()
	authority, err := nativeVideoDelegation(d.base.requestContext(t, false), d.base.policy)
	if err != nil {
		t.Fatal(err)
	}
	binding := handlers.NativeVideoBinding{SessionID: "play-session", UserID: 7, ProfileID: "profile", Authority: authority, Identity: nativestorage.PlaybackIdentity{FileID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: d.base.file.FilePath}}
	d.base.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return binding, nil }
	return binding
}
func TestNativeVideoDelegationCarriesOnlyValidatedAuthority(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	binding := d.binding(t)
	if binding.Authority.UserID != 7 || binding.Authority.ProfileID != "profile" || binding.Authority.LoginSessionID != "login-session" || binding.Authority.ProfilePINRevision != 1 || binding.Authority.ProfileCredentialRevision != 2 || binding.Authority.Tenant != f.tenant.tenant || !binding.Authority.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid delegation metadata")
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(f.bearer)) || bytes.Contains(payload, []byte("pin-proof")) {
		t.Fatal("binding contains bearer or profile token")
	}
	workerCtx := middleware.SetClaims(t.Context(), &auth.Claims{UserID: 99, Role: "admin"})
	resolve := f.policy.resolveFile
	f.policy.resolveFile = func(ctx context.Context, id int) (*models.MediaFile, error) {
		if middleware.IsAdmin(ctx) {
			return nil, errors.New("worker inherited admin claims")
		}
		return resolve(ctx, id)
	}
	authority, err := nativeVideoDelegatedAuthority(workerCtx, f.file, binding, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authority.AuthorizeFile(workerCtx, f.file); err != nil {
		t.Fatal(err)
	}
	if err = authority.AuthorizeSource(workerCtx, f.file, f.source); err != nil {
		t.Fatal(err)
	}
}
func TestNativeVideoDelegatedAuthorityRejectsCurrentRevocations(t *testing.T) {
	for _, kind := range []string{"expiry", "account", "login-owner", "login-revoked", "login-expired", "pin", "credential", "profile-tenant", "tenant", "viewer", "playback-deny", "session-owner", "session-file", "session-missing", "catalog", "source"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			binding := d.binding(t)
			switch kind {
			case "expiry":
				binding.Authority.ExpiresAt = time.Now().Add(-time.Minute)
			case "account":
				f.login.enabled = false
			case "login-owner":
				d.login.UserID = 8
			case "login-revoked":
				now := time.Now()
				d.login.RevokedAt = &now
			case "login-expired":
				d.login.ExpiresAt = time.Now().Add(-time.Minute)
			case "pin":
				d.profile.PINRevision++
			case "credential":
				d.profile.CredentialRevision++
			case "profile-tenant":
				d.profile.OrganizationID = uuid.New().String()
			case "tenant":
				f.tenant.tenant.SecurityRevision++
			case "viewer":
				f.viewer.scope.PlaybackAllowed = false
			case "playback-deny":
				f.denied = true
			case "session-owner":
				f.session.UserID = 8
			case "session-file":
				f.session.MediaFileID = 12
			case "session-missing":
				f.session = nil
			case "catalog":
				f.fileErr = errors.New("hidden")
			case "source":
				f.sourceErr = errors.New("source hidden")
			}
			authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
			if err == nil {
				err = authority.AuthorizeSource(t.Context(), f.file, f.source)
			}
			if err == nil {
				t.Fatal("delegated revocation survived")
			}
		})
	}
}
func TestNativeVideoDelegatedAPIKeyRequiresSameUnscopedMetadata(t *testing.T) {
	for _, kind := range []string{"allowed", "owner", "revision", "scopes", "missing"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			keys := &nativeVideoAPIKeyFixture{key: &models.APIKey{ID: 99, UserID: 7, Key: "sa_native_fixture"}}
			f.auth = middleware.NewAuthMiddleware(nil, nil, keys, f.login)
			f.policy.credentials = f.auth
			f.bearer = "Bearer sa_native_fixture"
			f.claims = &auth.Claims{TokenType: auth.TokenTypeAPIKey, APIKeyID: 99, UserID: 7}
			binding := d.binding(t)
			if binding.Authority.APIKeyRevision != 1 {
				t.Fatal("key generation not frozen")
			}
			switch kind {
			case "owner":
				d.key.UserID = 8
			case "revision":
				d.key.Revision++
			case "scopes":
				d.key.Scopes = []string{auth.ScopeLibrariesRead}
			case "missing":
				d.key = nil
			}
			authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
			if err == nil {
				_, err = authority.AuthorizeFile(t.Context(), f.file)
			}
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("result=%v", err)
			}
		})
	}
}

func TestNativeVideoDelegatedWorkerRecoveryRequiresCurrentBinding(t *testing.T) {
	for _, kind := range []string{"allowed", "disabled", "missing-binding", "changed-binding", "denied", "lookup-failure"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			binding := d.binding(t)
			f.policy.allowRecovery = true
			f.policy.getSession = func(string) (*playback.Session, error) { return nil, playback.ErrSessionNotFound }
			switch kind {
			case "disabled":
				f.policy.allowRecovery = false
			case "missing-binding":
				f.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) {
					return handlers.NativeVideoBinding{}, playback.ErrSessionNotFound
				}
			case "changed-binding":
				live := binding
				live.Identity.ConfigurationRevision++
				f.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return live, nil }
			case "denied":
				f.denied = true
			case "lookup-failure":
				f.policy.getSession = func(string) (*playback.Session, error) { return nil, errors.New("session store unavailable") }
			}
			authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
			if err == nil {
				err = authority.AuthorizeSource(t.Context(), f.file, f.source)
			}
			if (err == nil) != (kind == "allowed") {
				t.Fatalf("recovery=%v", err)
			}
			if kind == "allowed" {
				f.denied = true
				if _, err = authority.AuthorizeFile(t.Context(), f.file); !errors.Is(err, middleware.ErrCurrentCredentialForbidden) {
					t.Fatalf("restored worker survived current deny=%v", err)
				}
			}
		})
	}
}

func TestNativeVideoDelegatedBindingChangesStopOpenAuthority(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	binding := d.binding(t)
	live := binding
	f.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return live, nil }
	authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authority.AuthorizeFile(t.Context(), f.file); err != nil {
		t.Fatal(err)
	}
	live.Authority.ProfilePINRevision++
	if _, err = authority.AuthorizeFile(t.Context(), f.file); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("changed live binding=%v", err)
	}
}

func TestNativeVideoStreamDelegationKeepsOriginalSessionAndExpiry(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	ctx := f.requestContext(t, true)
	claims, ok := middleware.StreamTokenClaims(ctx)
	if !ok || claims.ExpiresAt == nil {
		t.Fatal("fixture has no stream expiry")
	}
	delegation, err := nativeVideoDelegation(ctx, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if delegation.TokenType != "stream" || delegation.PlaybackSessionID != "play-session" || !delegation.ExpiresAt.Equal(claims.ExpiresAt.Time) || delegation.LoginSessionID != "" || delegation.APIKeyID != 0 {
		t.Fatal("stream delegation lost its original bounds")
	}
	binding := handlers.NativeVideoBinding{SessionID: "play-session", UserID: 7, ProfileID: "profile", Authority: delegation, Identity: nativestorage.PlaybackIdentity{FileID: 11, MediaFolderID: 9, ContentID: "movie", FilePath: f.file.FilePath}}
	f.policy.loadBinding = func(context.Context, string, int) (handlers.NativeVideoBinding, error) { return binding, nil }
	authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authority.AuthorizeFile(t.Context(), f.file); err != nil {
		t.Fatal(err)
	}
	binding.SessionID = "other-session"
	if _, err = nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy); !errors.Is(err, middleware.ErrCurrentCredentialForbidden) {
		t.Fatalf("delegated stream admitted another session=%v", err)
	}
}

func TestNativeVideoDelegatedDirectProfileRequiresCurrentLoginRevision(t *testing.T) {
	d := newNativeVideoDelegationFixture(t)
	f := d.base
	pid, revision := "profile", d.profile.CredentialRevision
	d.login.AuthMethod, d.login.ProfileID, d.login.ProfileCredentialRevision = auth.AuthMethodDirectProfile, &pid, &revision
	f.claims.AuthMethod = auth.AuthMethodDirectProfile
	binding := d.binding(t)
	authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authority.AuthorizeFile(t.Context(), f.file); err != nil {
		t.Fatal(err)
	}
	revision++
	if _, err = authority.AuthorizeFile(t.Context(), f.file); !errors.Is(err, middleware.ErrCurrentCredentialInvalid) {
		t.Fatalf("changed direct-profile login revision=%v", err)
	}
}

func TestNativeVideoDelegateConstructorRefusesMissingDependencies(t *testing.T) {
	for _, coordinator := range []*nativestorage.Coordinator{nil, {}} {
		service, err := NewNativeVideoDelegateService(Dependencies{}, coordinator)
		if service != nil || !errors.Is(err, storagesource.ErrSourceUnavailable) {
			t.Fatalf("service=%v error=%v", service, err)
		}
	}
}

type nativeVideoDenyIO struct {
	mu   sync.Mutex
	keys map[string]bool
	err  error
}

func (*nativeVideoDenyIO) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*nativeVideoDenyIO) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (d *nativeVideoDenyIO) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.err != nil {
			cmd.SetErr(d.err)
			return d.err
		}
		key := fmt.Sprint(cmd.Args()[1])
		switch cmd.Name() {
		case "set":
			d.keys[key] = true
			cmd.(*redis.StatusCmd).SetVal("OK")
		case "get":
			if !d.keys[key] {
				cmd.SetErr(redis.Nil)
				return redis.Nil
			}
			cmd.(*redis.StringCmd).SetVal("deny")
		case "exists":
			if d.keys[key] {
				cmd.(*redis.IntCmd).SetVal(1)
			} else {
				cmd.(*redis.IntCmd).SetVal(0)
			}
		default:
			return errors.New("unexpected deny command")
		}
		return nil
	}
}

func TestNativeVideoDelegatedRecoveryUsesFreshFailClosedDeny(t *testing.T) {
	for _, kind := range []string{"allowed", "stale-negative", "redis-error"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			binding := d.binding(t)
			io := &nativeVideoDenyIO{keys: make(map[string]bool)}
			client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
			client.AddHook(io)
			t.Cleanup(func() { _ = client.Close() })
			cached := playback.NewStreamDeny(client)
			if cached.Denied(t.Context(), binding.SessionID) {
				t.Fatal("initial fixture deny")
			}
			switch kind {
			case "stale-negative":
				playback.NewStreamDeny(client).Deny(t.Context(), binding.SessionID)
			case "redis-error":
				io.err = errors.New("redis unavailable")
			}
			if cached.Denied(t.Context(), binding.SessionID) {
				t.Fatal("fixture did not preserve stale negative cache")
			}
			f.policy.denied = nativeVideoSessionDenied(client, cached)
			f.policy.allowRecovery = true
			f.policy.getSession = func(string) (*playback.Session, error) { return nil, playback.ErrSessionNotFound }
			authority, err := nativeVideoDelegatedAuthority(t.Context(), f.file, binding, f.policy)
			if kind == "allowed" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = authority.AuthorizeFile(t.Context(), f.file); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, middleware.ErrCurrentCredentialForbidden) {
				t.Fatalf("unsafe recovery admitted: kind=%s err=%v", kind, err)
			}
		})
	}
}

type nativeVideoBoundBindingsFixture struct{ binding handlers.NativeVideoBinding }

func (s *nativeVideoBoundBindingsFixture) Put(_ context.Context, _ string, b handlers.NativeVideoBinding) error {
	s.binding = b
	return nil
}
func (s *nativeVideoBoundBindingsFixture) Get(_ context.Context, sid string, fid int) (handlers.NativeVideoBinding, error) {
	if sid != s.binding.SessionID || fid != s.binding.Identity.FileID {
		return handlers.NativeVideoBinding{}, storagesource.ErrReferenceConflict
	}
	return s.binding, nil
}
func (s *nativeVideoBoundBindingsFixture) RevokeSession(context.Context, string) error {
	s.binding = handlers.NativeVideoBinding{}
	return nil
}

type nativeVideoBoundReaderFixture struct {
	*bytes.Reader
	ctx       context.Context
	binding   handlers.NativeVideoBinding
	authority nativestorage.PlaybackAuthority
	file      *models.MediaFile
	source    storagesource.SourceConfig
	read      func()
}

func (f *nativeVideoBoundReaderFixture) Identity() nativestorage.PlaybackIdentity {
	return f.binding.Identity
}
func (f *nativeVideoBoundReaderFixture) Info() mediasource.Info   { return f.binding.Info }
func (f *nativeVideoBoundReaderFixture) Context() context.Context { return f.ctx }
func (f *nativeVideoBoundReaderFixture) Cancel()                  {}
func (f *nativeVideoBoundReaderFixture) Close() error             { return nil }
func (f *nativeVideoBoundReaderFixture) Err() error               { return nil }
func (f *nativeVideoBoundReaderFixture) Reauthorize(ctx context.Context) error {
	return f.authority.AuthorizeSource(ctx, f.file, f.source)
}
func (f *nativeVideoBoundReaderFixture) Read(p []byte) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.Reader.Read(p)
	if f.read != nil {
		f.read()
	}
	if denied := f.Reauthorize(f.ctx); denied != nil {
		clear(p)
		return 0, denied
	}
	return n, err
}
func (f *nativeVideoBoundReaderFixture) ReadAt(p []byte, offset int64) (int, error) {
	if err := f.Reauthorize(f.ctx); err != nil {
		clear(p)
		return 0, err
	}
	n, err := f.Reader.ReadAt(p, offset)
	if f.read != nil {
		f.read()
	}
	if denied := f.Reauthorize(f.ctx); denied != nil {
		clear(p)
		return 0, denied
	}
	return n, err
}

func TestNativeVideoBoundServingRechecksFreshSessionAndOriginAuthority(t *testing.T) {
	for _, kind := range []string{"bearer-stale-negative", "bearer-denied-during-read", "bearer-redis-error", "signed-origin-revoked", "signed-current-secret-revoked"} {
		t.Run(kind, func(t *testing.T) {
			d := newNativeVideoDelegationFixture(t)
			f := d.base
			binding := d.binding(t)
			binding.Identity.Reference.Revision = "v1"
			binding.Info = mediasource.Info{Name: "movie.mp4", Size: 10, Revision: "v1"}
			binding.Format = "mov"
			store := &nativeVideoBoundBindingsFixture{binding: binding}
			f.policy.loadBinding = store.Get
			dio := &nativeVideoDenyIO{keys: make(map[string]bool)}
			client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
			client.AddHook(dio)
			t.Cleanup(func() { _ = client.Close() })
			cached := playback.NewStreamDeny(client)
			if cached.Denied(t.Context(), binding.SessionID) {
				t.Fatal("initial fixture deny")
			}
			f.policy.denied = nativeVideoSessionDenied(client, cached)
			service := &handlers.NativeVideoService{Bindings: store, Deny: cached}
			configureNativeVideoBoundAuthority(service, f.policy)
			ctx := f.requestContext(t, strings.HasPrefix(kind, "signed"))
			var duringRead func()
			service.Open = func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
				authority, err := nativeVideoAuthority(ctx, file.ID, f.policy)
				if err != nil {
					return nil, err
				}
				return &nativeVideoBoundReaderFixture{Reader: bytes.NewReader([]byte("0123456789")), ctx: ctx, binding: binding, authority: authority, file: file, source: f.source, read: func() {
					if duringRead != nil {
						duringRead()
					}
				}}, nil
			}
			opened, _, err := service.OpenBound(ctx, f.session, f.file)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			marker := func() { playback.NewStreamDeny(client).Deny(t.Context(), binding.SessionID) }
			switch kind {
			case "bearer-stale-negative":
				marker()
			case "bearer-denied-during-read":
				duringRead = marker
			case "bearer-redis-error":
				dio.err = errors.New("redis unavailable")
			case "signed-origin-revoked":
				now := time.Now()
				d.login.RevokedAt = &now
			case "signed-current-secret-revoked":
				f.secret = "rotated"
			}
			if cached.Denied(t.Context(), binding.SessionID) {
				t.Fatal("fixture lost cached negative")
			}
			buffer := bytes.Repeat([]byte{0x7f}, 16)
			n, err := opened.ReadAt(buffer, 0)
			if n != 0 || err == nil || !bytes.Equal(buffer, make([]byte, len(buffer))) {
				t.Fatalf("revoked bound read returned n=%d err=%v data=%x", n, err, buffer)
			}
		})
	}
}

func TestNativeVideoStreamTokenAllowsOnlyBoundSubtitleAndFontRoutes(t *testing.T) {
	for _, c := range []struct {
		name, method, path string
		allowed            bool
	}{
		{"subtitle", http.MethodGet, "/api/v2/stream/play-session/subtitles/0.vtt", true},
		{"subtitle-head", http.MethodHead, "/api/v1/stream/play-session/subtitles/2.srt", true},
		{"fonts", http.MethodGet, "/api/v2/stream/play-session/subtitles/1.ass/fonts", true},
		{"fonts-head", http.MethodHead, "/api/v2/stream/play-session/subtitles/1.ass/fonts", false},
		{"mutation", http.MethodPost, "/api/v2/stream/play-session/subtitles/0.vtt", false},
		{"other-session", http.MethodGet, "/api/v2/stream/other-session/subtitles/0.vtt", false},
		{"negative-track", http.MethodGet, "/api/v2/stream/play-session/subtitles/-1.vtt", false},
		{"unknown-format", http.MethodGet, "/api/v2/stream/play-session/subtitles/0.exe", false},
		{"extra-font-path", http.MethodGet, "/api/v2/stream/play-session/subtitles/0.ass/fonts/name.ttf", false},
		{"traversal", http.MethodGet, "/api/v2/stream/play-session/subtitles/../fonts", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newNativeVideoPolicyFixture(t)
			ctx := f.requestContext(t, true)
			captured := ctx.Value(nativeVideoRequestKey{}).(nativeVideoRequest)
			captured.method, captured.path = c.method, c.path
			ctx = context.WithValue(ctx, nativeVideoRequestKey{}, captured)
			authority, err := nativeVideoAuthority(ctx, f.file.ID, f.policy)
			if err == nil {
				_, err = authority.AuthorizeFile(t.Context(), f.file)
			}
			if (err == nil) != c.allowed {
				t.Fatalf("route allowed=%v error=%v", c.allowed, err)
			}
		})
	}
}
