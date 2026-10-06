package storagesource

import (
	"context"
	"fmt"
	"github.com/google/uuid"
)

func (r *Repository) CreateSource(ctx context.Context, config SourceConfig) (SourceConfig, error) {
	if !validText(config.PluginID, 1024, true) || !validText(config.ProviderSourceID, 1024, true) || !validText(config.RootEntryID, 1024, true) || config.ConfigurationRevision <= 0 || (config.InstallationID != nil && *config.InstallationID <= 0) {
		return SourceConfig{}, fmt.Errorf("invalid storage source configuration")
	}
	if config.Key == uuid.Nil {
		config.Key = uuid.New()
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO bloem_storage_sources(key,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)`, config.Key, config.InstallationID, config.PluginID, config.ProviderSourceID, config.RootEntryID, config.ConfigurationRevision, config.Enabled)
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
