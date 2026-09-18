package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/go-chi/chi/v5"
)

type profileCredentialManager interface {
	Status(context.Context, int, string) (auth.ProfileCredentialStatus, error)
	SetAtRevision(context.Context, int, string, string, string, int64) error
	ClearAtRevision(context.Context, int, string, int64) error
}
type BloemProfileCredentialsHandler struct {
	profiles    *ProfileHandler
	credentials profileCredentialManager
	reauth      AdminReauthenticationVerifier
}

func NewBloemProfileCredentialsHandler(provider userstore.UserStoreProvider, users *auth.UserRepository, tokens *access.ProfileTokenService, credentials *auth.ProfileCredentialService) *BloemProfileCredentialsHandler {
	profiles := NewProfileHandler(provider)
	profiles.UserRepo, profiles.ProfileTokens = users, tokens
	return &BloemProfileCredentialsHandler{profiles: profiles, credentials: credentials, reauth: auth.NewAccountCredentialVerifier(users)}
}
func (h *BloemProfileCredentialsHandler) authorize(w http.ResponseWriter, r *http.Request) (int, bool) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 || claims.SessionID == "" || claims.TokenType != auth.TokenTypeAccess || claims.AuthMethod == auth.AuthMethodDirectProfile || claims.ImpersonatorUserID != nil {
		writeError(w, 403, "account_session_required", "A non-impersonated account login session is required")
		return 0, false
	}
	if h == nil || h.profiles == nil || h.profiles.storeProvider == nil || h.credentials == nil || h.reauth == nil {
		writeError(w, 503, "unavailable", "Profile credential management is unavailable")
		return 0, false
	}
	store, err := h.profiles.storeProvider.ForUser(r.Context(), claims.UserID)
	if err != nil || store == nil {
		writeError(w, 503, "unavailable", "Profile management is unavailable")
		return 0, false
	}
	allowed, err := h.profiles.canManageHouseholdProfiles(r, store)
	if err != nil {
		writeProfileManagementPermissionError(w, err)
		return 0, false
	}
	if !allowed {
		writeError(w, 403, "forbidden", "Select and verify the household primary profile")
		return 0, false
	}
	return claims.UserID, true
}
func (h *BloemProfileCredentialsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	account, ok := h.authorize(w, r)
	if !ok {
		return
	}
	value, err := h.credentials.Status(r.Context(), account, chi.URLParam(r, "id"))
	if err != nil {
		writeProfileCredentialError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (h *BloemProfileCredentialsHandler) HandleChange(w http.ResponseWriter, r *http.Request) {
	account, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var input struct {
		CurrentPassword  string `json:"current_password"`
		LoginEmail       string `json:"login_email"`
		Password         string `json:"password"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if !decodeAdminPlatformJSON(w, r, &input) {
		return
	}
	if input.ExpectedRevision <= 0 {
		writeAdminValidation(w, map[string]string{"expected_revision": "must be a positive credential revision from the reviewed status"})
		return
	}
	// Ownership is checked before password work, and rechecked by the service's
	// transaction. Changing or clearing a credential revokes its direct sessions.
	id := chi.URLParam(r, "id")
	if _, err := h.credentials.Status(r.Context(), account, id); err != nil {
		writeProfileCredentialError(w, err)
		return
	}
	verified, err := h.reauth.VerifyPassword(r.Context(), account, input.CurrentPassword)
	if err != nil {
		writeError(w, 503, "unavailable", "Could not verify the account password")
		return
	}
	if !verified {
		writeError(w, 403, "reauthentication_failed", "The current account password could not be verified")
		return
	}
	if r.Method == http.MethodDelete {
		err = h.credentials.ClearAtRevision(r.Context(), account, id, input.ExpectedRevision)
	} else {
		email, parseErr := mail.ParseAddress(strings.TrimSpace(input.LoginEmail))
		if parseErr != nil || email.Address != strings.TrimSpace(input.LoginEmail) {
			writeAdminValidation(w, map[string]string{"login_email": "must be an email address"})
			return
		}
		if err := auth.ValidateNewPassword(input.Password); err != nil {
			writeAdminValidation(w, map[string]string{"password": err.Error()})
			return
		}
		err = h.credentials.SetAtRevision(r.Context(), account, id, email.Address, input.Password, input.ExpectedRevision)
	}
	if err != nil {
		writeProfileCredentialError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeProfileCredentialError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrProfileCredentialNotFound):
		writeError(w, 404, "not_found", "Profile not found")
	case errors.Is(err, auth.ErrCredentialEmailInUse):
		writeError(w, 409, "credential_email_in_use", "This login email is already registered")
	case errors.Is(err, auth.ErrProfileCredentialRevisionConflict):
		writeError(w, 409, "credential_revision_conflict", "Profile credentials changed. Reload and review before trying again")
	default:
		writeError(w, 503, "unavailable", "Profile credential operation failed")
	}
}
