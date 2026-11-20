package api

import (
	"context"
	"encoding/json"
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
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

var errRouterTemplateStoreUnused = errors.New("not used by this test")

type routerTemplateStoreStub struct{}

func (routerTemplateStoreStub) List(context.Context, bool) ([]entitlements.Template, error) {
	return []entitlements.Template{{Key: "standard", Name: "Standard", Revision: 3, Enabled: true}}, nil
}
func (routerTemplateStoreStub) Get(context.Context, string, int64) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Latest(_ context.Context, key string) (entitlements.Template, error) {
	if key != "standard" {
		return entitlements.Template{}, entitlements.ErrTemplateNotFound
	}
	return entitlements.Template{Key: "standard", Name: "Standard", Revision: 3, Enabled: true}, nil
}

// ListRevisions answers newest first, as the store does.
func (routerTemplateStoreStub) ListRevisions(_ context.Context, key string) ([]entitlements.Template, error) {
	if key != "standard" {
		return nil, entitlements.ErrTemplateNotFound
	}
	return []entitlements.Template{
		{Key: "standard", Name: "Standard", Revision: 3, Enabled: true, Policy: entitlements.Policy{PlaybackAllowed: true, MaxStreams: 3}},
		{Key: "standard", Name: "Standard", Revision: 2, Enabled: true, Policy: entitlements.Policy{PlaybackAllowed: true, MaxStreams: 2}},
	}, nil
}
func (routerTemplateStoreStub) Create(context.Context, entitlements.CreateTemplateInput) (entitlements.Template, error) {
	return entitlements.Template{}, errRouterTemplateStoreUnused
}
func (routerTemplateStoreStub) Revise(_ context.Context, key string, expected int64, input entitlements.ReviseTemplateInput) (entitlements.Template, error) {
	return entitlements.Template{Key: key, Name: input.Name, Revision: expected + 1, Enabled: input.Enabled, Policy: input.Policy}, nil
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
func (routerTemplateStoreStub) ApplyDefaultAccountTemplate(_ context.Context, _, _ int, key string, revision int64, dryRun bool) (entitlements.ApplyResult, error) {
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

const templateRevisionsPath = "/admin/platform/entitlement-templates/standard/revisions"

// templateRevisionsRoutes mounts the template routes with one API key whose
// scopes and owner a test changes between requests.
type templateRevisionsRoutes struct {
	tokens auth.AdminContextTokenService
	key    *models.APIKey
	owner  *models.User
}

func newTemplateRevisionsRoutes() *templateRevisionsRoutes {
	return &templateRevisionsRoutes{
		tokens: auth.NewAdminContextTokenService("router-template-revisions-api-key-test-secret"),
		key:    &models.APIKey{ID: 1, UserID: 7, Key: "sa_storefront"},
		owner:  &models.User{ID: 7, Role: models.RoleAdmin, Enabled: true},
	}
}

// router builds the routes; a nil adminMW validates platform contexts only.
func (f *templateRevisionsRoutes) router(platformAdmins auth.PlatformAdminAuthorizer, adminMW *apimw.AdminContextMiddleware) http.Handler {
	if adminMW == nil {
		adminMW = apimw.NewAdminContextMiddleware(f.tokens, bloemAdminTenantResolverStub{}, bloemAdminMembershipStoreStub{}, platformAdmins, bloemSessionValidator{})
	}
	authMW := apimw.NewAuthMiddleware(nil, nil, bloemAPIKeyValidatorStub{key: f.key}, bloemAPIKeyOwnerStub{owner: f.owner})
	templates := handlers.NewEntitlementTemplatesHandler(routerTemplateStoreStub{}, []byte("router-template-secret"))
	templates.SetPlatformAdminAuthorizer(platformAdmins)
	router := chi.NewRouter()
	mountBloemRoutes(router, handlers.NewBloemSystemHandler(nil), nil, authMW, adminMW, bloemRouteSurfaces{Entitlement: templates})
	return router
}

func serveTemplateRoute(handler http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, NativeAPIPrefix+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// wantTemplateStatus fails unless the response has status and, when errorCode
// is set, that native error code.
func wantTemplateStatus(t *testing.T, label string, rec *httptest.ResponseRecorder, status int, errorCode string) {
	t.Helper()
	if rec.Code != status || (errorCode != "" && !strings.Contains(rec.Body.String(), `"error":"`+errorCode+`"`)) {
		t.Fatalf("%s = %d %s, want %d %s", label, rec.Code, rec.Body.String(), status, errorCode)
	}
}

// wantStandardRevisions checks the stub's two revisions, newest first.
func wantStandardRevisions(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantTemplateStatus(t, label, rec, http.StatusOK, "")
	var body struct {
		Revisions []struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
			Status   string `json:"status"`
			Policy   struct {
				MaxStreams int `json:"max_streams"`
			} `json:"policy"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode revisions: %v (%s)", label, err, rec.Body.String())
	}
	if len(body.Revisions) != 2 || body.Revisions[0].Revision != 3 || body.Revisions[1].Revision != 2 ||
		body.Revisions[1].Key != "standard" || body.Revisions[1].Status != "enabled" || body.Revisions[1].Policy.MaxStreams != 2 {
		t.Fatalf("%s: revisions = %+v", label, body.Revisions)
	}
}

// A storefront's API key reads one template's revisions, so it can show the
// limits of a plan an account still holds at an older revision. Reading one
// template, the history alias and every template change keep requiring an
// admin session, and only a platform administrator's key is admitted.
func TestBloemTemplateRevisionsAcceptPlatformAPIKey(t *testing.T) {
	f := newTemplateRevisionsRoutes()
	router := f.router(bloemAdminPlatformAuthorizerAllowedStub{}, nil)
	sessionOnly := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/platform/entitlement-templates/standard", ""},
		{http.MethodGet, "/admin/platform/entitlement-templates/standard?revision=2", ""},
		{http.MethodGet, "/admin/platform/entitlement-templates/standard/history", ""},
		{http.MethodPost, templateRevisionsPath, `{"expected_revision":3,"name":"Standard","enabled":true,"policy":{}}`},
		{http.MethodPost, "/admin/platform/entitlement-templates/standard/clone", `{"source_revision":3,"key":"copy","name":"Copy"}`},
		{http.MethodPost, "/admin/platform/entitlement-templates/standard/archive", `{"expected_revision":3}`},
	}
	for _, scopes := range [][]string{nil, {auth.ScopeAdminEntitlementsAccounts}} {
		f.key.Scopes = scopes
		label := "key scopes [" + strings.Join(scopes, ",") + "]"
		wantStandardRevisions(t, label+" GET revisions", serveTemplateRoute(router, http.MethodGet, templateRevisionsPath, "", "sa_storefront"))
		wantTemplateStatus(t, label+" GET unknown template revisions",
			serveTemplateRoute(router, http.MethodGet, "/admin/platform/entitlement-templates/missing/revisions", "", "sa_storefront"), http.StatusNotFound, "not_found")
		for _, item := range sessionOnly {
			wantTemplateStatus(t, label+" "+item.method+" "+item.path, serveTemplateRoute(router, item.method, item.path, item.body, "sa_storefront"), http.StatusUnauthorized, "")
		}
		// Refused by the handler for an unscoped key, by the scope check for a scoped one.
		wantTemplateStatus(t, label+" template creation",
			serveTemplateRoute(router, http.MethodPost, "/admin/platform/entitlement-templates", `{"key":"new","name":"New"}`, "sa_storefront"), http.StatusForbidden, "")
	}

	for _, scopes := range [][]string{{auth.ScopeAdminUsers}, {auth.ScopeAdminEntitlementsBulk}, {auth.ScopeAdminPreview}} {
		f.key.Scopes = scopes
		wantTemplateStatus(t, "key scopes "+strings.Join(scopes, ",")+" GET revisions",
			serveTemplateRoute(router, http.MethodGet, templateRevisionsPath, "", "sa_storefront"), http.StatusForbidden, "forbidden")
	}

	// Without platform authority: an organization administrator's account
	// (whose admin role lives on its membership), an account the live check no
	// longer holds to be a platform administrator, and a disabled account.
	f.key.Scopes = nil
	f.owner.Role = models.RoleUser
	wantTemplateStatus(t, "non-admin owner GET revisions",
		serveTemplateRoute(router, http.MethodGet, templateRevisionsPath, "", "sa_storefront"), http.StatusForbidden, "insufficient_platform_authority")
	f.owner.Role = models.RoleAdmin
	wantTemplateStatus(t, "former platform administrator GET revisions",
		serveTemplateRoute(f.router(bloemAdminPlatformAuthorizerStub{}, nil), http.MethodGet, templateRevisionsPath, "", "sa_storefront"), http.StatusForbidden, "insufficient_platform_authority")
	f.owner.Enabled = false
	wantTemplateStatus(t, "disabled owner GET revisions",
		serveTemplateRoute(router, http.MethodGet, templateRevisionsPath, "", "sa_storefront"), http.StatusUnauthorized, "")
}

// Moving GET revisions out of the session-only /admin group leaves the
// platform context's template reads and changes where they were, and an
// organization context still cannot read platform templates.
func TestBloemTemplateRevisionsKeepAdminContexts(t *testing.T) {
	f := newTemplateRevisionsRoutes()
	router := f.router(bloemAdminPlatformAuthorizerAllowedStub{}, nil)
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	session, err := f.tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: incarnation, SessionID: "session", Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	wantStandardRevisions(t, "platform context GET revisions", serveTemplateRoute(router, http.MethodGet, templateRevisionsPath, "", session))
	wantStandardRevisions(t, "platform context GET history", serveTemplateRoute(router, http.MethodGet, "/admin/platform/entitlement-templates/standard/history", "", session))
	wantTemplateStatus(t, "platform context single template read",
		serveTemplateRoute(router, http.MethodGet, "/admin/platform/entitlement-templates/standard", "", session), http.StatusOK, "")
	wantTemplateStatus(t, "platform context new revision",
		serveTemplateRoute(router, http.MethodPost, templateRevisionsPath, `{"expected_revision":3,"name":"Standard","enabled":true,"policy":{}}`, session), http.StatusCreated, "")
	// As before the move, an unrouted method falls through to the /admin group's
	// catch-all 404.
	wantTemplateStatus(t, "platform context PUT revisions",
		serveTemplateRoute(router, http.MethodPut, templateRevisionsPath, `{}`, session), http.StatusNotFound, "not_found")

	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	organizationMW := apimw.NewAdminContextMiddleware(
		f.tokens,
		bloemAdminTenantResolverStub{tenant: tenancy.Context{AccountID: 7, OrganizationID: organizationID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 7, SecurityRevision: 11}},
		bloemAdminMembershipStoreStub{membership: tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}},
		bloemAdminPlatformAuthorizerStub{},
		bloemSessionValidator{},
	)
	organization, err := f.tokens.Mint(auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: incarnation, SessionID: "session", Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11})
	if err != nil {
		t.Fatal(err)
	}
	wantTemplateStatus(t, "organization context GET revisions",
		serveTemplateRoute(f.router(bloemAdminPlatformAuthorizerStub{}, organizationMW), http.MethodGet, templateRevisionsPath, "", organization), http.StatusForbidden, "insufficient_platform_authority")
}
