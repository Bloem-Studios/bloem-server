package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Authorize is what a connection that outlives its request (the organization
// live socket) revalidates with. It must hold and refuse exactly as Require
// does, and tell a pass that stopped holding from a check that could not run.
//
//nolint:funlen // one table of every way a pass stops holding, with the fixtures it needs
func TestAdminContextAuthorize(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	claims := auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: incarnation, SessionID: "login-session", Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11,
		EffectiveAuthority: "organization_admin",
	}
	resolved := tenancy.Context{AccountID: 41, OrganizationID: organizationID, MembershipID: membershipID, PolicyRevision: 7, SecurityRevision: 11}
	membership := tenancy.Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 41, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 11}

	build := func(resolver *adminContextResolverStub, members *adminContextMembershipStoreStub, platform auth.PlatformAdminAuthorizer, sessions SessionValidator) *AdminContextMiddleware {
		return NewAdminContextMiddleware(auth.NewAdminContextTokenService("authorize-test-secret"), resolver, members, platform, sessions)
	}

	t.Run("holds and returns the handler context", func(t *testing.T) {
		m := build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
		ctx, refusal := m.Authorize(context.Background(), claims)
		if refusal != nil {
			t.Fatalf("refused: %+v", refusal)
		}
		tenant, ok := tenancy.FromContext(ctx)
		got, claimed := GetAdminContextClaims(ctx)
		if !ok || tenant != resolved || !claimed || got.OrganizationID != organizationID {
			t.Fatalf("context = %+v %v %+v %v", tenant, ok, got, claimed)
		}
	})

	lost := map[string]struct {
		mutate func(*auth.AdminContextClaims)
		m      func() *AdminContextMiddleware
		status int
		code   string
	}{
		"session revoked": {
			m: func() *AdminContextMiddleware {
				return build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: false})
			},
			status: http.StatusUnauthorized, code: "authorization_state_stale",
		},
		"membership revision moved": {
			m: func() *AdminContextMiddleware {
				moved := resolved
				moved.SecurityRevision = 12
				return build(&adminContextResolverStub{tenant: moved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
			},
			status: http.StatusUnauthorized, code: "authorization_state_stale",
		},
		"no longer an administrator": {
			m: func() *AdminContextMiddleware {
				member := membership
				member.LegacyRole = "user"
				return build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: member}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
			},
			status: http.StatusUnauthorized, code: "authorization_state_stale",
		},
		"membership suspended": {
			m: func() *AdminContextMiddleware {
				member := membership
				member.Status = tenancy.MembershipSuspended
				return build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: member}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
			},
			status: http.StatusUnauthorized, code: "authorization_state_stale",
		},
		"organization suspended": {
			m: func() *AdminContextMiddleware {
				return build(&adminContextResolverStub{err: tenancy.ErrTenantSuspended}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
			},
			status: http.StatusForbidden, code: "organization_suspended",
		},
		"no session id": {
			mutate: func(c *auth.AdminContextClaims) { c.SessionID = "" },
			m: func() *AdminContextMiddleware {
				return build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
			},
			status: http.StatusUnauthorized, code: "tenant_session_required",
		},
	}
	for name, tc := range lost {
		t.Run(name, func(t *testing.T) {
			c := claims
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			_, refusal := tc.m().Authorize(context.Background(), c)
			if refusal == nil || refusal.Status != tc.status || refusal.Code != tc.code {
				t.Fatalf("refusal = %+v, want %d %s", refusal, tc.status, tc.code)
			}
			if refusal.Transient() {
				t.Fatalf("%s is a lost pass, not a failed check", name)
			}
		})
	}

	t.Run("a failed check is transient, not a lost pass", func(t *testing.T) {
		m := build(&adminContextResolverStub{err: errors.New("database down")}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{}, fixedSessionValidator{valid: true})
		_, refusal := m.Authorize(context.Background(), claims)
		if refusal == nil || refusal.Status != http.StatusServiceUnavailable || !refusal.Transient() {
			t.Fatalf("refusal = %+v", refusal)
		}
		m = build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{err: errors.New("down")}, fixedSessionValidator{valid: true})
		if _, refusal := m.Authorize(context.Background(), claims); refusal == nil || !refusal.Transient() {
			t.Fatalf("an unavailable operator check = %+v", refusal)
		}
	})

	t.Run("a platform pass cannot be mistaken for an organization one", func(t *testing.T) {
		platform := claims
		platform.Scope = auth.AdminScopePlatform
		m := build(&adminContextResolverStub{tenant: resolved}, &adminContextMembershipStoreStub{membership: membership}, adminContextPlatformAuthorizerStub{allowed: true}, fixedSessionValidator{valid: true})
		ctx, refusal := m.Authorize(context.Background(), platform)
		if refusal != nil {
			t.Fatalf("refused: %+v", refusal)
		}
		// Authorized, but with no tenant: the organization routes' own scope
		// check, which the live ticket route repeats, is what refuses it.
		if _, ok := tenancy.FromContext(ctx); ok {
			t.Fatal("a platform pass carries a tenant context")
		}
	})
}

// The refusal a REST route writes and the one a connection closes on are the
// same value.
func TestTenantResolveRefusalMatchesTheWriter(t *testing.T) {
	for _, err := range []error{tenancy.ErrTenantSuspended, tenancy.ErrTenantNotFoundOrHidden, tenancy.ErrOwnershipResolutionRequired, errors.New("other")} {
		refusal := tenantResolveRefusal(err)
		rec := httptest.NewRecorder()
		writeTenantResolveError(rec, err)
		if rec.Code != refusal.Status || !strings.Contains(rec.Body.String(), `"`+refusal.Code+`"`) {
			t.Errorf("%v: writer answered %d %s, refusal is %+v", err, rec.Code, rec.Body.String(), refusal)
		}
	}
}
