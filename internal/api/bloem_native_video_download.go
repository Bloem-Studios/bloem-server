package api

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// nativeVideoCompatDownloadTokenType marks a download authority delegated from
// a Jellyfin-compatible session. Unlike nativeVideoCompatTokenType it serves
// no playback session.
const nativeVideoCompatDownloadTokenType = "jellycompat_download"

type nativeVideoCompatDownloadKey struct{}

// nativeVideoCompatDownload is request memory only: never log or store it.
type nativeVideoCompatDownload struct {
	credential string
	userID     int
	profileID  string
}

// WithNativeVideoCompatDownload marks ctx as a download for the
// Jellyfin-compatible session with this token, so a native original is read
// under that session's authority. The Jellyfin listener has no Bloem bearer
// credential to recheck.
func WithNativeVideoCompatDownload(ctx context.Context, credential string, userID int, profileID string) context.Context {
	if ctx == nil || credential == "" || userID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, nativeVideoCompatDownloadKey{}, nativeVideoCompatDownload{credential: credential, userID: userID, profileID: profileID})
}

// newNativeVideoDownloads composes original downloads of native-storage and
// `.strm` videos from the playback policy and readers.
func newNativeVideoDownloads(deps Dependencies, service *handlers.NativeVideoService, p nativeVideoPolicy, strm *strmPlayback, storage nativeStorageOpen) *handlers.NativeVideoDownloads {
	if deps.DB == nil || service == nil || storage == nil {
		return nil
	}
	populateNativeVideoStores(deps, &p)
	downloads := &handlers.NativeVideoDownloads{Service: service, Open: func(ctx context.Context, file *models.MediaFile) (handlers.NativeVideoFile, error) {
		authority, err := nativeVideoDownloadAuthority(ctx, file.ID, p)
		if err != nil {
			return nil, err
		}
		return strm.open(ctx, file, authority, storage)
	}}
	if strm != nil && strm.sources != nil {
		downloads.Size = func(ctx context.Context, file *models.MediaFile) (int64, bool, error) {
			src, found, err := strm.sources.Get(ctx, file.FilePath)
			if err != nil || !found || src.RemoteSize <= 0 {
				return 0, false, err
			}
			return src.RemoteSize, true, nil
		}
	}
	return downloads
}

// attachNativeVideoDownloads lets the download service serve native
// originals. It runs once, before the router accepts requests.
func attachNativeVideoDownloads(svc *downloads.Service, h *handlers.PlaybackHandler) {
	if svc == nil || h == nil || h.NativeVideo == nil || h.NativeVideo.Downloads == nil {
		return
	}
	svc.SetNativeOriginals(h.NativeVideo.Downloads)
}

// nativeVideoDownloadAuthority checks the download request's viewer once, then
// rechecks it on every open and read: the credential behind it (login session,
// API key or Jellyfin-compatible session), the account, the profile and its
// PIN and credential revisions, the organization membership, the library
// access and the source, as delegated playback does. A download outlives an
// access token, so it is bound to the login session, never longer than the
// longest playback token lifetime.
func nativeVideoDownloadAuthority(ctx context.Context, fileID int, p nativeVideoPolicy) (nativestorage.PlaybackAuthority, error) {
	if fileID <= 0 {
		return nativestorage.PlaybackAuthority{}, middleware.ErrCurrentCredentialInvalid
	}
	d, err := nativeVideoDownloadDelegation(ctx, fileID, p)
	if err != nil {
		return nativestorage.PlaybackAuthority{}, err
	}
	subject := &nativeVideoSubject{policy: p, delegated: &d, download: true, userID: d.UserID, profileID: d.ProfileID, fileID: fileID, skipPIN: true, tenant: &d.Tenant}
	if _, err := subject.refresh(ctx); err != nil {
		return nativestorage.PlaybackAuthority{}, err
	}
	return nativeVideoCallbacks(subject), nil
}

func nativeVideoDownloadDelegation(ctx context.Context, fileID int, p nativeVideoPolicy) (handlers.NativeVideoAuthority, error) {
	empty := handlers.NativeVideoAuthority{}
	if ctx == nil || p.viewer == nil || p.tenants == nil || p.resolveFile == nil || p.authorizeSource == nil || p.loadLogin == nil || p.loadUser == nil {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	d := handlers.NativeVideoAuthority{ExpiresAt: time.Now().UTC().Add(playback.MaxTokenTTL), OriginalMethod: "GET", OriginalPath: "download"}
	link := false
	if compat, ok := ctx.Value(nativeVideoCompatDownloadKey{}).(nativeVideoCompatDownload); ok {
		sessions := p.compat.currentSessions()
		if sessions == nil {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		current, err := sessions.Recheck(ctx, compat.credential)
		if err != nil {
			return empty, err
		}
		// Sessions an `sa_` admin key synthesizes have no login session.
		if current.UserID != compat.userID || current.ProfileID != compat.profileID || current.LoginSessionID == "" || strings.HasPrefix(compat.credential, "sa_") {
			return empty, middleware.ErrCurrentCredentialForbidden
		}
		d.UserID, d.ProfileID = current.UserID, current.ProfileID
		d.TokenType, d.AuthMethod = nativeVideoCompatDownloadTokenType, nativeVideoCompatDownloadTokenType
		d.LoginSessionID = current.LoginSessionID
		d.CompatSessionDigest = nativeVideoCompatDigest(compat.credential)
		d.PINVerificationSkipped = true
		if !current.ExpiresAt.IsZero() && current.ExpiresAt.Before(d.ExpiresAt) {
			d.ExpiresAt = current.ExpiresAt.UTC()
		}
	} else {
		// The viewer access middleware resolved this scope for the request,
		// with its PIN proof; the delegation pins the revisions it saw.
		scope, hasScope := access.GetScope(ctx)
		claims := middleware.GetClaims(ctx)
		if !hasScope || claims == nil || scope.UserID <= 0 || claims.UserID != scope.UserID || !scope.PlaybackAllowed || claims.ProfileID != "" && claims.ProfileID != scope.ProfileID {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		if profile := middleware.GetProfileID(ctx); profile != "" && profile != scope.ProfileID {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		d.UserID, d.ProfileID = scope.UserID, scope.ProfileID
		switch claims.TokenType {
		case auth.TokenTypeDirectDownloadLink:
			// A link authorizes one file of its login session.
			if claims.FileID != fileID {
				return empty, middleware.ErrCurrentCredentialForbidden
			}
			link = true
			d.PINVerificationSkipped = true
			fallthrough
		case auth.TokenTypeAccess:
			if claims.SessionID == "" {
				return empty, middleware.ErrCurrentCredentialInvalid
			}
			d.TokenType = auth.TokenTypeAccess
			d.LoginSessionID = claims.SessionID
			d.AuthMethod = claims.AuthMethod
			d.PINVerificationSkipped = d.PINVerificationSkipped || claims.AuthMethod == auth.AuthMethodDirectProfile
		case auth.TokenTypeAPIKey:
			if p.loadAPIKeyMetadata == nil {
				return empty, middleware.ErrCurrentCredentialInvalid
			}
			key, err := p.loadAPIKeyMetadata(ctx, claims.APIKeyID)
			if err != nil {
				return empty, err
			}
			if key == nil || key.ID != claims.APIKeyID || key.UserID != d.UserID || key.Revision <= 0 || len(key.Scopes) != 0 {
				return empty, middleware.ErrCurrentCredentialForbidden
			}
			d.TokenType = auth.TokenTypeAPIKey
			d.APIKeyID, d.APIKeyRevision = key.ID, key.Revision
			d.PINVerificationSkipped = true
		default:
			return empty, middleware.ErrCurrentCredentialForbidden
		}
	}
	if d.LoginSessionID != "" {
		login, err := p.loadLogin(ctx, d.LoginSessionID)
		if err != nil {
			return empty, err
		}
		if login == nil || login.ID != d.LoginSessionID || login.UserID != d.UserID {
			return empty, middleware.ErrCurrentCredentialInvalid
		}
		if link {
			// A link token names no sign-in method; the session's own does.
			d.AuthMethod = login.AuthMethod
		}
		if login.Purpose == models.SessionPurposeRemotePlayback && login.AbsoluteExpiresAt != nil && login.AbsoluteExpiresAt.Before(d.ExpiresAt) {
			d.ExpiresAt = login.AbsoluteExpiresAt.UTC()
		}
		if login.ExpiresAt.Before(d.ExpiresAt) {
			d.ExpiresAt = login.ExpiresAt.UTC()
		}
	}
	captured, ok := tenancy.FromContext(ctx)
	if !ok || captured.AccountID != d.UserID || captured.OrganizationID == uuid.Nil || captured.MembershipID == uuid.Nil {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	tenant, err := p.tenants.ResolveSubjectTenant(ctx, d.UserID, d.ProfileID)
	if err != nil {
		return empty, err
	}
	tenant.Legacy = captured.Legacy
	if tenant != captured {
		return empty, middleware.ErrCurrentCredentialInvalid
	}
	d.Tenant = tenant
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
	if err := verifyNativeVideoActor(ctx, d, p); err != nil {
		return empty, err
	}
	return d, nil
}
