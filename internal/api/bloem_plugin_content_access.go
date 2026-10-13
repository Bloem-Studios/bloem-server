package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func optionalPluginAccessToken(r *http.Request) string {
	if parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2); len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		if token := strings.TrimSpace(parts[1]); token != "" {
			return token
		}
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	if cookie, err := r.Cookie(auth.PluginAccessCookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

// Keep denial distinct from anonymous access: public plugin descriptors must
// not turn an authenticated browse-only viewer into a permitted guest.
func resolveBloemPluginContentAccess(r *http.Request, jwt *auth.JWTService, sessions *auth.SessionRepository, keys *auth.APIKeyRepository, users *auth.UserRepository, viewer apimw.ViewerResolver) plugins.ContentAccess {
	authenticated, admin, uid, pid, bound := resolveOptionalPluginAccessUser(r, jwt, sessions, keys, users)
	result := plugins.ContentAccess{Authenticated: authenticated, Admin: admin, UserID: uid, ProfileID: pid, ProfileBound: bound}
	if !authenticated || bound {
		return result
	}
	result.PlaybackDenied = true
	if viewer == nil {
		return result
	}
	if users != nil {
		user, err := users.GetByID(r.Context(), uid)
		if err != nil || user == nil || !user.Enabled {
			return result
		}
	}
	input := access.ResolveInput{UserID: uid, ProfileID: pid, SkipPINVerification: true}
	token := optionalPluginAccessToken(r)
	if !strings.HasPrefix(token, "sa_") {
		claims, err := jwt.ValidateToken(token)
		if err != nil {
			return result
		}
		input.SessionID = claims.SessionID
	}
	scope, err := viewer.Resolve(r.Context(), input)
	result.PlaybackDenied = err != nil || !scope.ProfileVerified || !scope.PlaybackAllowed
	return result
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
