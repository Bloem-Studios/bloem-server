package middleware

import (
	"context"
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
	tokens := auth.NewAdminContextTokenService("admin-context-middleware-test-secret")
	token, err := tokens.Mint(claims)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	membership := tenancy.Membership{
		ID: claims.MembershipID, OrganizationID: claims.OrganizationID, AccountID: claims.AccountID,
		Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: claims.SecurityRevision,
	}
	middleware := NewAdminContextMiddleware(tokens, resolver, &adminContextMembershipStoreStub{membership: membership}, platform)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/organization/overview?organization_id="+uuid.NewString(), nil)
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
		AccountID: 41, Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, MembershipID: membershipID,
		PolicyRevision: 7, SecurityRevision: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	membership := &adminContextMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 41, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}}
	resolver := &adminContextResolverStub{tenant: resolved}
	middleware := NewAdminContextMiddleware(tokens, resolver, membership, adminContextPlatformAuthorizerStub{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/organization/overview?organization_id="+uuid.NewString(), nil)
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
		AccountID: 41, Scope: auth.AdminScopeOrganization,
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
		AccountID: 41, Scope: auth.AdminScopeOrganization,
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
		AccountID: 41, Scope: auth.AdminScopeOrganization,
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
		AccountID: 41,
		Scope:     auth.AdminScopePlatform,
	}, &adminContextResolverStub{}, adminContextPlatformAuthorizerStub{})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_platform_authority") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
