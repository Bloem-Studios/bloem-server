package plugins

import (
	"context"
	"errors"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const nativeS3PluginID = "bloem.storage.s3"

// State paths are runtime inputs from the host, never user-owned configuration.
// Other providers retain their own configuration contracts.
func validateNativeS3UserConfig(pluginID string, config map[string]map[string]any) error {
	if pluginID != nativeS3PluginID {
		return nil
	}
	connection, ok := config["connection"]
	if !ok || len(config) != 1 || connection == nil {
		return &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	if _, supplied := connection["state_path"]; supplied {
		return &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return nil
}

// Both Snapshot and Upgrade prepare runtime configuration from their retained
// transaction view. No derived host path is written back to the config store.
func (r *NativeStorageRegistry) nativeRuntimeConfigTx(ctx context.Context, tx pgx.Tx, installation *Installation) ([]*publicv1.ConfigEntry, error) {
	rows, err := tx.Query(ctx, `SELECT config_key,config_value FROM plugin_runtime_configs WHERE plugin_installation_id=$1 ORDER BY config_key`, installation.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]*publicv1.ConfigEntry, 0)
	for rows.Next() {
		var key string
		var data []byte
		if err = rows.Scan(&key, &data); err != nil {
			return nil, err
		}
		value, err := decodeRuntimeConfigValue(r.configs.cipher, installation.ID, key, data)
		if err != nil {
			return nil, err
		}
		v, err := structpb.NewStruct(value)
		if err != nil {
			return nil, err
		}
		entries = append(entries, &publicv1.ConfigEntry{Key: key, Value: v})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return r.nativeRuntimeConfig(installation, entries)
}

func (r *NativeStorageRegistry) nativeRuntimeConfig(installation *Installation, entries []*publicv1.ConfigEntry) ([]*publicv1.ConfigEntry, error) {
	if installation.PluginID != nativeS3PluginID {
		return entries, nil
	}
	if len(entries) != 1 || entries[0].GetKey() != "connection" || entries[0].GetValue() == nil {
		return nil, errors.New("invalid S3 runtime configuration shape")
	}
	if _, supplied := entries[0].Value.Fields["state_path"]; supplied {
		return nil, errors.New("S3 state path must be host-derived")
	}
	path, err := nativeS3StatePath(r.baseDir, installation.ID)
	if err != nil {
		return nil, err
	}
	entry := proto.CloneOf(entries[0])
	if entry.Value.Fields == nil {
		entry.Value.Fields = map[string]*structpb.Value{}
	}
	entry.Value.Fields["state_path"] = structpb.NewStringValue(path)
	return []*publicv1.ConfigEntry{entry}, nil
}
