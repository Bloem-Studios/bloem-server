package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

type staticTokenValidator struct{ claims *auth.Claims }

func (v staticTokenValidator) ValidateToken(string) (*auth.Claims, error) { return v.claims, nil }

type alwaysValidSession struct{}

func (alwaysValidSession) IsValid(context.Context, string) (bool, error) { return true, nil }

// The boundary is default-deny, and that must include the middleware's own
// construction: an AuthMiddleware nobody installed a route guard on refuses
// direct-profile sessions rather than admitting them everywhere. A fixture
// that wants an unrestricted middleware says so with an allow-all guard.
func TestDirectProfileSessionsFailClosedWithoutARouteGuard(t *testing.T) {
	direct := &auth.Claims{
		UserID: 7, ProfileID: "reader", SessionID: "session-1",
		TokenType: auth.TokenTypeAccess, AuthMethod: auth.AuthMethodDirectProfile,
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/items/1", nil)
		r.Header.Set("Authorization", "Bearer direct-token")
		return r
	}

	t.Run("no guard refuses", func(t *testing.T) {
		am := NewAuthMiddleware(staticTokenValidator{claims: direct}, alwaysValidSession{}, nil, nil)
		rec := httptest.NewRecorder()
		am.RequireAuth(next).ServeHTTP(rec, request())
		if rec.Code != http.StatusForbidden {
			t.Fatalf("guardless direct session = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("allow-all guard admits", func(t *testing.T) {
		am := NewAuthMiddleware(staticTokenValidator{claims: direct}, alwaysValidSession{}, nil, nil)
		am.SetDirectProfileRouteGuard(func(*http.Request) bool { return true })
		rec := httptest.NewRecorder()
		am.RequireAuth(next).ServeHTTP(rec, request())
		if rec.Code != http.StatusNoContent {
			t.Fatalf("allow-all direct session = %d, want %d", rec.Code, http.StatusNoContent)
		}
	})

	t.Run("account sessions unaffected", func(t *testing.T) {
		account := &auth.Claims{UserID: 7, SessionID: "session-2", TokenType: auth.TokenTypeAccess}
		am := NewAuthMiddleware(staticTokenValidator{claims: account}, alwaysValidSession{}, nil, nil)
		rec := httptest.NewRecorder()
		am.RequireAuth(next).ServeHTTP(rec, request())
		if rec.Code != http.StatusNoContent {
			t.Fatalf("guardless account session = %d, want %d", rec.Code, http.StatusNoContent)
		}
	})
}
