package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type adminSessionResolverStub struct {
	tenant tenancy.Context
	err    error
}

func (s adminSessionResolverStub) Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error) {
	return s.tenant, s.err
}

type adminSessionMembershipStoreStub struct {
	membership   tenancy.Membership
	organization tenancy.Organization
	err          error
}

type adminSessionPlatformAuthorizerStub struct {
	allowed bool
	err     error
}

func (s adminSessionPlatformAuthorizerStub) IsPlatformAdmin(context.Context, int) (bool, error) {
	return s.allowed, s.err
}

func (s adminSessionMembershipStoreStub) GetMembership(context.Context, int, uuid.UUID) (tenancy.Membership, error) {
	return s.membership, s.err
}

func (s adminSessionMembershipStoreStub) GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error) {
	return s.organization, s.err
}

func TestV2AdminSessionMintsOrganizationContextForOrganizationAdmin(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	tokens := auth.NewAdminContextTokenService("admin-session-test-secret")
	handler := NewAdminContextSessionHandler(tokens,
		adminSessionResolverStub{tenant: tenancy.Context{
			AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID,
			PolicyRevision: 7, SecurityRevision: 11,
		}},
		adminSessionMembershipStoreStub{membership: tenancy.Membership{
			ID: membershipID, OrganizationID: organizationID, AccountID: 41,
			Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11,
		}, organization: tenancy.Organization{ID: organizationID, Name: "Vondel", Status: tenancy.OrganizationActive}}, adminSessionPlatformAuthorizerStub{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", strings.NewReader(`{"scope":"organization","organization_id":"`+organizationID.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 41, Role: "user"}))
	rec := httptest.NewRecorder()

	handler.HandleSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		Context     struct {
			Scope          auth.AdminScope `json:"scope"`
			OrganizationID string          `json:"organization_id"`
			MembershipID   string          `json:"membership_id"`
			Name           string          `json:"name"`
			Status         string          `json:"status"`
		} `json:"context"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims, err := tokens.Parse(body.AccessToken)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.Scope != auth.AdminScopeOrganization || claims.AccountID != 41 || claims.OrganizationID != organizationID || claims.MembershipID != membershipID ||
		body.Context.Scope != auth.AdminScopeOrganization || body.Context.OrganizationID != organizationID.String() || body.Context.MembershipID != membershipID.String() ||
		body.Context.Name != "Vondel" || body.Context.Status != "active" {
		t.Fatalf("claims/context = %#v %#v", claims, body.Context)
	}
}

func TestV2AdminSessionRequiresPlatformAuthorityForPlatformScope(t *testing.T) {
	handler := NewAdminContextSessionHandler(auth.NewAdminContextTokenService("admin-session-test-secret"), adminSessionResolverStub{}, adminSessionMembershipStoreStub{}, adminSessionPlatformAuthorizerStub{})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", strings.NewReader(`{"scope":"platform"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 41, Role: "user"}))
	rec := httptest.NewRecorder()

	handler.HandleSession(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_platform_authority") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminSessionMintsPlatformContextForCurrentPlatformAdmin(t *testing.T) {
	tokens := auth.NewAdminContextTokenService("admin-session-test-secret")
	handler := NewAdminContextSessionHandler(tokens, adminSessionResolverStub{}, adminSessionMembershipStoreStub{}, adminSessionPlatformAuthorizerStub{allowed: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", strings.NewReader(`{"scope":"platform"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 41, Role: "user"}))
	rec := httptest.NewRecorder()

	handler.HandleSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		Context     struct {
			Scope     auth.AdminScope `json:"scope"`
			Authority string          `json:"authority"`
		} `json:"context"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims, err := tokens.Parse(body.AccessToken)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.Scope != auth.AdminScopePlatform || claims.AccountID != 41 || claims.OrganizationID != uuid.Nil || claims.MembershipID != uuid.Nil ||
		body.Context.Scope != auth.AdminScopePlatform || body.Context.Authority != "platform_admin" {
		t.Fatalf("claims/context = %#v %#v", claims, body.Context)
	}
}

func TestV2AdminSessionRejectsNonAdminOrganizationMembership(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	handler := NewAdminContextSessionHandler(auth.NewAdminContextTokenService("admin-session-test-secret"),
		adminSessionResolverStub{tenant: tenancy.Context{AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11}},
		adminSessionMembershipStoreStub{
			membership:   tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 41, Status: tenancy.MembershipActive, LegacyRole: "user", SecurityRevision: 11},
			organization: tenancy.Organization{ID: organizationID, Name: "Vondel", Status: tenancy.OrganizationActive},
		},
		adminSessionPlatformAuthorizerStub{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", strings.NewReader(`{"scope":"organization","organization_id":"`+organizationID.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 41, Role: "admin"}))
	rec := httptest.NewRecorder()

	handler.HandleSession(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_organization_authority") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2AdminSessionRejectsCallerSuppliedMembershipID(t *testing.T) {
	handler := NewAdminContextSessionHandler(auth.NewAdminContextTokenService("admin-session-test-secret"), adminSessionResolverStub{}, adminSessionMembershipStoreStub{}, adminSessionPlatformAuthorizerStub{allowed: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/session", strings.NewReader(`{"scope":"organization","organization_id":"`+uuid.NewString()+`","membership_id":"`+uuid.NewString()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 41, Role: "admin"}))
	rec := httptest.NewRecorder()

	handler.HandleSession(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
