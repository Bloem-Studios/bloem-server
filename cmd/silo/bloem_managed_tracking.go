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
