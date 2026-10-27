package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
)

type entitlementPlatformAdminStub struct {
	allowed bool
	err     error
}

func (s entitlementPlatformAdminStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return s.allowed, s.err
}

// entitlementAPIKeyRouter mounts the routes a storefront API key may call, with
// the key's claims already resolved, as RequireAuth leaves them.
func entitlementAPIKeyRouter(handler *EntitlementTemplatesHandler, claims *auth.Claims) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.SetClaims(r.Context(), claims)))
		})
	})
	router.Get("/entitlement-templates", handler.HandleList)
	router.Post("/entitlement-templates", handler.HandleCreate)
	router.Post("/accounts/{account_id}/entitlement/dry-run", handler.HandleAccountDryRun)
	router.Post("/accounts/{account_id}/entitlement/apply", handler.HandleAccountApply)
	return router
}

func platformAdminAPIKey(userID int, scopes ...string) *auth.Claims {
	return &auth.Claims{UserID: userID, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: scopes}
}

func TestEntitlementCatalogAndAccountApplyAcceptPlatformAdminAPIKey(t *testing.T) {
	for _, tt := range []struct {
		name   string
		scopes []string
	}{
		{"unscoped admin key", nil},
		{"entitlement accounts scope", []string{auth.ScopeAdminEntitlementsAccounts}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &entitlementHandlerStoreStub{template: entitlements.Template{Key: "standard", Revision: 3, Enabled: true}}
			handler := NewEntitlementTemplatesHandler(store, []byte("api-key-secret"))
			handler.SetPlatformAdminAuthorizer(entitlementPlatformAdminStub{allowed: true})
			router := entitlementAPIKeyRouter(handler, platformAdminAPIKey(7, tt.scopes...))

			list := httptest.NewRecorder()
			router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/entitlement-templates?status=enabled", nil))
			if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"key":"standard"`) {
				t.Fatalf("catalog = %d %s, want the enabled template", list.Code, list.Body.String())
			}

			dryRun := httptest.NewRecorder()
			router.ServeHTTP(dryRun, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/dry-run", strings.NewReader(`{"template_key":"standard","template_revision":3}`)))
			if dryRun.Code != http.StatusOK {
				t.Fatalf("dry-run = %d %s", dryRun.Code, dryRun.Body.String())
			}
			var preview struct {
				ConfirmationToken string `json:"confirmation_token"`
			}
			requireJSONDecode(t, dryRun.Body, &preview)

			apply := httptest.NewRecorder()
			router.ServeHTTP(apply, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/apply", strings.NewReader(
				`{"template_key":"standard","template_revision":3,"confirmation_token":"`+preview.ConfirmationToken+`","idempotency_key":"garden-provision-42"}`)))
			if apply.Code != http.StatusOK {
				t.Fatalf("apply = %d %s", apply.Code, apply.Body.String())
			}
		})
	}
}

// The confirmation is bound to the key's owning account: another platform
// administrator's key cannot spend it.
func TestEntitlementAPIKeyConfirmationIsBoundToKeyOwner(t *testing.T) {
	store := &entitlementHandlerStoreStub{template: entitlements.Template{Key: "standard", Revision: 3, Enabled: true}}
	handler := NewEntitlementTemplatesHandler(store, []byte("api-key-secret"))
	handler.SetPlatformAdminAuthorizer(entitlementPlatformAdminStub{allowed: true})

	dryRun := httptest.NewRecorder()
	entitlementAPIKeyRouter(handler, platformAdminAPIKey(7)).ServeHTTP(dryRun, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/dry-run", strings.NewReader(`{"template_key":"standard","template_revision":3}`)))
	var preview struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	requireJSONDecode(t, dryRun.Body, &preview)

	apply := httptest.NewRecorder()
	entitlementAPIKeyRouter(handler, platformAdminAPIKey(8)).ServeHTTP(apply, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/apply", strings.NewReader(
		`{"template_key":"standard","template_revision":3,"confirmation_token":"`+preview.ConfirmationToken+`","idempotency_key":"other-owner"}`)))
	if apply.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply with another owner's confirmation = %d %s, want 422", apply.Code, apply.Body.String())
	}
}

func TestEntitlementAPIKeyCallerNeedsPlatformAdministratorAndScope(t *testing.T) {
	for _, tt := range []struct {
		name       string
		claims     *auth.Claims
		authorizer auth.PlatformAdminAuthorizer
		want       int
	}{
		{"no credential", nil, entitlementPlatformAdminStub{allowed: true}, http.StatusForbidden},
		{"session token without admin context", &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAccess}, entitlementPlatformAdminStub{allowed: true}, http.StatusForbidden},
		{"non-admin key owner", &auth.Claims{UserID: 7, Role: "user", TokenType: auth.TokenTypeAPIKey}, entitlementPlatformAdminStub{allowed: true}, http.StatusForbidden},
		{"scoped key without the scope", platformAdminAPIKey(7, auth.ScopeAdminUsers), entitlementPlatformAdminStub{allowed: true}, http.StatusForbidden},
		{"owner is not a platform administrator", platformAdminAPIKey(7), entitlementPlatformAdminStub{allowed: false}, http.StatusForbidden},
		{"platform check unavailable", platformAdminAPIKey(7), entitlementPlatformAdminStub{err: errors.New("database down")}, http.StatusServiceUnavailable},
		{"no platform authorizer wired", platformAdminAPIKey(7), nil, http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewEntitlementTemplatesHandler(&entitlementHandlerStoreStub{}, []byte("api-key-secret"))
			if tt.authorizer != nil {
				handler.SetPlatformAdminAuthorizer(tt.authorizer)
			}
			router := entitlementAPIKeyRouter(handler, tt.claims)
			for _, request := range []*http.Request{
				httptest.NewRequest(http.MethodGet, "/entitlement-templates", nil),
				httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/dry-run", strings.NewReader(`{"template_key":"standard","template_revision":3}`)),
			} {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				if recorder.Code != tt.want {
					t.Fatalf("%s %s = %d %s, want %d", request.Method, request.URL.Path, recorder.Code, recorder.Body.String(), tt.want)
				}
			}
		})
	}
}

// Template authoring keeps requiring an admin session even for a platform
// administrator's unscoped key.
func TestEntitlementTemplateAuthoringStaysSessionOnly(t *testing.T) {
	handler := NewEntitlementTemplatesHandler(&entitlementHandlerStoreStub{}, []byte("api-key-secret"))
	handler.SetPlatformAdminAuthorizer(entitlementPlatformAdminStub{allowed: true})
	recorder := httptest.NewRecorder()
	entitlementAPIKeyRouter(handler, platformAdminAPIKey(7)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/entitlement-templates", strings.NewReader(`{"key":"new","name":"New"}`)))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("create with API key = %d %s, want 403", recorder.Code, recorder.Body.String())
	}
}

// accountMovingStore previews the account in one organization and writes it
// in another, as an account moved between the preview and the write would.
type accountMovingStore struct {
	entitlementHandlerStoreStub
	previewIn, appliedIn uuid.UUID
	audits               []entitlements.AuditEvent
}

func (s *accountMovingStore) ApplyDefaultAccountTemplate(_ context.Context, _, accountID int, key string, revision int64, dryRun bool) (entitlements.ApplyResult, error) {
	organizationID := s.appliedIn
	if dryRun {
		organizationID = s.previewIn
	}
	return entitlements.ApplyResult{TenantID: organizationID, AccountID: accountID, TemplateKey: key, TemplateRevision: revision, DryRun: dryRun, Changed: true}, nil
}
func (s *accountMovingStore) RecordAudit(_ context.Context, event entitlements.AuditEvent) error {
	s.audits = append(s.audits, event)
	return nil
}
func (s *accountMovingStore) ListOrganizationAudit(context.Context, uuid.UUID) ([]entitlements.AuditEvent, error) {
	return s.audits, nil
}

// An apply that committed is answered and audited as committed, and a retry
// with its key gets the same answer.
func TestEntitlementAccountApplyAnswersAndAuditsWhatItCommitted(t *testing.T) {
	store := &accountMovingStore{entitlementHandlerStoreStub: entitlementHandlerStoreStub{template: entitlements.Template{Key: "standard", Revision: 3, Enabled: true}}, previewIn: uuid.New(), appliedIn: uuid.New()}
	handler := NewEntitlementTemplatesHandler(store, []byte("api-key-secret"))
	handler.SetPlatformAdminAuthorizer(entitlementPlatformAdminStub{allowed: true})
	router := entitlementAPIKeyRouter(handler, platformAdminAPIKey(7))

	dryRun := httptest.NewRecorder()
	router.ServeHTTP(dryRun, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/dry-run", strings.NewReader(`{"template_key":"standard","template_revision":3}`)))
	var preview struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	requireJSONDecode(t, dryRun.Body, &preview)

	for attempt := 1; attempt <= 2; attempt++ {
		apply := httptest.NewRecorder()
		router.ServeHTTP(apply, httptest.NewRequest(http.MethodPost, "/accounts/42/entitlement/apply", strings.NewReader(
			`{"template_key":"standard","template_revision":3,"confirmation_token":"`+preview.ConfirmationToken+`","idempotency_key":"garden-provision-42"}`)))
		if apply.Code != http.StatusOK || !strings.Contains(apply.Body.String(), store.appliedIn.String()) {
			t.Fatalf("apply attempt %d = %d %s, want 200 naming the organization it applied in", attempt, apply.Code, apply.Body.String())
		}
	}
	if len(store.audits) != 1 || store.audits[0].Action != "account.entitlement_applied" || store.audits[0].OrganizationID != store.appliedIn {
		t.Fatalf("audits = %+v, want one account.entitlement_applied in the organization applied", store.audits)
	}
}
