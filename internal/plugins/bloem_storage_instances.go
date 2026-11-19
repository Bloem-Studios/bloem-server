package plugins

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
