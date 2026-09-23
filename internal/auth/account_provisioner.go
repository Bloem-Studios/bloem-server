package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type AccountUserRepository interface {
	Create(ctx context.Context, input models.CreateUserInput) (*models.User, error)
	Delete(ctx context.Context, id int) error
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

type AccountProvisioner struct {
	users         AccountUserRepository
	storeProvider userstore.UserStoreProvider
	memberships   MembershipProvisioner
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

// CreateAccountInTransaction creates the identity, default membership and
// optional default profile on a caller-owned transaction.
func (p *AccountProvisioner) CreateAccountInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	input CreateAccountInput,
) (CreatedAccount, error) {
	if input.DefaultProfile.Enabled && !p.SupportsTransactionalProfiles() {
		return CreatedAccount{}, ErrTransactionalProfileUnavailable
	}

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

	return p.createProfileInTransaction(ctx, tx, input, created)
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

	profile, err := defaultAccountProfile(input)
	if err != nil {
		return err
	}
	if err := store.CreateProfile(ctx, profile); err != nil {
		return fmt.Errorf("store profile: %w", err)
	}

	return nil
}

// CreateInvitedAccount couples account provisioning to invite redemption.
func (p *AccountProvisioner) CreateInvitedAccount(ctx context.Context, input CreateAccountInput, code string) (*models.User, error) {
	users, ok := p.users.(interface {
		CreateInvited(ctx context.Context, input models.CreateUserInput, code string, provision func(*models.User, pgx.Tx) error) (*models.User, error)
	})
	if !ok {
		return nil, fmt.Errorf("invited account provisioning unavailable")
	}
	return users.CreateInvited(ctx, input.User, code, func(user *models.User, tx pgx.Tx) error {
		created := CreatedAccount{User: user}
		if p.memberships != nil {
			memberships, ok := p.memberships.(transactionalMembershipProvisioner)
			if !ok {
				return fmt.Errorf("membership provisioner does not support transactional creation")
			}
			var err error
			created.OrganizationID, created.MembershipID, err = memberships.ProvisionDefaultMembershipInTransaction(ctx, tx, user.ID, MembershipLegacyRole(input.User.Role))
			if err != nil {
				return err
			}
		}
		_, err := p.createProfileInTransaction(ctx, tx, input, created)
		return err
	})
}

// CreateInitialAccountInTransaction inserts the first administrator and its
// optional profile in the caller's transaction. The caller owns commit and
// rollback. SQLite bridge stores keep their separate profile writer; the
// account still does not commit if that writer fails.
func (p *AccountProvisioner) CreateInitialAccountInTransaction(ctx context.Context, tx pgx.Tx, input CreateAccountInput) (*models.User, error) {
	created, err := p.CreateAccountInTransaction(ctx, tx, input)
	return created.User, err
}

// createProfileInTransactionOrBridge writes the requested default profile
// through tx when the store can join it, otherwise through the SQLite bridge
// store's own writer. Either failure must abort the caller's transaction.
func (p *AccountProvisioner) createProfileInTransactionOrBridge(ctx context.Context, tx pgx.Tx, userID int, input CreateAccountInput) error {
	if !input.DefaultProfile.Enabled {
		return nil
	}
	if provider, ok := p.storeProvider.(transactionalProviderProfileCreator); ok {
		profile, err := defaultAccountProfile(input)
		if err != nil {
			return err
		}
		if err := provider.CreateProfileInTransaction(ctx, tx, userID, profile); err != nil {
			return fmt.Errorf("store profile: %w", err)
		}
		return nil
	}
	// SQLite bridge stores are separate from the account database. Preserve
	// their existing profile writer, but do not commit the account or invite
	// if that writer fails.
	return p.createDefaultProfile(ctx, userID, input)
}

// ErrTransactionalProfileUnavailable means the selected profile store cannot
// participate in account creation's transaction. No account has been inserted.
var ErrTransactionalProfileUnavailable = errors.New("transactional profile creation unavailable")

type transactionalProviderProfileCreator interface {
	CreateProfileInTransaction(context.Context, pgx.Tx, int, userstore.Profile) error
}

func defaultAccountProfile(input CreateAccountInput) (userstore.Profile, error) {
	name := strings.TrimSpace(input.DefaultProfile.Name)
	if name == "" {
		name = strings.TrimSpace(input.User.Username)
	}
	if name == "" {
		return userstore.Profile{}, fmt.Errorf("default profile name is required")
	}
	return userstore.Profile{Name: name, ShowForcedSubtitles: true}, nil
}

// SupportsTransactionalProfiles reports the selected provider's capability,
// including wrappers that preserve its transaction-aware profile writer.
func (p *AccountProvisioner) SupportsTransactionalProfiles() bool {
	_, ok := p.storeProvider.(transactionalProviderProfileCreator)
	return ok
}
