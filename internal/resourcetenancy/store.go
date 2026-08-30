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

// ListLibraries returns organization-owned libraries and platform libraries
// with a live entitlement. Foreign organization roots and unentitled platform
// roots never enter the result set.
func (s *Store) ListLibraries(ctx context.Context, organizationID uuid.UUID) ([]LibraryProjection, error) {
	if s == nil || s.pool == nil {
		return nil, ErrResourceUnavailable
	}
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.name, f.type, ro.kind,
		       e.id, e.organization_id, e.status, e.security_revision
		FROM media_folders f
		JOIN resource_owners ro ON ro.id=f.owner_id
		LEFT JOIN organization_entitlements e
		  ON e.organization_id=$1 AND e.media_folder_id=f.id
		 AND e.root_owner_id=ro.id AND e.status IN ('active','suspended')
		WHERE (ro.kind='organization' AND ro.organization_id=$1)
		   OR (ro.kind='platform' AND e.id IS NOT NULL)
		ORDER BY lower(f.name),f.id`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("%w: list organization libraries: %w", ErrResourceUnavailable, err)
	}
	defer rows.Close()
	items := make([]LibraryProjection, 0)
	for rows.Next() {
		var item LibraryProjection
		var ownerKind OwnerKind
		var entitlementID, entitlementOrganizationID *uuid.UUID
		var status *EntitlementStatus
		var revision *int64
		if err := rows.Scan(&item.FolderID, &item.Name, &item.Type, &ownerKind, &entitlementID, &entitlementOrganizationID, &status, &revision); err != nil {
			return nil, fmt.Errorf("%w: scan organization library: %w", ErrResourceUnavailable, err)
		}
		if ownerKind == OwnerOrganization {
			item.AccessKind = LibraryOwned
		} else if entitlementID != nil && entitlementOrganizationID != nil && status != nil && revision != nil {
			item.AccessKind = LibraryEntitled
			item.Entitlement = &LibraryEntitlement{ID: *entitlementID, OrganizationID: *entitlementOrganizationID, Status: *status, SecurityRevision: *revision}
		} else {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate organization libraries: %w", ErrResourceUnavailable, err)
	}
	return items, nil
}

func (s *Store) SetLibraryEntitlementStatus(ctx context.Context, organizationID uuid.UUID, folderID, expectedRevision int64, status EntitlementStatus) (LibraryEntitlement, error) {
	if s == nil || s.pool == nil {
		return LibraryEntitlement{}, ErrResourceUnavailable
	}
	if organizationID == uuid.Nil || folderID <= 0 || expectedRevision <= 0 || (status != EntitlementActive && status != EntitlementSuspended) {
		return LibraryEntitlement{}, ErrInvalidRoot
	}
	var result LibraryEntitlement
	err := s.pool.QueryRow(ctx, `
		UPDATE organization_entitlements
		SET status=$4,security_revision=security_revision+1,updated_at=now()
		WHERE organization_id=$1 AND media_folder_id=$2
		  AND security_revision=$3 AND status IN ('active','suspended')
		RETURNING id,organization_id,status,security_revision`, organizationID, folderID, expectedRevision, status).Scan(&result.ID, &result.OrganizationID, &result.Status, &result.SecurityRevision)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LibraryEntitlement{}, fmt.Errorf("%w: update library entitlement: %w", ErrResourceUnavailable, err)
	}
	var current int64
	err = s.pool.QueryRow(ctx, `SELECT security_revision FROM organization_entitlements WHERE organization_id=$1 AND media_folder_id=$2 AND status IN ('active','suspended')`, organizationID, folderID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryEntitlement{}, ErrResourceHidden
	}
	if err != nil {
		return LibraryEntitlement{}, fmt.Errorf("%w: load library entitlement revision: %w", ErrResourceUnavailable, err)
	}
	return LibraryEntitlement{}, ErrAuthorizationStateChanged
}

func (s *Store) DeleteLibraryEntitlement(ctx context.Context, organizationID uuid.UUID, folderID, expectedRevision int64) error {
	if s == nil || s.pool == nil {
		return ErrResourceUnavailable
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE organization_entitlements
		SET status='revoked',revoked_at=now(),security_revision=security_revision+1,updated_at=now()
		WHERE organization_id=$1 AND media_folder_id=$2 AND security_revision=$3
		  AND status IN ('active','suspended')`, organizationID, folderID, expectedRevision)
	if err != nil {
		return fmt.Errorf("%w: revoke library entitlement: %w", ErrResourceUnavailable, err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var current int64
	err = s.pool.QueryRow(ctx, `SELECT security_revision FROM organization_entitlements WHERE organization_id=$1 AND media_folder_id=$2 AND status IN ('active','suspended')`, organizationID, folderID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceHidden
	}
	if err != nil {
		return fmt.Errorf("%w: load library entitlement revision: %w", ErrResourceUnavailable, err)
	}
	return ErrAuthorizationStateChanged
}

// AvailableMediaFolderIDs returns the media folders visible to the resolved
// tenant: organization-owned folders and platform folders with an active
// entitlement for that organization.
func (s *Store) AvailableMediaFolderIDs(ctx context.Context, tenant tenancy.Context) ([]int, error) {
	if !tenantCanAccessResources(tenant) {
		return nil, ErrResourceHidden
	}
	if s == nil || s.pool == nil {
		return nil, ErrResourceUnavailable
	}

	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT folders.id
		FROM media_folders AS folders
		JOIN resource_owners AS owners ON owners.id = folders.owner_id
		LEFT JOIN organization_entitlements AS entitlements
		  ON entitlements.organization_id = $1
		 AND entitlements.root_owner_id = owners.id
		 AND entitlements.media_folder_id = folders.id
		 AND entitlements.status = 'active'
		WHERE (owners.kind = 'organization' AND owners.organization_id = $1)
		   OR (owners.kind = 'platform' AND entitlements.id IS NOT NULL)
		ORDER BY folders.id`, tenant.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("%w: load available media folders: %w", ErrResourceUnavailable, err)
	}
	defer rows.Close()

	ids := make([]int, 0)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: scan available media folder: %w", ErrResourceUnavailable, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: load available media folders: %w", ErrResourceUnavailable, err)
	}
	return ids, nil
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
		return Owner{}, fmt.Errorf("%w: load root owner: %w", ErrResourceUnavailable, err)
	}
	return owner, nil
}

func (s *Store) RequireAccess(ctx context.Context, tenant tenancy.Context, root RootRef) (Grant, error) {
	if err := validateRoot(root); err != nil {
		return Grant{}, err
	}
	if !tenantCanAccessResources(tenant) {
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
		return Entitlement{}, fmt.Errorf("%w: load root entitlement: %w", ErrResourceUnavailable, err)
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
