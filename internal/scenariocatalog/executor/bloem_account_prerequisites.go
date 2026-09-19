package executor

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Match the command's transaction-preserving tenancy adapter. Signup and
// invitation acceptance must not use an incomplete account provisioner merely
// because their catalog is executed through a test router.
type bloemFixtureMemberships struct{ store *tenancy.Store }

func (b bloemFixtureMemberships) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (b bloemFixtureMemberships) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

func (b bloemFixtureMemberships) ProvisionMembershipInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionMembershipInTransaction(ctx, tx, organizationID, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

// Bind the existing seeded session to its real account incarnation, as the
// production auth service does. Preserve all generated expiry/type/session
// claims; do not insert a new session while constructing a scenario request.
func (e *Env) bindFixtureIncarnation(token string, user *models.User) string {
	claims, err := e.jwt.ValidateToken(token)
	if err != nil {
		e.t.Fatalf("scenario token validation: %v", err)
	}
	if user == nil || user.AccountIncarnationID == uuid.Nil || claims.UserID != user.ID {
		e.t.Fatal("scenario account incarnation is unavailable")
	}
	claims.AccountIncarnationID = user.AccountIncarnationID.String()
	claims.AuthMethod = "account"
	bound, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		e.t.Fatalf("scenario account token signing: %v", err)
	}
	return bound
}
