package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// A session that started before its organization revoked the account is told
// to sign in again. It gets the same 401 session_invalid as any ended
// session, which internal/apiv2 answers as session_expired. A 404 "Profile not
// found" left clients refreshing a session the server would never accept for
// a profile again, with no prompt to sign in.
func TestViewerAccessSessionRevokedByOrganizationSignsIn(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("refused request reached the handler") })
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Header.Set("X-Profile-Id", "p1")
	r = r.WithContext(SetClaims(r.Context(), &auth.Claims{UserID: 1, Role: "user", SessionID: "s"}))
	rec := newReasonWriter()

	err := fmt.Errorf("resolve viewer: %w", ErrSessionRevokedByOrganization)
	NewViewerAccessMiddleware(stubResolver{err: err}).RequireViewerAccess(next).ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized || rec.reason != ReasonSessionInvalid {
		t.Fatalf("status %d reason %q, want 401 %q: %s", rec.Code, rec.reason, ReasonSessionInvalid, rec.Body.String())
	}
	if got := decodeDenial(t, rec); got.Error != "unauthorized" {
		t.Fatalf("error code %q, want unauthorized", got.Error)
	}
}

// Callers that only know access.ErrProfileNotFound keep refusing the profile.
func TestSessionRevokedByOrganizationIsAProfileRefusal(t *testing.T) {
	if !errors.Is(ErrSessionRevokedByOrganization, access.ErrProfileNotFound) {
		t.Fatal("ErrSessionRevokedByOrganization must match access.ErrProfileNotFound")
	}
}
