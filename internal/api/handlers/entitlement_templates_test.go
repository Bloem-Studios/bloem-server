package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type entitlementHandlerStoreStub struct {
	template     entitlements.Template
	applyCalls   int
	lastTenantID uuid.UUID
	lastDryRun   bool
	changed      *bool
}

func (s *entitlementHandlerStoreStub) List(context.Context, bool) ([]entitlements.Template, error) {
	return []entitlements.Template{s.template}, nil
}
func (s *entitlementHandlerStoreStub) Get(context.Context, string, int64) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) Latest(context.Context, string) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) ListRevisions(context.Context, string) ([]entitlements.Template, error) {
	return []entitlements.Template{s.template}, nil
}
func (s *entitlementHandlerStoreStub) Create(context.Context, entitlements.CreateTemplateInput) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) Revise(context.Context, string, int64, entitlements.ReviseTemplateInput) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) Clone(context.Context, string, int64, entitlements.CreateTemplateInput) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) Archive(context.Context, string, int64) (entitlements.Template, error) {
	return s.template, nil
}
func (s *entitlementHandlerStoreStub) ApplyTemplate(_ context.Context, tenantID uuid.UUID, key string, revision int64, dryRun bool) (entitlements.ApplyResult, error) {
	s.applyCalls++
	s.lastTenantID, s.lastDryRun = tenantID, dryRun
	changed := true
	if s.changed != nil {
		changed = *s.changed
	}
	return entitlements.ApplyResult{TenantID: tenantID, TemplateKey: key, TemplateRevision: revision, GroupID: 55, DryRun: dryRun, Changed: changed}, nil
}
func (s *entitlementHandlerStoreStub) ApplyDefaultAccountTemplate(context.Context, int, string, int64, bool) (entitlements.ApplyResult, error) {
	return entitlements.ApplyResult{}, nil
}
func (s *entitlementHandlerStoreStub) GetOrganizationEntitlement(context.Context, uuid.UUID) (entitlements.OrganizationEntitlement, error) {
	return entitlements.OrganizationEntitlement{}, nil
}
func (s *entitlementHandlerStoreStub) GetDefaultAccountEntitlement(context.Context, int) (entitlements.AccountEntitlement, error) {
	return entitlements.AccountEntitlement{}, nil
}

func platformEntitlementRouter(handler *EntitlementTemplatesHandler, authorized bool) http.Handler {
	router := chi.NewRouter()
	if authorized {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims := auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}
				next.ServeHTTP(w, r.WithContext(middleware.SetAdminContextClaims(r.Context(), claims)))
			})
		})
	}
	router.Post("/organizations/{id}/entitlement/dry-run", handler.HandleOrganizationDryRun)
	router.Post("/organizations/{id}/entitlement/apply", handler.HandleOrganizationApply)
	router.Get("/entitlement-templates", handler.HandleList)
	return router
}

func TestEntitlementCatalogEnabledStatusExcludesDisabledPresets(t *testing.T) {
	store := &entitlementHandlerStoreStub{template: entitlements.Template{Key: "browse-only", Enabled: false}}
	response := httptest.NewRecorder()
	platformEntitlementRouter(NewEntitlementTemplatesHandler(store, []byte("secret")), true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/entitlement-templates?status=enabled", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Templates []entitlementTemplateJSON `json:"templates"`
	}
	requireJSONDecode(t, response.Body, &payload)
	if len(payload.Templates) != 0 {
		t.Fatalf("enabled templates = %+v, want disabled presets excluded", payload.Templates)
	}
}

func TestEntitlementApplyRequiresPlatformAuthority(t *testing.T) {
	handler := NewEntitlementTemplatesHandler(&entitlementHandlerStoreStub{}, []byte("test-secret"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/organizations/"+uuid.NewString()+"/entitlement/dry-run", strings.NewReader(`{"template_key":"premium","template_revision":1}`))
	platformEntitlementRouter(handler, false).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestEntitlementDryRunTokenIsRequiredAndApplyIsIdempotent(t *testing.T) {
	organizationID := uuid.New()
	store := &entitlementHandlerStoreStub{}
	handler := NewEntitlementTemplatesHandler(store, []byte("test-secret"))
	handler.now = func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) }
	router := platformEntitlementRouter(handler, true)

	dryRun := httptest.NewRecorder()
	router.ServeHTTP(dryRun, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/dry-run", strings.NewReader(`{"template_key":"premium","template_revision":3}`)))
	if dryRun.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d; body=%s", dryRun.Code, dryRun.Body.String())
	}
	var preview struct {
		ConfirmationToken string `json:"confirmation_token"`
		DryRunToken       string `json:"dry_run_token"`
		ExpiresAt         string `json:"expires_at"`
	}
	if err := json.NewDecoder(dryRun.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.ConfirmationToken == "" || preview.DryRunToken != preview.ConfirmationToken || preview.ExpiresAt == "" || !store.lastDryRun {
		t.Fatalf("preview = %+v, dry_run=%t", preview, store.lastDryRun)
	}

	applyBody := `{"template_key":"premium","template_revision":3,"confirmation_token":"` + preview.ConfirmationToken + `","idempotency_key":"park-command-1"}`
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/apply", strings.NewReader(applyBody)))
		if response.Code != http.StatusOK {
			t.Fatalf("apply %d status = %d; body=%s", attempt, response.Code, response.Body.String())
		}
	}
	if store.applyCalls != 3 { // preview, apply-time confirmation CAS, and one durable apply
		t.Fatalf("ApplyTemplate calls = %d, want 3", store.applyCalls)
	}
}

func TestEntitlementPolicyNullLibrariesMeansDynamicAll(t *testing.T) {
	policy := (entitlementPolicyJSON{LibraryIDs: nil}).policy()
	if policy.LibraryIDs != nil {
		t.Fatalf("LibraryIDs = %#v, want nil dynamic-all selection", policy.LibraryIDs)
	}
}

func TestEntitlementApplyRejectsTokenForAnotherOrganization(t *testing.T) {
	store := &entitlementHandlerStoreStub{}
	handler := NewEntitlementTemplatesHandler(store, []byte("test-secret"))
	router := platformEntitlementRouter(handler, true)
	firstID, secondID := uuid.New(), uuid.New()
	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest(http.MethodPost, "/organizations/"+firstID.String()+"/entitlement/dry-run", strings.NewReader(`{"template_key":"premium","template_revision":3}`)))
	var token struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	_ = json.NewDecoder(preview.Body).Decode(&token)

	response := httptest.NewRecorder()
	body := `{"template_key":"premium","template_revision":3,"confirmation_token":"` + token.ConfirmationToken + `","idempotency_key":"park-command-2"}`
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/organizations/"+secondID.String()+"/entitlement/apply", strings.NewReader(body)))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
}

func TestEntitlementApplyRejectsExpiredConfirmationToken(t *testing.T) {
	store := &entitlementHandlerStoreStub{}
	handler := NewEntitlementTemplatesHandler(store, []byte("test-secret"))
	issuedAt := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return issuedAt }
	router := platformEntitlementRouter(handler, true)
	organizationID := uuid.New()
	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/dry-run", strings.NewReader(`{"template_key":"premium","template_revision":3}`)))
	var token struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.NewDecoder(preview.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return issuedAt.Add(entitlementConfirmationTTL + time.Second) }

	response := httptest.NewRecorder()
	body := `{"template_key":"premium","template_revision":3,"confirmation_token":"` + token.ConfirmationToken + `","idempotency_key":"expired-command"}`
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/apply", strings.NewReader(body)))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	if store.applyCalls != 1 {
		t.Fatalf("ApplyTemplate calls = %d, want dry-run only", store.applyCalls)
	}
}

func TestEntitlementApplyRejectsStalePreview(t *testing.T) {
	organizationID := uuid.New()
	changed := true
	store := &entitlementHandlerStoreStub{changed: &changed}
	handler := NewEntitlementTemplatesHandler(store, []byte("test-secret"))
	router := platformEntitlementRouter(handler, true)
	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/dry-run", strings.NewReader(`{"template_key":"premium","template_revision":3}`)))
	var token struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	requireJSONDecode(t, preview.Body, &token)
	changed = false
	response := httptest.NewRecorder()
	body := `{"template_key":"premium","template_revision":3,"confirmation_token":"` + token.ConfirmationToken + `","idempotency_key":"stale"}`
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/organizations/"+organizationID.String()+"/entitlement/apply", strings.NewReader(body)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "confirmation_stale") {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
}

func requireJSONDecode(t *testing.T, body interface{ Read([]byte) (int, error) }, value any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(value); err != nil {
		t.Fatal(err)
	}
}
