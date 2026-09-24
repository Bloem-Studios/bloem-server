package abs

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/compatgateway"
)

type absPublicMountContextKey struct{}

type absInProcessDispatchContextKey struct{}

func (h *Handler) publicMountMiddleware(next http.Handler) http.Handler {
	return compatgateway.PublicMountHandler(
		h.deps.InternalGatewayIdentityVerified,
		func(w http.ResponseWriter, r *http.Request, mount string, inProcessDispatch bool) {
			if mount != "" {
				r = r.WithContext(context.WithValue(r.Context(), absPublicMountContextKey{}, mount))
			}
			if inProcessDispatch {
				r = r.WithContext(context.WithValue(r.Context(), absInProcessDispatchContextKey{}, true))
			}
			next.ServeHTTP(w, r)
		},
	)
}
