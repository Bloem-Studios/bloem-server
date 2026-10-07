package resourcetenancy

import (
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/google/uuid"
	"testing"
	"time"
)

// These are claim-shape controls, not claims of membership or resource grants.
func TestNativeOnboardingAuthorityRejectsIncompleteClaims(t *testing.T) {
	base := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New(), PolicyRevision: 1, SecurityRevision: 1, EffectiveAuthority: "organization_admin", ExpiresAt: time.Now().Add(time.Hour), SessionID: "login"}
	for _, tc := range []struct {
		name   string
		change func(*auth.AdminContextClaims)
	}{
		{"incarnation", func(a *auth.AdminContextClaims) { a.AccountIncarnationID = uuid.Nil }},
		{"account", func(a *auth.AdminContextClaims) { a.AccountID = 0 }},
		{"session", func(a *auth.AdminContextClaims) { a.SessionID = "" }},
		{"expired", func(a *auth.AdminContextClaims) { a.ExpiresAt = time.Now().Add(-time.Minute) }},
		{"unbounded", func(a *auth.AdminContextClaims) { a.ExpiresAt = time.Time{} }},
		{"membership", func(a *auth.AdminContextClaims) { a.MembershipID = uuid.Nil }},
		{"organization", func(a *auth.AdminContextClaims) { a.OrganizationID = uuid.Nil }},
		{"policy", func(a *auth.AdminContextClaims) { a.PolicyRevision = 0 }},
		{"security", func(a *auth.AdminContextClaims) { a.SecurityRevision = 0 }},
		{"authority", func(a *auth.AdminContextClaims) { a.EffectiveAuthority = "user" }},
		{"legacy-platform", func(a *auth.AdminContextClaims) { a.Scope = auth.AdminScopePlatform }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			tc.change(&a)
			if nativeManagementActorShape(a) {
				t.Fatal("incomplete administrative claims admitted")
			}
		})
	}
}
