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

// SetAPIKeyAuth wires API-key authentication into the handlers whose own
// extractClaims previously only accepted a JWT — the same asymmetry
// AuthMiddleware.RequireAuth already closed for the rest of the API. Without
// this, a long-lived "sa_" API key authenticates against every other
// endpoint but is silently rejected by /auth/me, /auth/sessions, and
// friends, which is exactly backwards for a key whose whole purpose is to
// outlive a login session. Nil validator/loader (the zero value) preserves
// today's JWT-only behavior.
func (h *AuthHandler) SetAPIKeyAuth(validator apimw.APIKeyValidator, loader apimw.APIKeyUserLoader) {
	h.apiKeyValidator = validator
	h.apiKeyUserLoader = loader
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
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password")
			return
		}
		if errors.Is(err, passwordhash.ErrBusy) {
			writeAPIError(w, busyError(err))
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

// bloemAPIKeyClaims validates a long-lived "sa_"-prefixed API key when
// SetAPIKeyAuth has wired one in, the same way AuthMiddleware.RequireAuth
// validates one for the rest of the API. Without that parity, an API key
// works everywhere except here. handled=false leaves the token to the JWT path.
func (h *AuthHandler) bloemAPIKeyClaims(r *http.Request, token string) (*auth.Claims, bool, error) {
	if !strings.HasPrefix(token, "sa_") {
		return nil, false, nil
	}
	if h.apiKeyValidator == nil || h.apiKeyUserLoader == nil {
		return nil, true, auth.ErrInvalidToken
	}
	apiKey, err := h.apiKeyValidator.GetByKey(r.Context(), token)
	if err != nil {
		return nil, true, auth.ErrInvalidToken
	}
	user, err := h.apiKeyUserLoader.GetByID(r.Context(), apiKey.UserID)
	if err != nil {
		return nil, true, auth.ErrInvalidToken
	}
	if !user.Enabled {
		return nil, true, auth.ErrInvalidToken
	}
	go func(id int64) {
		_ = h.apiKeyValidator.UpdateLastUsed(context.Background(), id)
	}(apiKey.ID)
	return &auth.Claims{
		UserID:               user.ID,
		AccountIncarnationID: user.AccountIncarnationID.String(),
		Role:                 user.Role,
		SessionID:            "",
		TokenType:            auth.TokenTypeAPIKey,
		APIKeyID:             apiKey.ID,
		RateTier:             apiKey.RateTier,
		APIKeyScopes:         apiKey.Scopes,
	}, true, nil
}
