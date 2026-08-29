package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/golang-jwt/jwt/v5"
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

func TestAdminContextTokenServiceRoundTripsAccountIncarnation(t *testing.T) {
	service := auth.NewAdminContextTokenService("admin-context-test-secret")
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	token, err := service.Mint(auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: incarnation, Scope: auth.AdminScopePlatform,
	})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	claims, err := service.Parse(token)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.AccountIncarnationID != incarnation {
		t.Fatalf("account incarnation = %s, want %s", claims.AccountIncarnationID, incarnation)
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

func TestAdminContextTokenServiceRejectsSignedTokenExceedingMaximumLifetime(t *testing.T) {
	const secret = "admin-context-test-secret"
	now := time.Now().UTC().Truncate(time.Second)
	service := auth.NewAdminContextTokenService(secret)
	token := signedAdminContextToken(t, secret, jwt.MapClaims{
		"account_id": 41,
		"scope":      string(auth.AdminScopePlatform),
		"token_type": "admin_context",
		"iat":        now.Unix(),
		"exp":        now.Add(auth.AdminContextTokenLifetime + time.Second).Unix(),
	})

	_, err := service.Parse(token)
	if !errors.Is(err, auth.ErrInvalidAdminContext) {
		t.Fatalf("Parse() error = %v, want ErrInvalidAdminContext", err)
	}
}

func TestAdminContextTokenServiceRejectsSignedTokenWithoutIssuedAt(t *testing.T) {
	const secret = "admin-context-test-secret"
	now := time.Now().UTC().Truncate(time.Second)
	service := auth.NewAdminContextTokenService(secret)
	token := signedAdminContextToken(t, secret, jwt.MapClaims{
		"account_id": 41,
		"scope":      string(auth.AdminScopePlatform),
		"token_type": "admin_context",
		"exp":        now.Add(5 * time.Minute).Unix(),
	})

	_, err := service.Parse(token)
	if !errors.Is(err, auth.ErrInvalidAdminContext) {
		t.Fatalf("Parse() error = %v, want ErrInvalidAdminContext", err)
	}
}

type platformAdminAccountStoreStub struct {
	account *models.User
	err     error
}

func (s platformAdminAccountStoreStub) GetByID(context.Context, int) (*models.User, error) {
	return s.account, s.err
}

func TestPlatformAdminAuthorizerRejectsMissingDisabledAndNonAdminAccounts(t *testing.T) {
	tests := []struct {
		name  string
		store platformAdminAccountStoreStub
	}{
		{name: "missing", store: platformAdminAccountStoreStub{err: auth.ErrNotFound}},
		{name: "disabled", store: platformAdminAccountStoreStub{account: &models.User{ID: 41, Role: "admin", Enabled: false}}},
		{name: "non_admin", store: platformAdminAccountStoreStub{account: &models.User{ID: 41, Role: "user", Enabled: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := auth.NewPlatformAdminAuthorizer(tt.store).IsPlatformAdmin(context.Background(), 41)
			if err != nil {
				t.Fatalf("IsPlatformAdmin() error = %v", err)
			}
			if allowed {
				t.Fatal("IsPlatformAdmin() = true, want false")
			}
		})
	}
}

func signedAdminContextToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}
