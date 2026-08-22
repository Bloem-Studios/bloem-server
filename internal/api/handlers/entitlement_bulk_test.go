package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type platformEntitlementBulkCohortStub struct {
	items []entitlements.CohortRevision
	item  entitlements.CohortRevision
	err   error
	org   uuid.UUID
}

func (s *platformEntitlementBulkCohortStub) ListCohorts(_ context.Context, organizationID uuid.UUID, _ bool) ([]entitlements.CohortRevision, error) {
	s.org = organizationID
	return s.items, s.err
}

func (s *platformEntitlementBulkCohortStub) GetCohort(_ context.Context, organizationID, _ uuid.UUID) (entitlements.CohortRevision, error) {
	s.org = organizationID
	return s.item, s.err
}

type platformEntitlementBulkPeopleStub struct {
	selection      adminpeople.Selection
	preview        adminpeople.PolicyPreview
	job            adminpeople.BulkResult
	err            error
	org            uuid.UUID
	actorID        int
	filter         adminpeople.Filter
	jobID          string
	getCalls       int
	cancelCalls    int
	operationScope adminpeople.PolicyOperationScope
}

func (s *platformEntitlementBulkPeopleStub) CreateSelection(_ context.Context, organizationID uuid.UUID, filter adminpeople.Filter) (adminpeople.Selection, error) {
	s.org, s.filter = organizationID, filter
	return s.selection, s.err
}

func (s *platformEntitlementBulkPeopleStub) PreviewPolicy(_ context.Context, organizationID uuid.UUID, actorID int, _ string, _ adminpeople.PolicyCommand) (adminpeople.PolicyPreview, error) {
	s.org, s.actorID, s.operationScope = organizationID, actorID, adminpeople.PolicyOperationScopeOrganization
	return s.preview, s.err
}

func (s *platformEntitlementBulkPeopleStub) PreviewPolicyForScope(_ context.Context, organizationID uuid.UUID, actorID int, _ string, _ adminpeople.PolicyCommand, operationScope adminpeople.PolicyOperationScope) (adminpeople.PolicyPreview, error) {
	s.org, s.actorID, s.operationScope = organizationID, actorID, operationScope
	return s.preview, s.err
}

func (s *platformEntitlementBulkPeopleStub) EnqueuePolicyBulk(_ context.Context, organizationID uuid.UUID, actorID int, _ adminpeople.PolicyBulkAction) (adminpeople.BulkResult, error) {
	s.org, s.actorID, s.operationScope = organizationID, actorID, adminpeople.PolicyOperationScopeOrganization
	return s.job, s.err
}

func (s *platformEntitlementBulkPeopleStub) EnqueuePolicyBulkForScope(_ context.Context, organizationID uuid.UUID, actorID int, _ adminpeople.PolicyBulkAction, operationScope adminpeople.PolicyOperationScope) (adminpeople.BulkResult, error) {
	s.org, s.actorID, s.operationScope = organizationID, actorID, operationScope
	return s.job, s.err
}

func (s *platformEntitlementBulkPeopleStub) GetPolicyBulkJob(_ context.Context, organizationID uuid.UUID, jobID string) (adminpeople.BulkResult, error) {
	s.org, s.jobID, s.getCalls = organizationID, jobID, s.getCalls+1
	return s.job, s.err
}

func (s *platformEntitlementBulkPeopleStub) CancelPolicyBulkJob(_ context.Context, organizationID uuid.UUID, actorID int, jobID string) (adminpeople.BulkResult, error) {
	s.org, s.actorID, s.jobID, s.cancelCalls = organizationID, actorID, jobID, s.cancelCalls+1
	return s.job, s.err
}

type platformEntitlementBulkOrganizationsStub struct {
	defaultOrganization tenancy.Organization
	organization        tenancy.Organization
	err                 error
}

func (s platformEntitlementBulkOrganizationsStub) DefaultOrganization(context.Context) (tenancy.Organization, error) {
	return s.defaultOrganization, s.err
}

func (s platformEntitlementBulkOrganizationsStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return s.organization, s.err
}

type platformEntitlementBulkAuthorizerStub struct {
	allowed bool
	err     error
}

func (s platformEntitlementBulkAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return s.allowed, s.err
}

type platformEntitlementBulkWake struct{ calls int }

func (w *platformEntitlementBulkWake) Wake() { w.calls++ }

func newPlatformEntitlementBulkTestHandler(
	cohorts PlatformEntitlementBulkCohortStore,
	people PlatformEntitlementBulkPeopleService,
	organizations PlatformEntitlementBulkOrganizationStore,
	authorizer auth.PlatformAdminAuthorizer,
	wake AdminPeopleWorkerWake,
) *AdminHandler {
	handler := &AdminHandler{}
	handler.SetPlatformEntitlementBulk(cohorts, people, organizations, authorizer, wake)
	return handler
}

func platformEntitlementBulkRouter(handler *AdminHandler, claims auth.AdminContextClaims, apiKeyClaims *auth.Claims) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if claims.AccountID > 0 {
				ctx = apimw.SetAdminContextClaims(ctx, claims)
			}
			if apiKeyClaims != nil {
				ctx = apimw.SetClaims(ctx, apiKeyClaims)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Get("/api/v2/admin/platform/organizations/{organization_id}/entitlement-cohorts", handler.HandleListPlatformEntitlementCohorts)
	router.Get("/api/v2/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}", handler.HandleGetPlatformEntitlementCohort)
	router.Post("/api/v2/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews", handler.HandleCreatePlatformOrganizationPolicyPreview)
	router.Post("/api/v2/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs", handler.HandleCreatePlatformOrganizationPolicyJob)
	router.Get("/api/v2/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}", handler.HandleGetPlatformOrganizationPolicyJob)
	router.Post("/api/v2/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel", handler.HandleCancelPlatformOrganizationPolicyJob)
	router.Post("/api/v2/admin/platform/accounts/entitlement-bulk/policy-previews", handler.HandleCreatePlatformDirectPolicyPreview)
	router.Post("/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs", handler.HandleCreatePlatformDirectPolicyJob)
	router.Get("/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}", handler.HandleGetPlatformDirectPolicyJob)
	router.Post("/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel", handler.HandleCancelPlatformDirectPolicyJob)
	return router
}

func TestPlatformEntitlementBulkRequiresPlatformAdministratorOrScopedAPIKey(t *testing.T) {
	org := uuid.New()
	people := &platformEntitlementBulkPeopleStub{selection: adminpeople.Selection{Token: "selection", Matched: 1}, preview: adminpeople.PolicyPreview{ConfirmationToken: "confirmation"}}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	body := `{"account_ids":[42],"command":{"kind":"restore_default_entitlement"}}`
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-previews"

	for _, test := range []struct {
		name   string
		claims auth.AdminContextClaims
		apiKey *auth.Claims
		want   int
	}{
		{name: "organization context rejected", claims: auth.AdminContextClaims{Scope: auth.AdminScopeOrganization, AccountID: 7}, want: http.StatusForbidden},
		{name: "platform context allowed", claims: auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, want: http.StatusCreated},
		{name: "scoped platform api key allowed", apiKey: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: []string{"admin:entitlements:bulk"}}, want: http.StatusCreated},
		{name: "unscoped legacy platform api key allowed", apiKey: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey}, want: http.StatusCreated},
		{name: "wrong api key scope rejected", apiKey: &auth.Claims{UserID: 7, Role: "admin", TokenType: auth.TokenTypeAPIKey, APIKeyScopes: []string{"admin:users"}}, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			platformEntitlementBulkRouter(handler, test.claims, test.apiKey).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPlatformEntitlementBulkOrganizationPreviewBindsExactAccountScope(t *testing.T) {
	org := uuid.New()
	people := &platformEntitlementBulkPeopleStub{
		selection: adminpeople.Selection{Token: "selection-token", Matched: 2, ExpiresAt: time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)},
		preview:   adminpeople.PolicyPreview{Matched: 2, ConfirmationToken: "confirmation-token"},
	}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	recorder := httptest.NewRecorder()
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-previews"
	platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"account_ids":[42,41],"command":{"kind":"restore_default_entitlement"}}`)))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if people.org != org || people.actorID != 7 || people.operationScope != adminpeople.PolicyOperationScopeOrganization {
		t.Fatalf("service scope = (%s,%d,%s), want (%s,7,%s)", people.org, people.actorID, people.operationScope, org, adminpeople.PolicyOperationScopeOrganization)
	}
	if got := people.filter.AccountIDs; len(got) != 2 || got[0] != 41 || got[1] != 42 || !people.filter.RequireAllAccountIDs {
		t.Fatalf("selection filter = %#v, want sorted strict account IDs", people.filter)
	}
	for _, fragment := range []string{`"selection":{"token":"selection-token"`, `"preview":`, `"confirmation_token":"confirmation-token"`} {
		if !strings.Contains(recorder.Body.String(), fragment) {
			t.Fatalf("response %q does not contain %q", recorder.Body.String(), fragment)
		}
	}
}

func TestPlatformEntitlementBulkDirectPreviewUsesDefaultOrganization(t *testing.T) {
	defaultOrg := uuid.New()
	people := &platformEntitlementBulkPeopleStub{selection: adminpeople.Selection{Token: "selection", Matched: 1}, preview: adminpeople.PolicyPreview{ConfirmationToken: "confirmation"}}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{defaultOrganization: tenancy.Organization{ID: defaultOrg, Default: true, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	recorder := httptest.NewRecorder()
	platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v2/admin/platform/accounts/entitlement-bulk/policy-previews", strings.NewReader(`{"account_ids":[42],"command":{"kind":"restore_default_entitlement"}}`)))
	if recorder.Code != http.StatusCreated || people.org != defaultOrg || people.operationScope != adminpeople.PolicyOperationScopeDirectAccounts {
		t.Fatalf("status/scope = %d/%s/%s, want 201/%s/%s: %s", recorder.Code, people.org, people.operationScope, defaultOrg, adminpeople.PolicyOperationScopeDirectAccounts, recorder.Body.String())
	}
}

func TestPlatformEntitlementBulkRejectsInactiveOrganization(t *testing.T) {
	org := uuid.New()
	for _, status := range []tenancy.OrganizationStatus{tenancy.OrganizationInitializing, tenancy.OrganizationSuspended} {
		people := &platformEntitlementBulkPeopleStub{}
		handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: status}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
		recorder := httptest.NewRecorder()
		path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-cohorts"
		platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound || people.getCalls != 0 {
			t.Fatalf("status %q response = %d %s", status, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPlatformEntitlementBulkRejectsForeignAccountWithoutDisclosure(t *testing.T) {
	org := uuid.New()
	people := &platformEntitlementBulkPeopleStub{err: adminpeople.ErrNotFound}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	recorder := httptest.NewRecorder()
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-previews"
	platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"account_ids":[999],"command":{"kind":"restore_default_entitlement"}}`)))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"error":"not_found"`) || strings.Contains(recorder.Body.String(), "999") {
		t.Fatalf("foreign target response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPlatformEntitlementBulkRejectsOversizedAndUnknownJSON(t *testing.T) {
	org := uuid.New()
	people := &platformEntitlementBulkPeopleStub{}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	router := platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil)
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-previews"
	ids := make([]string, 10001)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "ten thousand one", body: `{"account_ids":[` + strings.Join(ids, ",") + `],"command":{"kind":"restore_default_entitlement"}}`, want: http.StatusUnprocessableEntity},
		{name: "unknown field", body: `{"account_ids":[42],"command":{"kind":"restore_default_entitlement"},"provider_data":"private"}`, want: http.StatusBadRequest},
		{name: "trailing json", body: `{"account_ids":[42],"command":{"kind":"restore_default_entitlement"}} {}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(test.body)))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPlatformEntitlementBulkPolicyJobUsesPolicyMethodsAndWakes(t *testing.T) {
	org := uuid.New()
	wake := &platformEntitlementBulkWake{}
	people := &platformEntitlementBulkPeopleStub{job: adminpeople.BulkResult{JobID: "job-1", Status: "queued"}}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, wake)
	router := platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil)
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-jobs"
	body := `{"selection_token":"selection","confirmation_token":"confirmation","idempotency_key":"command-1","command":{"kind":"restore_default_entitlement"}}`
	for attempt := 0; attempt < 2; attempt++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"job_id":"job-1"`) {
			t.Fatalf("attempt %d response = %d %s", attempt, recorder.Code, recorder.Body.String())
		}
	}
	if wake.calls != 2 {
		t.Fatalf("wake calls = %d, want 2", wake.calls)
	}
	if people.operationScope != adminpeople.PolicyOperationScopeOrganization {
		t.Fatalf("operation scope = %s, want %s", people.operationScope, adminpeople.PolicyOperationScopeOrganization)
	}
}

func TestPlatformEntitlementBulkDirectPolicyJobPassesDirectOperationScope(t *testing.T) {
	defaultOrg := uuid.New()
	people := &platformEntitlementBulkPeopleStub{job: adminpeople.BulkResult{JobID: "job-direct", Status: "queued"}}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{defaultOrganization: tenancy.Organization{ID: defaultOrg, Default: true, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	recorder := httptest.NewRecorder()
	body := `{"selection_token":"selection","confirmation_token":"confirmation","idempotency_key":"direct-command","command":{"kind":"restore_default_entitlement"}}`
	platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated || people.operationScope != adminpeople.PolicyOperationScopeDirectAccounts {
		t.Fatalf("response/scope = %d/%s, want 201/%s: %s", recorder.Code, people.operationScope, adminpeople.PolicyOperationScopeDirectAccounts, recorder.Body.String())
	}
}

func TestPlatformEntitlementBulkPolicyStatusAndCancelUsePolicySpecificMethods(t *testing.T) {
	org := uuid.New()
	people := &platformEntitlementBulkPeopleStub{job: adminpeople.BulkResult{JobID: "job-1", Status: "queued"}}
	handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	router := platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil)
	base := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-jobs/job-1"

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, base, nil),
		httptest.NewRequest(http.MethodPost, base+"/cancel", strings.NewReader(`{}`)),
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"job_id":"job-1"`) {
			t.Fatalf("%s %s = %d %s", request.Method, request.URL.Path, recorder.Code, recorder.Body.String())
		}
	}
	if people.getCalls != 1 || people.cancelCalls != 1 || people.org != org || people.actorID != 7 || people.jobID != "job-1" {
		t.Fatalf("policy method calls = get:%d cancel:%d org:%s actor:%d job:%q", people.getCalls, people.cancelCalls, people.org, people.actorID, people.jobID)
	}
}

func TestPlatformEntitlementBulkMapsStablePolicyErrors(t *testing.T) {
	org := uuid.New()
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-bulk/policy-jobs"
	body := `{"selection_token":"selection","confirmation_token":"confirmation","idempotency_key":"command-1","command":{"kind":"restore_default_entitlement"}}`
	for _, test := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{name: "expired token", err: adminpeople.ErrSelectionExpired, want: http.StatusConflict, code: "selection_expired"},
		{name: "stale confirmation", err: adminpeople.ErrInvalidPolicyConfirmation, want: http.StatusConflict, code: "policy_confirmation_stale"},
		{name: "idempotency conflict", err: adminpeople.ErrBulkIdempotencyConflict, want: http.StatusConflict, code: "idempotency_conflict"},
		{name: "rate limited", err: ErrPlatformEntitlementBulkRateLimited, want: http.StatusTooManyRequests, code: "rate_limited"},
		{name: "store failure", err: errors.New("database offline secret=do-not-leak"), want: http.StatusServiceUnavailable, code: "entitlements_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			people := &platformEntitlementBulkPeopleStub{err: test.err}
			handler := newPlatformEntitlementBulkTestHandler(&platformEntitlementBulkCohortStub{}, people, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
			recorder := httptest.NewRecorder()
			platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			if recorder.Code != test.want || !strings.Contains(recorder.Body.String(), `"error":"`+test.code+`"`) || strings.Contains(recorder.Body.String(), "do-not-leak") {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			if test.want == http.StatusTooManyRequests && recorder.Header().Get("Retry-After") == "" {
				t.Fatal("429 response has no Retry-After")
			}
		})
	}
}

func TestPlatformEntitlementBulkCohortScopeAndRedirectRejection(t *testing.T) {
	org, cohortID := uuid.New(), uuid.New()
	cohorts := &platformEntitlementBulkCohortStub{item: entitlements.CohortRevision{ID: cohortID, OrganizationID: org, Name: "Premium", Revision: 2}}
	handler := newPlatformEntitlementBulkTestHandler(cohorts, &platformEntitlementBulkPeopleStub{}, platformEntitlementBulkOrganizationsStub{organization: tenancy.Organization{ID: org, Status: tenancy.OrganizationActive}}, platformEntitlementBulkAuthorizerStub{allowed: true}, nil)
	router := platformEntitlementBulkRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, nil)
	path := "/api/v2/admin/platform/organizations/" + org.String() + "/entitlement-cohorts/" + cohortID.String()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK || cohorts.org != org || !strings.Contains(recorder.Body.String(), `"cohort_id":"`+cohortID.String()+`"`) {
		t.Fatalf("cohort response = %d %s", recorder.Code, recorder.Body.String())
	}

	for _, requestPath := range []string{path + "/", strings.Replace(path, "/entitlement-cohorts/", "//entitlement-cohorts/", 1)} {
		recorder = httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if recorder.Code >= 300 && recorder.Code < 400 {
			t.Fatalf("alternate path %q redirected with %d", requestPath, recorder.Code)
		}
	}
}
