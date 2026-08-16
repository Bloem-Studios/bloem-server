package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/go-chi/chi/v5"
)

func directProfileClaims(profileID string) *auth.Claims {
	return &auth.Claims{UserID: 7, ProfileID: profileID, AuthMethod: auth.AuthMethodDirectProfile}
}

func servedOK(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

func TestRejectDirectProfileSessionBlocksAccountSurfaces(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(SetClaims(req.Context(), directProfileClaims("reader")))
	rec := httptest.NewRecorder()

	RejectDirectProfileSession(http.HandlerFunc(servedOK)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("direct profile status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRejectDirectProfileSessionPreservesAccountSessions(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("X-Profile-Id", "any-profile")
	req = req.WithContext(SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()

	RejectDirectProfileSession(http.HandlerFunc(servedOK)).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("account session status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequireOwnDirectProfileRejectsSiblingProfileRoute(t *testing.T) {
	router := chi.NewRouter()
	router.With(RequireOwnDirectProfile("id")).Put("/profiles/{id}", servedOK)

	for name, tc := range map[string]struct {
		claims *auth.Claims
		target string
		want   int
	}{
		"direct session on sibling": {directProfileClaims("reader"), "/profiles/sibling", http.StatusForbidden},
		"direct session on own":     {directProfileClaims("reader"), "/profiles/reader", http.StatusNoContent},
		"direct session unbound":    {directProfileClaims(""), "/profiles/reader", http.StatusForbidden},
		"account session":           {&auth.Claims{UserID: 7}, "/profiles/sibling", http.StatusNoContent},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, tc.target, nil)
			req = req.WithContext(SetClaims(req.Context(), tc.claims))
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequireProfileBindsDirectProfileSessionToItsToken(t *testing.T) {
	for name, tc := range map[string]struct {
		claims  *auth.Claims
		header  string
		want    int
		profile string
	}{
		"direct session without header":      {directProfileClaims("reader"), "", http.StatusNoContent, "reader"},
		"direct session with own header":     {directProfileClaims("reader"), "reader", http.StatusNoContent, "reader"},
		"direct session naming a sibling":    {directProfileClaims("reader"), "sibling", http.StatusForbidden, ""},
		"account session declares a profile": {&auth.Claims{UserID: 7}, "selected", http.StatusNoContent, "selected"},
		"account session without a profile":  {&auth.Claims{UserID: 7}, "", http.StatusBadRequest, ""},
	} {
		t.Run(name, func(t *testing.T) {
			var seen string
			req := httptest.NewRequest(http.MethodGet, "/devices", nil)
			if tc.header != "" {
				req.Header.Set("X-Profile-Id", tc.header)
			}
			req = req.WithContext(SetClaims(req.Context(), tc.claims))
			rec := httptest.NewRecorder()

			RequireProfile(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = GetProfileID(r.Context())
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if seen != tc.profile {
				t.Fatalf("resolved profile = %q, want %q", seen, tc.profile)
			}
		})
	}
}

func TestActiveProfileIDIgnoresHeaderForDirectProfileSessions(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("X-Profile-Id", "primary-sibling")
	req = req.WithContext(SetClaims(req.Context(), directProfileClaims("reader")))
	req = req.WithContext(SetProfileID(req.Context(), "primary-sibling"))

	if got := ActiveProfileID(req); got != "reader" {
		t.Fatalf("active profile = %q, want the token-bound profile", got)
	}
}

func TestActiveProfileIDKeepsLegacyDeclaration(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("X-Profile-Id", "selected")
	req = req.WithContext(SetClaims(req.Context(), &auth.Claims{UserID: 7}))

	if got := ActiveProfileID(req); got != "selected" {
		t.Fatalf("active profile = %q, want the declared profile", got)
	}
}
