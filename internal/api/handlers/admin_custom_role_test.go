package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vondel-Media/vondel-server/internal/models"
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
func (*customRoleAdminUserRepo) Delete(context.Context, int) error                  { return nil }
func (*customRoleAdminUserRepo) GetByID(context.Context, int) (*models.User, error) { return nil, nil }

type strictLegacyRoleProvisioner struct {
	legacyRole string
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
