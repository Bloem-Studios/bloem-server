package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type tenantResolverStub struct {
	gotAccountID      int
	gotOrganizationID *uuid.UUID
	gotLegacy         bool
	result            tenancy.Context
	err               error
}

func (s *tenantResolverStub) Resolve(_ context.Context, accountID int, organizationID *uuid.UUID, legacy bool) (tenancy.Context, error) {
	s.gotAccountID = accountID
	s.gotOrganizationID = organizationID
	s.gotLegacy = legacy
	return s.result, s.err
}

func runTenantMiddleware(t *testing.T, middleware func(http.Handler) http.Handler, claims *auth.Claims, headers map[string]string) (int, tenancy.Context, bool, *auth.Claims) {
	t.Helper()
	var gotTenant tenancy.Context
	var gotTenantOK bool
	var gotClaims *auth.Claims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenant, gotTenantOK = tenancy.FromContext(r.Context())
		gotClaims = GetClaims(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/bloem/v2/organizations", nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if claims != nil {
		req = req.WithContext(SetClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	middleware(next).ServeHTTP(rec, req)
	return rec.Code, gotTenant, gotTenantOK, gotClaims
}

func TestTenantResolveLegacyRejectsSuspendedMembership(t *testing.T) {
	middleware := NewTenantMiddleware(&tenantResolverStub{err: tenancy.ErrTenantSuspended})
	status, _, ok, _ := runTenantMiddleware(t, middleware.ResolveLegacy, &auth.Claims{UserID: 41}, nil)
	if status != http.StatusForbidden || ok {
		t.Fatalf("status/context = %d/%v, want 403/false", status, ok)
	}
}

func TestTenantResolveLegacyTokenUsesDefaultAndPreservesClaims(t *testing.T) {
	want := tenancy.Context{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 41, PolicyRevision: 7, SecurityRevision: 11, Legacy: true}
	resolver := &tenantResolverStub{result: want}
	middleware := NewTenantMiddleware(resolver)
	claims := &auth.Claims{UserID: 41, Role: "custom-operator", ProfileID: "profile-7"}

	status, got, ok, gotClaims := runTenantMiddleware(t, middleware.ResolveLegacy, claims, map[string]string{
		"X-Organization-Id": uuid.NewString(),
	})
	if status != http.StatusNoContent || !ok || got != want {
		t.Fatalf("status/context = %d, %#v, %v; want 204, %#v, true", status, got, ok, want)
	}
	if resolver.gotOrganizationID != nil || !resolver.gotLegacy {
		t.Fatalf("legacy resolver accepted caller organization: organization=%v legacy=%v", resolver.gotOrganizationID, resolver.gotLegacy)
	}
	if gotClaims != claims || gotClaims.Role != "custom-operator" || gotClaims.ProfileID != "profile-7" {
		t.Fatalf("legacy claims changed: %#v", gotClaims)
	}
}

func TestTenantResolveLegacyRejectsResolverFailure(t *testing.T) {
	middleware := NewTenantMiddleware(&tenantResolverStub{err: errors.New("database unavailable")})
	status, _, ok, _ := runTenantMiddleware(t, middleware.ResolveLegacy, &auth.Claims{UserID: 41}, nil)
	if status != http.StatusServiceUnavailable || ok {
		t.Fatalf("status/context = %d/%v, want 503/false", status, ok)
	}
}

// TestTenantResolveNativeProjectsIntoTheDefaultOrganization: no token selects
// an organization of its own, so a native request is the account's default
// organization, whatever the caller sends.
func TestTenantResolveNativeProjectsIntoTheDefaultOrganization(t *testing.T) {
	want := tenancy.Context{OrganizationID: uuid.New(), MembershipID: uuid.New(), AccountID: 41, PolicyRevision: 7, SecurityRevision: 11, Legacy: true}
	resolver := &tenantResolverStub{result: want}
	middleware := NewTenantMiddleware(resolver)
	status, got, ok, _ := runTenantMiddleware(t, middleware.ResolveNative, &auth.Claims{UserID: 41}, map[string]string{
		"X-Organization-Id": uuid.NewString(),
	})
	if status != http.StatusNoContent || !ok || got != want || !resolver.gotLegacy || resolver.gotOrganizationID != nil {
		t.Fatalf("native request not projected into the default organization: %d %#v legacy=%v organization=%v", status, got, resolver.gotLegacy, resolver.gotOrganizationID)
	}
}
