package compatgateway

import (
	"context"
	"net/http"
	"strings"
)

const publicMountHeader = "X-Bloem-Public-Mount"

type publicMountContextKey struct{}

func withPublicMount(ctx context.Context, mount string) context.Context {
	mount = normalizePublicMount(mount)
	if mount == "" {
		return ctx
	}
	return context.WithValue(ctx, publicMountContextKey{}, mount)
}

func publicMountFromContext(ctx context.Context) string {
	mount, _ := ctx.Value(publicMountContextKey{}).(string)
	return normalizePublicMount(mount)
}

// PublicMountHandler passes trusted mount metadata to an application without
// exposing the private context key. inProcessDispatch is true only when the
// mount came from that context. A companion may supply the fixed header only
// when its surrounding internal-gateway identity verifier has accepted the
// request, but that metadata does not impersonate in-process dispatch.
func PublicMountHandler(
	identityVerified func(*http.Request) bool,
	next func(http.ResponseWriter, *http.Request, string, bool),
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mount := publicMountFromContext(r.Context())
		inProcessDispatch := mount != ""
		if mount == "" && identityVerified != nil && identityVerified(r) {
			mount = normalizePublicMount(r.Header.Get(publicMountHeader))
		}

		clone := r.Clone(r.Context())
		clone.Header = r.Header.Clone()
		clone.Header.Del(publicMountHeader)
		next(w, clone, mount, inProcessDispatch)
	})
}

func normalizePublicMount(mount string) string {
	mount = strings.TrimSuffix(mount, "/")
	if mount == audiobookshelfPrefix {
		return mount
	}
	return ""
}
