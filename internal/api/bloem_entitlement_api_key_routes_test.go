package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
)

var errRouterTemplateStoreUnused = errors.New("not used by this test")

type routerTemplateStoreStub struct{}

func (routerTemplateStoreStub) List(context.Context, bool) ([]entitlements.Template, error) {
	return []entitlements.Template{{Key: "standard", Name: "Standard", Revision: 3, Enabled: true}}, nil
}
func (routerTemplateStoreStub) Get(context.Context, string, int64) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Latest(context.Context, string) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) ListRevisions(context.Context, string) ([]entitlements.Template, error) {
	return nil, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Create(context.Context, entitlements.CreateTemplateInput) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Revise(context.Context, string, int64, entitlements.ReviseTemplateInput) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Clone(context.Context, string, int64, entitlements.CreateTemplateInput) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Archive(context.Context, string, int64) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) ApplyTemplate(context.Context, uuid.UUID, string, int64, bool) (entitlements.ApplyResult, error) {
	return entitlements.ApplyResult{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) ApplyDefaultAccountTemplate(_ context.Context, _ int, key string, revision int64, dryRun bool) (entitlements.ApplyResult, error) {
	return entitlements.ApplyResult{TemplateKey: key, TemplateRevision: revision, DryRun: dryRun}, nil
}
func (routerTemplateStoreStub) GetOrganizationEntitlement(context.Context, uuid.UUID) (entitlements.OrganizationEntitlement, error) {
	return entitlements.OrganizationEntitlement{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) GetDefaultAccountEntitlement(context.Context, int) (entitlements.AccountEntitlement, error) {
	return entitlements.AccountEntitlement{}, errRouterTemplateStoreUnused
}

// A storefront's API key reaches the template catalog and per-account preview
// and apply through the platform-credential group, while template authoring
// and every other platform route keep requiring an admin session.
func TestBloemTemplateCatalogAndAccountApplyAcceptStorefrontAPIKey(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("router-template-api-key-test-secret")
	platformAdmins := bloemAdminPlatformAuthorizerAllowedStub{}
	adminMW := apimw.NewAdminContextMiddleware(tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, platformAdmins, bloemSessionValidator{})
	key := &models.APIKey{ID: 1, UserID: 7, Key: "sa_garden"}
	authMW := apimw.NewAuthMiddleware(nil, nil, bloemAPIKeyValidatorStub{key: key}, bloemAPIKeyOwnerStub{owner: &models.User{ID: 7, Role: "admin", Enabled: true}})
	templates := handlers.NewEntitlementTemplatesHandler(routerTemplateStoreStub{}, []byte("router-template-secret"))
	templates.SetPlatformAdminAuthorizer(platformAdmins)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, bloemRouteSurfaces{Entitlement: templates})

	serve := func(method, path, body, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, NativeAPIPrefix+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+bearer)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	allowed := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/platform/entitlement-templates?status=enabled", ""},
		{http.MethodPost, "/admin/platform/accounts/42/entitlement/dry-run", `{"template_key":"standard","template_revision":3}`},
	}
	for _, scopes := range [][]string{nil, {auth.ScopeAdminEntitlementsAccounts}} {
		key.Scopes = scopes
		for _, item := range allowed {
			if rec := serve(item.method, item.path, item.body, "sa_garden"); rec.Code != http.StatusOK {
				t.Fatalf("key scopes %v: %s %s = %d %s, want 200", scopes, item.method, item.path, rec.Code, rec.Body.String())
			}
		}
	}

	key.Scopes = []string{auth.ScopeAdminUsers}
	if rec := serve(http.MethodGet, "/admin/platform/entitlement-templates", "", "sa_garden"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong-scope catalog = %d %s, want 403", rec.Code, rec.Body.String())
	}
	key.Scopes = nil
	if rec := serve(http.MethodPost, "/admin/platform/entitlement-templates", `{"key":"new","name":"New"}`, "sa_garden"); rec.Code != http.StatusForbidden {
		t.Fatalf("template creation with API key = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodGet, "/admin/platform/entitlement-templates/standard", "", "sa_garden"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("single template read with API key = %d %s, want 401", rec.Code, rec.Body.String())
	}

	session, err := tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), SessionID: "session", Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	if rec := serve(http.MethodGet, "/admin/platform/entitlement-templates", "", session); rec.Code != http.StatusOK {
		t.Fatalf("admin session catalog = %d %s, want 200", rec.Code, rec.Body.String())
	}
}
