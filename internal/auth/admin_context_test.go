package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/google/uuid"
)

func TestAdminContextTokenServiceMintsOnlyOneOrganizationScope(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	service := auth.NewAdminContextTokenService("admin-context-test-secret")

	token, err := service.Mint(auth.AdminContextClaims{
		AccountID:        41,
		Scope:            auth.AdminScopeOrganization,
		OrganizationID:   organizationID,
		MembershipID:     membershipID,
		PolicyRevision:   7,
		SecurityRevision: 11,
	})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	claims, err := service.Parse(token)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.AccountID != 41 || claims.Scope != auth.AdminScopeOrganization ||
		claims.OrganizationID != organizationID || claims.MembershipID != membershipID ||
		claims.PolicyRevision != 7 || claims.SecurityRevision != 11 {
		t.Fatalf("claims = %#v", claims)
	}
	if claims.ExpiresAt.IsZero() || claims.ExpiresAt.After(time.Now().UTC().Add(15*time.Minute)) {
		t.Fatalf("expires_at = %s, want a non-zero value no later than 15 minutes", claims.ExpiresAt)
	}
}

func TestAdminContextTokenServiceRejectsMixedScopes(t *testing.T) {
	service := auth.NewAdminContextTokenService("admin-context-test-secret")

	_, err := service.Mint(auth.AdminContextClaims{
		AccountID:      41,
		Scope:          auth.AdminScopePlatform,
		OrganizationID: uuid.New(),
	})
	if !errors.Is(err, auth.ErrInvalidAdminContext) {
		t.Fatalf("Mint() error = %v, want ErrInvalidAdminContext", err)
	}
}

func TestAdminContextTokenServiceCapsCallerExpiryAtFifteenMinutes(t *testing.T) {
	service := auth.NewAdminContextTokenService("admin-context-test-secret")
	before := time.Now().UTC()

	token, err := service.Mint(auth.AdminContextClaims{
		AccountID: 41,
		Scope:     auth.AdminScopePlatform,
		ExpiresAt: before.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	claims, err := service.Parse(token)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.ExpiresAt.After(before.Add(15 * time.Minute)) {
		t.Fatalf("expires_at = %s, want no later than %s", claims.ExpiresAt, before.Add(15*time.Minute))
	}
}
