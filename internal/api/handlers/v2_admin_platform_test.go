package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/activitylog"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type adminPlatformStoreStub struct {
	organization tenancy.Organization
	membership   tenancy.Membership
	page         tenancy.OrganizationPage
	memberships  tenancy.MembershipPage
	listErr      error
	getErr       error
	mutationErr  error
	calls        int
}

func (s *adminPlatformStoreStub) ListOrganizations(context.Context, tenancy.OrganizationFilter) (tenancy.OrganizationPage, error) {
	s.calls++
	return s.page, s.listErr
}

func (s *adminPlatformStoreStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	s.calls++
	return s.organization, s.getErr
}

func (s *adminPlatformStoreStub) GetOrganizationSummary(context.Context, uuid.UUID) (tenancy.OrganizationSummary, error) {
	s.calls++
	return tenancy.OrganizationSummary{Organization: s.organization}, s.getErr
}

func (s *adminPlatformStoreStub) CreateOrganization(context.Context, tenancy.CreateOrganizationInput) (tenancy.Organization, error) {
	s.calls++
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) UpdateOrganization(context.Context, uuid.UUID, int64, tenancy.UpdateOrganizationInput) (tenancy.Organization, error) {
	s.calls++
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) SetOrganizationStatus(context.Context, uuid.UUID, int64, tenancy.OrganizationStatus) (tenancy.Organization, error) {
	s.calls++
	return s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) TransferOwnership(context.Context, uuid.UUID, int64, int) (tenancy.Organization, error) {
	s.calls++
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

func (s *adminPlatformStoreStub) CreateMembership(context.Context, uuid.UUID, int64, tenancy.CreateMembershipInput) (tenancy.Membership, tenancy.Organization, error) {
	s.calls++
	return s.membership, s.organization, s.mutationErr
}

func (s *adminPlatformStoreStub) UpdateMembership(context.Context, uuid.UUID, uuid.UUID, int64, tenancy.UpdateMembershipInput) (tenancy.Membership, tenancy.Organization, error) {
	s.calls++
	return s.membership, s.organization, s.mutationErr
}

type adminAuditRecorderStub struct {
	events []activitylog.AdminEvent
	err    error
}

func (s *adminAuditRecorderStub) RecordAdminEvent(_ context.Context, event activitylog.AdminEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func TestV2AdminPlatformRejectsOrganizationContextBeforeStoreAccess(t *testing.T) {
	store := &adminPlatformStoreStub{}
	handler := NewV2AdminPlatformHandler(store, &adminAuditRecorderStub{})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/platform/organizations", nil)
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

func TestV2AdminPlatformCreateValidatesFieldsAndAuditsWithoutSecrets(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ownerID := 42
	store := &adminPlatformStoreStub{organization: tenancy.Organization{
		ID: organizationID, Name: "North Sea Media", Slug: "north-sea-media", Status: tenancy.OrganizationActive,
		OwnerAccountID: &ownerID, PolicyRevision: 1,
	}}
	audit := &adminAuditRecorderStub{}
	handler := NewV2AdminPlatformHandler(store, audit)

	invalid := adminPlatformRequest(http.MethodPost, "/api/v2/admin/platform/organizations", `{"name":"","slug":"Bad Slug","owner_account_id":0}`, nil)
	invalidRec := httptest.NewRecorder()
	handler.HandleCreateOrganization(invalidRec, invalid)
	if invalidRec.Code != http.StatusUnprocessableEntity || !strings.Contains(invalidRec.Body.String(), `"fields"`) || !strings.Contains(invalidRec.Body.String(), `"slug"`) {
		t.Fatalf("invalid response = %d %s", invalidRec.Code, invalidRec.Body.String())
	}

	req := adminPlatformRequest(http.MethodPost, "/api/v2/admin/platform/organizations", `{"name":"North Sea Media","slug":"north-sea-media","owner_account_id":42,"invite_token":"secret"}`, nil)
	rec := httptest.NewRecorder()
	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown secret field response = %d %s", rec.Code, rec.Body.String())
	}

	req = adminPlatformRequest(http.MethodPost, "/api/v2/admin/platform/organizations", `{"name":"North Sea Media","slug":"north-sea-media","owner_account_id":42}`, nil)
	rec = httptest.NewRecorder()
	handler.HandleCreateOrganization(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"policy_revision":1`) || strings.Contains(rec.Body.String(), `"PolicyRevision"`) {
		t.Fatalf("response does not use v2 snake_case fields: %s", rec.Body.String())
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	event := audit.events[0]
	encoded, _ := json.Marshal(event)
	if event.ActorAccountID != 7 || event.AuthorityContext != "platform" || event.OrganizationID != organizationID || event.RequestID != "request-123" ||
		event.BeforeRevision != 0 || event.AfterRevision != 1 || event.Outcome != "success" || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "token") {
		t.Fatalf("audit event = %s", encoded)
	}
}

func TestV2AdminPlatformMapsStaleRevisionWithCurrentRevision(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPlatformStoreStub{
		organization: tenancy.Organization{ID: organizationID, PolicyRevision: 9},
		mutationErr:  tenancy.ErrAuthorizationStateChanged,
	}
	handler := NewV2AdminPlatformHandler(store, &adminAuditRecorderStub{})
	req := adminPlatformRequest(http.MethodPatch, "/api/v2/admin/platform/organizations/"+organizationID.String(), `{"expected_revision":8,"name":"Changed"}`, map[string]string{"id": organizationID.String()})
	rec := httptest.NewRecorder()

	handler.HandleUpdateOrganization(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"authorization_state_changed"`) || !strings.Contains(rec.Body.String(), `"current_revision":9`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminPlatformMembershipIdentifierIsNonDisclosing(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	store := &adminPlatformStoreStub{getErr: tenancy.ErrMembershipNotFound}
	handler := NewV2AdminPlatformHandler(store, &adminAuditRecorderStub{})
	req := adminPlatformRequest(http.MethodPatch, "/api/v2/admin/platform/organizations/"+organizationID.String()+"/memberships/"+membershipID.String(), `{"expected_revision":3,"status":"suspended"}`, map[string]string{
		"id": organizationID.String(), "membership_id": membershipID.String(),
	})
	rec := httptest.NewRecorder()

	handler.HandleUpdateMembership(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error":"not_found"`) || strings.Contains(rec.Body.String(), membershipID.String()) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminPlatformMapsUnavailableStore(t *testing.T) {
	handler := NewV2AdminPlatformHandler(&adminPlatformStoreStub{listErr: errors.New("database offline")}, &adminAuditRecorderStub{})
	req := adminPlatformRequest(http.MethodGet, "/api/v2/admin/platform/organizations", "", nil)
	rec := httptest.NewRecorder()

	handler.HandleListOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"tenant_unavailable"`) {
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
