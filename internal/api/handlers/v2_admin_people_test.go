package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type adminPeopleServiceStub struct {
	organizationID uuid.UUID
	actorID        int
	accountID      int
	calls          int
	person         adminpeople.PersonSummary
	page           adminpeople.Page
	selection      adminpeople.Selection
	result         adminpeople.BulkResult
	err            error
	authority      string
}

func (s *adminPeopleServiceStub) List(_ context.Context, org uuid.UUID, _ adminpeople.Filter) (adminpeople.Page, error) {
	s.calls++
	s.organizationID = org
	return s.page, s.err
}
func (s *adminPeopleServiceStub) Get(_ context.Context, org uuid.UUID, accountID int) (adminpeople.PersonSummary, error) {
	s.calls++
	s.organizationID = org
	s.accountID = accountID
	return s.person, s.err
}
func (s *adminPeopleServiceStub) CreateSelection(_ context.Context, org uuid.UUID, _ adminpeople.Filter) (adminpeople.Selection, error) {
	s.calls++
	s.organizationID = org
	return s.selection, s.err
}
func (s *adminPeopleServiceStub) ExecuteBulk(ctx context.Context, org uuid.UUID, actorID int, _ adminpeople.BulkAction) (adminpeople.BulkResult, error) {
	s.calls++
	s.organizationID = org
	s.actorID = actorID
	if actor, ok := adminpeople.MutationActorFromContext(ctx); ok {
		s.authority = actor.Authority
	}
	return s.result, s.err
}
func (s *adminPeopleServiceStub) GetBulkJob(_ context.Context, org uuid.UUID, _ string) (adminpeople.BulkResult, error) {
	s.calls++
	s.organizationID = org
	return s.result, s.err
}
func (s *adminPeopleServiceStub) UpdateMembership(ctx context.Context, org uuid.UUID, actorID, accountID int, _ int64, _ tenancy.MembershipStatus) (adminpeople.PersonSummary, error) {
	s.calls++
	s.organizationID = org
	s.actorID = actorID
	s.accountID = accountID
	if actor, ok := adminpeople.MutationActorFromContext(ctx); ok {
		s.authority = actor.Authority
	}
	return s.person, s.err
}
func (s *adminPeopleServiceStub) UpdateProfileGroup(ctx context.Context, org uuid.UUID, actorID, accountID int, _ string, _ int64, _ int) (adminpeople.PersonSummary, error) {
	s.calls++
	s.organizationID = org
	s.actorID = actorID
	s.accountID = accountID
	if actor, ok := adminpeople.MutationActorFromContext(ctx); ok {
		s.authority = actor.Authority
	}
	return s.person, s.err
}

func TestV2AdminPeopleRequiresResolvedOrganizationContextBeforeStoreAccess(t *testing.T) {
	store := &adminPeopleServiceStub{}
	handler := NewV2AdminPeopleHandler(store)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/organization/people", nil)
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}))
	rec := httptest.NewRecorder()

	handler.HandleListPeople(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"insufficient_organization_authority"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestV2AdminPeopleUsesOnlyMiddlewareOrganizationAndActor(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPeopleServiceStub{result: adminpeople.BulkResult{JobID: "job-1", Succeeded: 2}}
	handler := NewV2AdminPeopleHandler(store)
	req := adminPeopleRequest(http.MethodPost, "/api/v2/admin/organization/people/bulk-jobs", `{"selection_token":"signed","kind":"suspend_memberships"}`, organizationID, 7, nil)
	rec := httptest.NewRecorder()

	handler.HandleCreateBulkJob(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.organizationID != organizationID || store.actorID != 7 {
		t.Fatalf("authority = %s/%d", store.organizationID, store.actorID)
	}

	malicious := adminPeopleRequest(http.MethodPost, "/api/v2/admin/organization/people/bulk-jobs", `{"organization_id":"`+uuid.NewString()+`","selection_token":"signed","kind":"suspend_memberships"}`, organizationID, 7, nil)
	maliciousRec := httptest.NewRecorder()
	handler.HandleCreateBulkJob(maliciousRec, malicious)
	if maliciousRec.Code != http.StatusBadRequest {
		t.Fatalf("organization selector response = %d %s", maliciousRec.Code, maliciousRec.Body.String())
	}
}

func TestV2AdminPeoplePropagatesEffectivePlatformAuthorityInOrganizationContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPeopleServiceStub{result: adminpeople.BulkResult{JobID: "job-1"}}
	handler := NewV2AdminPeopleHandler(store)
	req := adminPeopleRequest(http.MethodPost, "/api/v2/admin/organization/people/bulk-jobs", `{"selection_token":"signed","kind":"suspend_memberships"}`, organizationID, 7, nil)
	claims, _ := apimw.GetAdminContextClaims(req.Context())
	claims.EffectiveAuthority = adminpeople.AuthorityPlatformAdmin
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	handler.HandleCreateBulkJob(rec, req)
	if rec.Code != http.StatusCreated || store.authority != adminpeople.AuthorityPlatformAdmin {
		t.Fatalf("response=%d %s authority=%q", rec.Code, rec.Body.String(), store.authority)
	}
}

func TestV2AdminPeopleForeignPathTargetsAreNonDisclosing(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	store := &adminPeopleServiceStub{err: adminpeople.ErrNotFound}
	handler := NewV2AdminPeopleHandler(store)
	req := adminPeopleRequest(http.MethodGet, "/api/v2/admin/organization/people/42", "", organizationID, 7, map[string]string{"account_id": "42"})
	rec := httptest.NewRecorder()

	handler.HandleGetPerson(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error":"not_found"`) || strings.Contains(rec.Body.String(), "42") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if store.organizationID != organizationID || store.accountID != 42 {
		t.Fatalf("target = %s/%d", store.organizationID, store.accountID)
	}

	store.err = errors.New("database offline")
	unavailable := httptest.NewRecorder()
	handler.HandleGetPerson(unavailable, req)
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"error":"tenant_unavailable"`) {
		t.Fatalf("unavailable response = %d %s", unavailable.Code, unavailable.Body.String())
	}
}

func adminPeopleRequest(method, target, body string, organizationID uuid.UUID, accountID int, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	membershipID := uuid.New()
	ctx := tenancy.WithContext(req.Context(), tenancy.Context{OrganizationID: organizationID, AccountID: accountID, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive})
	ctx = apimw.SetAdminContextClaims(ctx, auth.AdminContextClaims{AccountID: accountID, Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID})
	if len(params) > 0 {
		routeCtx := chi.NewRouteContext()
		for key, value := range params {
			routeCtx.URLParams.Add(key, value)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	}
	return req.WithContext(ctx)
}
