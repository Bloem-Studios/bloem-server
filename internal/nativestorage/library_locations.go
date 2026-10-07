package nativestorage

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	sources   *storagesource.Repository
	resources *resourcetenancy.Store
}

func NewLibraryLocations(pool *pgxpool.Pool) *LibraryLocations {
	return &LibraryLocations{sources: storagesource.NewRepository(pool), resources: resourcetenancy.NewStore(pool)}
}

// AttachTx makes sourceKey the storage location of the library being created
// in tx. The library must be an enabled ebook library, and its owner must be
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
