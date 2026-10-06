package storagesource

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CreateSource(ctx context.Context, config SourceConfig) (SourceConfig, error) {
	if !validText(config.PluginID, 1024, true) || !validText(config.ProviderSourceID, 1024, true) || !validText(config.RootEntryID, 1024, true) || config.ConfigurationRevision <= 0 || (config.InstallationID != nil && *config.InstallationID <= 0) {
		return SourceConfig{}, fmt.Errorf("invalid storage source configuration")
	}
	if config.Key == uuid.Nil {
		config.Key = uuid.New()
	}
	// Callers remain responsible for host membership and entitlement checks.
	// Derive only legacy trusted internal ownership; matching an owner is not
	// authorization to create or bind a resource.
	var owner any
	if config.OwnerID != uuid.Nil {
		owner = config.OwnerID
	}
	err := r.pool.QueryRow(ctx, `INSERT INTO bloem_storage_sources(key,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled,owner_id)
 SELECT $1,$2,$3,$4,$5,$6,$7,COALESCE($8::uuid,(SELECT owner_id FROM plugin_installations WHERE id=$2),bloem_platform_resource_owner_id())
 WHERE $2::bigint IS NULL OR EXISTS(SELECT 1 FROM plugin_installations WHERE id=$2 AND plugin_id=$3 AND owner_id=COALESCE($8::uuid,owner_id))
 RETURNING owner_id`, config.Key, config.InstallationID, config.PluginID, config.ProviderSourceID, config.RootEntryID, config.ConfigurationRevision, config.Enabled, owner).Scan(&config.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceConfig{}, ErrSourceUnavailable
	}
	return config, err
}

// Bind must be called only after the host authorizes source configuration and
// library access. This repository does not replace those policy checks.
func (r *Repository) Bind(ctx context.Context, sourceKey uuid.UUID, folderID int) (Binding, error) {
	if sourceKey == uuid.Nil || folderID <= 0 {
		return Binding{}, fmt.Errorf("invalid storage binding")
	}
	b := Binding{ID: uuid.New(), SourceKey: sourceKey, FolderID: folderID}
	err := r.pool.QueryRow(ctx, `INSERT INTO bloem_storage_bindings(id,source_key,folder_id) VALUES($1,$2,$3) ON CONFLICT(source_key,folder_id) DO UPDATE SET source_key=EXCLUDED.source_key RETURNING id`, b.ID, b.SourceKey, b.FolderID).Scan(&b.ID)
	return b, err
}

// Source reads retained identity. It grants no tenant authority or runtime access.
func (r *Repository) Source(ctx context.Context, key uuid.UUID) (SourceConfig, error) {
	var s SourceConfig
	err := r.pool.QueryRow(ctx, `SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled FROM bloem_storage_sources WHERE key=$1`, key).Scan(&s.Key, &s.OwnerID, &s.InstallationID, &s.PluginID, &s.ProviderSourceID, &s.RootEntryID, &s.ConfigurationRevision, &s.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrSourceUnavailable
	}
	return s, err
}
