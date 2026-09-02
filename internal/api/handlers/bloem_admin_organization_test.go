package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type organizationOverviewStub struct {
	got  uuid.UUID
	item tenancy.OrganizationSummary
	err  error
}

func (s *organizationOverviewStub) GetOrganizationSummary(_ context.Context, id uuid.UUID) (tenancy.OrganizationSummary, error) {
	s.got = id
	return s.item, s.err
}

type organizationGroupStub struct {
	gotOrganization uuid.UUID
	gotID           int64
	groups          []access.Group
	group           *access.Group
	impact          access.GroupDeletionImpact
	err             error
}

func (s *organizationGroupStub) List(_ context.Context, id uuid.UUID) ([]access.Group, error) {
	s.gotOrganization = id
	return s.groups, s.err
}
func (s *organizationGroupStub) Get(_ context.Context, id uuid.UUID, groupID int64) (*access.Group, error) {
	s.gotOrganization, s.gotID = id, groupID
	return s.group, s.err
}
func (s *organizationGroupStub) Create(_ context.Context, id uuid.UUID, input access.CreateGroupInput) (*access.Group, error) {
	s.gotOrganization = id
	return s.group, s.err
}
func (s *organizationGroupStub) Update(_ context.Context, id uuid.UUID, groupID int64, input access.UpdateGroupInput) (*access.Group, error) {
	s.gotOrganization, s.gotID = id, groupID
	return s.group, s.err
}
func (s *organizationGroupStub) DeleteWithImpact(_ context.Context, id uuid.UUID, groupID int64) (access.GroupDeletionImpact, error) {
	s.gotOrganization, s.gotID = id, groupID
	return s.impact, s.err
}

type organizationResourceStub struct {
	got         uuid.UUID
	gotFolderID int64
	gotRevision int64
	libraries   []resourcetenancy.LibraryProjection
	entitlement resourcetenancy.LibraryEntitlement
	err         error
}

func (s *organizationResourceStub) ListLibraries(_ context.Context, id uuid.UUID) ([]resourcetenancy.LibraryProjection, error) {
	s.got = id
	return s.libraries, s.err
}
func (s *organizationResourceStub) SetLibraryEntitlementStatus(_ context.Context, id uuid.UUID, folderID, revision int64, status resourcetenancy.EntitlementStatus) (resourcetenancy.LibraryEntitlement, error) {
	s.got, s.gotFolderID, s.gotRevision = id, folderID, revision
	return s.entitlement, s.err
}
func (s *organizationResourceStub) DeleteLibraryEntitlement(_ context.Context, id uuid.UUID, folderID, revision int64) error {
	s.got, s.gotFolderID, s.gotRevision = id, folderID, revision
	return s.err
}

type organizationInvitationStub struct {
	got        uuid.UUID
	gotInviter int64
	items      []*models.Invitation
	created    *models.Invitation
	err        error
}

func (s *organizationInvitationStub) ListForOrganization(_ context.Context, id uuid.UUID) ([]*models.Invitation, error) {
	s.got = id
	return s.items, s.err
}
func (s *organizationInvitationStub) CreateForOrganization(_ context.Context, id uuid.UUID, input models.CreateInvitationInput, tokenHash string) (*models.Invitation, error) {
	s.got, s.gotInviter = id, input.InvitedBy
	return s.created, s.err
}

func TestBloemOrganizationOverviewUsesOnlyResolvedContext(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	foreignID := uuid.MustParse("10000000-0000-0000-0000-000000000099")
	store := &organizationOverviewStub{item: tenancy.OrganizationSummary{Organization: tenancy.Organization{ID: organizationID, Name: "Local"}, MembershipCount: 3}}
	h := NewBloemAdminOrganizationHandler(store, nil, nil, nil)
	req := organizationRequest(http.MethodGet, NativeAPIPrefix+"/admin/organization/overview?organization_id="+foreignID.String(), "", organizationID, 7, nil)
	rec := httptest.NewRecorder()

	h.HandleOverview(rec, req)
	if rec.Code != http.StatusOK || store.got != organizationID || !strings.Contains(rec.Body.String(), `"name":"Local"`) || strings.Contains(rec.Body.String(), foreignID.String()) {
		t.Fatalf("response=%d %s store organization=%s", rec.Code, rec.Body.String(), store.got)
	}
}

func TestBloemOrganizationGroupsCRUDIsScopedRevisionGuardedAndReturnsReassignmentImpact(t *testing.T) {
	organizationID := uuid.New()
	group := &access.Group{ID: 21, OrganizationID: organizationID, Name: "Kids"}
	store := &organizationGroupStub{groups: []access.Group{*group}, group: group, impact: access.GroupDeletionImpact{ProfilesReassigned: 4, DefaultGroupID: 3}}
	h := NewBloemAdminOrganizationHandler(nil, store, nil, nil)

	list := httptest.NewRecorder()
	h.HandleListGroups(list, organizationRequest(http.MethodGet, "/groups", "", organizationID, 7, nil))
	if list.Code != http.StatusOK || store.gotOrganization != organizationID || !strings.Contains(list.Body.String(), `"name":"Kids"`) {
		t.Fatalf("list=%d %s organization=%s", list.Code, list.Body.String(), store.gotOrganization)
	}

	stale := httptest.NewRecorder()
	h.HandleUpdateGroup(stale, organizationRequest(http.MethodPut, "/groups/21", `{"expected_revision":6,"name":"Teens"}`, organizationID, 7, map[string]string{"id": "21"}))
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"error":"authorization_state_changed"`) || store.gotID != 0 {
		t.Fatalf("stale=%d %s store id=%d", stale.Code, stale.Body.String(), store.gotID)
	}

	deleted := httptest.NewRecorder()
	h.HandleDeleteGroup(deleted, organizationRequest(http.MethodDelete, "/groups/21", `{"expected_revision":7}`, organizationID, 7, map[string]string{"id": "21"}))
	if deleted.Code != http.StatusOK || store.gotOrganization != organizationID || store.gotID != 21 || !strings.Contains(deleted.Body.String(), `"profiles_reassigned":4`) || !strings.Contains(deleted.Body.String(), `"default_group_id":3`) {
		t.Fatalf("delete=%d %s target=%s/%d", deleted.Code, deleted.Body.String(), store.gotOrganization, store.gotID)
	}

	store.err = access.ErrGroupNotFound
	foreign := httptest.NewRecorder()
	h.HandleGetGroup(foreign, organizationRequest(http.MethodGet, "/groups/99", "", organizationID, 7, map[string]string{"id": "99"}))
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), "99") {
		t.Fatalf("foreign=%d %s", foreign.Code, foreign.Body.String())
	}
}

func TestBloemOrganizationLibrariesDistinguishOwnedAndEntitledAndHideForeign(t *testing.T) {
	organizationID := uuid.New()
	foreignID := uuid.New()
	store := &organizationResourceStub{libraries: []resourcetenancy.LibraryProjection{
		{FolderID: 4, Name: "Owned", Type: "movies", AccessKind: resourcetenancy.LibraryOwned},
		{FolderID: 8, Name: "Granted", Type: "series", AccessKind: resourcetenancy.LibraryEntitled, Entitlement: &resourcetenancy.LibraryEntitlement{SecurityRevision: 2, Status: resourcetenancy.EntitlementActive}},
	}}
	h := NewBloemAdminOrganizationHandler(nil, nil, store, nil)
	rec := httptest.NewRecorder()
	h.HandleListLibraries(rec, organizationRequest(http.MethodGet, "/libraries?organization_id="+foreignID.String(), "", organizationID, 7, nil))
	if rec.Code != http.StatusOK || store.got != organizationID || !strings.Contains(rec.Body.String(), `"access_kind":"owned"`) || !strings.Contains(rec.Body.String(), `"access_kind":"entitled"`) || strings.Contains(rec.Body.String(), foreignID.String()) {
		t.Fatalf("response=%d %s organization=%s", rec.Code, rec.Body.String(), store.got)
	}

	store.err = resourcetenancy.ErrResourceHidden
	foreign := httptest.NewRecorder()
	h.HandleUpdateEntitlement(foreign, organizationRequest(http.MethodPut, "/entitlements/44", `{"expected_revision":1,"status":"suspended"}`, organizationID, 7, map[string]string{"folder_id": "44"}))
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), "44") {
		t.Fatalf("foreign=%d %s", foreign.Code, foreign.Body.String())
	}
}

func TestBloemOrganizationInvitationsUseResolvedOrganizationAndNeverReturnTokenHashes(t *testing.T) {
	organizationID := uuid.New()
	foreignID := uuid.New()
	store := &organizationInvitationStub{items: []*models.Invitation{{ID: 2, OrganizationID: organizationID, Email: "local@example.test", TokenHash: "must-not-leak", ExpiresAt: time.Now().Add(time.Hour)}}}
	h := NewBloemAdminOrganizationHandler(nil, nil, nil, store)
	rec := httptest.NewRecorder()
	h.HandleListInvitations(rec, organizationRequest(http.MethodGet, "/invitations?organization_id="+foreignID.String(), "", organizationID, 7, nil))
	if rec.Code != http.StatusOK || store.got != organizationID || !strings.Contains(rec.Body.String(), "local@example.test") || strings.Contains(rec.Body.String(), "must-not-leak") || strings.Contains(rec.Body.String(), foreignID.String()) {
		t.Fatalf("response=%d %s organization=%s", rec.Code, rec.Body.String(), store.got)
	}

	store.created = &models.Invitation{ID: 3, OrganizationID: organizationID, Email: "new@example.test", ExpiresAt: time.Now().Add(time.Hour)}
	created := httptest.NewRecorder()
	h.HandleCreateInvitation(created, organizationRequest(http.MethodPost, "/invitations", `{"email":"new@example.test","expected_revision":7}`, organizationID, 7, nil))
	if created.Code != http.StatusCreated || store.got != organizationID || store.gotInviter != 7 || !strings.Contains(created.Body.String(), "new@example.test") {
		t.Fatalf("created=%d %s authority=%s/%d", created.Code, created.Body.String(), store.got, store.gotInviter)
	}
}

func TestBloemOrganizationRoutesRejectPlatformContextBeforeStoreAccess(t *testing.T) {
	store := &organizationOverviewStub{}
	h := NewBloemAdminOrganizationHandler(store, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/admin/organization/overview", nil)
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}))
	rec := httptest.NewRecorder()
	h.HandleOverview(rec, req)
	if rec.Code != http.StatusForbidden || store.got != uuid.Nil {
		t.Fatalf("response=%d %s store=%s", rec.Code, rec.Body.String(), store.got)
	}
}

func organizationRequest(method, target, body string, organizationID uuid.UUID, revision int64, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	membershipID := uuid.New()
	ctx := tenancy.WithContext(req.Context(), tenancy.Context{OrganizationID: organizationID, AccountID: 7, MembershipID: membershipID, MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: revision, SecurityRevision: 3})
	ctx = apimw.SetAdminContextClaims(ctx, auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopeOrganization, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: revision, SecurityRevision: 3, EffectiveAuthority: "organization_admin"})
	if len(params) > 0 {
		routeCtx := chi.NewRouteContext()
		for key, value := range params {
			routeCtx.URLParams.Add(key, value)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	}
	return req.WithContext(ctx)
}

var _ = errors.Is
var _ = invitations.ErrNotFound
