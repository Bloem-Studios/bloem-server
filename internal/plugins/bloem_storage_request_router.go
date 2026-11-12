package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/capability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// replaceNativeRequestRoutersTx publishes the request_router.v1 capabilities
// an approved native storage manifest declares, replacing the installation's
// earlier rows. The request lanes join plugin_capabilities to find a router;
// the ordinary plugin service keeps hiding native installations, so these rows
// are never served by its runtime.
func replaceNativeRequestRoutersTx(ctx context.Context, tx pgx.Tx, installationID int, manifest *publicv1.PluginManifest) error {
	records, err := CapabilityRecordsFromManifest(manifest)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM plugin_capabilities WHERE plugin_installation_id=$1`, installationID); err != nil {
		return err
	}
	for _, record := range records {
		if record.Type != capability.RequestRouter {
			return errors.New("native storage manifest declares a public capability other than request_router.v1")
		}
		metadata := record.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		data, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id,metadata) VALUES($1,$2,$3,$4)`, installationID, record.Type, record.ID, data); err != nil {
			return err
		}
	}
	return nil
}

// RequestRouterSource returns the live source of a native installation whose
// session serves its request routers. Snapshot still checks ownership,
// approval and generation before any process is used.
func (r *NativeStorageRegistry) RequestRouterSource(ctx context.Context, installationID int) (key, owner uuid.UUID, err error) {
	err = r.pool.QueryRow(ctx, `SELECT s.key,s.owner_id FROM bloem_storage_sources s JOIN bloem_storage_installations n ON n.installation_id=s.installation_id AND n.owner_id=s.owner_id
 WHERE s.installation_id=$1 AND s.enabled ORDER BY s.key LIMIT 1`, installationID).Scan(&key, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		err = storagesource.ErrSourceUnavailable
	}
	return key, owner, err
}

// NativeRequestRouterDescriptor reads the request_router.v1 descriptor a native
// installation published, without starting its process.
func (r *NativeStorageRegistry) NativeRequestRouterDescriptor(ctx context.Context, installationID int, capabilityID string) (*publicv1.RequestRouterDescriptor, error) {
	var metadata map[string]any
	err := r.pool.QueryRow(ctx, `SELECT c.metadata FROM plugin_capabilities c JOIN bloem_storage_installations n ON n.installation_id=c.plugin_installation_id
 WHERE c.plugin_installation_id=$1 AND c.capability_type=$2 AND c.capability_id=$3`, installationID, capability.RequestRouter, capabilityID).Scan(&metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("native installation %d declares no request router %q", installationID, capabilityID)
	}
	if err != nil {
		return nil, err
	}
	descriptor, err := DecodeCapability(&Capability{Type: capability.RequestRouter, ID: capabilityID, Metadata: metadata})
	if err != nil {
		return nil, err
	}
	if router := descriptor.GetRequestRouter(); router != nil {
		return router, nil
	}
	return &publicv1.RequestRouterDescriptor{}, nil
}
