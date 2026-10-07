package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativeScannerRepositoriesReady validates the exact concrete repository pools.
// It grants neither library admission nor installed-schema readiness.
func (r *FolderRepository) NativeScannerRepositoriesReady(pool *pgxpool.Pool,
	library *LibraryItemRepository, episodeLibrary *EpisodeLibraryRepository,
	item *ItemRepository, person *PersonRepository, episode *EpisodeRepository,
	extra *ExtraRepository) bool {
	return pool != nil && r != nil && r.pool == pool &&
		library != nil && library.pool == pool && episodeLibrary != nil && episodeLibrary.pool == pool &&
		item != nil && item.pool == pool && person != nil && person.pool == pool &&
		episode != nil && episode.pool == pool && extra != nil && extra.pool == pool
}

type NativeLibraryState struct {
	LibraryID     int
	OwnerID       uuid.UUID
	CreationKey   uuid.UUID
	Revision      int64
	Initialized   bool
	DeletingJobID *string
}
type NativeLibraryCreate struct {
	OwnerID          uuid.UUID
	CreationKey      uuid.UUID
	Name             string
	MetadataLanguage string
}
type nativeModeQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *FolderRepository) CreateNativeEbookTx(ctx context.Context, tx pgx.Tx, input NativeLibraryCreate) (NativeLibraryState, error) {
	unavailable := func() (NativeLibraryState, error) {
		return NativeLibraryState{}, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	if r == nil || r.pool == nil || tx == nil {
		return unavailable()
	}
	name := strings.TrimSpace(input.Name)
	language := strings.TrimSpace(input.MetadataLanguage)
	if input.MetadataLanguage == "" {
		language = "en"
	}
	if input.OwnerID == uuid.Nil || input.CreationKey == uuid.Nil || name == "" || len(name) > 256 || language == "" || len(language) > 64 ||
		!utf8.ValidString(name) || !utf8.ValidString(language) || strings.ContainsRune(name, 0) || strings.ContainsRune(language, 0) {
		return NativeLibraryState{}, &NativeOnboardingError{Code: "invalid_request"}
	}
	if !NativeStorageSchemaReady(ctx, tx) {
		return unavailable()
	}
	var owner uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM resource_owners WHERE id=$1 AND revision>0
 AND ((kind='platform' AND organization_id IS NULL) OR (kind='organization' AND organization_id IS NOT NULL))
 FOR SHARE NOWAIT`, input.OwnerID).Scan(&owner)
	if err != nil {
		return NativeLibraryState{}, MapNativeOnboardingError(err)
	}
	var state NativeLibraryState
	err = tx.QueryRow(ctx, `INSERT INTO media_folders(type,name,metadata_language,owner_id,enabled,
 chapter_thumbnails_enabled,intro_detection_enabled,trickplay_enabled,realtime_monitoring,trailer_kinds,sort_order)
 VALUES('ebook',$1,$2,$3,true,false,false,false,false,$4,(SELECT COALESCE(MAX(sort_order),0)+1 FROM media_folders))
 RETURNING id`, name, language, owner, defaultTrailerKinds()).Scan(&state.LibraryID)
	if err != nil {
		return NativeLibraryState{}, MapNativeOnboardingError(err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO bloem_native_libraries(folder_id,owner_id,creation_key)
 VALUES($1,$2,$3) RETURNING owner_id,creation_key,revision,initialized,deleting_job_id`,
		state.LibraryID, owner, input.CreationKey).Scan(&state.OwnerID, &state.CreationKey, &state.Revision, &state.Initialized, &state.DeletingJobID)
	if err != nil {
		return NativeLibraryState{}, MapNativeOnboardingError(err)
	}
	if err = seedCanonicalUserCollectionsGroup(ctx, tx, state.LibraryID); err != nil {
		return NativeLibraryState{}, MapNativeOnboardingError(err)
	}
	return state, nil
}

func (r *FolderRepository) RequireNativeLibraryLifecycleTx(ctx context.Context, tx pgx.Tx, id int) (NativeLibraryState, error) {
	var state NativeLibraryState
	if r == nil || r.pool == nil || tx == nil || id <= 0 || !NativeStorageSchemaReady(ctx, tx) {
		return state, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	var acquired bool
	err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,8500002))", "bloem:native-library:"+strconv.Itoa(id)).Scan(&acquired)
	if err != nil {
		return state, MapNativeOnboardingError(err)
	}
	if !acquired {
		return state, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	// Folder first agrees with binding and existing section/resource writers;
	// every inverse acquisition fails bounded rather than blocking.
	var actualID int
	err = tx.QueryRow(ctx, "SELECT id FROM media_folders WHERE id=$1 FOR UPDATE NOWAIT", id).Scan(&actualID)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrFolderNotFound
	}
	if err != nil {
		return state, MapNativeOnboardingError(err)
	}
	err = tx.QueryRow(ctx, `SELECT folder_id,owner_id,creation_key,revision,initialized,deleting_job_id
 FROM bloem_native_libraries WHERE folder_id=$1 FOR UPDATE NOWAIT`, id).Scan(
		&state.LibraryID, &state.OwnerID, &state.CreationKey, &state.Revision, &state.Initialized, &state.DeletingJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, &NativeOnboardingError{Code: "native_library_required"}
	}
	if err != nil {
		return state, MapNativeOnboardingError(err)
	}
	var class string
	if err = tx.QueryRow(ctx, "SELECT bloem_native_folder_class($1)", id).Scan(&class); err != nil {
		return state, MapNativeOnboardingError(err)
	}
	if class != "native" {
		return state, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	if state.DeletingJobID != nil {
		return state, &NativeOnboardingError{Code: "native_library_deleting"}
	}
	return state, nil
}
func (r *FolderRepository) NativeScanFolder(ctx context.Context, id int) (*models.MediaFolder, bool, error) {
	if r == nil || r.pool == nil || id <= 0 || !NativeStorageSchemaReady(ctx, r.pool) {
		return nil, false, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	var class string
	if err := r.pool.QueryRow(ctx, "SELECT bloem_native_folder_class($1)", id).Scan(&class); err != nil {
		return nil, false, MapNativeOnboardingError(err)
	}
	if class == "inconsistent" {
		return nil, false, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	folder, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, class == "native", err
	}
	if class == "local" {
		return folder, false, nil
	}
	var initialized bool
	var deleting *string
	err = r.pool.QueryRow(ctx, "SELECT initialized,deleting_job_id FROM bloem_native_libraries WHERE folder_id=$1", id).Scan(&initialized, &deleting)
	if err != nil {
		return nil, true, MapNativeOnboardingError(err)
	}
	if deleting != nil {
		return nil, true, &NativeOnboardingError{Code: "native_library_deleting"}
	}
	if !initialized {
		return nil, true, &NativeOnboardingError{Code: "native_library_not_initialized"}
	}
	if !folder.Enabled || folder.Type != "ebook" || len(folder.Paths) != 0 {
		return nil, true, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	return folder, true, nil
}

type NativeLocalFolderReader struct{ folders *FolderRepository }

func NewNativeLocalFolderReader(r *FolderRepository) *NativeLocalFolderReader {
	return &NativeLocalFolderReader{folders: r}
}
func (r *NativeLocalFolderReader) GetByID(ctx context.Context, id int) (*models.MediaFolder, error) {
	if r == nil || r.folders == nil || r.folders.pool == nil {
		return nil, &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	if err := r.folders.requireLocalLibraryMutation(ctx, r.folders.pool, id); err != nil {
		return nil, err
	}
	return r.folders.GetByID(ctx, id)
}
func (r *FolderRepository) requireLocalLibraryMutation(ctx context.Context, q nativeModeQueryer, id int) error {
	if r == nil || r.pool == nil || q == nil || id <= 0 || !NativeStorageSchemaReady(ctx, q) {
		return &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	var class string
	if err := q.QueryRow(ctx, "SELECT bloem_native_folder_class($1)", id).Scan(&class); err != nil {
		return MapNativeOnboardingError(err)
	}
	switch class {
	case "local":
		return nil
	case "native":
		return &NativeOnboardingError{Code: nativeLocalOperationUnsupportedCode}
	default:
		return &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
}
func (r *FolderRepository) requireLocalLibraryDelete(ctx context.Context, id int) error {
	if r == nil || r.pool == nil {
		return &NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	err := r.requireLocalLibraryMutation(ctx, r.pool, id)
	var typed *NativeOnboardingError
	if errors.As(err, &typed) && typed.Code == nativeLocalOperationUnsupportedCode {
		return &NativeOnboardingError{Code: "native_library_delete_unsupported", Cause: err}
	}
	return err
}
