package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type V10OrganizationStore interface {
	ListMemberships(context.Context, int) ([]tenancy.Membership, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type V10SystemHandler struct {
	organizations V10OrganizationStore
}

func NewV10SystemHandler(organizations V10OrganizationStore) *V10SystemHandler {
	return &V10SystemHandler{organizations: organizations}
}

type v10CapabilitiesResponse struct {
	API            string                `json:"api"`
	IdentitySchema int                   `json:"identity_schema"`
	Features       v10CapabilityFeatures `json:"features"`
}

type v10CapabilityFeatures struct {
	LegacySiloV1            bool `json:"legacy_silo_v1"`
	OrganizationMemberships bool `json:"organization_memberships"`
	DirectProfileLogin      bool `json:"direct_profile_login"`
	SharedDevicePairing     bool `json:"shared_device_pairing"`
	DelegatedAdminRoles     bool `json:"delegated_admin_roles"`
}

func (h *V10SystemHandler) HandleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, v10CapabilitiesResponse{
		API:            "v10",
		IdentitySchema: 1,
		Features: v10CapabilityFeatures{
			LegacySiloV1:            true,
			OrganizationMemberships: true,
		},
	})
}

type v10OrganizationListResponse struct {
	Organizations []v10Organization `json:"organizations"`
}

type v10Organization struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Default          bool   `json:"default"`
	MembershipID     string `json:"membership_id"`
	MembershipRole   string `json:"membership_role"`
	PolicyRevision   int64  `json:"policy_revision"`
	SecurityRevision int64  `json:"security_revision"`
}

func (h *V10SystemHandler) HandleOrganizations(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if h == nil || h.organizations == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}

	memberships, err := h.organizations.ListMemberships(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}

	result := make([]v10Organization, 0, len(memberships))
	for _, membership := range memberships {
		if membership.AccountID != claims.UserID || membership.Status != tenancy.MembershipActive {
			continue
		}
		organization, err := h.organizations.GetOrganization(r.Context(), membership.OrganizationID)
		if errors.Is(err, tenancy.ErrOrganizationNotFound) {
			continue
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
			return
		}
		if organization.Status != tenancy.OrganizationActive || organization.OwnerAccountID == nil || organization.ID != membership.OrganizationID {
			continue
		}
		result = append(result, v10Organization{
			ID:               organization.ID.String(),
			Slug:             organization.Slug,
			Name:             organization.Name,
			Default:          organization.Default,
			MembershipID:     membership.ID.String(),
			MembershipRole:   membership.LegacyRole,
			PolicyRevision:   organization.PolicyRevision,
			SecurityRevision: membership.SecurityRevision,
		})
	}

	writeJSON(w, http.StatusOK, v10OrganizationListResponse{Organizations: result})
}
