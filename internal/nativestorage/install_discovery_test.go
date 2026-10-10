package nativestorage

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
)

func TestInitialRootDiscoveryRequiresMatchingHost(t *testing.T) {
	management := NewSourceManagement(nil, nil)
	if management.InitialRootDiscoveryReady() {
		t.Fatal("unconfigured discovery advertised")
	}
	management.SetInstallDiscoveryHost(&Host{Manager: storageplugin.NewManager(storageplugin.Config{})})
	if management.InitialRootDiscoveryReady() {
		t.Fatal("missing registry advertised discovery")
	}
	registry := &plugins.NativeStorageRegistry{}
	management.registry = registry
	management.SetInstallDiscoveryHost(&Host{Registry: &plugins.NativeStorageRegistry{}, Manager: storageplugin.NewManager(storageplugin.Config{})})
	if management.InitialRootDiscoveryReady() {
		t.Fatal("foreign registry advertised discovery")
	}
	_, err := management.DiscoverInstall(context.Background(), auth.AdminContextClaims{}, DiscoverInstallCommand{})
	if err == nil {
		t.Fatal("unconfigured discovery accepted")
	}
}
