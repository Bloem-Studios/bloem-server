package handlers

import (
	"context"
	"errors"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type BloemOrganizationOverviewStore interface {
	GetOrganizationSummary(context.Context, uuid.UUID) (tenancy.OrganizationSummary, error)
}

type BloemOrganizationGroupStore interface {
	List(context.Context, uuid.UUID) ([]access.Group, error)
	Get(context.Context, uuid.UUID, int64) (*access.Group, error)
	Create(context.Context, uuid.UUID, access.CreateGroupInput) (*access.Group, error)
	Update(context.Context, uuid.UUID, int64, access.UpdateGroupInput) (*access.Group, error)
	DeleteWithImpact(context.Context, uuid.UUID, int64) (access.GroupDeletionImpact, error)
}

type BloemOrganizationResourceStore interface {
	ListLibraries(context.Context, uuid.UUID) ([]resourcetenancy.LibraryProjection, error)
	SetLibraryEntitlementStatus(context.Context, uuid.UUID, int64, int64, resourcetenancy.EntitlementStatus) (resourcetenancy.LibraryEntitlement, error)
	DeleteLibraryEntitlement(context.Context, uuid.UUID, int64, int64) error
}

type BloemOrganizationInvitationStore interface {
	ListForOrganization(context.Context, uuid.UUID) ([]*models.Invitation, error)
	CreateForOrganization(context.Context, uuid.UUID, models.CreateInvitationInput, string) (*models.Invitation, error)
}

type BloemAdminOrganizationHandler struct {
	overview    BloemOrganizationOverviewStore
	groups      BloemOrganizationGroupStore
	resources   BloemOrganizationResourceStore
	invitations BloemOrganizationInvitationStore
}

func NewBloemAdminOrganizationHandler(overview BloemOrganizationOverviewStore, groups BloemOrganizationGroupStore, resources BloemOrganizationResourceStore, invitationStore BloemOrganizationInvitationStore) *BloemAdminOrganizationHandler {
	return &BloemAdminOrganizationHandler{overview: overview, groups: groups, resources: resources, invitations: invitationStore}
}

func (h *BloemAdminOrganizationHandler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	tenant, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return
	}
	if h == nil || h.overview == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	item, err := h.overview.GetOrganizationSummary(r.Context(), tenant.OrganizationID)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Organization tenancy.OrganizationSummary `json:"organization"`
	}{item})
}

func (h *BloemAdminOrganizationHandler) HandleListGroups(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireGroups(w, r)
	if !ok {
		return
	}
	groups, err := h.groups.List(r.Context(), tenant.OrganizationID)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	items := make([]accessGroupResponse, 0, len(groups))
	for _, group := range groups {
		items = append(items, toAccessGroupResponse(group))
	}
	writeJSON(w, http.StatusOK, struct {
		Groups []accessGroupResponse `json:"groups"`
	}{items})
}

func (h *BloemAdminOrganizationHandler) HandleGetGroup(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireGroups(w, r)
	if !ok {
		return
	}
	id, ok := bloemPositivePathID(w, r, "id")
	if !ok {
		return
	}
	group, err := h.groups.Get(r.Context(), tenant.OrganizationID, id)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Group accessGroupResponse `json:"group"`
	}{toAccessGroupResponse(*group)})
}

func (h *BloemAdminOrganizationHandler) HandleCreateGroup(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireGroups(w, r)
	if !ok {
		return
	}
	var request struct {
		accessGroupCreateRequest
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !requireOrganizationRevision(w, tenant, request.ExpectedRevision) {
		return
	}
	input, ok := request.toInput(w)
	if !ok {
		return
	}
	group, err := h.groups.Create(r.Context(), tenant.OrganizationID, input)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Group          accessGroupResponse `json:"group"`
		PolicyRevision int64               `json:"policy_revision"`
	}{toAccessGroupResponse(*group), tenant.PolicyRevision})
}

func (h *BloemAdminOrganizationHandler) HandleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireGroups(w, r)
	if !ok {
		return
	}
	id, ok := bloemPositivePathID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		accessGroupUpdateRequest
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !requireOrganizationRevision(w, tenant, request.ExpectedRevision) {
		return
	}
	input, ok := request.toInput(w)
	if !ok {
		return
	}
	group, err := h.groups.Update(r.Context(), tenant.OrganizationID, id, input)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Group          accessGroupResponse `json:"group"`
		PolicyRevision int64               `json:"policy_revision"`
	}{toAccessGroupResponse(*group), tenant.PolicyRevision})
}

func (h *BloemAdminOrganizationHandler) HandleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireGroups(w, r)
	if !ok {
		return
	}
	id, ok := bloemPositivePathID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !requireOrganizationRevision(w, tenant, request.ExpectedRevision) {
		return
	}
	impact, err := h.groups.DeleteWithImpact(r.Context(), tenant.OrganizationID, id)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, impact)
}

func (h *BloemAdminOrganizationHandler) HandleListLibraries(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireResources(w, r)
	if !ok {
		return
	}
	items, err := h.resources.ListLibraries(r.Context(), tenant.OrganizationID)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Libraries []resourcetenancy.LibraryProjection `json:"libraries"`
	}{items})
}

func (h *BloemAdminOrganizationHandler) HandleUpdateEntitlement(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireResources(w, r)
	if !ok {
		return
	}
	folderID, ok := bloemPositivePathID(w, r, "folder_id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64                             `json:"expected_revision"`
		Status           resourcetenancy.EntitlementStatus `json:"status"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 || (request.Status != resourcetenancy.EntitlementActive && request.Status != resourcetenancy.EntitlementSuspended) {
		writeAdminValidation(w, map[string]string{"request": "must include a current expected_revision and active or suspended status"})
		return
	}
	item, err := h.resources.SetLibraryEntitlementStatus(r.Context(), tenant.OrganizationID, folderID, request.ExpectedRevision, request.Status)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Entitlement resourcetenancy.LibraryEntitlement `json:"entitlement"`
	}{item})
}

func (h *BloemAdminOrganizationHandler) HandleDeleteEntitlement(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireResources(w, r)
	if !ok {
		return
	}
	folderID, ok := bloemPositivePathID(w, r, "folder_id")
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if request.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{compatFieldExpectedRevision: "must be positive"})
		return
	}
	if err := h.resources.DeleteLibraryEntitlement(r.Context(), tenant.OrganizationID, folderID, request.ExpectedRevision); err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemAdminOrganizationHandler) HandleListInvitations(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireInvitations(w, r)
	if !ok {
		return
	}
	items, err := h.invitations.ListForOrganization(r.Context(), tenant.OrganizationID)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	now := time.Now()
	response := make([]invitationResponse, 0, len(items))
	for _, item := range items {
		response = append(response, toInvitationResponse(item, now))
	}
	writeJSON(w, http.StatusOK, struct {
		Invitations []invitationResponse `json:"invitations"`
	}{response})
}

func (h *BloemAdminOrganizationHandler) HandleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.requireInvitations(w, r)
	if !ok {
		return
	}
	var request struct {
		Email            string `json:"email"`
		ExpectedRevision int64  `json:"expected_revision"`
		AccessGroupID    *int64 `json:"access_group_id"`
		LibraryIDs       []int  `json:"library_ids"`
		CreateProfile    *bool  `json:"create_profile"`
		ShowTour         *bool  `json:"show_tour"`
		Note             string `json:"note"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) {
		return
	}
	if !requireOrganizationRevision(w, tenant, request.ExpectedRevision) {
		return
	}
	parsed, err := netmail.ParseAddress(strings.TrimSpace(request.Email))
	if err != nil || parsed.Address != strings.TrimSpace(request.Email) {
		writeAdminValidation(w, map[string]string{"email": "must be a valid email address"})
		return
	}
	createProfile, showTour := true, true
	if request.CreateProfile != nil {
		createProfile = *request.CreateProfile
	}
	if request.ShowTour != nil {
		showTour = *request.ShowTour
	}
	token, tokenHash, err := invitations.NewToken()
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	item, err := h.invitations.CreateForOrganization(r.Context(), tenant.OrganizationID, models.CreateInvitationInput{Email: parsed.Address, Role: "user", AccessGroupID: request.AccessGroupID, LibraryIDs: request.LibraryIDs, CreateProfile: createProfile, ShowTour: showTour, Note: strings.TrimSpace(request.Note), InvitedBy: int64(tenant.AccountID), ExpiresAt: time.Now().Add(invitations.DefaultTTL)}, tokenHash)
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Invitation invitationResponse `json:"invitation"`
		ClaimToken string             `json:"claim_token"`
	}{toInvitationResponse(item, time.Now()), token})
}

func (h *BloemAdminOrganizationHandler) requireGroups(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	tenant, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return tenancy.Context{}, false
	}
	if h == nil || h.groups == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return tenancy.Context{}, false
	}
	return tenant, true
}
func (h *BloemAdminOrganizationHandler) requireResources(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	tenant, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return tenancy.Context{}, false
	}
	if h == nil || h.resources == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return tenancy.Context{}, false
	}
	return tenant, true
}
func (h *BloemAdminOrganizationHandler) requireInvitations(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	tenant, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return tenancy.Context{}, false
	}
	if h == nil || h.invitations == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return tenancy.Context{}, false
	}
	return tenant, true
}

func requireBloemOrganizationContext(w http.ResponseWriter, r *http.Request) (tenancy.Context, bool) {
	claims, claimsOK := middleware.GetAdminContextClaims(r.Context())
	tenant, tenantOK := tenancy.FromContext(r.Context())
	if !claimsOK || !tenantOK || claims.Scope != auth.AdminScopeOrganization || claims.AccountID <= 0 || claims.OrganizationID == uuid.Nil || claims.AccountID != tenant.AccountID || claims.OrganizationID != tenant.OrganizationID || claims.MembershipID != tenant.MembershipID {
		writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
		return tenancy.Context{}, false
	}
	return tenant, true
}

func requireOrganizationRevision(w http.ResponseWriter, tenant tenancy.Context, expected int64) bool {
	if expected <= 0 {
		writeAdminValidation(w, map[string]string{compatFieldExpectedRevision: compatRevisionValidationMessage})
		return false
	}
	if expected != tenant.PolicyRevision {
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{"authorization_state_changed", "Authorization state changed; reload and retry", tenant.PolicyRevision})
		return false
	}
	return true
}

func bloemPositivePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return 0, false
	}
	return id, true
}

func writeBloemOrganizationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tenancy.ErrOrganizationNotFound), errors.Is(err, access.ErrGroupNotFound), errors.Is(err, invitations.ErrNotFound), errors.Is(err, resourcetenancy.ErrResourceHidden):
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
	case errors.Is(err, resourcetenancy.ErrAuthorizationStateChanged):
		tenant, _ := tenancy.FromContext(r.Context())
		writeJSON(w, http.StatusConflict, struct {
			Error           string `json:"error"`
			Message         string `json:"message"`
			CurrentRevision int64  `json:"current_revision"`
		}{"authorization_state_changed", "Authorization state changed; reload and retry", tenant.PolicyRevision})
	case errors.Is(err, access.ErrGroupDuplicate), errors.Is(err, access.ErrDefaultGroupRequired):
		writeError(w, http.StatusConflict, "conflict", "The requested organization security change conflicts with current state")
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
	}
}
