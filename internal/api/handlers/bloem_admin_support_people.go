package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/orgdomains"
	"github.com/Silo-Server/silo-server/internal/passwordreset"
	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
)

// supportDependencyUnavailable answers a revoke whose post-commit
// compatibility-session invalidation failed: the sessions are already ended,
// so a retry is safe.
func supportDependencyUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "30")
	writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "The change took effect but a connected service could not be told; try again shortly")
}

type supportResetRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Delivery         string `json:"delivery"`
}

type supportResetResponse struct {
	Reset  supportResetBody          `json:"reset"`
	Person adminpeople.PersonSummary `json:"person"`
}

type supportResetBody struct {
	Delivery       string    `json:"delivery"`
	DeliveryStatus string    `json:"delivery_status"`
	ResetURL       string    `json:"reset_url,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// HandleResetPassword handles POST /admin/organization/people/{account_id}/password-reset.
func (h *BloemOrganizationSupportHandler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	var body supportResetRequest
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	fields := map[string]string{}
	if body.ExpectedRevision <= 0 {
		fields["expected_revision"] = "must be the person's current security_revision"
	}
	delivery := passwordreset.Delivery(body.Delivery)
	if delivery != passwordreset.DeliveryEmail && delivery != passwordreset.DeliveryLink {
		fields["delivery"] = "must be email or link"
	}
	if len(fields) > 0 {
		writeAdminValidation(w, fields)
		return
	}
	if h.resets == nil {
		writeJSON(w, http.StatusConflict, struct {
			Error      string `json:"error"`
			Message    string `json:"message"`
			Capability string `json:"capability"`
		}{"capability_not_configured", "This server cannot deliver that kind of reset link", string(delivery)})
		return
	}
	// Links and emails follow the target organization's domain and name.
	result, err := h.resets.IssueForOrganization(orgdomains.WithOrganization(r.Context(), request.Tenant.OrganizationID), passwordreset.OrganizationIssueInput{
		OrganizationID: request.Tenant.OrganizationID, Actor: request.Actor,
		TargetAccountID: accountID, ExpectedRevision: body.ExpectedRevision, Delivery: delivery,
	})
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	person, err := h.people.Get(r.Context(), request.Tenant.OrganizationID, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, supportResetResponse{
		Reset: supportResetBody{
			Delivery: string(result.Delivery), DeliveryStatus: result.DeliveryStatus,
			ResetURL: result.URL, ExpiresAt: result.ExpiresAt.UTC(),
		},
		Person: person,
	})
}

// HandleListSessions handles GET /admin/organization/people/{account_id}/sessions.
func (h *BloemOrganizationSupportHandler) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	sessions, err := h.people.ListSessions(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Sessions []adminpeople.SessionView `json:"sessions"`
	}{sessions})
}

// HandleRevokeSessions handles DELETE /admin/organization/people/{account_id}/sessions:
// sign the person out everywhere.
func (h *BloemOrganizationSupportHandler) HandleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &body) {
		return
	}
	if body.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{"expected_revision": "must be the person's current security_revision"})
		return
	}
	result, err := h.people.RevokeSessions(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID, body.ExpectedRevision)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	if h.sessionsRevoked != nil {
		if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
			return h.sessionsRevoked(callbackCtx, accountID)
		}); err != nil {
			supportDependencyUnavailable(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Person  adminpeople.PersonSummary `json:"person"`
		Revoked struct {
			Sessions int `json:"sessions"`
		} `json:"revoked"`
	}{Person: result.Person, Revoked: struct {
		Sessions int `json:"sessions"`
	}{result.Sessions}})
}

// HandleRevokeSession handles DELETE /admin/organization/people/{account_id}/sessions/{session_id}.
func (h *BloemOrganizationSupportHandler) HandleRevokeSession(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	revoked, err := h.people.RevokeSession(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID, sessionID)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	if revoked.Changed && revoked.ProfileID != nil && h.profileSessionsRevoked != nil {
		if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
			return h.profileSessionsRevoked(callbackCtx, accountID, []string{*revoked.ProfileID})
		}); err != nil {
			supportDependencyUnavailable(w)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleListDevices handles GET /admin/organization/people/{account_id}/devices.
func (h *BloemOrganizationSupportHandler) HandleListDevices(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	devices, err := h.people.ListDevices(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Devices []adminpeople.DeviceView `json:"devices"`
	}{devices})
}

// HandleDeleteDevice handles DELETE /admin/organization/people/{account_id}/devices/{device_id}.
func (h *BloemOrganizationSupportHandler) HandleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	request, ok := h.require(w, r)
	if !ok {
		return
	}
	accountID, ok := supportAccountID(w, r)
	if !ok {
		return
	}
	deviceID := strings.TrimSpace(chi.URLParam(r, "device_id"))
	if deviceID == "" {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	result, err := h.people.SignOutDevice(r.Context(), request.Tenant.OrganizationID, request.Actor, accountID, deviceID)
	if err != nil {
		h.writeSupportError(w, r, err, request.Tenant.OrganizationID, accountID)
		return
	}
	if len(result.ProfileIDs) > 0 && h.profileSessionsRevoked != nil {
		if err := sessioninvalidation.Run(r.Context(), func(callbackCtx context.Context) error {
			return h.profileSessionsRevoked(callbackCtx, accountID, result.ProfileIDs)
		}); err != nil {
			supportDependencyUnavailable(w)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
