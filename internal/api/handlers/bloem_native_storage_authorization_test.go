package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
)

// An owner or source whose authority changed while the request waited for its
// locks is a conflict to reload, as in the organization handlers, not an outage.
func TestNativeStorageAuthorizationStateChangedIsAConflict(t *testing.T) {
	for name, err := range map[string]error{
		"mapped by the domain": catalog.MapNativeOnboardingError(resourcetenancy.ErrAuthorizationStateChanged),
		"bare":                 resourcetenancy.ErrAuthorizationStateChanged,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeNativeStorageError(w, err, true)
			assertNativeError(t, w, http.StatusConflict, "authorization_state_changed")
		})
	}
}
