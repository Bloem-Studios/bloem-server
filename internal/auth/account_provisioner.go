package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

type AccountUserRepository interface {
	Create(ctx context.Context, input models.CreateUserInput) (*models.User, error)
	Delete(ctx context.Context, id int) error
}

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

// MembershipProvisioner assigns an account to the deployment's default
// organization immediately after account creation.
type MembershipProvisioner interface {
	ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error
}

type transactionalMembershipProvisioner interface {
	ProvisionDefaultMembershipInTransaction(context.Context, pgx.Tx, int, string) (uuid.UUID, uuid.UUID, error)
}

type transactionalProfileCreator interface {
	CreateProfileInTransaction(context.Context, pgx.Tx, userstore.Profile) error
}

type DefaultProfileOptions struct {
	Enabled bool
	Name    string
}

type CreateAccountInput struct {
	User           models.CreateUserInput
	DefaultProfile DefaultProfileOptions
}

// CreatedAccount contains every database-generated identity produced by one
// account create. Lifecycle receipts persist these exact values before commit
// so a replay never rediscovers a replacement numeric account.
type CreatedAccount struct {
	User           *models.User
	OrganizationID uuid.UUID
	MembershipID   uuid.UUID
	ProfileID      string
}

// CreateDefaultProfile is createDefaultProfile, exported for
// AdminHandler.HandleCreateUser's park-tenant branch (bloem-park growth
// G2): that path provisions its own membership directly against a specific
// organization instead of going through CreateAccount's default-membership
// step, but still wants the identical default-profile creation (and the
// identical cleanup-on-failure) CreateAccount already implements — so it
// calls this rather than duplicate it.
func (p *AccountProvisioner) CreateDefaultProfile(ctx context.Context, userID int, input CreateAccountInput) error {
	return p.createDefaultProfile(ctx, userID, input)
}

type AccountProvisioner struct {
	users         AccountUserRepository
	storeProvider userstore.UserStoreProvider
	memberships   MembershipProvisioner
}

const (
	legacyRoleAdmin = "admin"
	legacyRoleUser  = "user"
)

// SetMembershipProvisioner installs the default-membership provisioning seam.
// Nil remains valid for isolated compatibility fixtures without tenant state.
func (p *AccountProvisioner) SetMembershipProvisioner(provisioner MembershipProvisioner) {
	p.memberships = provisioner
}

func NewAccountProvisioner(
	users AccountUserRepository,
	storeProvider userstore.UserStoreProvider,
) *AccountProvisioner {
	return &AccountProvisioner{
		users:         users,
		storeProvider: storeProvider,
	}
}

func (p *AccountProvisioner) CreateAccount(
	ctx context.Context,
	input CreateAccountInput,
) (*models.User, error) {
	user, err := p.users.Create(ctx, input.User)
	if err != nil {
		return nil, err
	}

	if p.memberships != nil {
		if err := p.memberships.ProvisionDefaultMembership(ctx, user.ID, MembershipLegacyRole(input.User.Role)); err != nil {
			if deleteErr := p.users.Delete(ctx, user.ID); deleteErr != nil {
				return nil, fmt.Errorf(
					"provision default membership: %w (cleanup user: %w)",
					err,
					deleteErr,
				)
			}
			return nil, fmt.Errorf("provision default membership: %w", err)
		}
	}

	if !input.DefaultProfile.Enabled {
		return user, nil
	}

	if err := p.createDefaultProfile(ctx, user.ID, input); err != nil {
		if deleteErr := p.users.Delete(ctx, user.ID); deleteErr != nil {
			return nil, fmt.Errorf(
				"create default profile: %w (cleanup user: %w)",
				err,
				deleteErr,
			)
		}
		return nil, fmt.Errorf("create default profile: %w", err)
	}

	return user, nil
}

// CreateUserInTransaction creates a user through the same validation,
// normalization, defaults, and password hashing as CreateAccount while using
// the caller's transaction. Membership is intentionally left to that caller:
// it is the tenant service's quota-bearing write in the same transaction. The
// boolean distinguishes a uniqueness conflict without coupling the tenant
// package back to auth's repository sentinels.
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

// CreateAccountInTransaction creates the identity, default membership and
// optional default profile on a caller-owned transaction.
func (p *AccountProvisioner) CreateAccountInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	input CreateAccountInput,
) (CreatedAccount, error) {
	user, conflict, err := p.CreateUserInTransaction(ctx, tx, input.User)
	if err != nil {
		return CreatedAccount{}, err
	}
	if conflict {
		return CreatedAccount{}, ErrDuplicate
	}
	created := CreatedAccount{User: user}

	if p.memberships != nil {
		memberships, ok := p.memberships.(transactionalMembershipProvisioner)
		if !ok {
			return CreatedAccount{}, fmt.Errorf("membership provisioner does not support transactional creation")
		}
		created.OrganizationID, created.MembershipID, err = memberships.ProvisionDefaultMembershipInTransaction(
			ctx, tx, user.ID, MembershipLegacyRole(input.User.Role),
		)
		if err != nil {
			return CreatedAccount{}, fmt.Errorf("provision default membership: %w", err)
		}
	}

	if !input.DefaultProfile.Enabled {
		return created, nil
	}
	if p.storeProvider == nil {
		return CreatedAccount{}, fmt.Errorf("user store provider unavailable")
	}
	store, err := p.storeProvider.ForUser(ctx, user.ID)
	if err != nil {
		return CreatedAccount{}, fmt.Errorf("open user store: %w", err)
	}
	profiles, ok := store.(transactionalProfileCreator)
	if !ok {
		return CreatedAccount{}, fmt.Errorf("user store does not support transactional profile creation")
	}
	name := strings.TrimSpace(input.DefaultProfile.Name)
	if name == "" {
		name = strings.TrimSpace(input.User.Username)
	}
	if name == "" {
		return CreatedAccount{}, fmt.Errorf("default profile name is required")
	}
	created.ProfileID = uuid.NewString()
	if err := profiles.CreateProfileInTransaction(ctx, tx, userstore.Profile{
		ID:                  created.ProfileID,
		Name:                name,
		ShowForcedSubtitles: true,
	}); err != nil {
		return CreatedAccount{}, fmt.Errorf("store profile: %w", err)
	}
	return created, nil
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

func (p *AccountProvisioner) createDefaultProfile(
	ctx context.Context,
	userID int,
	input CreateAccountInput,
) error {
	if p.storeProvider == nil {
		return fmt.Errorf("user store provider unavailable")
	}

	store, err := p.storeProvider.ForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("open user store: %w", err)
	}

	name := strings.TrimSpace(input.DefaultProfile.Name)
	if name == "" {
		name = strings.TrimSpace(input.User.Username)
	}
	if name == "" {
		return fmt.Errorf("default profile name is required")
	}

	if err := store.CreateProfile(ctx, userstore.Profile{
		Name:                name,
		ShowForcedSubtitles: true,
	}); err != nil {
		return fmt.Errorf("store profile: %w", err)
	}

	return nil
}
