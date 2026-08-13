package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const AdminContextTokenLifetime = 15 * time.Minute

var ErrInvalidAdminContext = errors.New("invalid administrative context")

type AdminScope string

const (
	AdminScopePlatform     AdminScope = "platform"
	AdminScopeOrganization AdminScope = "organization"
)

// AdminContextClaims bind an administrative request to either the Platform or
// one exact organization membership. They are intentionally independent of
// the account-session JWT claims used by legacy routes.
type AdminContextClaims struct {
	AccountID        int
	Scope            AdminScope
	OrganizationID   uuid.UUID
	MembershipID     uuid.UUID
	PolicyRevision   int64
	SecurityRevision int64
	ExpiresAt        time.Time
}

type AdminContextTokenService interface {
	Mint(AdminContextClaims) (string, error)
	Parse(string) (AdminContextClaims, error)
}

type adminContextTokenService struct {
	secret []byte
}

type adminContextJWTClaims struct {
	AccountID        int        `json:"account_id"`
	Scope            AdminScope `json:"scope"`
	OrganizationID   string     `json:"organization_id,omitempty"`
	MembershipID     string     `json:"membership_id,omitempty"`
	PolicyRevision   int64      `json:"policy_revision,omitempty"`
	SecurityRevision int64      `json:"security_revision,omitempty"`
	TokenType        string     `json:"token_type"`
	jwt.RegisteredClaims
}

// NewAdminContextTokenService constructs a signed token service with a fixed,
// maximum 15-minute lifetime. The JWT secret deliberately follows the account
// JWT secret so rotating it also invalidates context sessions.
func NewAdminContextTokenService(secret string) AdminContextTokenService {
	return &adminContextTokenService{secret: []byte(secret)}
}

func (s *adminContextTokenService) Mint(claims AdminContextClaims) (string, error) {
	if err := validateAdminContextClaims(claims); err != nil {
		return "", err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(AdminContextTokenLifetime)
	if !claims.ExpiresAt.IsZero() && claims.ExpiresAt.Before(expiresAt) {
		expiresAt = claims.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) {
		return "", fmt.Errorf("%w: expiry must be in the future", ErrInvalidAdminContext)
	}

	jwtClaims := adminContextJWTClaims{
		AccountID:        claims.AccountID,
		Scope:            claims.Scope,
		PolicyRevision:   claims.PolicyRevision,
		SecurityRevision: claims.SecurityRevision,
		TokenType:        "admin_context",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	if claims.Scope == AdminScopeOrganization {
		jwtClaims.OrganizationID = claims.OrganizationID.String()
		jwtClaims.MembershipID = claims.MembershipID.String()
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims).SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign administrative context: %w", err)
	}
	return token, nil
}

func (s *adminContextTokenService) Parse(tokenStr string) (AdminContextClaims, error) {
	if tokenStr == "" {
		return AdminContextClaims{}, ErrInvalidAdminContext
	}
	jwtClaims := &adminContextJWTClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, jwtClaims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid || jwtClaims.TokenType != "admin_context" || jwtClaims.ExpiresAt == nil {
		return AdminContextClaims{}, fmt.Errorf("%w: token validation failed", ErrInvalidAdminContext)
	}

	claims := AdminContextClaims{
		AccountID:        jwtClaims.AccountID,
		Scope:            jwtClaims.Scope,
		PolicyRevision:   jwtClaims.PolicyRevision,
		SecurityRevision: jwtClaims.SecurityRevision,
		ExpiresAt:        jwtClaims.ExpiresAt.Time.UTC(),
	}
	if claims.Scope == AdminScopeOrganization {
		var parseErr error
		claims.OrganizationID, parseErr = uuid.Parse(jwtClaims.OrganizationID)
		if parseErr != nil {
			return AdminContextClaims{}, fmt.Errorf("%w: organization id", ErrInvalidAdminContext)
		}
		claims.MembershipID, parseErr = uuid.Parse(jwtClaims.MembershipID)
		if parseErr != nil {
			return AdminContextClaims{}, fmt.Errorf("%w: membership id", ErrInvalidAdminContext)
		}
	}
	if err := validateAdminContextClaims(claims); err != nil {
		return AdminContextClaims{}, err
	}
	return claims, nil
}

func validateAdminContextClaims(claims AdminContextClaims) error {
	if claims.AccountID <= 0 {
		return fmt.Errorf("%w: account id", ErrInvalidAdminContext)
	}
	switch claims.Scope {
	case AdminScopePlatform:
		if claims.OrganizationID != uuid.Nil || claims.MembershipID != uuid.Nil || claims.PolicyRevision != 0 || claims.SecurityRevision != 0 {
			return fmt.Errorf("%w: platform scope cannot carry organization authority", ErrInvalidAdminContext)
		}
	case AdminScopeOrganization:
		if claims.OrganizationID == uuid.Nil || claims.MembershipID == uuid.Nil || claims.PolicyRevision <= 0 || claims.SecurityRevision <= 0 {
			return fmt.Errorf("%w: incomplete organization authority", ErrInvalidAdminContext)
		}
	default:
		return fmt.Errorf("%w: scope", ErrInvalidAdminContext)
	}
	return nil
}

// PlatformAdminAuthorizer checks the durable account authority that grants
// Platform scope. It is kept separate from organization membership authority.
type PlatformAdminAuthorizer interface {
	IsPlatformAdmin(context.Context, int) (bool, error)
}

type platformAdminAccountStore interface {
	GetByID(context.Context, int) (*models.User, error)
}

type platformAdminAuthorizer struct {
	accounts platformAdminAccountStore
}

func NewPlatformAdminAuthorizer(accounts platformAdminAccountStore) PlatformAdminAuthorizer {
	return &platformAdminAuthorizer{accounts: accounts}
}

func (a *platformAdminAuthorizer) IsPlatformAdmin(ctx context.Context, accountID int) (bool, error) {
	if a == nil || a.accounts == nil || accountID <= 0 {
		return false, ErrInvalidAdminContext
	}
	account, err := a.accounts.GetByID(ctx, accountID)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return account != nil && account.Enabled && account.Role == "admin", nil
}
