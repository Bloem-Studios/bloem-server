package api

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemHoldPluginPreviews makes Silo's plugin resolvers treat preview
// principals as anonymous: a preview session is not active to them, and a
// preview account's API key resolves no user.
func bloemHoldPluginPreviews(sessions *auth.SessionRepository, resolvers ...*pluginAccessResolver) {
	if sessions == nil {
		return
	}
	for _, resolver := range resolvers {
		resolver.sessions = bloemPluginSessions{sessions: sessions}
		if resolver.users != nil {
			resolver.users = bloemPluginUsers{inner: resolver.users, sessions: sessions}
		}
	}
}

type bloemPluginSessions struct{ sessions *auth.SessionRepository }

func (s bloemPluginSessions) ActiveSession(ctx context.Context, sessionID string, userID int) (auth.SessionAuthority, bool, error) {
	authority, preview, active, err := s.sessions.ActivePreviewSession(ctx, sessionID, userID)
	return authority, active && !preview, err
}

var errPluginPreviewAccount = errors.New("plugin access: preview accounts reach plugins only as anonymous")

type bloemPluginUsers struct {
	inner    pluginUsers
	sessions *auth.SessionRepository
}

func (u bloemPluginUsers) GetByID(ctx context.Context, id int) (*models.User, error) {
	user, err := u.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if preview, err := u.sessions.AccountPreviewOnly(ctx, user.ID); err != nil || preview {
		return nil, errPluginPreviewAccount
	}
	return user, nil
}

// bloemPluginBoundary adds Bloem's refusals to what Silo's resolver decided.
// Keep denial distinct from anonymous access: public plugin descriptors must
// not turn an authenticated browse-only viewer into a permitted guest. The
// plugin entry points authenticate on their own and so never pass
// RequireAuth, which is where direct-profile sessions are held to their
// allowlist, so those are refused here too.
type bloemPluginBoundary struct {
	jwt    *auth.JWTService
	users  *auth.UserRepository
	viewer apimw.ViewerResolver
}

func (b bloemPluginBoundary) content(resolve func(*http.Request) plugins.ContentAccess) func(*http.Request) plugins.ContentAccess {
	return func(r *http.Request) plugins.ContentAccess { return b.apply(r, resolve(r)) }
}

// refused answers 403 for a plugin route or asset request Bloem does not let
// through.
func (b bloemPluginBoundary) refused(w http.ResponseWriter, r *http.Request, resolved pluginAccess) bool {
	access := b.apply(r, plugins.ContentAccess{Authenticated: resolved.authenticated, Admin: resolved.admin, UserID: resolved.userID, ProfileID: resolved.profileID})
	switch {
	case access.PlaybackDenied:
		http.Error(w, "Plugin content access has not been granted", http.StatusForbidden)
	case access.ProfileBound:
		http.Error(w, "Direct profile sessions cannot use plugin routes", http.StatusForbidden)
	default:
		return false
	}
	return true
}

func (b bloemPluginBoundary) apply(r *http.Request, result plugins.ContentAccess) plugins.ContentAccess {
	if !result.Authenticated {
		return result
	}
	input := access.ResolveInput{UserID: result.UserID, ProfileID: result.ProfileID, SkipPINVerification: true}
	claims, ok := b.sessionClaims(r)
	switch {
	case !ok:
		result.PlaybackDenied = true
		return result
	case claims != nil && claims.AuthMethod == auth.AuthMethodDirectProfile:
		result.ProfileBound = true
		return result
	case claims != nil:
		// Silo's resolver verified the profile already; asset access does not
		// carry it.
		input.UserID, input.SessionID = claims.UserID, claims.SessionID
		input.ProfileID = cmp.Or(input.ProfileID, claims.ProfileID, strings.TrimSpace(r.Header.Get("X-Profile-Id")))
	}
	result.PlaybackDenied = true
	if b.viewer == nil {
		return result
	}
	if b.users != nil {
		user, err := b.users.GetByID(r.Context(), input.UserID)
		if err != nil || user == nil || !user.Enabled {
			return result
		}
	}
	scope, err := b.viewer.Resolve(r.Context(), input)
	result.PlaybackDenied = err != nil || !scope.ProfileVerified || !scope.PlaybackAllowed
	return result
}

// sessionClaims reads the request's login token: nil for an API key, and
// false when the token does not verify.
func (b bloemPluginBoundary) sessionClaims(r *http.Request) (*auth.Claims, bool) {
	token, _ := pluginRequestToken(r)
	if strings.HasPrefix(token, "sa_") {
		return nil, true
	}
	claims, err := b.jwt.ValidateToken(token)
	return claims, err == nil
}

// Self-authenticating content endpoints lack the normal tenant middleware.
// Resolve current tenant authority from the validated identity before policy.
type bloemContentViewer struct {
	inner   apimw.ViewerResolver
	tenants *tenancy.SubjectResolver
}

func (v bloemContentViewer) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	tenant, err := v.tenants.ResolveSubjectTenant(ctx, in.UserID, in.ProfileID)
	if err != nil {
		return access.Scope{}, err
	}
	return v.inner.Resolve(tenancy.WithContext(ctx, tenant), in)
}
