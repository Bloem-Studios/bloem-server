package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

func strictPasswordPolicyHandler() *AdminHandler {
	return &AdminHandler{SettingsRepo: &fakeServerSettingsStore{values: map[string]string{
		config.AuthPasswordMinLengthSettingKey:        "12",
		config.AuthPasswordRequireUppercaseSettingKey: "true",
		config.AuthPasswordRequireSpecialSettingKey:   "true",
	}}}
}

var passwordRuleCases = map[string]string{
	"Short-1":                "password_too_short",
	strings.Repeat("A!", 37): "password_too_long",
	"lowercase-only-1":       "password_needs_uppercase",
	"Uppercase1234":          "password_needs_special",
}

func decodePasswordRefusal(t *testing.T, rec *httptest.ResponseRecorder) bloemPasswordRuleResponse {
	t.Helper()
	var body bloemPasswordRuleResponse
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("refusal = %d %s, want a 400 JSON body", rec.Code, rec.Body)
	}
	return body
}

// A password the server's policy refuses on a platform account or a
// household login is bad_request with code naming the rule, both from
// validateAdminPassword (the account and login routes) and from
// BloemCheckNewPassword (the tenant member routes).
func TestBloemPasswordRefusalNamesTheRule(t *testing.T) {
	h := strictPasswordPolicyHandler()
	for password, code := range passwordRuleCases {
		for name, err := range map[string]error{
			"account":     h.validateAdminPassword(t.Context(), password),
			"bloem check": h.BloemCheckNewPassword(t.Context(), password),
		} {
			rec := httptest.NewRecorder()
			writeBloemPlatformAccountError(rec, err)
			body := decodePasswordRefusal(t, rec)
			if body.Error != "bad_request" || body.Code != code || body.Message == "" {
				t.Errorf("%s refusal of %q = %+v, want bad_request with code %s", name, password, body, code)
			}
		}
	}
	if err := h.BloemCheckNewPassword(t.Context(), "Policy-Password-1"); err != nil {
		t.Fatalf("a password the policy accepts = %v", err)
	}
}

type refusingTenantMembers struct {
	tenantMemberService
	err error
}

func (r refusingTenantMembers) Create(context.Context, uuid.UUID, string, tenancy.CreateMemberInput) (models.User, bool, error) {
	return models.User{}, false, r.err
}

func (r refusingTenantMembers) ResetPassword(context.Context, uuid.UUID, int, string) (models.User, error) {
	return models.User{}, r.err
}

// The tenant member create and reset routes answer the member service's
// policy refusal the same way.
func TestBloemTenantMemberPasswordRefusalNamesTheRule(t *testing.T) {
	policy := strictPasswordPolicyHandler()
	for password, code := range passwordRuleCases {
		members := NewAdminTenantMembersHandler(refusingTenantMembers{err: policy.BloemCheckNewPassword(t.Context(), password)}, nil)
		tenant := uuid.NewString()
		for name, route := range map[string]struct {
			path    string
			headers map[string]string
			serve   http.HandlerFunc
		}{
			"create": {"/admin/platform/tenants/" + tenant + "/members", map[string]string{"Idempotency-Key": "key"}, members.HandleCreate},
			"reset":  {"/admin/platform/tenants/" + tenant + "/members/7/reset-password", nil, members.HandleResetPassword},
		} {
			req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(`{"username":"member","email":"member@example.test","password":"x"}`))
			for k, v := range route.headers {
				req.Header.Set(k, v)
			}
			params := chi.NewRouteContext()
			params.URLParams.Add("tenant_id", tenant)
			params.URLParams.Add("user_id", "7")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, params))
			rec := httptest.NewRecorder()
			route.serve(rec, req)
			body := decodePasswordRefusal(t, rec)
			if body.Error != "bad_request" || body.Code != code {
				t.Errorf("%s refusal of %q = %+v, want bad_request with code %s", name, password, body, code)
			}
		}
	}
}
