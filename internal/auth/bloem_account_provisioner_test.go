package auth

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type recordingMembershipProvisioner struct {
	provisionFn func(context.Context, int, string) error
}

func (p recordingMembershipProvisioner) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) error {
	return p.provisionFn(ctx, accountID, legacyRole)
}

func TestAccountProvisionerCreateAccount_ProvisionsDefaultMembershipBeforeProfile(t *testing.T) {
	steps := []string{}
	provisioner := NewAccountProvisioner(
		stubAccountUsers{
			createFn: func(context.Context, models.CreateUserInput) (*models.User, error) {
				steps = append(steps, "create-user")
				return &models.User{ID: 13, Username: "alex"}, nil
			},
		},
		stubStoreProvider{
			store: stubUserStore{
				createProfileFn: func(context.Context, userstore.Profile) error {
					steps = append(steps, "create-profile")
					return nil
				},
			},
		},
	)
	provisioner.SetMembershipProvisioner(recordingMembershipProvisioner{
		provisionFn: func(_ context.Context, accountID int, legacyRole string) error {
			if accountID != 13 || legacyRole != "user" {
				t.Fatalf("membership provisioning input = (%d, %q), want (13, user)", accountID, legacyRole)
			}
			steps = append(steps, "provision-membership")
			return nil
		},
	})

	_, err := provisioner.CreateAccount(context.Background(), CreateAccountInput{
		User: models.CreateUserInput{Username: "alex", Role: "user"},
		DefaultProfile: DefaultProfileOptions{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if got, want := len(steps), 3; got != want || steps[0] != "create-user" || steps[1] != "provision-membership" || steps[2] != "create-profile" {
		t.Fatalf("creation steps = %v, want [create-user provision-membership create-profile]", steps)
	}
}

func TestAccountProvisionerCreateAccount_DeletesUserWhenMembershipProvisioningFails(t *testing.T) {
	provisionErr := errors.New("membership provisioning failed")
	var deletedUserID int
	provisioner := NewAccountProvisioner(
		stubAccountUsers{
			createFn: func(context.Context, models.CreateUserInput) (*models.User, error) {
				return &models.User{ID: 14, Username: "alex"}, nil
			},
			deleteFn: func(_ context.Context, id int) error {
				deletedUserID = id
				return nil
			},
		},
		nil,
	)
	provisioner.SetMembershipProvisioner(recordingMembershipProvisioner{
		provisionFn: func(context.Context, int, string) error { return provisionErr },
	})

	_, err := provisioner.CreateAccount(context.Background(), CreateAccountInput{
		User: models.CreateUserInput{Username: "alex", Role: "user"},
	})
	if !errors.Is(err, provisionErr) {
		t.Fatalf("CreateAccount error = %v, want membership provisioning error", err)
	}
	if deletedUserID != 14 {
		t.Fatalf("deleted account = %d, want 14", deletedUserID)
	}
}

func TestAccountProvisionerCreateAccount_MapsCustomRoleToUserMembership(t *testing.T) {
	var provisionedRole string
	provisioner := NewAccountProvisioner(
		stubAccountUsers{
			createFn: func(_ context.Context, input models.CreateUserInput) (*models.User, error) {
				return &models.User{ID: 15, Username: input.Username, Role: input.Role}, nil
			},
		},
		nil,
	)
	provisioner.SetMembershipProvisioner(recordingMembershipProvisioner{
		provisionFn: func(_ context.Context, accountID int, legacyRole string) error {
			if accountID != 15 {
				t.Fatalf("membership account ID = %d, want 15", accountID)
			}
			provisionedRole = legacyRole
			return nil
		},
	})

	user, err := provisioner.CreateAccount(context.Background(), CreateAccountInput{
		User: models.CreateUserInput{Username: "moderator", Role: "moderator"},
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if user.Role != "moderator" {
		t.Fatalf("created user role = %q, want moderator", user.Role)
	}
	if provisionedRole != "user" {
		t.Fatalf("membership legacy role = %q, want user", provisionedRole)
	}
}

func TestAccountProvisionerCreateAccount_MapsBlankRoleToUserMembership(t *testing.T) {
	var provisionedRole string
	provisioner := NewAccountProvisioner(
		stubAccountUsers{
			createFn: func(_ context.Context, input models.CreateUserInput) (*models.User, error) {
				return &models.User{ID: 16, Username: input.Username, Role: input.Role}, nil
			},
		},
		nil,
	)
	provisioner.SetMembershipProvisioner(recordingMembershipProvisioner{
		provisionFn: func(_ context.Context, _ int, legacyRole string) error {
			provisionedRole = legacyRole
			return nil
		},
	})

	if _, err := provisioner.CreateAccount(context.Background(), CreateAccountInput{
		User: models.CreateUserInput{Username: "default-role"},
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if provisionedRole != "user" {
		t.Fatalf("membership legacy role = %q, want user", provisionedRole)
	}
}
