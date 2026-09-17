package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type adminContextResolverStub struct {
	tenant            tenancy.Context
	err               error
	gotAccountID      int
	gotOrganizationID *uuid.UUID
	gotLegacy         bool
}

func (s *adminContextResolverStub) Resolve(_ context.Context, accountID int, organizationID *uuid.UUID, legacy bool) (tenancy.Context, error) {
	s.gotAccountID = accountID
	s.gotOrganizationID = organizationID
	s.gotLegacy = legacy
	return s.tenant, s.err
}

type adminContextPlatformAuthorizerStub struct {
	allowed bool
	err     error
}

func (s adminContextPlatformAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return s.allowed, s.err
}

// ResolveOperator reports an enabled account of the requested incarnation.
func (s adminContextPlatformAuthorizerStub) ResolveOperator(_ context.Context, accountID int, incarnationID uuid.UUID) (auth.OperatorAuthority, error) {
	if s.err != nil {
		return auth.OperatorAuthority{}, s.err
	}
	return auth.OperatorAuthority{AccountID: accountID, AccountIncarnationID: incarnationID, PlatformAdmin: s.allowed}, nil
}

// legacyPlatformAuthorizerStub lacks the operator capability entirely.
type legacyPlatformAuthorizerStub struct{}

func (legacyPlatformAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return true, nil
}

type adminContextMembershipStoreStub struct {
	membership        tenancy.Membership
	err               error
	gotAccountID      int
	gotOrganizationID uuid.UUID
}

func (s *adminContextMembershipStoreStub) GetMembership(_ context.Context, accountID int, organizationID uuid.UUID) (tenancy.Membership, error) {
	s.gotAccountID = accountID
	s.gotOrganizationID = organizationID
	return s.membership, s.err
}

func performAdminContextRequest(t *testing.T, claims auth.AdminContextClaims, resolver *adminContextResolverStub, platform auth.PlatformAdminAuthorizer) *httptest.ResponseRecorder {
	t.Helper()
	if claims.SessionID == "" {
		claims.SessionID = "login-session"
	}
	return performAdminContextRequestWithSessions(t, claims, resolver, platform, fixedSessionValidator{valid: true})
}

func performAdminContextRequestWithSessions(t *testing.T, claims auth.AdminContextClaims, resolver *adminContextResolverStub, platform auth.PlatformAdminAuthorizer, sessions SessionValidator) *httptest.ResponseRecorder {
	t.Helper()
	tokens := auth.NewAdminContextTokenService("admin-context-middleware-test-secret")
	token, err := tokens.Mint(claims)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	membership := tenancy.Membership{
		ID: claims.MembershipID, OrganizationID: claims.OrganizationID, AccountID: claims.AccountID,
		Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: claims.SecurityRevision,
	}
	middleware := NewAdminContextMiddleware(tokens, resolver, &adminContextMembershipStoreStub{membership: membership}, platform, sessions)
	req := httptest.NewRequest(http.MethodGet, "/api/bloem/v1/admin/organization/overview?organization_id="+uuid.NewString(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Organization-Id", uuid.NewString())
	rec := httptest.NewRecorder()
	middleware.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)
	return rec
}

func TestAdminContextMiddlewareInjectsOnlyResolvedOrganizationContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	resolved := tenancy.Context{
		AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 7, SecurityRevision: 11,
	}
	tokens := auth.NewAdminContextTokenService("admin-context-middleware-test-secret")
	token, err := tokens.Mint(auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), SessionID: "login-session", Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 7, SecurityRevision: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	membership := &adminContextMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 41, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}}
	resolver := &adminContextResolverStub{tenant: resolved}
	middleware := NewAdminContextMiddleware(tokens, resolver, membership, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
	request := httptest.NewRequest(http.MethodGet, "/api/bloem/v1/admin/organization/overview?organization_id="+uuid.NewString(), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Organization-Id", uuid.NewString())
	rec := httptest.NewRecorder()
	middleware.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := tenancy.FromContext(r.Context())
		if !ok || tenant != resolved {
			t.Fatalf("tenant = %#v, %v; want %#v, true", tenant, ok, resolved)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, request)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if resolver.gotAccountID != 41 || resolver.gotOrganizationID == nil || *resolver.gotOrganizationID != organizationID || resolver.gotLegacy {
		t.Fatalf("resolver input = account %d organization %v legacy %v", resolver.gotAccountID, resolver.gotOrganizationID, resolver.gotLegacy)
	}
	if membership.gotAccountID != 41 || membership.gotOrganizationID != organizationID {
		t.Fatalf("membership lookup = account %d organization %s", membership.gotAccountID, membership.gotOrganizationID)
	}
}

func TestAdminContextMiddlewareRejectsStaleOrganizationRevision(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	claims := auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 7, SecurityRevision: 11,
	}
	resolver := &adminContextResolverStub{tenant: tenancy.Context{
		AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 8, SecurityRevision: 11,
	}}
	rec := performAdminContextRequest(t, claims, resolver, adminContextPlatformAuthorizerStub{})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorization_state_stale") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminContextMiddlewareRejectsForeignOrganizationMembership(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	claims := auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, MembershipID: uuid.New(),
		PolicyRevision: 7, SecurityRevision: 11,
	}
	resolver := &adminContextResolverStub{tenant: tenancy.Context{
		AccountID: 41, OrganizationID: organizationID, MembershipID: uuid.New(),
		PolicyRevision: 7, SecurityRevision: 11,
	}}
	rec := performAdminContextRequest(t, claims, resolver, adminContextPlatformAuthorizerStub{})
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorization_state_stale") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminContextMiddlewareRejectsSuspendedOrganization(t *testing.T) {
	claims := auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopeOrganization,
		OrganizationID: uuid.New(), MembershipID: uuid.New(),
		PolicyRevision: 7, SecurityRevision: 11,
	}
	rec := performAdminContextRequest(t, claims, &adminContextResolverStub{err: tenancy.ErrTenantSuspended}, adminContextPlatformAuthorizerStub{})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "organization_suspended") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminContextMiddlewareRejectsLostPlatformAuthority(t *testing.T) {
	rec := performAdminContextRequest(t, auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		Scope: auth.AdminScopePlatform,
	}, &adminContextResolverStub{}, adminContextPlatformAuthorizerStub{})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_platform_authority") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminContextMiddlewareRejectsIneligibleOperator(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	resolver := &adminContextResolverStub{tenant: tenancy.Context{
		AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 7, SecurityRevision: 11,
	}}
	scopes := map[string]auth.AdminContextClaims{
		"platform": {AccountID: 41, AccountIncarnationID: incarnation, Scope: auth.AdminScopePlatform},
		"organization": {
			AccountID: 41, AccountIncarnationID: incarnation, Scope: auth.AdminScopeOrganization,
			OrganizationID: organizationID, MembershipID: membershipID,
			PolicyRevision: 7, SecurityRevision: 11, EffectiveAuthority: "organization_admin",
		},
	}
	for name, claims := range scopes {
		t.Run(name+"/ineligible", func(t *testing.T) {
			rec := performAdminContextRequest(t, claims, resolver, adminContextPlatformAuthorizerStub{allowed: true, err: auth.ErrOperatorIneligible})
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorization_state_stale") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
		t.Run(name+"/unavailable", func(t *testing.T) {
			rec := performAdminContextRequest(t, claims, resolver, adminContextPlatformAuthorizerStub{allowed: true, err: errors.New("database down")})
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "tenant_unavailable") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
		t.Run(name+"/missing_capability", func(t *testing.T) {
			rec := performAdminContextRequest(t, claims, resolver, legacyPlatformAuthorizerStub{})
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "tenant_unavailable") {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

type failingSessionValidator struct{}

func (failingSessionValidator) IsValid(context.Context, string) (bool, error) {
	return false, errors.New("session store down")
}

// A context lives no longer than the login session it was exchanged from:
// revoking that session ends the context on its next request, and a context
// that names no session, or a middleware that cannot check one, is refused.
func TestAdminContextMiddlewareEnforcesLoginSession(t *testing.T) {
	platform := auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		SessionID: "login-session", Scope: auth.AdminScopePlatform,
	}
	sessionless := platform
	sessionless.SessionID = ""
	allowed := adminContextPlatformAuthorizerStub{allowed: true}

	if rec := performAdminContextRequestWithSessions(t, platform, &adminContextResolverStub{}, allowed, fixedSessionValidator{valid: true}); rec.Code != http.StatusNoContent {
		t.Fatalf("live session response = %d %s", rec.Code, rec.Body.String())
	}

	cases := map[string]struct {
		claims   auth.AdminContextClaims
		sessions SessionValidator
		status   int
		code     string
	}{
		"revoked session":    {platform, fixedSessionValidator{valid: false}, http.StatusUnauthorized, "authorization_state_stale"},
		"session store down": {platform, failingSessionValidator{}, http.StatusServiceUnavailable, "tenant_unavailable"},
		"unwired validator":  {platform, nil, http.StatusServiceUnavailable, "tenant_unavailable"},
		"no session bound":   {sessionless, fixedSessionValidator{valid: true}, http.StatusUnauthorized, "tenant_session_required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := performAdminContextRequestWithSessions(t, tc.claims, &adminContextResolverStub{}, allowed, tc.sessions)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
