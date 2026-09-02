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

type bloemTokenValidator struct{ claims *auth.Claims }

func (v bloemTokenValidator) ValidateToken(string) (*auth.Claims, error) { return v.claims, nil }

type bloemSessionValidator struct{}

func (bloemSessionValidator) IsValid(context.Context, string) (bool, error) { return true, nil }

func TestV2CapabilitiesMountedOutsideV1(t *testing.T) {
	router := NewRouter(Dependencies{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"api":"v2"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2MountedAndV10Absent(t *testing.T) {
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, nil, nil)

	v2 := httptest.NewRecorder()
	router.ServeHTTP(v2, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
	if v2.Code != http.StatusOK || !strings.Contains(v2.Body.String(), `"api":"v2"`) {
		t.Fatalf("v2 capabilities = %d %s", v2.Code, v2.Body.String())
	}

	v10 := httptest.NewRecorder()
	router.ServeHTTP(v10, httptest.NewRequest(http.MethodGet, "/api/v10/capabilities", nil))
	if v10.Code != http.StatusNotFound {
		t.Fatalf("v10 status = %d, want 404", v10.Code)
	}
}

func TestBloemOrganizationsRouteUsesAccountAuthentication(t *testing.T) {
	store := bloemOrganizationStoreStubForRouter{}
	system := handlers.NewBloemSystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		bloemSessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountBloemRoutes(router, system, nil, authMW, nil)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/organizations", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/organizations", nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer valid-token")
	authenticated := httptest.NewRecorder()
	router.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK || strings.TrimSpace(authenticated.Body.String()) != `{"organizations":[]}` {
		t.Fatalf("authenticated response = %d %s", authenticated.Code, authenticated.Body.String())
	}
}

func TestBloemAdminSessionRouteUsesAccountAuthentication(t *testing.T) {
	store := bloemOrganizationStoreStubForRouter{}
	system := handlers.NewBloemSystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		bloemSessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountBloemRoutes(router, system, nil, authMW, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/admin/session", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminGroupRequiresAdministrativeContextToken(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-context-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(
		tokens,
		bloemAdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11}},
		bloemAdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		bloemAdminPlatformAuthorizerStub{},
	)
	authMW := apimw.NewAuthMiddleware(
		bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		bloemSessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/organization/future-route", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error":"tenant_session_required"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformOrganizationRoutesAreMountedBehindPlatformContext(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-admin-platform-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, bloemAdminPlatformAuthorizerAllowedStub{})
	platform := handlers.NewBloemAdminPlatformHandler(nil, nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, nil, adminMW, platform)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/platform/organizations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAuthoritativeAccountPolicyRoutesAreMountedBehindPlatformContext(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-account-policy-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, bloemAdminPlatformAuthorizerAllowedStub{})
	authMW := apimw.NewAuthMiddleware(bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, bloemSessionValidator{}, nil, nil)
	policyHandler := handlers.NewAdminHandler(nil, nil, nil)
	policyHandler.SetAccountPolicies(bloemAccountPolicyReaderStub{})
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, policyHandler)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, NativeAPIPrefix + "/admin/platform/accounts/42/entitlement", ""},
		{http.MethodGet, NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/accounts/42/entitlement", ""},
		{http.MethodPost, NativeAPIPrefix + "/admin/platform/accounts/entitlement-snapshots", `{"account_ids":[42]}`},
		{http.MethodPost, NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/entitlement-snapshots", `{"account_ids":[42]}`},
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

func TestBloemPlatformEntitlementBulkRoutesUseExactMethodsWithoutRedirects(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	tokens := auth.NewAdminContextTokenService("router-entitlement-bulk-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, bloemAdminPlatformAuthorizerAllowedStub{})
	authMW := apimw.NewAuthMiddleware(bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, bloemSessionValidator{}, nil, nil)
	bulkStore := &bloemEntitlementBulkStoreStub{organization: tenancy.Organization{ID: organizationID, Status: tenancy.OrganizationActive}}
	handler := handlers.NewAdminHandler(nil, nil, nil)
	handler.SetPlatformEntitlementBulk(bulkStore, bulkStore, bulkStore, bloemAdminPlatformAuthorizerAllowedStub{}, nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, handler)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	path := NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/entitlement-cohorts"

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

type bloemAPIKeyValidatorStub struct{ key *models.APIKey }

func (s bloemAPIKeyValidatorStub) GetByKey(context.Context, string) (*models.APIKey, error) {
	return s.key, nil
}

func (bloemAPIKeyValidatorStub) UpdateLastUsed(context.Context, int64) error { return nil }

type bloemAPIKeyOwnerStub struct{ owner *models.User }

func (s bloemAPIKeyOwnerStub) GetByID(context.Context, int) (*models.User, error) {
	return s.owner, nil
}

func TestBloemPlatformEntitlementBulkScopedAPIKeyUsesExistingAuthentication(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	tokens := auth.NewAdminContextTokenService("router-entitlement-bulk-api-key-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, bloemAdminPlatformAuthorizerAllowedStub{})
	key := &models.APIKey{ID: 1, UserID: 7, Key: "sa_bulk", Scopes: []string{auth.ScopeAdminEntitlementsBulk}}
	authMW := apimw.NewAuthMiddleware(nil, nil, bloemAPIKeyValidatorStub{key: key}, bloemAPIKeyOwnerStub{owner: &models.User{ID: 7, Role: "admin", Enabled: true}})
	bulkStore := &bloemEntitlementBulkStoreStub{organization: tenancy.Organization{ID: organizationID, Status: tenancy.OrganizationActive}}
	handler := handlers.NewAdminHandler(nil, nil, nil)
	handler.SetPlatformEntitlementBulk(bulkStore, bulkStore, bulkStore, bloemAdminPlatformAuthorizerAllowedStub{}, nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, handler)
	path := NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/entitlement-cohorts"

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

func TestBloemAuthoritativeAccountPolicyReadsAcceptOnlyEntitlementBulkScopedAPIKey(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	tokens := auth.NewAdminContextTokenService("router-account-policy-api-key-test-secret")
	platformAuthorizer := bloemAdminPlatformAuthorizerAllowedStub{}
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, platformAuthorizer)
	key := &models.APIKey{ID: 1, UserID: 7, Key: "sa_bulk", Scopes: []string{auth.ScopeAdminEntitlementsBulk}}
	authMW := apimw.NewAuthMiddleware(nil, nil, bloemAPIKeyValidatorStub{key: key}, bloemAPIKeyOwnerStub{owner: &models.User{ID: 7, Role: "admin", Enabled: true}})
	handler := handlers.NewAdminHandler(nil, nil, nil)
	handler.SetAccountPolicies(bloemAccountPolicyReaderStub{})
	handler.SetPlatformEntitlementAuthorizer(platformAuthorizer)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, handler)

	reads := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, NativeAPIPrefix + "/admin/platform/accounts/42/entitlement", ""},
		{http.MethodGet, NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/accounts/42/entitlement", ""},
		{http.MethodPost, NativeAPIPrefix + "/admin/platform/accounts/entitlement-snapshots", `{"account_ids":[42]}`},
		{http.MethodPost, NativeAPIPrefix + "/admin/platform/organizations/" + organizationID.String() + "/entitlement-snapshots", `{"account_ids":[42]}`},
	}
	for _, item := range reads {
		req := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		req.Header.Set("Authorization", "Bearer sa_bulk")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("scoped read %s %s = %d, want 200: %s", item.method, item.path, rec.Code, rec.Body.String())
		}
	}

	key.Scopes = []string{auth.ScopeAdminUsers}
	wrongScope := httptest.NewRequest(http.MethodGet, reads[0].path, nil)
	wrongScope.Header.Set("Authorization", "Bearer sa_bulk")
	wrongScopeRec := httptest.NewRecorder()
	router.ServeHTTP(wrongScopeRec, wrongScope)
	if wrongScopeRec.Code != http.StatusForbidden {
		t.Fatalf("wrong-scope read = %d, want 403: %s", wrongScopeRec.Code, wrongScopeRec.Body.String())
	}

	key.Scopes = []string{auth.ScopeAdminEntitlementsBulk}
	mutation := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/accounts/42/entitlement/apply", strings.NewReader(`{}`))
	mutation.Header.Set("Authorization", "Bearer sa_bulk")
	mutationRec := httptest.NewRecorder()
	router.ServeHTTP(mutationRec, mutation)
	if mutationRec.Code != http.StatusUnauthorized {
		t.Fatalf("account policy mutation = %d, want 401: %s", mutationRec.Code, mutationRec.Body.String())
	}
}

func TestBloemAdminPeopleRoutesAreMountedBehindOrganizationContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-people-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(
		tokens,
		bloemAdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		bloemAdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		bloemAdminPlatformAuthorizerStub{},
	)
	people := handlers.NewBloemAdminPeopleHandler(nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, nil, adminMW, people)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/organization/people", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}

	previewReq := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/admin/organization/people/policy-previews", strings.NewReader(`{"selection_token":"signed","command":{"kind":"apply_entitlement_template","template_key":"premium","template_revision":1}}`))
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
		{http.MethodGet, NativeAPIPrefix + "/admin/organization/entitlement-cohorts", ""},
		{http.MethodGet, NativeAPIPrefix + "/admin/organization/entitlement-cohorts/20000000-0000-0000-0000-000000000002", ""},
		{http.MethodPost, NativeAPIPrefix + "/admin/organization/people/policy-jobs", `{}`},
		{http.MethodGet, NativeAPIPrefix + "/admin/organization/people/policy-jobs/job-1", ""},
		{http.MethodPost, NativeAPIPrefix + "/admin/organization/people/policy-jobs/job-1/cancel", `{}`},
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

func TestBloemAdminOrganizationProjectionRoutesAreMountedWithoutPolicyMutationRoutes(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("router-admin-organization-test-secret")
	adminMW := apimw.NewAdminContextMiddleware(tokens,
		bloemAdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		bloemAdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}}, bloemAdminPlatformAuthorizerStub{})
	organization := handlers.NewBloemAdminOrganizationHandler(nil, nil, nil, nil)
	explain := handlers.NewBloemPolicyExplainHandler(nil)
	authMW := apimw.NewAuthMiddleware(bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, bloemSessionValidator{}, nil, nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, organization, explain)
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11, EffectiveAuthority: "organization_admin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{NativeAPIPrefix + "/admin/organization/overview", NativeAPIPrefix + "/admin/organization/groups", NativeAPIPrefix + "/admin/organization/libraries", NativeAPIPrefix + "/admin/organization/invitations", NativeAPIPrefix + "/admin/organization/policy-decisions"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{NativeAPIPrefix + "/admin/organization/policy-documents", NativeAPIPrefix + "/admin/organization/policy-validate", NativeAPIPrefix + "/admin/organization/policy-activate"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("forbidden mutation route %s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
}

type bloemAdminTenantResolverStub struct {
	tenant tenancy.Context
}

func (s bloemAdminTenantResolverStub) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return s.tenant, nil
}

type bloemAdminMembershipStoreStub struct {
	membership tenancy.Membership
}

func (s bloemAdminMembershipStoreStub) GetMembership(context.Context, int, uuid.UUID) (tenancy.Membership, error) {
	return s.membership, nil
}

type bloemAdminPlatformAuthorizerStub struct{}

func (bloemAdminPlatformAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return false, nil
}

type bloemAdminPlatformAuthorizerAllowedStub struct{}

func (bloemAdminPlatformAuthorizerAllowedStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return true, nil
}

type bloemOrganizationStoreStubForRouter struct{}

func (bloemOrganizationStoreStubForRouter) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return []tenancy.Membership{}, nil
}

func (bloemOrganizationStoreStubForRouter) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}

type bloemAccountPolicyReaderStub struct{}

func (bloemAccountPolicyReaderStub) GetAccountPolicy(context.Context, uuid.UUID, int) (entitlements.AccountPolicySnapshot, error) {
	return entitlements.AccountPolicySnapshot{ObservedAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), AccountID: 42}, nil
}

type bloemEntitlementBulkStoreStub struct {
	organization tenancy.Organization
}

func (s *bloemEntitlementBulkStoreStub) ListCohorts(context.Context, uuid.UUID, bool) ([]entitlements.CohortRevision, error) {
	return []entitlements.CohortRevision{}, nil
}

func (s *bloemEntitlementBulkStoreStub) GetCohort(context.Context, uuid.UUID, uuid.UUID) (entitlements.CohortRevision, error) {
	return entitlements.CohortRevision{}, entitlements.ErrCohortNotFound
}

func (s *bloemEntitlementBulkStoreStub) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	return s.organization, nil
}

func (s *bloemEntitlementBulkStoreStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return s.organization, nil
}

func (s *bloemEntitlementBulkStoreStub) CreateSelection(context.Context, uuid.UUID, adminpeople.Filter) (adminpeople.Selection, error) {
	return adminpeople.Selection{}, nil
}

func (s *bloemEntitlementBulkStoreStub) PreviewPolicy(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand) (adminpeople.PolicyPreview, error) {
	return adminpeople.PolicyPreview{}, nil
}

func (s *bloemEntitlementBulkStoreStub) PreviewPolicyForScope(context.Context, uuid.UUID, int, string, adminpeople.PolicyCommand, adminpeople.PolicyOperationScope) (adminpeople.PolicyPreview, error) {
	return adminpeople.PolicyPreview{}, nil
}

func (s *bloemEntitlementBulkStoreStub) EnqueuePolicyBulk(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *bloemEntitlementBulkStoreStub) EnqueuePolicyBulkForScope(context.Context, uuid.UUID, int, adminpeople.PolicyBulkAction, adminpeople.PolicyOperationScope) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *bloemEntitlementBulkStoreStub) GetPolicyBulkJob(context.Context, uuid.UUID, string) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (s *bloemEntitlementBulkStoreStub) CancelPolicyBulkJob(context.Context, uuid.UUID, int, string) (adminpeople.BulkResult, error) {
	return adminpeople.BulkResult{}, nil
}

func (bloemAccountPolicyReaderStub) GetAccountPolicies(context.Context, uuid.UUID, []int) ([]entitlements.AccountPolicySnapshotResult, time.Time, error) {
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	return []entitlements.AccountPolicySnapshotResult{{AccountID: 42, Snapshot: &entitlements.AccountPolicySnapshot{ObservedAt: observedAt, AccountID: 42}}}, observedAt, nil
}
