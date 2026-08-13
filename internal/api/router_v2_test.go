package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type v2TokenValidator struct{ claims *auth.Claims }

func (v v2TokenValidator) ValidateToken(string) (*auth.Claims, error) { return v.claims, nil }

type v2SessionValidator struct{}

func (v2SessionValidator) IsValid(context.Context, string) (bool, error) { return true, nil }

func TestV2CapabilitiesMountedOutsideV1(t *testing.T) {
	router := NewRouter(Dependencies{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"api":"v2"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2MountedAndV10Absent(t *testing.T) {
	router := chi.NewRouter()
	mountV2Routes(router, handlers.NewV2SystemHandler(nil), nil, nil)

	v2 := httptest.NewRecorder()
	router.ServeHTTP(v2, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if v2.Code != http.StatusOK || !strings.Contains(v2.Body.String(), `"api":"v2"`) {
		t.Fatalf("v2 capabilities = %d %s", v2.Code, v2.Body.String())
	}

	v10 := httptest.NewRecorder()
	router.ServeHTTP(v10, httptest.NewRequest(http.MethodGet, "/api/v10/capabilities", nil))
	if v10.Code != http.StatusNotFound {
		t.Fatalf("v10 status = %d, want 404", v10.Code)
	}
}

func TestV2OrganizationsRouteUsesAccountAuthentication(t *testing.T) {
	store := v2OrganizationStoreStubForRouter{}
	system := handlers.NewV2SystemHandler(store)
	authMW := apimw.NewAuthMiddleware(
		v2TokenValidator{claims: &auth.Claims{UserID: 7, SessionID: "session", TokenType: auth.TokenTypeAccess}},
		v2SessionValidator{}, nil, nil,
	)
	router := chi.NewRouter()
	mountV2Routes(router, system, authMW, nil)

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil))
	if unauthenticated.Code != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("unauthenticated response = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer valid-token")
	authenticated := httptest.NewRecorder()
	router.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK || strings.TrimSpace(authenticated.Body.String()) != `{"organizations":[]}` {
		t.Fatalf("authenticated response = %d %s", authenticated.Code, authenticated.Body.String())
	}
}

type v2OrganizationStoreStubForRouter struct{}

func (v2OrganizationStoreStubForRouter) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return []tenancy.Membership{}, nil
}

func (v2OrganizationStoreStubForRouter) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
}
