package nativestorage

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func nativeQueuePresent(queue NativeLibraryQueue) bool {
	if queue == nil {
		return false
	}
	value := reflect.ValueOf(queue)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return !value.IsNil()
	}
	return true
}
func (s *LibraryManagement) begin(ctx context.Context) (pgx.Tx, error) {
	if s == nil || s.pool == nil || s.folders == nil || s.resources == nil {
		return nil, nativeDomainError("native_storage_unavailable")
	}
	return nativeDomainBegin(ctx, s.pool)
}
func (s *LibraryManagement) commitMutation(ctx context.Context, tx pgx.Tx, operation string, operationID uuid.UUID, state catalog.NativeLibraryState, key *uuid.UUID) error {
	var err error
	if s.commit == nil {
		err = tx.Commit(ctx)
	} else {
		err = s.commit(ctx, tx, operation)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return nativeDomainMap(err)
	}
	return &MutationOutcomeUnknown{OperationID: operationID, CreationKey: state.CreationKey, LibraryID: state.LibraryID, SourceKey: key, Operation: operation, Cause: err}
}
func (s *LibraryManagement) libraryAddressTx(ctx context.Context, tx pgx.Tx, id int) (uuid.UUID, *storagesource.Binding, error) {
	var owner uuid.UUID
	err := tx.QueryRow(ctx, "SELECT owner_id FROM media_folders WHERE id=$1", id).Scan(&owner)
	if err != nil {
		return owner, nil, nativeDomainMap(err)
	}
	var binding storagesource.Binding
	err = tx.QueryRow(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE folder_id=$1", id).Scan(&binding.ID, &binding.SourceKey, &binding.FolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return owner, nil, nil
	}
	if err != nil {
		return owner, nil, nativeDomainMap(err)
	}
	return owner, &binding, nil
}
func (s *LibraryManagement) Get(ctx context.Context, actor auth.AdminContextClaims, id int) (LibraryStatus, error) {
	if id <= 0 {
		return LibraryStatus{}, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return LibraryStatus{}, err
	}
	owner, binding, err := s.libraryAddressTx(ctx, tx, id)
	if err != nil {
		nativeDomainRollback(ctx, tx)
		return LibraryStatus{}, err
	}
	libraryID := int64(id)
	sourceVisible := false
	if binding != nil {
		source, e := nativeDomainSourceTx(ctx, tx, binding.SourceKey)
		if e == nil {
			e = s.resources.RequireNativeManagementTx(ctx, tx, actor, source.Key, nil, source.OwnerID, &libraryID, false)
		}
		if e == nil {
			sourceVisible = true
		} else {
			nativeDomainRollback(ctx, tx)
			// A visible library can retain a hidden platform source after detach. Start
			// a fresh folder-only read rather than bypassing failed source authority or
			// reversing the installation/folder lock order in an existing transaction.
			if !errors.Is(e, resourcetenancy.ErrResourceHidden) && !errors.Is(e, pgx.ErrNoRows) {
				return LibraryStatus{}, nativeDomainMap(e)
			}
			tx, err = s.begin(ctx)
			if err != nil {
				return LibraryStatus{}, err
			}
		}
	}
	defer nativeDomainRollback(ctx, tx)
	if !sourceVisible {
		if err = s.resources.RequireNativeManagementTx(ctx, tx, actor, uuid.Nil, nil, owner, &libraryID, false); err != nil {
			return LibraryStatus{}, nativeDomainMap(err)
		}
	}
	result, err := s.libraryStatusTx(ctx, tx, actor, id, sourceVisible)
	if err != nil {
		return LibraryStatus{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	return result, nil
}
func (s *LibraryManagement) GetByCreationKey(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID) (LibraryStatus, error) {
	if key == uuid.Nil {
		return LibraryStatus{}, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return LibraryStatus{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	var id int
	err = tx.QueryRow(ctx, "SELECT folder_id FROM bloem_native_libraries WHERE creation_key=$1", key).Scan(&id)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	// Get retains the actual actor and resource; this addressing read is not a grant.
	nativeDomainRollback(ctx, tx)
	return s.Get(ctx, actor, id)
}
func (s *LibraryManagement) List(ctx context.Context, actor auth.AdminContextClaims, after *int, limit int) (LibraryPage, error) {
	result := LibraryPage{Libraries: []LibraryStatus{}}
	limit, err := nativePageLimit(limit)
	if err != nil {
		return result, err
	}
	if after != nil && *after <= 0 {
		return result, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return result, err
	}
	defer nativeDomainRollback(ctx, tx)
	if _, err = s.resources.RequireNativeLibraryCreateTx(ctx, tx, actor, nil); err != nil {
		return result, nativeDomainMap(err)
	}
	rows, err := tx.Query(ctx, `SELECT f.id FROM media_folders f JOIN bloem_native_libraries n ON n.folder_id=f.id
 JOIN resource_owners fo ON fo.id=f.owner_id WHERE `+nativeVisibleFolderSQL+`
 AND ($3::bigint IS NULL OR f.id>$3) ORDER BY f.id LIMIT $4`, actor.Scope == auth.AdminScopePlatform, actor.OrganizationID, after, limit+1)
	if err != nil {
		return result, nativeDomainMap(err)
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, nativeDomainMap(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, nativeDomainMap(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return result, nativeDomainMap(err)
	}
	for i, id := range ids {
		if i == limit {
			previous := ids[i-1]
			result.NextAfter = &previous
			break
		}
		library, e := s.Get(ctx, actor, id)
		if e != nil {
			return LibraryPage{}, e
		}
		result.Libraries = append(result.Libraries, library)
	}
	return result, nil
}
func (s *LibraryManagement) libraryStatusTx(ctx context.Context, tx pgx.Tx, actor auth.AdminContextClaims, id int, sourceVisible bool) (LibraryStatus, error) {
	var state catalog.NativeLibraryState
	var folderOwner uuid.UUID
	var kind string
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT n.folder_id,n.owner_id,n.creation_key,n.revision,n.initialized,n.deleting_job_id,f.owner_id,f.type,f.enabled
 FROM bloem_native_libraries n JOIN media_folders f ON f.id=n.folder_id WHERE n.folder_id=$1`, id).Scan(&state.LibraryID, &state.OwnerID, &state.CreationKey, &state.Revision, &state.Initialized, &state.DeletingJobID, &folderOwner, &kind, &enabled)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	var class string
	if err = tx.QueryRow(ctx, "SELECT bloem_native_folder_class($1)", id).Scan(&class); err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	if class != "native" || folderOwner != state.OwnerID || kind != "ebook" || !enabled {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	operations := map[string]bool{"initialize": false, "bind": false, "full_scan": false, "source_disable": false, "source_uninstall": false,
		"library_update": false, "scoped_scan": false, "repair": false, "delete": false, "unbind": false}
	result := LibraryStatus{LibraryID: id, CreationKey: state.CreationKey, LibraryRevision: state.Revision, Mode: "native", Initialized: state.Initialized,
		State: "initialization_required", SourceAvailability: "unbound", SupportedOperations: operations}
	var owner resourcetenancy.Owner
	err = tx.QueryRow(ctx, "SELECT id,kind,organization_id,revision FROM resource_owners WHERE id=$1", state.OwnerID).Scan(&owner.ID, &owner.Kind, &owner.OrganizationID, &owner.Revision)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	canManage := actor.Scope == auth.AdminScopePlatform || (owner.Kind == resourcetenancy.OwnerOrganization && owner.OrganizationID != nil && *owner.OrganizationID == actor.OrganizationID)
	operations["initialize"] = canManage && s.sections != nil
	if state.Initialized {
		result.State = "unbound"
		operations["bind"] = canManage
	}
	var binding storagesource.Binding
	err = tx.QueryRow(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE folder_id=$1", id).Scan(&binding.ID, &binding.SourceKey, &binding.FolderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	if err == nil {
		if state.Revision != 3 || !state.Initialized {
			return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
		}
		result.State = "source_unavailable"
		result.SourceAvailability = "inconsistent"
		operations["bind"] = false
		if sourceVisible {
			source, e := nativeDomainSourceTx(ctx, tx, binding.SourceKey)
			if e != nil {
				return LibraryStatus{}, nativeDomainMap(e)
			}
			result.BindingID = &binding.ID
			result.SourceKey = &binding.SourceKey
			result.SourceRevision = &source.ConfigurationRevision
			if source.InstallationID == nil {
				result.SourceAvailability = "detached"
			} else if !source.Enabled {
				result.SourceAvailability = "disabled"
			} else {
				var installEnabled bool
				if e = tx.QueryRow(ctx, "SELECT enabled FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&installEnabled); e != nil {
					return LibraryStatus{}, nativeDomainMap(e)
				}
				result.SourceAvailability = "disabled"
				if installEnabled {
					available := true
					if canManage {
						// Use actual command authority, including the retained exact
						// cross-owner availability grant, for positive capabilities.
						grantErr := s.resources.RequireNativeBindTx(ctx, tx, actor, source.Key, id, source.ConfigurationRevision, state.Revision)
						if errors.Is(grantErr, resourcetenancy.ErrResourceHidden) {
							available = false
						} else if grantErr != nil {
							return LibraryStatus{}, nativeDomainMap(grantErr)
						}
					}
					result.SourceAvailability = "unavailable"
					if available {
						result.SourceAvailability = "attached_enabled"
						result.State = "bound"
						operations["bind"] = canManage
					}
				}
			}
		}
	} else if state.Revision == 3 {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	if state.Initialized && s.sections != nil {
		// Real row/revision witnesses are needed for a positive readiness projection.
		witnessErr := s.sections.RequireNativeInitializationWitnessesTx(ctx, tx, id)
		if witnessErr == nil && result.State == "bound" && canManage && nativeQueuePresent(s.queue) {
			result.ReadyToQueue = true
			operations["full_scan"] = true
		}
		if witnessErr != nil {
			var typed *catalog.NativeOnboardingError
			if !errors.As(witnessErr, &typed) || typed.Code != "initialization_incomplete" {
				return LibraryStatus{}, nativeDomainMap(witnessErr)
			}
		}
	}
	if state.DeletingJobID != nil {
		result.State = "deleting"
		result.ReadyToQueue = false
		for operation := range operations {
			operations[operation] = false
		}
	}
	return result, nil
}
func (s *LibraryManagement) reconcileInitializationError(ctx context.Context, actor auth.AdminContextClaims, state catalog.NativeLibraryState, operationID uuid.UUID, cause error) (LibraryStatus, error) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	current, err := s.Get(readCtx, actor, state.LibraryID)
	if err == nil {
		return current, &catalog.NativeOnboardingError{Code: "initialization_incomplete", Cause: cause}
	}
	var typed *catalog.NativeOnboardingError
	if errors.As(err, &typed) && (typed.Code == "authorization_state_stale" || typed.Code == "not_found") {
		return LibraryStatus{}, err
	}
	return LibraryStatus{}, &MutationOutcomeUnknown{OperationID: operationID, CreationKey: state.CreationKey, LibraryID: state.LibraryID, Operation: "initialize", Cause: cause}
}

func nativeLibraryEmptyTx(ctx context.Context, tx pgx.Tx, id int) error {
	// The retained folder UPDATE lock excludes new children via their parent FK;
	// the installed guards cover every native video/staging association as well.
	var populated bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_files WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_item_libraries WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM episode_libraries WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_item_roots WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_item_groups WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM scanned_media_roots WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM scanned_media_groups WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_group_locations WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM observed_media_locations WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_group_overrides WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM skipped_media_roots WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM media_root_overrides WHERE media_folder_id=$1) OR
 EXISTS(SELECT 1 FROM series_root_match_queue WHERE media_folder_id=$1)`, id).Scan(&populated)
	if err != nil {
		return nativeDomainMap(err)
	}
	if populated {
		return nativeDomainError("binding_conflict")
	}
	return nil
}
