package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/go-chi/chi/v5"
)

type customRoleAdminUserRepo struct {
	created *models.User
}

func (r *customRoleAdminUserRepo) List(context.Context) ([]*models.User, error) { return nil, nil }

func (r *customRoleAdminUserRepo) Create(_ context.Context, input models.CreateUserInput) (*models.User, error) {
	r.created = &models.User{ID: 88, Username: input.Username, Email: input.Email, Role: input.Role, Enabled: true}
	return r.created, nil
}

func (*customRoleAdminUserRepo) Update(context.Context, int, models.UpdateUserInput) error {
	return nil
}
func (*customRoleAdminUserRepo) Delete(context.Context, int) error { return nil }
func (r *customRoleAdminUserRepo) GetByID(context.Context, int) (*models.User, error) {
	if r.created == nil {
		r.created = &models.User{ID: 88, Username: "direct-user", Email: "direct@example.test", Role: "user", Enabled: true}
	}
	return r.created, nil
}

type strictLegacyRoleProvisioner struct {
	legacyRole string
}

type recordingDirectEntitlements struct {
	accountID int
	key       string
	revision  int64
	dryRun    bool
}

func (r *recordingDirectEntitlements) ApplyDefaultAccountTemplate(_ context.Context, accountID int, key string, revision int64, dryRun bool) (entitlements.ApplyResult, error) {
	r.accountID, r.key, r.revision, r.dryRun = accountID, key, revision, dryRun
	return entitlements.ApplyResult{AccountID: accountID, TemplateKey: key, TemplateRevision: revision, GroupID: 44}, nil
}

func (p *strictLegacyRoleProvisioner) ProvisionDefaultMembership(_ context.Context, _ int, legacyRole string) error {
	p.legacyRole = legacyRole
	if legacyRole != "user" {
		return errors.New("unexpected membership legacy role")
	}
	return nil
}

func TestAdminHandlerCreateUser_CustomRoleUsesUserMembershipLegacyRole(t *testing.T) {
	users := &customRoleAdminUserRepo{}
	memberships := &strictLegacyRoleProvisioner{}
	handler := NewAdminHandler(users, nil, nil)
	handler.SetMembershipProvisioner(memberships)

	request := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{
		"username":"moderator",
		"email":"moderator@example.test",
		"password":"password",
		"role":"moderator"
	}`))
	response := httptest.NewRecorder()
	handler.HandleCreateUser(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if users.created == nil || users.created.Role != "moderator" {
		t.Fatalf("created user = %#v, want preserved moderator role", users.created)
	}
	if memberships.legacyRole != "user" {
		t.Fatalf("membership legacy role = %q, want user", memberships.legacyRole)
	}
}

func TestAdminHandlerCreateUser_AppliesDirectEntitlementRevision(t *testing.T) {
	users := &customRoleAdminUserRepo{}
	memberships := &strictLegacyRoleProvisioner{}
	direct := &recordingDirectEntitlements{}
	handler := NewAdminHandler(users, nil, nil)
	handler.SetMembershipProvisioner(memberships)
	handler.SetDirectEntitlements(direct)

	request := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(`{
		"username":"direct-user",
		"email":"direct@example.test",
		"password":"password",
		"role":"user",
		"entitlement_template_key":"premium",
		"entitlement_template_revision":3
	}`))
	response := httptest.NewRecorder()
	handler.HandleCreateUser(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if direct.accountID != 88 || direct.key != "premium" || direct.revision != 3 || direct.dryRun {
		t.Fatalf("direct entitlement call = %+v", direct)
	}
	if users.created.AccessGroupID == nil || *users.created.AccessGroupID != 44 {
		t.Fatalf("created user access group = %v, want 44", users.created.AccessGroupID)
	}
	if !strings.Contains(response.Body.String(), `"applied_entitlement_revision":3`) {
		t.Fatalf("response body = %s, want applied entitlement revision", response.Body.String())
	}
}

func TestAdminHandlerUpdateUser_AdoptsExactDirectEntitlementRevision(t *testing.T) {
	users := &customRoleAdminUserRepo{}
	direct := &recordingDirectEntitlements{}
	handler := NewAdminHandler(users, nil, nil)
	handler.SetDirectEntitlements(direct)

	request := httptest.NewRequest(http.MethodPut, "/admin/users/88", strings.NewReader(`{
		"entitlement_template_key":"standard",
		"entitlement_template_revision":7
	}`))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "88")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx))
	response := httptest.NewRecorder()
	handler.HandleUpdateUser(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if direct.accountID != 88 || direct.key != "standard" || direct.revision != 7 || direct.dryRun {
		t.Fatalf("direct entitlement call = %+v", direct)
	}
	if !strings.Contains(response.Body.String(), `"applied_entitlement_revision":7`) {
		t.Fatalf("response body = %s, want applied entitlement revision", response.Body.String())
	}
}
