package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/plugins"
)

// pluginInstallations is the installation store the plugin handlers use.
type pluginInstallations interface {
	GetByID(ctx context.Context, id int) (*plugins.Installation, error)
	List(ctx context.Context) ([]*plugins.Installation, error)
	ListEnabled(ctx context.Context) ([]*plugins.Installation, error)
	ListCapabilities(ctx context.Context, installationID int) ([]*plugins.Capability, error)
	Update(ctx context.Context, id int, input plugins.UpdateInstallationInput) error
	Delete(ctx context.Context, id int) error
}

// HideNativeStorage keeps native storage installations out of the ordinary
// plugin handlers. They have no plugin manifest or settings of their own;
// without this, listing plugin settings failed on their manifest.
func (h *PluginHandler) HideNativeStorage(installations *plugins.InstallationStore, registry plugins.NativeStorageIsolationRegistry) {
	if h != nil && installations != nil && registry != nil {
		h.installations = plugins.NewNativeStorageHiddenInstallations(installations, registry)
	}
}
