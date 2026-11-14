package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func supportClaims() AdminContextClaims {
	return AdminContextClaims{
		AccountID: 7, AccountIncarnationID: uuid.New(), Scope: AdminScopeOrganization, OrganizationID: uuid.New(),
		SupportAccessID: uuid.New(), PolicyRevision: 3, EffectiveAuthority: "platform_admin", SessionID: "session-1",
	}
}

// TestSupportAccessPassShape pins what a pass opened under break-glass support
// access may and may not carry: no membership, no security revision, platform
// authority only. A membership-less pass without a support access id stays
// invalid, so the shape cannot be reached any other way.
func TestSupportAccessPassShape(t *testing.T) {
	tokens := NewAdminContextTokenService("support-access-test-secret")
	valid := supportClaims()
	token, err := tokens.Mint(valid)
	if err != nil {
		t.Fatalf("mint a support pass: %v", err)
	}
	parsed, err := tokens.Parse(token)
	if err != nil {
		t.Fatalf("parse a support pass: %v", err)
	}
	if parsed.SupportAccessID != valid.SupportAccessID || parsed.MembershipID != uuid.Nil || parsed.SecurityRevision != 0 || parsed.EffectiveAuthority != "platform_admin" {
		t.Fatalf("parsed = %+v", parsed)
	}

	for name, mutate := range map[string]func(*AdminContextClaims){
		"membership and support access": func(c *AdminContextClaims) { c.MembershipID = uuid.New() },
		"a security revision":           func(c *AdminContextClaims) { c.SecurityRevision = 1 },
		"organization authority":        func(c *AdminContextClaims) { c.EffectiveAuthority = "organization_admin" },
		"no authority":                  func(c *AdminContextClaims) { c.EffectiveAuthority = "" },
		"no policy revision":            func(c *AdminContextClaims) { c.PolicyRevision = 0 },
		"no organization":               func(c *AdminContextClaims) { c.OrganizationID = uuid.Nil },
	} {
		claims := valid
		mutate(&claims)
		if _, err := tokens.Mint(claims); !errors.Is(err, ErrInvalidAdminContext) {
			t.Errorf("%s: mint = %v, want ErrInvalidAdminContext", name, err)
		}
	}
	noMembership := valid
	noMembership.SupportAccessID = uuid.Nil
	noMembership.SecurityRevision = 1
	if _, err := tokens.Mint(noMembership); !errors.Is(err, ErrInvalidAdminContext) {
		t.Errorf("an organization pass with neither membership nor support access = %v", err)
	}
	platform := valid
	platform.Scope, platform.OrganizationID, platform.PolicyRevision, platform.EffectiveAuthority = AdminScopePlatform, uuid.Nil, 0, ""
	if _, err := tokens.Mint(platform); !errors.Is(err, ErrInvalidAdminContext) {
		t.Errorf("a platform pass carrying a support access id = %v, want it refused or stripped", err)
	}

	// A signed token that names both a membership and a support access is
	// refused on parse, whoever signed it.
	forged := adminContextJWTClaims{
		AccountID: 7, AccountIncarnationID: uuid.NewString(), Scope: AdminScopeOrganization, OrganizationID: uuid.NewString(),
		MembershipID: uuid.NewString(), SupportAccessID: uuid.NewString(), PolicyRevision: 3, SecurityRevision: 1,
		EffectiveAuthority: "platform_admin", SessionID: "session-1", TokenType: "admin_context",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), IssuedAt: jwt.NewNumericDate(time.Now())},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, forged).SignedString([]byte("support-access-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Parse(signed); !errors.Is(err, ErrInvalidAdminContext) {
		t.Errorf("a token naming a membership and a support access = %v, want ErrInvalidAdminContext", err)
	}
}
