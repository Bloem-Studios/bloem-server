package resourcetenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) RootOwner(ctx context.Context, root RootRef) (Owner, error) {
	if err := validateRoot(root); err != nil {
		return Owner{}, err
	}
	if s == nil || s.pool == nil {
		return Owner{}, ErrResourceUnavailable
	}

	const ownerColumns = `owners.id, owners.kind, owners.organization_id, owners.revision`
	var row pgx.Row
	switch root.Kind {
	case RootMediaFolder:
		row = s.pool.QueryRow(ctx, `
			SELECT `+ownerColumns+`
			FROM media_folders AS roots
			JOIN resource_owners AS owners ON owners.id = roots.owner_id
			WHERE roots.id = $1`, root.ID)
	case RootPluginInstallation:
		row = s.pool.QueryRow(ctx, `
			SELECT `+ownerColumns+`
			FROM plugin_installations AS roots
			JOIN resource_owners AS owners ON owners.id = roots.owner_id
			WHERE roots.id = $1`, root.ID)
	default:
		return Owner{}, ErrInvalidRoot
	}

	var owner Owner
	if err := row.Scan(&owner.ID, &owner.Kind, &owner.OrganizationID, &owner.Revision); errors.Is(err, pgx.ErrNoRows) {
		return Owner{}, ErrResourceHidden
	} else if err != nil {
		return Owner{}, fmt.Errorf("%w: load root owner: %v", ErrResourceUnavailable, err)
	}
	return owner, nil
}

func (s *Store) RequireAccess(ctx context.Context, tenant tenancy.Context, root RootRef) (Grant, error) {
	if err := validateRoot(root); err != nil {
		return Grant{}, err
	}
	if tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil || tenant.AccountID <= 0 || tenant.MembershipStatus != tenancy.MembershipActive {
		return Grant{}, ErrResourceHidden
	}
	if tenant.OrganizationStatus != tenancy.OrganizationActive && !(tenant.Legacy && tenant.OrganizationStatus == tenancy.OrganizationInitializing) {
		return Grant{}, ErrResourceHidden
	}

	owner, err := s.RootOwner(ctx, root)
	if err != nil {
		return Grant{}, err
	}
	grant := Grant{Root: root, Owner: owner}
	switch owner.Kind {
	case OwnerOrganization:
		if owner.OrganizationID == nil || *owner.OrganizationID != tenant.OrganizationID {
			return Grant{}, ErrResourceHidden
		}
		return grant, nil
	case OwnerPlatform:
		entitlement, err := s.activeEntitlement(ctx, tenant.OrganizationID, root, owner.ID)
		if err != nil {
			return Grant{}, err
		}
		grant.Entitlement = &entitlement
		return grant, nil
	default:
		return Grant{}, ErrResourceHidden
	}
}

func (s *Store) activeEntitlement(ctx context.Context, organizationID uuid.UUID, root RootRef, ownerID uuid.UUID) (Entitlement, error) {
	if s == nil || s.pool == nil {
		return Entitlement{}, ErrResourceUnavailable
	}
	const entitlementColumns = `
		id,
		organization_id,
		root_owner_id,
		status,
		source_bundle_id,
		source_bundle_revision,
		security_revision`
	var row pgx.Row
	switch root.Kind {
	case RootMediaFolder:
		row = s.pool.QueryRow(ctx, `
			SELECT `+entitlementColumns+`
			FROM organization_entitlements
			WHERE organization_id = $1
			  AND root_owner_id = $2
			  AND root_kind = 'media_folder'
			  AND media_folder_id = $3
			  AND status = 'active'`, organizationID, ownerID, root.ID)
	case RootPluginInstallation:
		row = s.pool.QueryRow(ctx, `
			SELECT `+entitlementColumns+`
			FROM organization_entitlements
			WHERE organization_id = $1
			  AND root_owner_id = $2
			  AND root_kind = 'plugin_installation'
			  AND plugin_installation_id = $3
			  AND status = 'active'`, organizationID, ownerID, root.ID)
	default:
		return Entitlement{}, ErrInvalidRoot
	}

	entitlement := Entitlement{Root: root}
	if err := row.Scan(
		&entitlement.ID,
		&entitlement.OrganizationID,
		&entitlement.RootOwnerID,
		&entitlement.Status,
		&entitlement.SourceBundleID,
		&entitlement.SourceBundleRevision,
		&entitlement.SecurityRevision,
	); errors.Is(err, pgx.ErrNoRows) {
		return Entitlement{}, ErrResourceHidden
	} else if err != nil {
		return Entitlement{}, fmt.Errorf("%w: load root entitlement: %v", ErrResourceUnavailable, err)
	}
	return entitlement, nil
}

func validateRoot(root RootRef) error {
	if root.ID <= 0 {
		return ErrInvalidRoot
	}
	switch root.Kind {
	case RootMediaFolder, RootPluginInstallation:
		return nil
	default:
		return ErrInvalidRoot
	}
}
