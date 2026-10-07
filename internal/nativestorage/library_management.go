package nativestorage

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LibraryStatus struct {
	LibraryID           int             `json:"library_id"`
	CreationKey         uuid.UUID       `json:"creation_key"`
	LibraryRevision     int64           `json:"library_revision"`
	Mode                string          `json:"mode"`
	Initialized         bool            `json:"initialized"`
	State               string          `json:"state"`
	BindingID           *uuid.UUID      `json:"binding_id"`
	SourceKey           *uuid.UUID      `json:"source_key"`
	SourceRevision      *int64          `json:"source_revision"`
	SourceAvailability  string          `json:"source_availability"`
	SupportedOperations map[string]bool `json:"supported_operations"`
	ReadyToQueue        bool            `json:"ready_to_queue"`
}

func nativeInitializeRevisionAllowed(expected, current int64, bound bool) bool {
	return expected > 0 && (expected == current || (current == 2 && expected == 1 && !bound))
}
func nativeBindRevisionAllowed(expected, current int64, exact bool) bool {
	return expected > 0 && (expected == current || (current == 3 && expected == 2 && exact))
}

type LibraryCreateCommand struct {
	Name             string     `json:"name"`
	MetadataLanguage string     `json:"metadata_language"`
	OrganizationID   *uuid.UUID `json:"organization_id"`
}
type LibraryPage struct {
	Libraries []LibraryStatus `json:"libraries"`
	NextAfter *int            `json:"next_after"`
}
type BindResult struct {
	BindingID       uuid.UUID `json:"binding_id"`
	SourceKey       uuid.UUID `json:"source_key"`
	FolderID        int       `json:"folder_id"`
	SourceRevision  int64     `json:"source_revision"`
	LibraryRevision int64     `json:"library_revision"`
	Repeated        bool      `json:"repeated"`
}
type ScanResult struct {
	LibraryID       int    `json:"library_id"`
	ScanRunID       string `json:"scan_run_id"`
	Mode            string `json:"mode"`
	State           string `json:"state"`
	Created         bool   `json:"created"`
	LibraryRevision int64  `json:"library_revision"`
	SourceRevision  int64  `json:"source_revision"`
}
type NativeLibraryQueue interface {
	EnqueueNativeLibraryAuthorized(context.Context, int, func(context.Context, pgx.Tx) error) (*models.ScanRun, bool, error)
}
type LibraryManagement struct {
	pool      *pgxpool.Pool
	folders   *catalog.FolderRepository
	sections  *sections.Repository
	resources *resourcetenancy.Store
	queue     NativeLibraryQueue
	commit    func(context.Context, pgx.Tx, string) error
}

func NewLibraryManagement(pool *pgxpool.Pool, folders *catalog.FolderRepository, sections *sections.Repository, resources *resourcetenancy.Store, queue NativeLibraryQueue) *LibraryManagement {
	return &LibraryManagement{pool: pool, folders: folders, sections: sections, resources: resources, queue: queue}
}
func (s *LibraryManagement) Create(ctx context.Context, actor auth.AdminContextClaims, cmd LibraryCreateCommand) (LibraryStatus, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return LibraryStatus{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	owner, err := s.resources.RequireNativeLibraryCreateTx(ctx, tx, actor, cmd.OrganizationID)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	operationID, creationKey := uuid.New(), uuid.New()
	state, err := s.folders.CreateNativeEbookTx(ctx, tx, catalog.NativeLibraryCreate{OwnerID: owner.ID, CreationKey: creationKey, Name: cmd.Name, MetadataLanguage: cmd.MetadataLanguage})
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	status, err := s.libraryStatusTx(ctx, tx, actor, state.LibraryID, false)
	if err != nil {
		return LibraryStatus{}, err
	}
	if err = s.commitMutation(ctx, tx, "create", operationID, state, nil); err != nil {
		return LibraryStatus{}, err
	}
	return status, nil
}
func (s *LibraryManagement) Initialize(ctx context.Context, actor auth.AdminContextClaims, id int, expected int64) (LibraryStatus, error) {
	if id <= 0 || expected <= 0 {
		return LibraryStatus{}, nativeDomainError("invalid_request")
	}
	if s == nil || s.sections == nil {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return LibraryStatus{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	if _, err = s.resources.RequireNativeLibraryManagementTx(ctx, tx, actor, id); err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	state, err := s.folders.RequireNativeLibraryLifecycleTx(ctx, tx, id)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	var bound bool
	var name string
	if err = tx.QueryRow(ctx, "SELECT name,EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=$1) FROM media_folders WHERE id=$1", id).Scan(&name, &bound); err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	if !nativeInitializeRevisionAllowed(expected, state.Revision, bound) {
		return LibraryStatus{}, &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &state.Revision}
	}
	if state.Initialized {
		if err = tx.Commit(ctx); err != nil {
			return LibraryStatus{}, nativeDomainMap(err)
		}
		return s.Get(ctx, actor, id)
	}
	if state.Revision != 1 || bound {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	operationID := uuid.New()
	authorize := func(attemptCtx context.Context, attemptTx pgx.Tx) error {
		if _, e := attemptTx.Exec(attemptCtx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); e != nil {
			return nativeDomainMap(e)
		}
		if _, e := s.resources.RequireNativeLibraryManagementTx(attemptCtx, attemptTx, actor, id); e != nil {
			return nativeDomainMap(e)
		}
		current, e := s.folders.RequireNativeLibraryLifecycleTx(attemptCtx, attemptTx, id)
		if e != nil {
			return nativeDomainMap(e)
		}
		var hasBinding bool
		if e = attemptTx.QueryRow(attemptCtx, "SELECT EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=$1)", id).Scan(&hasBinding); e != nil {
			return nativeDomainMap(e)
		}
		if hasBinding || !nativeInitializeRevisionAllowed(expected, current.Revision, hasBinding) {
			return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &current.Revision}
		}
		return nil
	}
	// Each helper obtains the transaction INSIDE its actual serialized write
	// attempt. No transaction/advisory lock is carried between these stages.
	if err = s.sections.SeedNativeLibraryDefaultsAuthorized(ctx, id, authorize); err != nil {
		return s.reconcileInitializationError(ctx, actor, state, operationID, err)
	}
	if err = s.sections.SeedNativeHomeRecentAuthorized(ctx, id, name, authorize); err != nil {
		return s.reconcileInitializationError(ctx, actor, state, operationID, err)
	}
	finalTx, err := s.begin(ctx)
	if err != nil {
		return s.reconcileInitializationError(ctx, actor, state, operationID, err)
	}
	defer nativeDomainRollback(ctx, finalTx)
	if err = authorize(ctx, finalTx); err != nil {
		return LibraryStatus{}, err
	}
	current, err := s.folders.RequireNativeLibraryLifecycleTx(ctx, finalTx, id)
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	if current.Initialized {
		if err = finalTx.Commit(ctx); err != nil {
			return LibraryStatus{}, nativeDomainMap(err)
		}
		return s.Get(ctx, actor, id)
	}
	if current.Revision != 1 {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	if err = s.sections.RequireNativeInitializationWitnessesTx(ctx, finalTx, id); err != nil {
		status, e := s.libraryStatusTx(ctx, finalTx, actor, id, false)
		if e != nil {
			return LibraryStatus{}, e
		}
		return status, nativeDomainMap(err)
	}
	err = finalTx.QueryRow(ctx, `UPDATE bloem_native_libraries SET initialized=true,revision=2
 WHERE folder_id=$1 AND initialized=false AND revision=1 RETURNING revision`, id).Scan(&current.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryStatus{}, nativeDomainError("native_storage_unavailable")
	}
	if err != nil {
		return LibraryStatus{}, nativeDomainMap(err)
	}
	current.Initialized = true
	status, err := s.libraryStatusTx(ctx, finalTx, actor, id, false)
	if err != nil {
		return LibraryStatus{}, err
	}
	if err = s.commitMutation(ctx, finalTx, "initialize", operationID, current, nil); err != nil {
		return LibraryStatus{}, err
	}
	return status, nil
}
func (s *LibraryManagement) Bind(ctx context.Context, actor auth.AdminContextClaims, key uuid.UUID, id int, expectedSource, expectedLibrary int64) (BindResult, error) {
	if key == uuid.Nil || id <= 0 || expectedSource <= 0 || expectedLibrary <= 0 {
		return BindResult{}, nativeDomainError("invalid_request")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return BindResult{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	if err = s.resources.RequireNativeBindTx(ctx, tx, actor, key, id, expectedSource, expectedLibrary); err != nil {
		return BindResult{}, nativeDomainMap(err)
	}
	state, err := s.folders.RequireNativeLibraryLifecycleTx(ctx, tx, id)
	if err != nil {
		return BindResult{}, nativeDomainMap(err)
	}
	var enabled bool
	var kind string
	if err = tx.QueryRow(ctx, "SELECT enabled,type FROM media_folders WHERE id=$1", id).Scan(&enabled, &kind); err != nil {
		return BindResult{}, nativeDomainMap(err)
	}
	if !enabled || kind != "ebook" {
		return BindResult{}, nativeDomainError("native_storage_unavailable")
	}
	if !state.Initialized {
		return BindResult{}, nativeDomainError("native_library_not_initialized")
	}
	var existing storagesource.Binding
	err = tx.QueryRow(ctx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE folder_id=$1", id).Scan(&existing.ID, &existing.SourceKey, &existing.FolderID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BindResult{}, nativeDomainMap(err)
	}
	bound := err == nil
	exact := bound && existing.SourceKey == key && existing.FolderID == id
	if !nativeBindRevisionAllowed(expectedLibrary, state.Revision, exact) {
		return BindResult{}, &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &state.Revision}
	}
	if bound {
		if !exact {
			return BindResult{}, nativeDomainError("binding_conflict")
		}
		if state.Revision != 3 {
			return BindResult{}, nativeDomainError("native_storage_unavailable")
		}
		if err = tx.Commit(ctx); err != nil {
			return BindResult{}, nativeDomainMap(err)
		}
		return BindResult{BindingID: existing.ID, SourceKey: key, FolderID: id, SourceRevision: expectedSource, LibraryRevision: state.Revision, Repeated: true}, nil
	}
	if state.Revision != 2 {
		return BindResult{}, nativeDomainError("native_storage_unavailable")
	}
	if s.sections == nil {
		return BindResult{}, nativeDomainError("native_storage_unavailable")
	}
	if err = s.sections.RequireNativeInitializationWitnessesTx(ctx, tx, id); err != nil {
		return BindResult{}, nativeDomainMap(err)
	}
	if err = nativeLibraryEmptyTx(ctx, tx, id); err != nil {
		return BindResult{}, err
	}
	bindingID, operationID := uuid.New(), uuid.New()
	err = tx.QueryRow(ctx, `INSERT INTO bloem_storage_bindings(id,source_key,folder_id) VALUES($1,$2,$3) RETURNING id,source_key,folder_id`, bindingID, key, id).Scan(&existing.ID, &existing.SourceKey, &existing.FolderID)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return BindResult{}, nativeDomainError("binding_conflict")
		}
		return BindResult{}, nativeDomainMap(err)
	}
	err = tx.QueryRow(ctx, `UPDATE bloem_native_libraries SET revision=3 WHERE folder_id=$1 AND initialized=true AND revision=2 RETURNING revision`, id).Scan(&state.Revision)
	if err != nil {
		return BindResult{}, nativeDomainMap(err)
	}
	if err = s.commitMutation(ctx, tx, "bind", operationID, state, &key); err != nil {
		return BindResult{}, err
	}
	return BindResult{BindingID: existing.ID, SourceKey: key, FolderID: id, SourceRevision: expectedSource, LibraryRevision: state.Revision}, nil
}
func (s *LibraryManagement) Scan(ctx context.Context, actor auth.AdminContextClaims, id int, expectedLibrary, expectedSource int64) (ScanResult, error) {
	if id <= 0 || expectedLibrary <= 0 || expectedSource <= 0 {
		return ScanResult{}, nativeDomainError("invalid_request")
	}
	if s == nil || !nativeQueuePresent(s.queue) || s.sections == nil {
		return ScanResult{}, nativeDomainError("native_storage_unavailable")
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	defer nativeDomainRollback(ctx, tx)
	owner, binding, err := s.libraryAddressTx(ctx, tx, id)
	if err != nil {
		return ScanResult{}, err
	}
	if binding == nil {
		// A missing binding cannot bypass library authority to disclose its state.
		if _, err = s.resources.RequireNativeLibraryManagementTx(ctx, tx, actor, id); err != nil {
			return ScanResult{}, nativeDomainMap(err)
		}
		state, e := s.folders.RequireNativeLibraryLifecycleTx(ctx, tx, id)
		if e != nil {
			return ScanResult{}, nativeDomainMap(e)
		}
		if !state.Initialized {
			return ScanResult{}, nativeDomainError("native_library_not_initialized")
		}
		return ScanResult{}, nativeDomainError("native_storage_unavailable")
	}
	_ = owner
	var authorizedState catalog.NativeLibraryState
	authorize := func(attemptCtx context.Context, attemptTx pgx.Tx) error {
		if !catalog.NativeStorageSchemaReady(attemptCtx, attemptTx) {
			return nativeDomainError("native_storage_unavailable")
		}
		if e := s.resources.RequireNativeBindTx(attemptCtx, attemptTx, actor, binding.SourceKey, id, expectedSource, expectedLibrary); e != nil {
			return nativeDomainMap(e)
		}
		state, e := s.folders.RequireNativeLibraryLifecycleTx(attemptCtx, attemptTx, id)
		if e != nil {
			return nativeDomainMap(e)
		}
		if state.Revision != expectedLibrary {
			return &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &state.Revision}
		}
		if !state.Initialized {
			return nativeDomainError("native_library_not_initialized")
		}
		if state.Revision != 3 {
			return nativeDomainError("native_storage_unavailable")
		}
		var actual storagesource.Binding
		var enabled bool
		var kind string
		if e = attemptTx.QueryRow(attemptCtx, "SELECT id,source_key,folder_id FROM bloem_storage_bindings WHERE folder_id=$1", id).Scan(&actual.ID, &actual.SourceKey, &actual.FolderID); e != nil {
			return nativeDomainMap(e)
		}
		if actual != *binding {
			return nativeDomainError("binding_conflict")
		}
		if e = attemptTx.QueryRow(attemptCtx, "SELECT enabled,type FROM media_folders WHERE id=$1", id).Scan(&enabled, &kind); e != nil {
			return nativeDomainMap(e)
		}
		if !enabled || kind != "ebook" {
			return nativeDomainError("native_storage_unavailable")
		}
		if e = s.sections.RequireNativeInitializationWitnessesTx(attemptCtx, attemptTx, id); e != nil {
			return nativeDomainMap(e)
		}
		authorizedState = state
		return nil
	}
	if err = authorize(ctx, tx); err != nil {
		return ScanResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ScanResult{}, nativeDomainMap(err)
	}
	run, created, err := s.queue.EnqueueNativeLibraryAuthorized(ctx, id, authorize)
	if err != nil {
		var unknown *MutationOutcomeUnknown
		if errors.As(err, &unknown) {
			// Preserve the actual service-selected run ID and operation ID, while only
			// carrying identifiers retained by this command's authoritative callback.
			copy := *unknown
			copy.LibraryID = id
			copy.CreationKey = authorizedState.CreationKey
			key := binding.SourceKey
			copy.SourceKey = &key
			return ScanResult{}, &copy
		}
		return ScanResult{}, nativeDomainMap(err)
	}
	if run == nil || run.ID == "" || run.MediaFolderID != id || run.Mode != "library" || run.Path != "" || (run.Status != "accepted" && run.Status != "running") {
		return ScanResult{}, nativeDomainError("native_storage_unavailable")
	}
	return ScanResult{LibraryID: id, ScanRunID: run.ID, Mode: run.Mode, State: run.Status, Created: created, LibraryRevision: authorizedState.Revision, SourceRevision: expectedSource}, nil
}
