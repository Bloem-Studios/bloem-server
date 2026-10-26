package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Account creation gives the new account its one membership itself (see
// linkAccountMembership), so Silo's AccountProvisioner paths need no Bloem
// step. These additions place an account in a named organization and let a
// caller-owned transaction create, update or delete an account.

type accountUserUpdater interface {
	Update(ctx context.Context, id int, input models.UpdateUserInput) error
}

type transactionalAccountUserRepository interface {
	CreateInTransaction(ctx context.Context, tx pgx.Tx, input models.CreateUserInput) (*models.User, error)
}

type transactionalAccountUserUpdater interface {
	UpdateInTransaction(ctx context.Context, tx pgx.Tx, id int, input models.UpdateUserInput) error
}

type transactionalAccountUserDeleter interface {
	DeleteInTransaction(ctx context.Context, tx pgx.Tx, id int) error
}

// CreateDefaultProfile is createDefaultProfile, exported for the tenant
// account path in AdminHandler.HandleCreateUser, which creates the account in
// its own transaction and then wants CreateAccount's exact default profile.
func (p *AccountProvisioner) CreateDefaultProfile(ctx context.Context, userID int, input CreateAccountInput) error {
	return p.createDefaultProfile(ctx, userID, input)
}

const (
	legacyRoleAdmin = "admin"
	legacyRoleUser  = "user"
)

// CreateUserInTransaction creates a user through the same validation,
// normalization, defaults, and password hashing as CreateAccount while using
// the caller's transaction; the account joins the organization owning its
// access group, else the default one. The boolean distinguishes a uniqueness
// conflict without coupling the tenant package back to auth's repository
// sentinels.
func (p *AccountProvisioner) CreateUserInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	input models.CreateUserInput,
) (*models.User, bool, error) {
	users, ok := p.users.(transactionalAccountUserRepository)
	if !ok {
		return nil, false, fmt.Errorf("account repository does not support transactional creation")
	}
	user, err := users.CreateInTransaction(ctx, tx, input)
	if IsDuplicate(err) {
		return nil, true, nil
	}
	return user, false, err
}

// CreateUserForOrganizationInTransaction is CreateUserInTransaction for an
// account that joins organizationID, in that organization's default group
// unless input names one of its groups. The caller owns any quota the
// organization enforces.
func (p *AccountProvisioner) CreateUserForOrganizationInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	organizationID uuid.UUID,
	input models.CreateUserInput,
) (*models.User, bool, error) {
	if organizationID == uuid.Nil {
		return nil, false, errors.New("organization is required for account creation")
	}
	return p.CreateUserInTransaction(withAccountCreationOrganization(ctx, organizationID), tx, input)
}

// CreateAccountForOrganizationInTransaction creates an account and its
// optional default profile in organizationID, which must be active. Stored
// invitations name their organization this way.
func (p *AccountProvisioner) CreateAccountForOrganizationInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	organizationID uuid.UUID,
	input CreateAccountInput,
) (*models.User, error) {
	if organizationID == uuid.Nil {
		return nil, errors.New("organization is required for account creation")
	}
	var status string
	// Serialize with other writers of the organization without blocking the
	// KEY SHARE locks membership foreign keys take.
	err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1 FOR NO KEY UPDATE`, organizationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tenancy.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock organization for account creation: %w", err)
	}
	if status != string(tenancy.OrganizationActive) {
		return nil, tenancy.ErrTenantNotFoundOrHidden
	}
	return p.CreateAccountInTransaction(withAccountCreationOrganization(ctx, organizationID), tx, input)
}

// UpdateUser applies the native repository update and reports uniqueness
// conflicts without exposing repository-specific errors to tenant services.
func (p *AccountProvisioner) UpdateUser(ctx context.Context, userID int, input models.UpdateUserInput) (bool, error) {
	users, ok := p.users.(accountUserUpdater)
	if !ok {
		return false, fmt.Errorf("account repository does not support updates")
	}
	err := users.Update(ctx, userID, input)
	if IsDuplicate(err) {
		return true, nil
	}
	return false, err
}

// UpdateUserInTransaction is the native update path on a caller-owned
// transaction, allowing security-sensitive identity changes and session
// revocation to commit or roll back together.
func (p *AccountProvisioner) UpdateUserInTransaction(ctx context.Context, tx pgx.Tx, userID int, input models.UpdateUserInput) (bool, error) {
	users, ok := p.users.(transactionalAccountUserUpdater)
	if !ok {
		return false, fmt.Errorf("account repository does not support transactional updates")
	}
	err := users.UpdateInTransaction(ctx, tx, userID, input)
	if IsDuplicate(err) {
		return true, nil
	}
	return false, err
}

// DeleteUser applies the native account deletion path.
func (p *AccountProvisioner) DeleteUser(ctx context.Context, userID int) error {
	return p.users.Delete(ctx, userID)
}

// DeleteUserInTransaction is the native account deletion path on the
// tenant-member lifecycle transaction.
func (p *AccountProvisioner) DeleteUserInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	users, ok := p.users.(transactionalAccountUserDeleter)
	if !ok {
		return fmt.Errorf("account repository does not support transactional deletion")
	}
	return users.DeleteInTransaction(ctx, tx, userID)
}

// MembershipLegacyRole preserves the legacy migration's two-value membership
// contract without changing the account's stored role.
func MembershipLegacyRole(role string) string {
	if role == legacyRoleAdmin {
		return legacyRoleAdmin
	}
	return legacyRoleUser
}
