package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type replayLifecycleCoordinator struct{ result lifecycleidempotency.Result }

func (c replayLifecycleCoordinator) Execute(context.Context, lifecycleidempotency.Request, lifecycleidempotency.Mutator) (lifecycleidempotency.Result, error) {
	return c.result, nil
}
func (c replayLifecycleCoordinator) ExecuteCreate(context.Context, lifecycleidempotency.Request, lifecycleidempotency.CreateMutator) (lifecycleidempotency.Result, error) {
	return c.result, nil
}

type adminPlatformStoreStub struct {
	organization tenancy.Organization
	membership   tenancy.Membership
	page         tenancy.OrganizationPage
	memberships  tenancy.MembershipPage
	listErr      error
	getErr       error
	mutationErr  error
	getErrors    []error
	actor        tenancy.AdminMutationActor
	calls        int
}

func (s *adminPlatformStoreStub) ListOrganizations(context.Context, tenancy.OrganizationFilter) (tenancy.OrganizationPage, error) {
	s.calls++
	return s.page, s.listErr
}

func (s *adminPlatformStoreStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	s.calls++
	if len(s.getErrors) > 0 {
		err := s.getErrors[0]
		s.getErrors = s.getErrors[1:]
		return s.organization, err
	}
	return s.organization, s.getErr
}

func (s *adminPlatformStoreStub) GetOrganizationSummary(context.Context, uuid.UUID) (tenancy.OrganizationSummary, error) {
	s.calls++
	return tenancy.OrganizationSummary{Organization: s.organization}, s.getErr
}

func (s *adminPlatformStoreStub) CreateOrganization(ctx context.Context, _ tenancy.CreateOrganizationInput) (tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) UpdateOrganization(ctx context.Context, _ uuid.UUID, _ int64, _ tenancy.UpdateOrganizationInput) (tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) SetOrganizationStatus(ctx context.Context, _ uuid.UUID, _ int64, _ tenancy.OrganizationStatus) (tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) TransferOwnership(ctx context.Context, _ uuid.UUID, _ int64, _ int) (tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) ListOrganizationMemberships(context.Context, uuid.UUID, tenancy.MembershipFilter) (tenancy.MembershipPage, error) {
	s.calls++
	return s.memberships, s.listErr
}

func (s *adminPlatformStoreStub) GetOrganizationMembership(context.Context, uuid.UUID, uuid.UUID) (tenancy.Membership, error) {
	s.calls++
	return s.membership, s.getErr
}

func (s *adminPlatformStoreStub) CreateMembership(ctx context.Context, _ uuid.UUID, _ int64, _ tenancy.CreateMembershipInput) (tenancy.Membership, tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.membership, s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) UpdateMembership(ctx context.Context, _ uuid.UUID, _ uuid.UUID, _ int64, _ tenancy.UpdateMembershipInput) (tenancy.Membership, tenancy.Organization, error) {
	s.calls++
	s.actor, _ = tenancy.AdminMutationActorFromContext(ctx)
	return s.membership, s.organization, s.mutationErr
}

type adminReauthVerifierStub struct {
	allowed    bool
	err        error
	calls      int
	credential string
}

func (s *adminReauthVerifierStub) VerifyPassword(_ context.Context, _ int, credential string) (bool, error) {
	s.calls++
	s.credential = credential
	return s.allowed, s.err
}

func TestBloemAdminPlatformRejectsOrganizationContextBeforeStoreAccess(t *testing.T) {
	store := &adminPlatformStoreStub{}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/platform/organizations", nil)
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{
		AccountID: 7, Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New(), PolicyRevision: 1, SecurityRevision: 1,
	}))
	rec := httptest.NewRecorder()

	handler.HandleListOrganizations(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_platform_authority"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestBloemAdminPlatformCreateValidatesFieldsAndProvidesAtomicAuditContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ownerID := 42
	store := &adminPlatformStoreStub{organization: tenancy.Organization{
		ID: organizationID, Name: "North Sea Media", Slug: "north-sea-media", Status: tenancy.OrganizationActive,
		OwnerAccountID: &ownerID, PolicyRevision: 1,
	}}
	handler := NewBloemAdminPlatformHandler(store, nil)

	invalid := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations", `{"name":"","slug":"Bad Slug","owner_account_id":0}`, nil)
	invalidRec := httptest.NewRecorder()
	handler.HandleCreateOrganization(invalidRec, invalid)
	if invalidRec.Code != http.StatusUnprocessableEntity || !strings.Contains(invalidRec.Body.String(), `"fields"`) || !strings.Contains(invalidRec.Body.String(), `"slug"`) {
		t.Fatalf("invalid response = %d %s", invalidRec.Code, invalidRec.Body.String())
	}

	req := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations", `{"name":"North Sea Media","slug":"north-sea-media","owner_account_id":42,"invite_token":"secret"}`, nil)
	rec := httptest.NewRecorder()
	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown secret field response = %d %s", rec.Code, rec.Body.String())
	}

	req = adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations", `{"name":"North Sea Media","slug":"north-sea-media","owner_account_id":42}`, nil)
	rec = httptest.NewRecorder()
	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"policy_revision":1`) || strings.Contains(rec.Body.String(), `"PolicyRevision"`) {
		t.Fatalf("response does not use v2 snake_case fields: %s", rec.Body.String())
	}
	if store.actor.AccountID != 7 || store.actor.PlatformRole != "platform_admin" || store.actor.AuthorityContext != "platform" || store.actor.RequestID != "request-123" {
		t.Fatalf("atomic audit actor = %+v", store.actor)
	}
}

func TestBloemAdminPlatformCreateReplaysReceiptBeforeStoreAccess(t *testing.T) {
	store := &adminPlatformStoreStub{}
	handler := NewBloemAdminPlatformHandler(store, nil)
	body := []byte(`{"organization":{"id":"10000000-0000-0000-0000-000000000001","slug":"first","name":"First","status":"active","policy_revision":1}}`)
	handler.SetLifecycleIdempotency(replayLifecycleCoordinator{result: lifecycleidempotency.Result{Status: http.StatusCreated, Body: body, Replayed: true}}, func(string, string, map[string]string, url.Values, []byte) lifecycleidempotency.Digest {
		return lifecycleidempotency.Digest{1}
	})
	req := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations/", `{"name":"First","slug":"first","owner_account_id":42}`, nil)
	req.Header.Set("Idempotency-Key", "organization-create-0001")
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopePlatform}))
	rec := httptest.NewRecorder()

	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusCreated || rec.Body.String() != string(body) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want receipt-first replay", store.calls)
	}
}

func TestBloemAdminPlatformUpdateReplaysReceiptBeforeStoreAccess(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPlatformStoreStub{}
	handler := NewBloemAdminPlatformHandler(store, nil)
	body := []byte(`{"organization":{"id":"10000000-0000-0000-0000-000000000001","slug":"first","name":"First","status":"active","policy_revision":2}}`)
	handler.SetLifecycleIdempotency(replayLifecycleCoordinator{result: lifecycleidempotency.Result{Status: http.StatusOK, Body: body, Replayed: true}}, func(string, string, map[string]string, url.Values, []byte) lifecycleidempotency.Digest {
		return lifecycleidempotency.Digest{1}
	})
	req := adminPlatformRequest(http.MethodPatch, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String(), `{"expected_revision":1,"name":"First"}`, map[string]string{"id": organizationID.String()})
	req.Header.Set("Idempotency-Key", "organization-update-0001")
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.MustParse("11111111-2222-4333-8444-555555555555"), Scope: auth.AdminScopePlatform}))
	rec := httptest.NewRecorder()

	handler.HandleUpdateOrganization(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != string(body) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want receipt-first replay", store.calls)
	}
}

func TestBloemAdminPlatformMapsStaleRevisionWithCurrentRevision(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPlatformStoreStub{
		organization: tenancy.Organization{ID: organizationID, PolicyRevision: 9},
		mutationErr:  tenancy.ErrAuthorizationStateChanged,
	}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := adminPlatformRequest(http.MethodPatch, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String(), `{"expected_revision":8,"name":"Changed"}`, map[string]string{"id": organizationID.String()})
	rec := httptest.NewRecorder()

	handler.HandleUpdateOrganization(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"authorization_state_changed"`) || !strings.Contains(rec.Body.String(), `"current_revision":9`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformMembershipIdentifierIsNonDisclosing(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	store := &adminPlatformStoreStub{mutationErr: tenancy.ErrMembershipNotFound}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := adminPlatformRequest(http.MethodPatch, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String()+"/memberships/"+membershipID.String(), `{"expected_revision":3,"status":"suspended"}`, map[string]string{
		"id": organizationID.String(), "membership_id": membershipID.String(),
	})
	rec := httptest.NewRecorder()

	handler.HandleUpdateMembership(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error":"not_found"`) || strings.Contains(rec.Body.String(), membershipID.String()) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformMapsUnavailableStore(t *testing.T) {
	handler := NewBloemAdminPlatformHandler(&adminPlatformStoreStub{listErr: errors.New("database offline")}, nil)
	req := adminPlatformRequest(http.MethodGet, NativeAPIPrefix+"/admin/platform/organizations", "", nil)
	rec := httptest.NewRecorder()

	handler.HandleListOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformAtomicAuditFailureReturnsUnavailable(t *testing.T) {
	store := &adminPlatformStoreStub{mutationErr: errors.New("record admin audit event: forced failure")}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations", `{"name":"Atomic","slug":"atomic","owner_account_id":42}`, nil)
	rec := httptest.NewRecorder()

	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformTransferRequiresValidReauthentication(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ownerID := 42
	for _, test := range []struct {
		name       string
		password   string
		verifier   *adminReauthVerifierStub
		wantStatus int
		wantCalls  int
	}{
		{name: "absent credential", verifier: &adminReauthVerifierStub{allowed: true}, wantStatus: http.StatusUnauthorized},
		{name: "wrong credential", password: "wrong", verifier: &adminReauthVerifierStub{}, wantStatus: http.StatusUnauthorized},
		{name: "correct credential", password: "correct horse battery staple", verifier: &adminReauthVerifierStub{allowed: true}, wantStatus: http.StatusOK, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &adminPlatformStoreStub{organization: tenancy.Organization{ID: organizationID, OwnerAccountID: &ownerID, PolicyRevision: 5}}
			handler := NewBloemAdminPlatformHandler(store, test.verifier)
			body := `{"expected_revision":4,"owner_account_id":42,"confirmed":true,"password":"` + test.password + `"}`
			req := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String()+"/transfer-ownership", body, map[string]string{"id": organizationID.String()})
			rec := httptest.NewRecorder()

			handler.HandleTransferOwnership(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("response = %d %s, want %d", rec.Code, rec.Body.String(), test.wantStatus)
			}
			if store.calls != test.wantCalls {
				t.Fatalf("store calls = %d, want %d", store.calls, test.wantCalls)
			}
			if strings.Contains(rec.Body.String(), test.password) && test.password != "" {
				t.Fatalf("response disclosed credential: %s", rec.Body.String())
			}
		})
	}
}

func TestBloemAdminPlatformStaleRevisionLookupFailureReturnsUnavailable(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPlatformStoreStub{
		organization: tenancy.Organization{ID: organizationID, PolicyRevision: 9},
		mutationErr:  tenancy.ErrAuthorizationStateChanged,
		getErrors:    []error{errors.New("revision lookup failed")},
	}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := adminPlatformRequest(http.MethodPatch, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String(), `{"expected_revision":8,"name":"Changed"}`, map[string]string{"id": organizationID.String()})
	rec := httptest.NewRecorder()

	handler.HandleUpdateOrganization(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) || strings.Contains(rec.Body.String(), `"current_revision":0`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBloemAdminPlatformMissingMembershipAccountIsFieldAddressable(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPlatformStoreStub{mutationErr: tenancy.ErrAccountNotFound}
	handler := NewBloemAdminPlatformHandler(store, nil)
	req := adminPlatformRequest(http.MethodPost, NativeAPIPrefix+"/admin/platform/organizations/"+organizationID.String()+"/memberships", `{"expected_revision":3,"account_id":999,"legacy_role":"user"}`, map[string]string{"id": organizationID.String()})
	rec := httptest.NewRecorder()

	handler.HandleCreateMembership(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"account_id"`) || strings.Contains(rec.Body.String(), `"owner_account_id"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func adminPlatformRequest(method, path, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "request-123")
	ctx := apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform})
	if len(params) > 0 {
		routeCtx := chi.NewRouteContext()
		for key, value := range params {
			routeCtx.URLParams.Add(key, value)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	}
	return req.WithContext(ctx)
}
