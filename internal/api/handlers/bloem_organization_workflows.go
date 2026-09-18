package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type organizationInvitationLifecycle interface {
	ResendForOrganization(context.Context, uuid.UUID, int64, int64, string, time.Time) (*models.Invitation, error)
	RevokeForOrganization(context.Context, uuid.UUID, int64) error
}
type organizationAuditReader interface {
	ListOrganizationAudit(context.Context, uuid.UUID, string) (tenancy.OrganizationAuditPage, error)
}

func (h *BloemAdminOrganizationHandler) HandleAudit(w http.ResponseWriter, r *http.Request) {
	tenant, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return
	}
	if h == nil {
		writeError(w, 503, "tenant_unavailable", "Organization audit is unavailable")
		return
	}
	store, ok := h.overview.(organizationAuditReader)
	if !ok {
		writeError(w, 503, "tenant_unavailable", "Organization audit is unavailable")
		return
	}
	page, err := store.ListOrganizationAudit(r.Context(), tenant.OrganizationID, r.URL.Query().Get("cursor"))
	if errors.Is(err, tenancy.ErrInvalidCursor) {
		writeError(w, 400, "invalid_cursor", "Reload the audit list to reset pagination")
		return
	}
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *BloemAdminOrganizationHandler) invitationLifecycleRequest(w http.ResponseWriter, r *http.Request) (tenancy.Context, organizationInvitationLifecycle, int64, bool) {
	tenant, ok := h.requireInvitations(w, r)
	if !ok {
		return tenant, nil, 0, false
	}
	store, ok := h.invitations.(organizationInvitationLifecycle)
	if !ok {
		writeError(w, 503, "tenant_unavailable", "Invitation lifecycle is unavailable")
		return tenant, nil, 0, false
	}
	id, ok := bloemPositivePathID(w, r, "id")
	if !ok {
		return tenant, nil, 0, false
	}
	var request struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &request) || !requireOrganizationRevision(w, tenant, request.ExpectedRevision) {
		return tenant, nil, 0, false
	}
	return tenant, store, id, true
}
func (h *BloemAdminOrganizationHandler) HandleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	tenant, store, id, ok := h.invitationLifecycleRequest(w, r)
	if !ok {
		return
	}
	if err := store.RevokeForOrganization(r.Context(), tenant.OrganizationID, id); err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *BloemAdminOrganizationHandler) HandleResendInvitation(w http.ResponseWriter, r *http.Request) {
	tenant, store, id, ok := h.invitationLifecycleRequest(w, r)
	if !ok {
		return
	}
	token, hash, err := invitations.NewToken()
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	item, err := store.ResendForOrganization(r.Context(), tenant.OrganizationID, id, int64(tenant.AccountID), hash, time.Now().Add(invitations.DefaultTTL))
	if errors.Is(err, invitations.ErrNotClaimable) {
		writeError(w, http.StatusConflict, "invitation_not_claimable", "Only unaccepted, unrevoked user invitations can be renewed here; reload before trying again")
		return
	}
	if err != nil {
		writeBloemOrganizationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Invitation invitationResponse `json:"invitation"`
		ClaimToken string             `json:"claim_token"`
	}{toInvitationResponse(item, time.Now()), token})
}
