package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// A direct-profile session declares its token-bound profile, whatever the
// X-Profile-Id header says; other sessions declare the header.
func TestBloemDeclaredProfileIDIsTheDirectProfileBinding(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/bloem/v2/items/1", nil)
	r.Header.Set("X-Profile-Id", "sibling")
	direct := r.WithContext(SetClaims(r.Context(), &auth.Claims{UserID: 7, AuthMethod: auth.AuthMethodDirectProfile, ProfileID: "bound"}))
	if got := declaredProfileID(direct); got != "bound" {
		t.Fatalf("direct-profile declaredProfileID = %q, want the token-bound profile", got)
	}
	account := r.WithContext(SetClaims(r.Context(), &auth.Claims{UserID: 7}))
	if got := declaredProfileID(account); got != "sibling" {
		t.Fatalf("account declaredProfileID = %q, want the header profile", got)
	}
}
