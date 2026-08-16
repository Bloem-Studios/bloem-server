package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

type directProfileViewerResolver struct{ input access.ResolveInput }

func (r *directProfileViewerResolver) Resolve(_ context.Context, input access.ResolveInput) (access.Scope, error) {
	r.input = input
	return access.Scope{UserID: input.UserID, ProfileID: input.ProfileID, ProfileVerified: true}, nil
}

func TestRequireViewerAccessBindsDirectProfileClaimInsteadOfHeader(t *testing.T) {
	resolver := &directProfileViewerResolver{}
	middleware := NewViewerAccessMiddleware(resolver)
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set("X-Profile-Id", "sibling")
	req = req.WithContext(SetClaims(req.Context(), &auth.Claims{UserID: 7, ProfileID: "reader", AuthMethod: auth.AuthMethodDirectProfile}))
	rec := httptest.NewRecorder()
	middleware.RequireViewerAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want direct profile header override rejected", rec.Code)
	}

	requestWithoutHeader := httptest.NewRequest(http.MethodGet, "/items", nil)
	requestWithoutHeader = requestWithoutHeader.WithContext(SetClaims(requestWithoutHeader.Context(), &auth.Claims{UserID: 7, ProfileID: "reader", AuthMethod: auth.AuthMethodDirectProfile}))
	rec = httptest.NewRecorder()
	middleware.RequireViewerAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetProfileID(r.Context()) != "reader" {
			t.Fatalf("profile context = %q", GetProfileID(r.Context()))
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, requestWithoutHeader)
	if rec.Code != http.StatusNoContent || resolver.input.ProfileID != "reader" {
		t.Fatalf("direct profile resolution = %#v, status %d", resolver.input, rec.Code)
	}
}

func TestRequireViewerAccessPreservesLegacySelectedProfile(t *testing.T) {
	resolver := &directProfileViewerResolver{}
	middleware := NewViewerAccessMiddleware(resolver)
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set("X-Profile-Id", "selected")
	req = req.WithContext(SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	middleware.RequireViewerAccess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || resolver.input.ProfileID != "selected" {
		t.Fatalf("legacy profile resolution = %#v, status %d", resolver.input, rec.Code)
	}
}
