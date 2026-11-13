package handlers

import (
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
)

// writeSupportAuthorityError answers the per-target refusals of the shared
// authority rules (adminpeople.Judge). They are 409, never 403: the Android
// client reads a 403 on an organization route as loss of access to the whole
// organization, and a protected person is not that. It reports whether it
// wrote a response.
func writeSupportAuthorityError(w http.ResponseWriter, err error) bool {
	var refusal *adminpeople.TargetProtectedError
	switch {
	case errors.As(err, &refusal):
		writeJSON(w, http.StatusConflict, struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Reason  string `json:"reason"`
		}{"target_protected", "This person is protected from this action", string(refusal.Reason)})
	case errors.Is(err, adminpeople.ErrAccountShared):
		writeError(w, http.StatusConflict, "account_shared", "This account belongs to more than one organization")
	default:
		return false
	}
	return true
}
