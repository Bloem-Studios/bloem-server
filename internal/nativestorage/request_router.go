package nativestorage

import (
	"context"
	"fmt"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// IsNativeInstallation reports whether installationID is a native storage
// installation, whose request routers only its storage session serves.
func (h *Host) IsNativeInstallation(ctx context.Context, installationID int) (bool, error) {
	if h == nil || h.Registry == nil {
		return false, nil
	}
	ids, err := h.Registry.NativeStorageIDs(ctx, []int{installationID})
	if err != nil {
		return false, err
	}
	return ids[installationID], nil
}

// NativeRequestRouter returns the request_router.v1 client of a native
// installation's storage session. The session is the one storage reads use:
// the same verified executable, configuration and generation, started on
// demand from an authorized snapshot.
func (h *Host) NativeRequestRouter(ctx context.Context, installationID int, capabilityID string) (publicv1.RequestRouterClient, error) {
	if h == nil || h.Registry == nil || h.Manager == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	key, owner, err := h.Registry.RequestRouterSource(ctx, installationID)
	if err != nil {
		return nil, err
	}
	snapshot, err := h.Registry.Snapshot(ctx, key, owner)
	if err != nil {
		return nil, err
	}
	if snapshot.Installation == nil || snapshot.Installation.ID != installationID {
		return nil, storagesource.ErrSourceUnavailable
	}
	if !storageplugin.DeclaresRequestRouter(snapshot.Manifest, capabilityID) {
		return nil, fmt.Errorf("native installation %d declares no request router %q", installationID, capabilityID)
	}
	session, err := h.Manager.Ensure(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return nil, err
	}
	router, ok := session.RequestRouter(capabilityID)
	if !ok {
		return nil, fmt.Errorf("native installation %d declares no request router %q", installationID, capabilityID)
	}
	return router, nil
}

// NativeRequestRouterDescriptor reads a native request router's declared
// features without starting the provider.
func (h *Host) NativeRequestRouterDescriptor(ctx context.Context, installationID int, capabilityID string) (*publicv1.RequestRouterDescriptor, error) {
	if h == nil || h.Registry == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	return h.Registry.NativeRequestRouterDescriptor(ctx, installationID, capabilityID)
}
