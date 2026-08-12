package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vondel-Media/vondel-server/internal/api/handlers"
	apimw "github.com/Vondel-Media/vondel-server/internal/api/middleware"
	"github.com/Vondel-Media/vondel-server/internal/auth"
	"github.com/Vondel-Media/vondel-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type v10TokenValidator struct{ claims *auth.Claims }

func (v v10TokenValidator) ValidateToken(string) (*auth.Claims, error) { return v.claims, nil }

type v10SessionValidator struct{}

func (v10SessionValidator) IsValid(context.Context, string) (bool, error) { return true, nil }

func TestV10CapabilitiesMountedOutsideV1(t *testing.T) {
	router := NewRouter(Dependencies{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v10/capabilities", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"api":"v10"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV10OrganizationsRouteUsesAccountAuthentication(t *testing.T) {
	store := v10OrganizationStoreStubForRouter{}
	system := handlers.NewV10SystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		v10TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		v10SessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountV10Routes(router, system, authMW, nil)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v10/organizations", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v10/organizations", nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer valid-token")
	authenticated := httptest.NewRecorder()
	router.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK || strings.TrimSpace(authenticated.Body.String()) != `{"organizations":[]}` {
		t.Fatalf("authenticated response = %d %s", authenticated.Code, authenticated.Body.String())
	}
}

type v10OrganizationStoreStubForRouter struct{}

func (v10OrganizationStoreStubForRouter) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return []tenancy.Membership{}, nil
}

func (v10OrganizationStoreStubForRouter) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}
