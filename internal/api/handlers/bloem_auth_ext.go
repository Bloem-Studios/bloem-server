package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/auth/passwordhash"
	"github.com/Silo-Server/silo-server/internal/clientip"
)

type profileLoginService interface {
	LoginProfile(context.Context, string, string, auth.DeviceClaim) (*auth.TokenPair, auth.SessionSubject, error)
}

type profileLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

// profileLoginResponse deliberately excludes the account record and sibling
// profiles. A successful direct profile login establishes exactly one subject.
type profileLoginResponse struct {
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpiresIn          int    `json:"expires_in"`
	ProfileID          string `json:"profile_id"`
	OrganizationID     string `json:"organization_id"`
	MembershipID       string `json:"membership_id"`
	PolicyRevision     int64  `json:"policy_revision"`
	SecurityRevision   int64  `json:"security_revision"`
	CredentialRevision int64  `json:"credential_revision"`
}

// HandleProfileLogin exchanges an optional direct profile credential for a
// profile-bound session without changing the legacy account login flow.
func (h *AuthHandler) HandleProfileLogin(w http.ResponseWriter, r *http.Request) {
	if h.profileLogin == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Direct profile login is not configured")
		return
	}
	var req profileLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Email and password are required")
		return
	}
	// The session binds to exactly one device, and that binding is enforced on
	// every subsequent request. An empty device id would bind the session to
	// "no device" and make the enforcement vacuous.
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A device_id is required for direct profile login")
		return
	}
	pair, subject, err := h.profileLogin.LoginProfile(r.Context(), req.Email, req.Password, auth.DeviceClaim{
		ID:        req.DeviceID,
		Name:      r.UserAgent(),
		IPAddress: clientip.FromContext(r.Context()),
	})
	if err != nil {
		// ErrSessionRevoked means the subject changed between the password
		// check and the session insert (a credential reset, a suspension): the
		// credentials no longer hold, which is not a server fault.
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrSessionRevoked) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password")
			return
		}
		if errors.Is(err, passwordhash.ErrBusy) {
			writeAPIError(w, busyError(err))
			return
		}
		if errors.Is(err, auth.ErrProfilePasswordsDisabled) {
			writeProfilePasswordsDisabled(w)
			return
		}
		// The password-policy refusals use the account login's codes.
		if errors.Is(err, auth.ErrLocalLoginDisabled) || errors.Is(err, auth.ErrNotPermitted) {
			writeAPIError(w, externalSignInError(err))
			return
		}
		if apiErr := organizationDomainAPIError(err); apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		return
	}
	writeJSON(w, http.StatusOK, profileLoginResponse{
		AccessToken:        pair.AccessToken,
		RefreshToken:       pair.RefreshToken,
		ExpiresIn:          pair.ExpiresIn,
		ProfileID:          subject.ProfileID,
		OrganizationID:     subject.OrganizationID,
		MembershipID:       subject.MembershipID,
		PolicyRevision:     subject.PolicyRevision,
		SecurityRevision:   subject.SecurityRevision,
		CredentialRevision: subject.CredentialRevision,
	})
}

// bloemAuthHandlerExt holds the Bloem-only AuthHandler dependencies so the
// Silo-owned struct carries a single embedded line.
type bloemAuthHandlerExt struct {
	loginTenants apimw.TenantResolver
}
