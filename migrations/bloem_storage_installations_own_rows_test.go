package migrations

import (
	"strconv"
	"testing"
)

const (
	beforeStorageInstallationsOwnRows = 20261118193342
	storageInstallationsOwnRows       = 20261118203000
	uniquePluginInstallationPerPlugin = 20261118203050
)

// TestStorageInstallationsOwnRowsSurviveOneInstallationPerPlugin: two native
// storage sources of one plugin, each with its own instance, settings,
// package and default-organization entitlement, keep all of it through
// Bloem's move and Silo's one-installation-per-plugin migration, under the
// same ids. Down puts the instances back in Silo's tables.
func TestStorageInstallationsOwnRowsSurviveOneInstallationPerPlugin(t *testing.T) {
	db, provider := newTestDBMigratedTo(t, beforeStorageInstallationsOwnRows)
	exec(t, db,
		`INSERT INTO plugin_installations (id, plugin_id, version, install_path, enabled, update_policy, runtime_generation)
		 OVERRIDING SYSTEM VALUE VALUES
		 (30, 'bloem.storage.bookwarehouse', '0.3.12', '/native/ebooks/plugin', true, 'manual', 4),
		 (36, 'bloem.storage.bookwarehouse', '0.3.12', '/native/audio/plugin', true, 'manual', 7),
		 (40, 'silo.requests.arr', '1.0.0', '/plugins/arr', true, 'auto', 1)`,
		`INSERT INTO bloem_storage_installations (installation_id, owner_id) VALUES
		 (30, public.bloem_platform_resource_owner_id()), (36, public.bloem_platform_resource_owner_id())`,
		`INSERT INTO plugin_runtime_configs (plugin_installation_id, config_key, config_value) VALUES
		 (30, 'connection', '{"__silo_encrypted_runtime_config_v1": "ebooks-ciphertext"}'),
		 (36, 'connection', '{"__silo_encrypted_runtime_config_v1": "audio-ciphertext"}'),
		 (40, 'connection', '{"base_url": "http://arr.invalid"}')`,
		`INSERT INTO plugin_archives (plugin_installation_id, manifest_json, checksum, archive_bytes) VALUES
		 (30, '\x7b7d', 'ebooks-sum', '\x0102'), (36, '\x7b7d', 'audio-sum', '\x0304')`,
		`INSERT INTO bloem_storage_sources (key, installation_id, latest_installation_id, plugin_id, provider_source_id, root_entry_id, configuration_revision, enabled) VALUES
		 ('00000000-0000-4000-8000-000000000030', 30, 30, 'bloem.storage.bookwarehouse', 'bookwarehouse_ebooks', 'root', 3, true),
		 ('00000000-0000-4000-8000-000000000036', 36, 36, 'bloem.storage.bookwarehouse', 'bookwarehouse_audio', 'root', 5, true)`,
	)
	// Inserting the installations entitled the default organization to each.
	assertCount(t, db, `SELECT count(*) FROM organization_entitlements WHERE plugin_installation_id IN (30, 36) AND status = 'active'`, 2)

	if _, err := provider.UpTo(t.Context(), uniquePluginInstallationPerPlugin); err != nil {
		t.Fatal(err)
	}

	// Silo's table keeps only Silo's plugins, and its unique index exists.
	assertCount(t, db, `SELECT count(*) FROM plugin_installations WHERE id IN (30, 36)`, 0)
	assertCount(t, db, `SELECT count(*) FROM plugin_installations WHERE id = 40`, 1)
	assertCount(t, db, `SELECT count(*) FROM plugin_runtime_configs WHERE plugin_installation_id IN (30, 36)`, 0)
	assertCount(t, db, `SELECT count(*) FROM plugin_archives WHERE plugin_installation_id IN (30, 36)`, 0)
	assertCount(t, db, `SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_plugin_installations_plugin_id'`, 1)

	// Each instance kept its id, package, settings and generation.
	assertCount(t, db, `SELECT count(*) FROM bloem_storage_installations
		WHERE (installation_id, plugin_id, version, install_path, enabled, runtime_generation) IN
		((30, 'bloem.storage.bookwarehouse', '0.3.12', '/native/ebooks/plugin', true, 4),
		 (36, 'bloem.storage.bookwarehouse', '0.3.12', '/native/audio/plugin', true, 7))`, 2)
	assertCount(t, db, `SELECT count(*) FROM bloem_storage_installation_configs
		WHERE (installation_id, config_key, config_value) IN
		((30, 'connection', '{"__silo_encrypted_runtime_config_v1": "ebooks-ciphertext"}'::jsonb),
		 (36, 'connection', '{"__silo_encrypted_runtime_config_v1": "audio-ciphertext"}'::jsonb))`, 2)
	assertCount(t, db, `SELECT count(*) FROM bloem_storage_installation_archives
		WHERE (installation_id, checksum, archive_bytes) IN ((30, 'ebooks-sum', '\x0102'::bytea), (36, 'audio-sum', '\x0304'::bytea))`, 2)

	// The sources still name their instances.
	assertCount(t, db, `SELECT count(*) FROM bloem_storage_sources
		WHERE (key, installation_id) IN (('00000000-0000-4000-8000-000000000030'::uuid, 30), ('00000000-0000-4000-8000-000000000036'::uuid, 36))`, 2)

	// The default organization keeps its access, now on the instances.
	assertCount(t, db, `SELECT count(*) FROM organization_entitlements
		WHERE storage_installation_id IN (30, 36) AND plugin_installation_id IS NULL
		  AND root_kind = 'storage_installation' AND status = 'active'`, 2)

	// A new instance takes an id no Silo installation reaches, and the
	// default organization is entitled to it as before.
	var id int64
	if err := db.QueryRowContext(t.Context(), `INSERT INTO bloem_storage_installations (plugin_id, version, install_path)
		VALUES ('bloem.storage.s3', '1', '/native/s3/plugin') RETURNING installation_id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id < 1_000_000_000 {
		t.Fatalf("new instance id %d is in Silo's id range", id)
	}
	assertCount(t, db, `SELECT count(*) FROM organization_entitlements WHERE storage_installation_id = `+strconv.FormatInt(id, 10)+` AND status = 'active'`, 1)
	exec(t, db, `DELETE FROM organization_entitlements WHERE storage_installation_id = `+strconv.FormatInt(id, 10),
		`DELETE FROM bloem_storage_installations WHERE installation_id = `+strconv.FormatInt(id, 10))

	if _, err := provider.DownTo(t.Context(), beforeStorageInstallationsOwnRows); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT count(*) FROM plugin_installations WHERE id IN (30, 36) AND plugin_id = 'bloem.storage.bookwarehouse' AND runtime_generation IN (4, 7)`, 2)
	assertCount(t, db, `SELECT count(*) FROM plugin_runtime_configs WHERE plugin_installation_id IN (30, 36)`, 2)
	assertCount(t, db, `SELECT count(*) FROM plugin_archives WHERE plugin_installation_id IN (30, 36)`, 2)
	assertCount(t, db, `SELECT count(*) FROM organization_entitlements WHERE plugin_installation_id IN (30, 36) AND status = 'active'`, 2)
	assertCount(t, db, `SELECT count(*) FROM bloem_storage_sources WHERE installation_id IN (30, 36)`, 2)
}
