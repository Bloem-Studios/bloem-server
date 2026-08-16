package compatapi

import (
	"net/http"
	"time"
)

// Identity operations exchange end-user credentials for audience-bound
// subject tokens and manage trusted-device profile switching. The companion
// forwards a submitted password transiently; this API is the only place it
// goes, and it is never echoed back or logged.

type loginWire struct {
	GrantType string      `json:"grant_type"`
	Email     string      `json:"email"`
	Username  string      `json:"username"`
	Password  string      `json:"password"`
	Device    DeviceClaim `json:"device"`
}

type loginResultWire struct {
	SubjectToken string      `json:"subject_token"`
	ExpiresAt    time.Time   `json:"expires_at"`
	Subject      subjectWire `json:"subject"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	if h.svc.Subjects == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "identity is unavailable")
		return
	}
	var body loginWire
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.Password == "" || body.Device.ID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "password and device.id are required")
		return
	}
	var (
		subject Subject
		err     error
	)
	switch body.GrantType {
	case GrantTypeDirectProfile:
		if body.Email == "" {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "email is required for direct profile login")
			return
		}
		subject, err = h.svc.Subjects.LoginDirect(r.Context(), body.Email, body.Password, body.Device)
	case GrantTypeAccount:
		if body.Username == "" {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "username is required for account login")
			return
		}
		subject, err = h.svc.Subjects.LoginAccount(r.Context(), body.Username, body.Password, body.Device)
	default:
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "grant_type must be direct_profile or account")
		return
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeSubjectGrant(w, subject, rc.app.ApplicationID)
}

// writeSubjectGrant mints and returns a fresh audience-bound subject token.
func (h *Handler) writeSubjectGrant(w http.ResponseWriter, subject Subject, audience string) {
	token, expires := h.mintSubjectToken(subject, audience)
	h.writeJSON(w, http.StatusOK, loginResultWire{
		SubjectToken: token,
		ExpiresAt:    expires,
		Subject:      subjectToWire(subject),
	})
}

type introspectionWire struct {
	Subject       subjectWire `json:"subject"`
	ApplicationID string      `json:"application_id"`
	ExpiresAt     time.Time   `json:"expires_at"`
}

func (h *Handler) handleIntrospectSubject(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	h.writeJSON(w, http.StatusOK, introspectionWire{
		Subject:       subjectToWire(rc.subject),
		ApplicationID: rc.app.ApplicationID,
		ExpiresAt:     time.Unix(rc.tokenState.ExpiresAt, 0).UTC(),
	})
}

func (h *Handler) handleListDeviceProfiles(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	profiles, err := h.svc.Subjects.DeviceProfiles(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if profiles == nil {
		profiles = []ProfileTile{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

type pinVerifyWire struct {
	ProfileID string `json:"profile_id"`
	PIN       string `json:"pin"`
}

func (h *Handler) handleVerifyProfilePin(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	var body pinVerifyWire
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ProfileID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "profile_id is required")
		return
	}
	if err := h.svc.Subjects.VerifyPIN(r.Context(), rc.subject, body.ProfileID, body.PIN); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type switchProfileWire struct {
	ProfileID string `json:"profile_id"`
	PIN       string `json:"pin"`
}

func (h *Handler) handleSwitchProfile(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	var body switchProfileWire
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ProfileID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "profile_id is required")
		return
	}
	subject, err := h.svc.Subjects.SwitchProfile(r.Context(), rc.subject, body.ProfileID, body.PIN)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeSubjectGrant(w, subject, rc.app.ApplicationID)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	if err := h.svc.Subjects.Logout(r.Context(), rc.subject); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
