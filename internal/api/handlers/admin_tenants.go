package handlers

// The tenant admin API (vondel-park growth G2): the contract vondel-park's
// media adapter speaks — create (idempotent on the external service claim),
// list/get with live usage, limits in place, freeze/thaw, destroy. Park only
// STATES entitlements here; enforcement lives at membership provisioning
// (the slot quota, see AdminHandler.HandleCreateUser) and playback
// admission (the tenant transcode pool + the frozen flag, see
// internal/playback/session.go).
//
// A tenant IS an organization (internal/tenancy) — not a parallel entity —
// so Destroy is a soft retirement of that organization (see
// tenancy.Store.DeleteTenantOrganization for why a hard delete is unsafe
// here), and it runs BEFORE member accounts are deleted through the user
// repository: organizations.owner_account_id is RESTRICT, and retiring the
// organization first is what clears that reference.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// AdminTenantsHandler serves /api/v1/admin/tenants.
type AdminTenantsHandler struct {
	store    *tenancy.Store
	userRepo UserRepository
}

// NewAdminTenantsHandler builds the handler.
func NewAdminTenantsHandler(store *tenancy.Store, userRepo UserRepository) *AdminTenantsHandler {
	return &AdminTenantsHandler{store: store, userRepo: userRepo}
}

// tenantResponse is the wire shape the park adapter decodes. Field names
// are pinned on both sides; changing one without the other strands
// provisioning.
type tenantResponse struct {
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	ExternalRef struct {
		OperatorID string `json:"operator_id"`
		ServiceID  string `json:"service_id"`
	} `json:"external_ref"`
	Limits struct {
		Slots      int `json:"slots"`
		Transcodes int `json:"transcodes"`
	} `json:"limits"`
	Usage struct {
		SlotsUsed int `json:"slots_used"`
	} `json:"usage"`
	Frozen                     bool   `json:"frozen"`
	FrozenReason               string `json:"frozen_reason,omitempty"`
	AppliedEntitlementRevision int64  `json:"applied_entitlement_revision,omitempty"`
}

func toTenantResponse(t tenancy.TenantOrganization) tenantResponse {
	var resp tenantResponse
	resp.TenantID = t.ID.String()
	resp.Name = t.Name
	resp.ExternalRef.OperatorID = t.ExternalOperatorID
	resp.ExternalRef.ServiceID = t.ExternalServiceID
	resp.Limits.Slots = t.Slots
	resp.Limits.Transcodes = t.Transcodes
	resp.Usage.SlotsUsed = t.SlotsUsed
	resp.Frozen = t.Frozen
	resp.FrozenReason = t.FrozenReason
	resp.AppliedEntitlementRevision = t.AppliedEntitlementRevision
	return resp
}

// tenantID parses the path id as an organization UUID — an opaque string to
// park. A malformed or unrecognized id is refused identically as 404: this
// contract has nothing to disclose about which is which.
func (h *AdminTenantsHandler) tenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such tenant")
		return uuid.Nil, false
	}
	return id, true
}

// HandleCreate handles POST /admin/tenants — idempotent on
// external_ref.service_id, so a replayed park fulfill job adopts the SAME
// tenant organization instead of minting a second one.
func (h *AdminTenantsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		ExternalRef struct {
			OperatorID string `json:"operator_id"`
			ServiceID  string `json:"service_id"`
		} `json:"external_ref"`
		Limits struct {
			Slots      int `json:"slots"`
			Transcodes int `json:"transcodes"`
		} `json:"limits"`
		EntitlementTemplateKey      string `json:"entitlement_template_key"`
		EntitlementTemplateRevision int64  `json:"entitlement_template_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid request body")
		return
	}
	tenant, err := h.store.CreateTenantOrganization(r.Context(), tenancy.CreateTenantOrganizationInput{
		Name:                        req.Name,
		ExternalOperatorID:          req.ExternalRef.OperatorID,
		ExternalServiceID:           req.ExternalRef.ServiceID,
		Slots:                       req.Limits.Slots,
		Transcodes:                  req.Limits.Transcodes,
		EntitlementTemplateKey:      req.EntitlementTemplateKey,
		EntitlementTemplateRevision: req.EntitlementTemplateRevision,
	})
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantOrganizationInvalid) {
			writeError(w, http.StatusUnprocessableEntity, "validation",
				"A tenant needs a name, an external service reference, and at least one slot")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create tenant")
		return
	}
	writeJSON(w, http.StatusCreated, toTenantResponse(tenant))
}

// HandleList handles GET /admin/tenants — the reconcile sweep's observed
// side, and a bare array: the park adapter decodes []tenantBody directly.
func (h *AdminTenantsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListTenantOrganizations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list tenants")
		return
	}
	out := make([]tenantResponse, 0, len(list))
	for _, tenant := range list {
		out = append(out, toTenantResponse(tenant))
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleGet handles GET /admin/tenants/{id}.
func (h *AdminTenantsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	tenant, err := h.store.GetTenantOrganization(r.Context(), id)
	if err != nil {
		h.writeRepoError(w, err, "Failed to load tenant")
		return
	}
	writeJSON(w, http.StatusOK, toTenantResponse(tenant))
}

// HandleUpdateLimits handles PATCH /admin/tenants/{id}/limits — a plan
// change applied in place. A downgrade below the members in use freezes
// the tenant organization immediately (park's product ruling) until
// members are removed.
func (h *AdminTenantsHandler) HandleUpdateLimits(w http.ResponseWriter, r *http.Request) {
	id, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	var req struct {
		Slots      int `json:"slots"`
		Transcodes int `json:"transcodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid request body")
		return
	}
	tenant, err := h.store.UpdateTenantOrganizationLimits(r.Context(), id, req.Slots, req.Transcodes)
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantOrganizationInvalid) {
			writeError(w, http.StatusUnprocessableEntity, "validation", "A tenant needs at least one slot")
			return
		}
		h.writeRepoError(w, err, "Failed to update tenant limits")
		return
	}
	writeJSON(w, http.StatusOK, toTenantResponse(tenant))
}

// HandleFreeze handles POST /admin/tenants/{id}/freeze (park's dunning
// suspend): playback blocked, members locked out of new sessions, data kept.
func (h *AdminTenantsHandler) HandleFreeze(w http.ResponseWriter, r *http.Request) {
	h.setFrozen(w, r, true)
}

// HandleThaw handles POST /admin/tenants/{id}/thaw. A tenant still over its
// slot quota re-freezes as a quota freeze — the downgrade ruling holds.
func (h *AdminTenantsHandler) HandleThaw(w http.ResponseWriter, r *http.Request) {
	h.setFrozen(w, r, false)
}

func (h *AdminTenantsHandler) setFrozen(w http.ResponseWriter, r *http.Request, frozen bool) {
	id, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	if _, err := h.store.SetTenantOrganizationFrozen(r.Context(), id, frozen); err != nil {
		h.writeRepoError(w, err, "Failed to update tenant state")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleDelete handles DELETE /admin/tenants/{id}: the tenant's member
// accounts go with it, through the SAME user deletion the admin user API
// uses. Library content belongs to the operator, untouched.
//
// Order matters: the organization retires FIRST (clearing the owner
// reference as part of that), THEN members are deleted — reversed, deleting
// the owner's account while the organization still names them as owner
// fails outright (RESTRICT).
func (h *AdminTenantsHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	memberIDs, err := h.store.TenantMemberAccountIDs(r.Context(), id)
	if err != nil {
		if errors.Is(err, tenancy.ErrTenantOrganizationNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "No such tenant")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list tenant members")
		return
	}
	if err := h.store.DeleteTenantOrganization(r.Context(), id); err != nil {
		h.writeRepoError(w, err, "Failed to delete tenant")
		return
	}
	for _, userID := range memberIDs {
		if err := h.userRepo.Delete(r.Context(), userID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove a tenant member")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminTenantsHandler) writeRepoError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, tenancy.ErrTenantOrganizationNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such tenant")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", fallback)
}
