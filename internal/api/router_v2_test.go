package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type v2TokenValidator struct{ claims *auth.Claims }

func (v v2TokenValidator) ValidateToken(string) (*auth.Claims, error) { return v.claims, nil }

type v2SessionValidator struct{}

func (v2SessionValidator) IsValid(context.Context, string) (bool, error) { return true, nil }

func TestV2CapabilitiesMountedOutsideV1(t *testing.T) {
	router := NewRouter(Dependencies{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"api":"v2"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2MountedAndV10Absent(t *testing.T) {
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, nil, nil)

	v2 := httptest.NewRecorder()
	router.ServeHTTP(v2, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if v2.Code != http.StatusOK || !strings.Contains(v2.Body.String(), `"api":"v2"`) {
		t.Fatalf("v2 capabilities = %d %s", v2.Code, v2.Body.String())
	}

	v10 := httptest.NewRecorder()
	router.ServeHTTP(v10, httptest.NewRequest(http.MethodGet, "/api/v10/capabilities", nil))
	if v10.Code != http.StatusNotFound {
		t.Fatalf("v10 status = %d, want 404", v10.Code)
	}
}

func TestV2OrganizationsRouteUsesAccountAuthentication(t *testing.T) {
	store := v2OrganizationStoreStubForRouter{}
	system := handlers.NewV2SystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		v2SessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountV2Routes(router, system, nil, authMW, nil)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer valid-token")
	authenticated := httptest.NewRecorder()
	router.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK || strings.TrimSpace(authenticated.Body.String()) != `{"organizations":[]}` {
		t.Fatalf("authenticated response = %d %s", authenticated.Code, authenticated.Body.String())
	}
}

func TestV2AdminSessionRouteUsesAccountAuthentication(t *testing.T) {
	store := v2OrganizationStoreStubForRouter{}
	system := handlers.NewV2SystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		v2SessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountV2Routes(router, system, nil, authMW, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminGroupRequiresAdministrativeContextToken(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-context-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(
		tokens,
		v2AdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11}},
		v2AdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		v2AdminPlatformAuthorizerStub{},
	)
	authMW := apimw.NewAuthMiddleware(
		v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		v2SessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, authMW, adminMW)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/organization/future-route", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error":"tenant_session_required"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminPlatformOrganizationRoutesAreMountedBehindPlatformContext(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-admin-platform-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, v2AdminTenantResolverStub{}, v2AdminMembershipStoreStub{}, v2AdminPlatformAuthorizerAllowedStub{})
	platform := handlers.NewV2AdminPlatformHandler(nil, nil)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, nil, adminMW, platform)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/platform/organizations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminPeopleRoutesAreMountedBehindOrganizationContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-people-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(
		tokens,
		v2AdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		v2AdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		v2AdminPlatformAuthorizerStub{},
	)
	people := handlers.NewV2AdminPeopleHandler(nil)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, nil, adminMW, people)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/organization/people", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminOrganizationProjectionRoutesAreMountedWithoutPolicyMutationRoutes(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-organization-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens,
		v2AdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		v2AdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}}, v2AdminPlatformAuthorizerStub{})
	organization := handlers.NewV2AdminOrganizationHandler(nil, nil, nil, nil)
	explain := handlers.NewV2PolicyExplainHandler(nil)
	authMW := apimw.NewAuthMiddleware(v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, v2SessionValidator{}, nil, nil)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, authMW, adminMW, organization, explain)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11, EffectiveAuthority: "organization_admin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/v2/admin/organization/overview", "/api/v2/admin/organization/groups", "/api/v2/admin/organization/libraries", "/api/v2/admin/organization/invitations", "/api/v2/admin/organization/policy-decisions"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{"/api/v2/admin/organization/policy-documents", "/api/v2/admin/organization/policy-validate", "/api/v2/admin/organization/policy-activate"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("forbidden mutation route %s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
}

type v2AdminTenantResolverStub struct {
	tenant tenancy.Context
}

func (s v2AdminTenantResolverStub) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return s.tenant, nil
}

type v2AdminMembershipStoreStub struct {
	membership tenancy.Membership
}

func (s v2AdminMembershipStoreStub) GetMembership(context.Context, int, uuid.UUID) (tenancy.Membership, error) {
	return s.membership, nil
}

type v2AdminPlatformAuthorizerStub struct{}

func (v2AdminPlatformAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return false, nil
}

type v2AdminPlatformAuthorizerAllowedStub struct{}

func (v2AdminPlatformAuthorizerAllowedStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return true, nil
}

type v2OrganizationStoreStubForRouter struct{}

func (v2OrganizationStoreStubForRouter) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return []tenancy.Membership{}, nil
}

func (v2OrganizationStoreStubForRouter) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}
