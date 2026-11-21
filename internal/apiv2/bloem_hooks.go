package apiv2

import (
	"net/http"
	"strings"
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
// behind Silo's reference check. This rebuilds the prefix with Silo's
// RequireStreamReferenceAuth in RequireAuth's place and keeps every gate
// after it.
//
// Both gates read the same `st`. Silo's check runs on every request the
// stream-token gate authorized too: a Silo app is told playback_session_ended
// for a stopped attempt and 401 once the login session that started it has
// ended, as Silo answers. A request it admits keeps Bloem's delivery path
// (tenant and viewer scope from the token's own account and profile).
func bloemStreamReferenceChain(deps Dependencies, chain []func(http.Handler) http.Handler) []func(http.Handler) http.Handler {
	head := len(bloemAuthChain(deps, deps.Auth.RequireAuth))
	if len(chain) < head {
		return chain
	}
	return append(bloemAuthChain(deps, deps.Auth.RequireStreamReferenceAuth), chain[head:]...)
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

// bloemCheckedOperationPath lets checkOperation judge a native operation
// (BloemPrefix) by Silo's rules for its own surface: the checks see the path
// under Prefix, and every other path unchanged. Only checkOperation's copy of
// the operation changes; the route keeps its real path.
func bloemCheckedOperationPath(path string) string {
	if rest, ok := strings.CutPrefix(path, BloemPrefix+"/"); ok {
		return Prefix + "/" + rest
	}
	return path
}
