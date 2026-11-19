package plugins

import (
	"context"
	"fmt"
)

// Native storage instances live in Bloem's own tables, not in Silo's plugin
// tables: Silo keeps one plugin_installations row per plugin, and every
// native storage source has its own instance of its plugin (its own process,
// settings and package).
//
//	bloem_storage_installations          the instance
//	bloem_storage_installation_configs   its settings, encoded as
//	                                     plugin_runtime_configs values are
//	                                     (encodeRuntimeConfigValue, same AAD)
//	bloem_storage_installation_archives  its package

// nativeInstallationColumns reads an instance in installationColumns' order,
// so scanInstallation builds the same Installation value. The Silo-only
// fields (repository, update and signature state) have fixed values: native
// packages are host-approved and updated only by Bloem.
const nativeInstallationColumns = `installation_id, NULL::integer, plugin_id, version, install_path, enabled, 'plugin', 'manual', NULL::text, created_at, updated_at, runtime_generation, NULL::text, NULL::text, NULL::timestamptz, 'unknown', NULL::text, NULL::timestamptz`

// nativeArchiveColumns reads an instance's package in archiveColumns' order.
const nativeArchiveColumns = `installation_id, manifest_json, checksum, archive_bytes, created_at, updated_at`

// nativeConfigs returns a native instance's settings, decrypted, as
// RuntimeConfigStore.ListGlobalConfigs returns a Silo installation's.
func (r *NativeStorageRegistry) nativeConfigs(ctx context.Context, installationID int) ([]*RuntimeConfig, error) {
	rows, err := r.pool.Query(ctx, `SELECT installation_id, config_key, config_value, created_at, updated_at
 FROM bloem_storage_installation_configs WHERE installation_id=$1 ORDER BY config_key`, installationID)
	if err != nil {
		return nil, fmt.Errorf("listing native storage settings: %w", err)
	}
	defer rows.Close()
	var configs []*RuntimeConfig
	for rows.Next() {
		var config RuntimeConfig
		var valueJSON []byte
		if err := rows.Scan(&config.InstallationID, &config.Key, &valueJSON, &config.CreatedAt, &config.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning native storage setting: %w", err)
		}
		if config.Value, err = decodeRuntimeConfigValue(r.configs.cipher, config.InstallationID, config.Key, valueJSON); err != nil {
			return nil, fmt.Errorf("decoding native storage setting %d/%s: %w", config.InstallationID, config.Key, err)
		}
		configs = append(configs, &config)
	}
	return configs, rows.Err()
}
