package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type AccountUserRepository interface {
	Create(ctx context.Context, input models.CreateUserInput) (*models.User, error)
	Delete(ctx context.Context, id int) error
}

// MembershipProvisioner assigns an account to the deployment's default
// organization immediately after account creation.
type MembershipProvisioner interface {
	ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error
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
		if err := p.memberships.ProvisionDefaultMembership(ctx, user.ID, membershipLegacyRole(input.User.Role)); err != nil {
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

// membershipLegacyRole preserves the legacy migration's two-value membership
// contract without changing the account's stored role.
func membershipLegacyRole(role string) string {
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
