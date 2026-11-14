package apiv2

import (
	"fmt"
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemDependencies holds the Bloem-only middleware slots embedded in
// Dependencies. Fields are promoted, so callers still set deps.TenantIdentity
// and deps.StreamTokens directly.
type bloemDependencies struct {
	PreviewPresentation PreviewPresentationSource
	// PreviewFreePlay meters free play for the preview gate; nil without it.
	PreviewFreePlay PreviewFreePlayService
	// TenantIdentity revalidates tenant selection after authentication and before policy.
	TenantIdentity func(http.Handler) http.Handler
	StreamTokens   func(http.Handler) http.Handler
	// StorageCovers reads covers storage sources serve on demand; nil when no
	// storage runtime is configured.
	StorageCovers StorageCoverService
}

// bloemAuthChain builds the authenticated gate prefix: the optional stream
// token middleware, authentication, then the optional tenant identity gate.
func bloemAuthChain(deps Dependencies, requireAuth func(http.Handler) http.Handler) []func(http.Handler) http.Handler {
	chain := []func(http.Handler) http.Handler{}
	if deps.StreamTokens != nil {
		chain = append(chain, deps.StreamTokens)
	}
	chain = append(chain, requireAuth)
	if deps.TenantIdentity != nil {
		chain = append(chain, deps.TenantIdentity)
	}
	return chain
}

// bloemStreamReferenceChain redoes Silo's streamReferenceGateChain on Bloem's
// auth prefix. Silo swaps a delivery chain's first gate for
// RequireStreamReferenceAuth, but Bloem's chain starts with its stream-token
// gate (bloemAuthChain), so the swap dropped it and left Silo's RequireAuth
// behind Silo's reference check. This rebuilds the prefix around
// bloemStreamReferenceAuth and keeps every gate after it.
func bloemStreamReferenceChain(deps Dependencies, chain []func(http.Handler) http.Handler) []func(http.Handler) http.Handler {
	head := len(bloemAuthChain(deps, deps.Auth.RequireAuth))
	if len(chain) < head {
		return chain
	}
	return append(bloemAuthChain(deps, bloemStreamReferenceAuth(deps)), chain[head:]...)
}

// bloemStreamReferenceAuth is Silo's RequireStreamReferenceAuth for a request
// Bloem's stream-token gate has not already authorized. Both read the same
// `st`; a token the stream-token gate accepted keeps Bloem's delivery path
// (tenant and viewer scope from the token's own account and profile), as it
// did behind RequireAuth before Silo added stream references.
func bloemStreamReferenceAuth(deps Dependencies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		reference := deps.Auth.RequireStreamReferenceAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apimw.IsStreamTokenAuthorized(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			reference.ServeHTTP(w, r)
		})
	}
}

// bloemTenantIdentityGate wraps h with the tenant identity gate when one is wired.
func bloemTenantIdentityGate(deps Dependencies, h http.Handler) http.Handler {
	if deps.TenantIdentity != nil {
		return deps.TenantIdentity(h)
	}
	return h
}

// bloemWithTenantIdentityGate puts the tenant identity gate in front of the
// handler inner builds, for gate chains that assemble their own post-auth
// middleware (the Apple display route runs it on both credential paths).
func bloemWithTenantIdentityGate(deps Dependencies, inner func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return bloemTenantIdentityGate(deps, inner(next)) }
}

// bloemCheckOperationPath accepts both surfaces registered through this
// package: Silo's /api/v2 and Bloem's native /api/bloem/v2 (see
// bloem_native_document.go). Both get the same checks, metadata, body limits
// and class gates -- the alternative was a second registry that would drift
// from this one.
func bloemCheckOperationPath(path string) error {
	if !strings.HasPrefix(path, Prefix+"/") && !strings.HasPrefix(path, BloemPrefix+"/") {
		return fmt.Errorf("path %q must start with %s/ or %s/", path, Prefix, BloemPrefix)
	}
	return nil
}
