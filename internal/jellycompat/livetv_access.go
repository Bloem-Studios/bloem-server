package jellycompat

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
)

// LiveTVAccessResolver checks the current viewer grant independently of libraries.
type LiveTVAccessResolver func(context.Context, *Session) bool

// NewScopeLiveTVAccess shares native account, profile, and group permission rules.
func NewScopeLiveTVAccess(resolver ScopeResolver) LiveTVAccessResolver {
	return func(ctx context.Context, session *Session) bool {
		if resolver == nil || session == nil || session.StreamAppUserID <= 0 {
			return false
		}
		scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: session.StreamAppUserID, ProfileID: session.ProfileID, SkipPINVerification: true})
		return err == nil && scope.LiveTVAllowed
	}
}

var errLiveTVForbidden = errors.New("Live TV access is not allowed")

func (h *LiveTVHandler) allowed(ctx context.Context, session *Session) bool {
	return h.access != nil && h.access(ctx, session)
}

func (h *LiveTVHandler) requireAccess(w http.ResponseWriter, r *http.Request) bool {
	if h.allowed(r.Context(), SessionFromContext(r.Context())) {
		return true
	}
	writeLiveTVCompatError(w, errLiveTVForbidden)
	return false
}
