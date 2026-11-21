package apiv2

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type tenantGateResolver struct{ err error }

func (r tenantGateResolver) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return tenancy.Context{}, r.err
}

// The tenant identity gate runs inside the v2 gate chain, so its refusals
// reach a Silo app through the chain's translation. A tenant lookup that
// could not answer is Silo's retryable 503 dependency_unavailable with
// Retry-After, carrying Bloem's code in X-Bloem-Denial, not an
// internal_error 500 the app treats as a server fault. An account with no
// usable membership stays a 401 authentication_required.
func TestTenantGateRefusalsAreSiloProblems(t *testing.T) {
	claims := map[string]*auth.Claims{"access": {UserID: 1, Role: "user", SessionID: "live", TokenType: auth.TokenTypeAccess}}
	serve := func(err error) (int, string, http.Header) {
		deps := pilotDeps(nil, nil)
		deps.Auth = apimw.NewAuthMiddleware(fakeTokens{claims}, fakeSessions{map[string]string{"live": "user"}}, nil, nil)
		deps.TenantIdentity = apimw.NewTenantMiddleware(tenantGateResolver{err: err}).ResolveNative
		rec := do(t, NewHandler(deps), http.MethodGet, Prefix+"/profiles", "", bearer("access"))
		return rec.Code, rec.Body.String(), rec.Header()
	}

	code, body, header := serve(tenancy.ErrTenantUnavailable)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "/"+TypeDependencyUnavailable.ID) ||
		header.Get("Retry-After") == "" || header.Get("X-Bloem-Denial") != "tenant_unavailable" {
		t.Fatalf("tenant lookup down: %d %s headers %v; want 503 dependency_unavailable with Retry-After and X-Bloem-Denial", code, body, header)
	}
	if strings.Contains(body, "tenant_unavailable") {
		t.Fatalf("Bloem's code leaked into the problem body: %s", body)
	}

	code, body, _ = serve(tenancy.ErrTenantNotFoundOrHidden)
	if code != http.StatusUnauthorized || !strings.Contains(body, "/"+TypeAuthenticationRequired.ID) {
		t.Fatalf("no membership: %d %s; want 401 authentication_required", code, body)
	}
}
