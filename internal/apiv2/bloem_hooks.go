package apiv2

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// bloemDependencies holds the Bloem-only middleware slots embedded in
// Dependencies. Fields are promoted, so callers still set deps.TenantIdentity
// and deps.StreamTokens directly.
type bloemDependencies struct {
	// TenantIdentity revalidates tenant selection after authentication and before policy.
	TenantIdentity func(http.Handler) http.Handler
	StreamTokens   func(http.Handler) http.Handler
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

// bloemTenantIdentityGate wraps h with the tenant identity gate when one is wired.
func bloemTenantIdentityGate(deps Dependencies, h http.Handler) http.Handler {
	if deps.TenantIdentity != nil {
		return deps.TenantIdentity(h)
	}
	return h
}

// bloemCheckOperationPath accepts both surfaces registered through this
// package: Silo's /api/v2 and Bloem's native /api/bloem/v1 (see
// bloem_native_document.go). Both get the same checks, metadata, body limits
// and class gates -- the alternative was a second registry that would drift
// from this one.
func bloemCheckOperationPath(path string) error {
	if !strings.HasPrefix(path, Prefix+"/") && !strings.HasPrefix(path, BloemPrefix+"/") {
		return fmt.Errorf("path %q must start with %s/ or %s/", path, Prefix, BloemPrefix)
	}
	return nil
}

// bloemRegistrationLifecycle binds a setup/signup command to its
// Idempotency-Key lifecycle receipt.
func bloemRegistrationLifecycle(ctx context.Context, command handlers.RegistrationInput, key, routeID string, body []byte) handlers.RegistrationInput {
	command.Lifecycle = &handlers.RegistrationLifecycleInput{Key: key, Method: http.MethodPost, RouteID: routeID, Body: body}
	if r := requestFrom(ctx); r != nil {
		command.Lifecycle.Query = r.URL.Query()
	}
	return command
}
