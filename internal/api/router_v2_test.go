package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
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

func TestV2AuthoritativeAccountPolicyRoutesAreMountedBehindPlatformContext(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-account-policy-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, v2AdminTenantResolverStub{}, v2AdminMembershipStoreStub{}, v2AdminPlatformAuthorizerAllowedStub{})
	authMW := apimw.NewAuthMiddleware(v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, v2SessionValidator{}, nil, nil)
	policyHandler := handlers.NewAdminHandler(nil, nil, nil)
	policyHandler.SetAccountPolicies(v2AccountPolicyReaderStub{})
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, authMW, adminMW, policyHandler)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v2/admin/platform/accounts/42/entitlement", ""},
		{http.MethodGet, "/api/v2/admin/platform/organizations/" + organizationID.String() + "/accounts/42/entitlement", ""},
		{http.MethodPost, "/api/v2/admin/platform/accounts/entitlement-snapshots", `{"account_ids":[42]}`},
		{http.MethodPost, "/api/v2/admin/platform/organizations/" + organizationID.String() + "/entitlement-snapshots", `{"account_ids":[42]}`},
	}
	for _, item := range requests {
		req := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", item.method, item.path, rec.Code, rec.Body.String())
		}
	}
}

func TestV2PlatformEntitlementBulkRoutesUseExactMethodsWithoutRedirects(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	tokens := auth.NewAdminContextTokenService("router-entitlement-bulk-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, v2AdminTenantResolverStub{}, v2AdminMembershipStoreStub{}, v2AdminPlatformAuthorizerAllowedStub{})
	authMW := apimw.NewAuthMiddleware(v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, v2SessionValidator{}, nil, nil)
	bulkStore := &v2EntitlementBulkStoreStub{organization: tenancy.Organization{ID: organizationID, Status: tenancy.OrganizationActive}}
	handler := handlers.NewAdminHandler(nil, nil, nil)
	handler.SetPlatformEntitlementBulk(bulkStore, bulkStore, bulkStore, v2AdminPlatformAuthorizerAllowedStub{}, nil)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, authMW, adminMW, handler)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/admin/platform/organizations/" + organizationID.String() + "/entitlement-cohorts"

	for _, test := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: path, want: http.StatusOK},
		{method: http.MethodDelete, path: path, want: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: path + "/", want: http.StatusNotFound},
	} {
		req := httptest.NewRequest(test.method, test.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("%s %s = %d, want %d: %s", test.method, test.path, rec.Code, test.want, rec.Body.String())
		}
		if rec.Code >= 300 && rec.Code < 400 {
			t.Fatalf("%s %s redirected with %d", test.method, test.path, rec.Code)
		}
	}
}

type v2APIKeyValidatorStub struct{ key *models.APIKey }

func (s v2APIKeyValidatorStub) GetByKey(context.Context, string) (*models.APIKey, error) {
	return s.key, nil
}

func (v2APIKeyValidatorStub) UpdateLastUsed(context.Context, int64) error { return nil }

type v2APIKeyOwnerStub struct{ owner *models.User }

func (s v2APIKeyOwnerStub) GetByID(context.Context, int) (*models.User, error) { return s.owner, nil }

func TestV2PlatformEntitlementBulkScopedAPIKeyUsesExistingAuthentication(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	tokens := auth.NewAdminContextTokenService("router-entitlement-bulk-api-key-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, v2AdminTenantResolverStub{}, v2AdminMembershipStoreStub{}, v2AdminPlatformAuthorizerAllowedStub{})
	key := &models.APIKey{ID: 1, UserID: 7, Key: "sa_bulk", Scopes: []string{auth.ScopeAdminEntitlementsBulk}}
	authMW := apimw.NewAuthMiddleware(nil, nil, v2APIKeyValidatorStub{key: key}, v2APIKeyOwnerStub{owner: &models.User{ID: 7, Role: "admin", Enabled: true}})
	bulkStore := &v2EntitlementBulkStoreStub{organization: tenancy.Organization{ID: organizationID, Status: tenancy.OrganizationActive}}
	handler := handlers.NewAdminHandler(nil, nil, nil)
	handler.SetPlatformEntitlementBulk(bulkStore, bulkStore, bulkStore, v2AdminPlatformAuthorizerAllowedStub{}, nil)
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, authMW, adminMW, handler)
	path := "/api/v2/admin/platform/organizations/" + organizationID.String() + "/entitlement-cohorts"

	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer sa_bulk")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("scoped API key response = %d %s", recorder.Code, recorder.Body.String())
	}

	key.Scopes = []string{auth.ScopeAdminUsers}
	request = httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer sa_bulk")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong-scope API key response = %d, want 403: %s", recorder.Code, recorder.Body.String())
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

	previewReq := httptest.NewRequest(http.MethodPost, "/api/v2/admin/organization/people/policy-previews", strings.NewReader(`{"selection_token":"signed","command":{"kind":"apply_entitlement_template","template_key":"premium","template_revision":1}}`))
	previewReq.Header.Set("Authorization", "Bearer "+token)
	previewRec := httptest.NewRecorder()
	router.ServeHTTP(previewRec, previewReq)
	if previewRec.Code != http.StatusServiceUnavailable || !strings.Contains(previewRec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("policy preview route = %d %s", previewRec.Code, previewRec.Body.String())
	}

	for _, route := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v2/admin/organization/people/policy-jobs", `{}`},
		{http.MethodGet, "/api/v2/admin/organization/people/policy-jobs/job-1", ""},
		{http.MethodPost, "/api/v2/admin/organization/people/policy-jobs/job-1/cancel", `{}`},
	} {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
			t.Fatalf("policy job route %s %s = %d %s", route.method, route.path, rec.Code, rec.Body.String())
		}
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

type v2AccountPolicyReaderStub struct{}

func (v2AccountPolicyReaderStub) GetAccountPolicy(context.Context, uuid.UUID, int) (entitlements.AccountPolicySnapshot, error) {
	return entitlements.AccountPolicySnapshot{ObservedAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), AccountID: 42}, nil
}

type v2EntitlementBulkStoreStub struct {
	organization tenancy.Organization
}

func (s *v2EntitlementBulkStoreStub) ListCohorts(context.Context, uuid.UUID, bool) ([]entitlements.CohortRevision, error) {
	return []entitlements.CohortRevision{}, nil
}

func (s *v2EntitlementBulkStoreStub) GetCohort(context.Context, uuid.UUID, uuid.UUID) (entitlements.CohortRevision, error) {
	return entitlements.CohortRevision{}, entitlements.ErrCohortNotFound
}

func (s *v2EntitlementBulkStoreStub) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	return s.organization, nil
}

func (s *v2EntitlementBulkStoreStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return s.organization, nil
}

func (s *v2EntitlementBulkStoreStub) CreateSelection(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Selection, error) {
	return adminpeople.Selection{}, nil
}

func (s *v2EntitlementBulkStoreStub) PreviewPolicy(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand) (adminpeople.PolicyPreview, error) {
	return adminpeople.PolicyPreview{}, nil
}

func (s *v2EntitlementBulkStoreStub) PreviewPolicyForScope(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand, adminpeople.PolicyOperationScope) (adminpeople.PolicyPreview, error) {
	return adminpeople.PolicyPreview{}, nil
}

func (s *v2EntitlementBulkStoreStub) EnqueuePolicyBulk(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *v2EntitlementBulkStoreStub) EnqueuePolicyBulkForScope(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction, adminpeople.PolicyOperationScope) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *v2EntitlementBulkStoreStub) GetPolicyBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *v2EntitlementBulkStoreStub) CancelPolicyBulkJob(context.Context, uuid.UUID, int, string) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (v2AccountPolicyReaderStub) GetAccountPolicies(context.Context, uuid.UUID, []int) ([]entitlements.AccountPolicySnapshotResult, time.Time, error) {
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	return []entitlements.AccountPolicySnapshotResult{{AccountID: 42, Snapshot: &entitlements.AccountPolicySnapshot{ObservedAt: observedAt, AccountID: 42}}}, observedAt, nil
}
