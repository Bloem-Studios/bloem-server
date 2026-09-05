package auth

import (
	"context"
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

	if !input.DefaultProfile.Enabled {
		return user, nil
	}

	if err := p.createDefaultProfile(ctx, user.ID, input); err != nil {
		if deleteErr := p.users.Delete(ctx, user.ID); deleteErr != nil {
			return nil, fmt.Errorf(
				"create default profile: %w (cleanup user: %v)",
				err,
				deleteErr,
			)
		}
		return nil, fmt.Errorf("create default profile: %w", err)
	}

	return user, nil
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
		if !input.DefaultProfile.Enabled {
			return nil
		}
		if provider, ok := p.storeProvider.(interface {
			CreateProfileInTransaction(context.Context, pgx.Tx, int, userstore.Profile) error
		}); ok {
			profile, err := defaultAccountProfile(input)
			if err != nil {
				return err
			}
			return provider.CreateProfileInTransaction(ctx, tx, user.ID, profile)
		}
		// SQLite bridge stores are separate from the account database. Preserve
		// their existing profile writer, but do not commit the account or invite
		// if that writer fails.
		return p.createDefaultProfile(ctx, user.ID, input)
	})
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
