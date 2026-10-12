package main

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

// The wrapper leaves every ordinary provider on its existing client path.
type managedWatchSyncService struct {
	watchSyncPluginService
	managed *managedtracking.Service
}

func (s managedWatchSyncService) WrapWatchSyncProvider(installation int, plugin string, provider *watchsync.PluginProvider) watchsync.Provider {
	if plugin == "bloem.pastime" && s.managed != nil {
		return s.managed.WrapProvider(installation, provider, func(ctx context.Context) (watchsync.WatchSyncPluginClient, error) {
			client, err := s.WatchSyncProviderClient(ctx, installation, "pastime")
			if err != nil {
				return nil, err
			}
			return s.managed.WrapClient(installation, client), nil
		})
	}
	return provider
}

func (s managedWatchSyncService) WrapWatchSyncClient(ctx context.Context, installation int, capability string, client watchsync.WatchSyncPluginClient) (watchsync.WatchSyncPluginClient, error) {
	if capability == "pastime" && s.managed != nil {
		var plugin string
		if err := s.managed.Pool.QueryRow(ctx, `SELECT plugin_id FROM plugin_installations WHERE id=$1`, installation).Scan(&plugin); err != nil {
			return nil, err
		}
		if plugin == "bloem.pastime" {
			return s.managed.WrapClient(installation, client), nil
		}
	}
	return client, nil
}
