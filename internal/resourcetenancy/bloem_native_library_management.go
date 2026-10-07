package resourcetenancy

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RequireNativeLibraryCreateTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, organizationID *uuid.UUID) (Owner, error) {
	if s == nil || s.pool == nil || tx == nil {
		return Owner{}, ErrResourceUnavailable
	}
	if !nativeManagementActorShape(actor) {
		return Owner{}, ErrInvalidActor
	}
	target := organizationID
	if actor.Scope == auth.AdminScopeOrganization {
		if organizationID != nil {
			return Owner{}, &catalog.NativeOnboardingError{Code: "invalid_request"}
		}
		target = &actor.OrganizationID
	}
	if target != nil && *target == uuid.Nil {
		return Owner{}, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, "SELECT id FROM resource_owners WHERE (kind='platform' AND $1::uuid IS NULL) OR (kind='organization' AND organization_id=$1)", target).Scan(&id)
	if err != nil {
		return Owner{}, nativeScanError(err)
	}
	before, err := nativeManagementOwners(ctx, tx, []uuid.UUID{id}, false)
	if err != nil {
		return Owner{}, err
	}
	if err = retainNativeManagementActor(ctx, tx, actor, before); err != nil {
		return Owner{}, err
	}
	after, err := nativeManagementOwners(ctx, tx, []uuid.UUID{id}, true)
	if err != nil {
		return Owner{}, err
	}
	if !nativeManagementOwnersUnchanged(before, after) {
		return Owner{}, ErrAuthorizationStateChanged
	}
	// Recheck the same retained actor identities after the final owner wait.
	// Session and admin-context expiry use the current database clock.
	if err = retainNativeManagementActor(ctx, tx, actor, after); err != nil {
		return Owner{}, err
	}
	return after[id], nil
}
func (s *Store) RequireNativeLibraryManagementTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, folderID int) (Owner, error) {
	if s == nil || s.pool == nil || tx == nil {
		return Owner{}, ErrResourceUnavailable
	}
	if folderID <= 0 {
		return Owner{}, ErrResourceHidden
	}
	var ownerID uuid.UUID
	err := tx.QueryRow(ctx, "SELECT owner_id FROM media_folders WHERE id=$1", folderID).Scan(&ownerID)
	if err != nil {
		return Owner{}, nativeScanError(err)
	}
	id := int64(folderID)
	owners, err := s.retainNativeResources(ctx, tx, actor, uuid.Nil, nil, ownerID, &id, false, true)
	if err != nil {
		return Owner{}, err
	}
	return owners[ownerID], nil
}
func (s *Store) RequireNativeBindTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, sourceKey uuid.UUID, folderID int, expectedSourceRevision, expectedLibraryRevision int64) error {
	if s == nil || s.pool == nil || tx == nil {
		return ErrResourceUnavailable
	}
	if sourceKey == uuid.Nil || folderID <= 0 || expectedSourceRevision <= 0 || expectedLibraryRevision <= 0 {
		return &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	var ownerID uuid.UUID
	err := tx.QueryRow(ctx, "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", sourceKey).Scan(&ownerID)
	if err != nil {
		return nativeScanError(err)
	}
	id := int64(folderID)
	owners, err := s.retainNativeResources(ctx, tx, actor, sourceKey, nil, ownerID, &id, false, true)
	if err != nil {
		return err
	}
	var revision int64
	var installationID *int64
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT configuration_revision,installation_id,enabled FROM bloem_storage_sources WHERE key=$1", sourceKey).Scan(&revision, &installationID, &enabled)
	if err != nil {
		return nativeScanError(err)
	}
	if revision != expectedSourceRevision {
		return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentSourceRevision: &revision}
	}
	if installationID == nil || !enabled {
		return &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	var installEnabled bool
	if err = tx.QueryRow(ctx, "SELECT enabled FROM plugin_installations WHERE id=$1", *installationID).Scan(&installEnabled); err != nil {
		return nativeScanError(err)
	}
	if !installEnabled {
		return &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	var folderOwner uuid.UUID
	var libraryRevision int64
	if err = tx.QueryRow(ctx, "SELECT owner_id,revision FROM bloem_native_libraries WHERE folder_id=$1", folderID).Scan(&folderOwner, &libraryRevision); err != nil {
		return nativeScanError(err)
	}
	// The command implements the exact same-binding current-1 exception; this
	// authorizer admits only those two revisions and retains both real grants.
	if expectedLibraryRevision != libraryRevision && !(libraryRevision == 3 && expectedLibraryRevision == 2) {
		return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &libraryRevision}
	}
	folder, source := owners[folderOwner], owners[ownerID]
	if folder.Kind == OwnerPlatform {
		if source.Kind != OwnerPlatform {
			return ErrResourceHidden
		}
	} else if source.Kind == OwnerOrganization {
		if *source.OrganizationID != *folder.OrganizationID {
			return ErrResourceHidden
		}
	} else {
		// Even a platform actor needs the folder organization's actual source grant.
		var entitlement uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM organization_entitlements WHERE organization_id=$1 AND root_owner_id=$2
   AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability'
   AND plugin_installation_id=$3 AND status='active' AND security_revision>0 FOR SHARE`, *folder.OrganizationID, ownerID, *installationID).Scan(&entitlement)
		if err != nil {
			return nativeScanError(err)
		}
	}
	// The cross-owner entitlement is the final blocking predicate. Reload only
	// the actor/organization identities already retained by retainNativeResources.
	return retainNativeManagementActor(ctx, tx, actor, owners)
}
