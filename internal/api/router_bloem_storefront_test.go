package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// The storefront routes sit behind the platform gate: a platform context gets
// through the credential check to the account recheck (which, without a
// database, fails closed), an organization context is refused, and a missing
// credential is unauthenticated. The artwork route needs no credential.
func TestBloemPlatformStorefrontRoutesAreMountedBehindThePlatformGate(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-storefront-test-secret")
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	adminMW := apimw.NewAdminContextMiddleware(tokens,
		bloemAdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		bloemAdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		bloemAdminPlatformAuthorizerAllowedStub{}, bloemSessionValidator{})
	authMW := apimw.NewAuthMiddleware(bloemTokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}}, bloemSessionValidator{}, nil, nil)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, bloemRouteSurfaces{
		Storefront: bloemPlatformStorefrontHandlers{
			Tenants: &handlers.AdminTenantsHandler{}, TenantMembers: &handlers.AdminTenantMembersHandler{},
			Accounts: handlers.NewAdminHandler(nil, nil, nil), Announcements: &handlers.AdminAnnouncementsHandler{},
			Promotions: &handlers.AdminPromotionsHandler{}, Ambience: &handlers.AmbienceHandler{}, ServeAmbienceAssets: true,
			Domains: &handlers.BloemOrganizationDomainsHandler{},
		},
	})
	mint := func(claims auth.AdminContextClaims) string {
		claims.AccountID, claims.SessionID = 7, "session"
		claims.AccountIncarnationID = uuid.MustParse("11111111-2222-4333-8444-555555555555")
		token, err := tokens.Mint(claims)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	platform := mint(auth.AdminContextClaims{Scope: auth.AdminScopePlatform})
	organization := mint(auth.AdminContextClaims{Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11})
	tenant := "10000000-0000-0000-0000-000000000002"
	routes := []struct{ method, path string }{
		{http.MethodGet, "/admin/platform/tenants"},
		{http.MethodPost, "/admin/platform/tenants"},
		{http.MethodGet, "/admin/platform/tenants/" + tenant},
		{http.MethodDelete, "/admin/platform/tenants/" + tenant},
		{http.MethodPatch, "/admin/platform/tenants/" + tenant + "/limits"},
		{http.MethodPost, "/admin/platform/tenants/" + tenant + "/freeze"},
		{http.MethodPost, "/admin/platform/tenants/" + tenant + "/thaw"},
		{http.MethodGet, "/admin/platform/tenants/" + tenant + "/members"},
		{http.MethodPost, "/admin/platform/tenants/" + tenant + "/members"},
		{http.MethodPut, "/admin/platform/tenants/" + tenant + "/members/42"},
		{http.MethodPost, "/admin/platform/tenants/" + tenant + "/members/42/reset-password"},
		{http.MethodGet, "/admin/platform/tenants/" + tenant + "/members/42/profiles"},
		{http.MethodDelete, "/admin/platform/tenants/" + tenant + "/members/42/auth-sessions"},
		{http.MethodPost, "/admin/platform/accounts"},
		{http.MethodPut, "/admin/platform/accounts/42"},
		{http.MethodGet, "/admin/platform/accounts/42/profiles"},
		{http.MethodPut, "/admin/platform/accounts/42/profiles/p1"},
		{http.MethodDelete, "/admin/platform/accounts/42/devices/d1"},
		{http.MethodDelete, "/admin/platform/accounts/42/auth-sessions/s1"},
		{http.MethodGet, "/admin/platform/announcements"},
		{http.MethodDelete, "/admin/platform/announcements/a1"},
		{http.MethodGet, "/admin/platform/promotions/"},
		{http.MethodPost, "/admin/platform/ambience/assets"},
		{http.MethodGet, "/admin/platform/tenants/" + tenant + "/domains"},
		{http.MethodPost, "/admin/platform/tenants/" + tenant + "/domains"},
		{http.MethodPut, "/admin/platform/tenants/" + tenant + "/domains/main.domain.com/primary"},
		{http.MethodDelete, "/admin/platform/tenants/" + tenant + "/domains/main.domain.com"},
		{http.MethodGet, "/admin/platform/tenants/" + tenant + "/branding"},
		{http.MethodPut, "/admin/platform/tenants/" + tenant + "/branding"},
		{http.MethodPut, "/admin/platform/tenants/" + tenant + "/branding/assets/mark"},
		{http.MethodDelete, "/admin/platform/tenants/" + tenant + "/branding/assets/mark"},
	}
	send := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, NativeAPIPrefix+path, strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, route := range routes {
		if rec := send(route.method, route.path, platform); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
			t.Errorf("platform context %s %s = %d %s", route.method, route.path, rec.Code, rec.Body.String())
		}
		if rec := send(route.method, route.path, organization); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_platform_authority"`) {
			t.Errorf("organization context %s %s = %d %s", route.method, route.path, rec.Code, rec.Body.String())
		}
		if rec := send(route.method, route.path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("no credential %s %s = %d %s", route.method, route.path, rec.Code, rec.Body.String())
		}
	}
	// Public, so no 401: the handler without a registry answers unavailable.
	if rec := send(http.MethodGet, "/ambience/assets/abc", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("public artwork = %d %s", rec.Code, rec.Body.String())
	}
}
