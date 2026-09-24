package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// CreateProfileInTransaction inserts a profile using a caller-owned
// transaction. Account lifecycle creation uses it to bind the generated
// profile to the same receipt as the account and membership.
func (s *PostgresUserStore) CreateProfileInTransaction(ctx context.Context, tx pgx.Tx, p userstore.Profile) error {
	return createProfile(ctx, tx, s.userID, p)
}

// GetProfileInTransaction reads a profile through a caller-owned transaction.
func (s *PostgresUserStore) GetProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) (*userstore.Profile, error) {
	return getProfile(ctx, tx, s.userID, id)
}

// ListProfilesInTransaction lists profiles through a caller-owned transaction.
func (s *PostgresUserStore) ListProfilesInTransaction(ctx context.Context, tx pgx.Tx) ([]userstore.Profile, error) {
	return listProfiles(ctx, tx, s.userID)
}

// DeleteProfileInTransaction deletes a profile through a caller-owned transaction.
func (s *PostgresUserStore) DeleteProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) error {
	return deleteProfile(ctx, tx, s.userID, id)
}

// resolveProfileTenancy stamps a new profile with its organization and access
// group: a legacy caller (no organization) resolves the account's legacy
// identity, an explicit organization must be an active, visible membership,
// and a profile without a group joins the organization's default group.
func resolveProfileTenancy(ctx context.Context, exec preferenceSettingsExecutor, userID int, p *userstore.Profile) error {
	if p.OrganizationID == "" {
		organizationID, legacyGroupID, err := tenancy.NewProfileIdentityResolver(exec).ResolveLegacyProfileIdentity(ctx, userID)
		if err != nil {
			return fmt.Errorf("resolving legacy identity for profile %s: %w", p.ID, err)
		}
		p.OrganizationID = organizationID.String()
		if p.AccessGroupID == nil {
			p.AccessGroupID = legacyGroupID
		}
	} else {
		var activeMembership bool
		if err := exec.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM organization_memberships memberships
				JOIN organizations ON organizations.id = memberships.organization_id
				WHERE memberships.account_id = $1
				  AND memberships.organization_id = $2
				  AND memberships.status = 'active'
				  AND organizations.status <> 'suspended'
			)`, userID, p.OrganizationID).Scan(&activeMembership); err != nil {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, err)
		}
		if !activeMembership {
			return fmt.Errorf("validating organization for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
		}
	}
	if p.AccessGroupID == nil {
		var defaultGroupID int64
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM access_groups
			WHERE organization_id = $1
			  AND is_default`, p.OrganizationID).Scan(&defaultGroupID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, tenancy.ErrTenantNotFoundOrHidden)
			}
			return fmt.Errorf("resolving default access group for profile %s: %w", p.ID, err)
		}
		p.AccessGroupID = &defaultGroupID
	}
	return nil
}
