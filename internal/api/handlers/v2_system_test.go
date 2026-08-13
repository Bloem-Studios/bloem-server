package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type v2OrganizationStoreStub struct {
	memberships   []tenancy.Membership
	organizations map[uuid.UUID]tenancy.Organization
	err           error
}

func (s v2OrganizationStoreStub) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return s.memberships, s.err
}

func (s v2OrganizationStoreStub) GetOrganization(_ context.Context, id uuid.UUID) (tenancy.Organization, error) {
	organization, ok := s.organizations[id]
	if !ok {
		return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
	}
	return organization, nil
}

func TestV2CapabilitiesExactContract(t *testing.T) {
	handler := NewV2SystemHandler(nil)
	rec := httptest.NewRecorder()
	handler.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := `{"api":"v2","identity_schema":1,"features":{"legacy_silo_v1":true,"organization_memberships":true,"tenant_bounded_media_scope":true,"direct_profile_login":false,"shared_device_pairing":false,"delegated_admin_roles":false}}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("body = %s, want %s", rec.Body.String(), want)
	}
}

func TestV2OrganizationsReturnsOnlyActiveMembershipsAndOrganizations(t *testing.T) {
	activeID, hiddenID, invitedID, ownerlessID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	activeMembershipID := uuid.New()
	ownerAccountID := 7
	handler := NewV2SystemHandler(v2OrganizationStoreStub{
		memberships: []tenancy.Membership{
			{ID: activeMembershipID, OrganizationID: activeID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 4},
			{ID: uuid.New(), OrganizationID: hiddenID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "user", SecurityRevision: 2},
			{ID: uuid.New(), OrganizationID: invitedID, AccountID: 7, Status: tenancy.MembershipInvited, LegacyRole: "user", SecurityRevision: 1},
			{ID: uuid.New(), OrganizationID: ownerlessID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "user", SecurityRevision: 1},
		},
		organizations: map[uuid.UUID]tenancy.Organization{
			activeID:    {ID: activeID, Slug: "vondel", Name: "Vondel", Status: tenancy.OrganizationActive, OwnerAccountID: &ownerAccountID, PolicyRevision: 9, Default: true},
			hiddenID:    {ID: hiddenID, Slug: "hidden", Name: "Hidden", Status: tenancy.OrganizationSuspended, PolicyRevision: 3},
			ownerlessID: {ID: ownerlessID, Slug: "ownerless", Name: "Ownerless", Status: tenancy.OrganizationActive, PolicyRevision: 1},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Organizations []map[string]any `json:"organizations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Organizations) != 1 {
		t.Fatalf("organizations = %#v, want exactly one", body.Organizations)
	}
	organization := body.Organizations[0]
	if organization["id"] != activeID.String() || organization["membership_id"] != activeMembershipID.String() || organization["name"] != "Vondel" {
		t.Fatalf("organization = %#v", organization)
	}
	for _, forbidden := range []string{"owner_email", "owner_account_id", "member_count", "members"} {
		if _, ok := organization[forbidden]; ok {
			t.Errorf("organization leaks %q: %#v", forbidden, organization)
		}
	}
}

func TestV2OrganizationsRequiresAuthentication(t *testing.T) {
	handler := NewV2SystemHandler(v2OrganizationStoreStub{})
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil))
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized","message":"Authentication required"}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2OrganizationsUnavailableWithoutStore(t *testing.T) {
	handler := NewV2SystemHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestV2OrganizationsFailsClosedOnStoreError(t *testing.T) {
	handler := NewV2SystemHandler(v2OrganizationStoreStub{err: errors.New("database failed")})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "database failed") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
