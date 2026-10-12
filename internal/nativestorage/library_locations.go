package nativestorage

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

var (
	// ErrLocationSourceUnavailable means the source does not exist, is
	// disabled, or may not back this library under tenancy rules.
	ErrLocationSourceUnavailable = errors.New("storage source unavailable for this library")
	// ErrLocationSourceInUse means the source already backs another library.
	ErrLocationSourceInUse = storagesource.ErrSourceInUse
)

// LibraryLocations gives new libraries their storage location.
type LibraryLocations struct {
	pool                *pgxpool.Pool
	nativeMovieReady    bool
	nativeAudioReady    bool
	mixedLocationsReady bool
	browser             *SourceBrowser
	sources             *storagesource.Repository
	resources           *resourcetenancy.Store
}

func NewLibraryLocations(pool *pgxpool.Pool) *LibraryLocations {
	return &LibraryLocations{pool: pool, sources: storagesource.NewRepository(pool), resources: resourcetenancy.NewStore(pool)}
}

// SetNativeMovieReady is startup-only, before library attachment is admitted.
// It preserves the resource authority check and changes only its kind hold.
func (l *LibraryLocations) SetNativeMovieReady(ready bool) {
	if l != nil {
		l.nativeMovieReady = ready
		l.resources.SetNativeMovieReady(ready)
	}
}

// AttachTx makes sourceKey the storage location of the library being created
// in tx. The library must be an enabled admitted library, and its owner must be
// allowed to use the source's installation; the same check gates every scan.
func (l *LibraryLocations) AttachTx(ctx context.Context, tx pgx.Tx, sourceKey uuid.UUID, folderID int) error {
	if l == nil {
		return ErrLocationSourceUnavailable
	}
	var source storagesource.SourceConfig
	err := tx.QueryRow(ctx, `SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled
		FROM bloem_storage_sources WHERE key=$1`, sourceKey).Scan(&source.Key, &source.OwnerID, &source.InstallationID,
		&source.PluginID, &source.ProviderSourceID, &source.RootEntryID, &source.ConfigurationRevision, &source.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLocationSourceUnavailable
	}
	if err != nil {
		return err
	}
	if source.InstallationID == nil || *source.InstallationID <= 0 {
		return ErrLocationSourceUnavailable
	}
	// The location INSERT takes a source KEY SHARE lock through its immediate
	// FK. Retain the installation first, matching native management's
	// installation -> source order. This is only a lock: the full authority
	// check below reloads the source and validates the new location after waits.
	var installationID int64
	err = tx.QueryRow(ctx, "SELECT id FROM plugin_installations WHERE id=$1 FOR SHARE", *source.InstallationID).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLocationSourceUnavailable
	}
	if err != nil {
		return err
	}
	location, err := l.sources.AddLocationTx(ctx, tx, sourceKey, folderID)
	if err != nil {
		return err
	}
	if err := l.resources.RequireStorageScanTx(ctx, tx, location, source); err != nil {
		if errors.Is(err, resourcetenancy.ErrResourceHidden) {
			return ErrLocationSourceUnavailable
		}
		return err
	}
	return nil
}

// SetNativeAudioReady must be called only after full audio composition.
func (l *LibraryLocations) SetNativeAudioReady(ready bool) {
	if l != nil {
		l.nativeAudioReady = ready
		l.resources.SetNativeAudioReady(ready)
	}
}

// SetMixedLocationsReady is startup-only. Call it only once scanning handles
// filesystem paths and several plugin sources in one library (mixed ingestion).
// It is the single switch for the save guard and the reported capability.
func (l *LibraryLocations) SetMixedLocationsReady(ready bool) {
	if l != nil {
		l.mixedLocationsReady = ready
	}
}

// MixedLocationsReady reports whether one library may combine filesystem paths
// and plugin sources, or use more than one plugin source.
func (l *LibraryLocations) MixedLocationsReady() bool {
	return l != nil && l.mixedLocationsReady
}
