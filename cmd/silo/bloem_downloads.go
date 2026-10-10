package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// newBloemCompatDownloadServer shares the native service's policy,
// configuration and bandwidth limits with the Jellyfin listener.
func newBloemCompatDownloadServer(svc *downloads.Service, pool *pgxpool.Pool) jellycompat.DownloadServer {
	if svc == nil || pool == nil {
		return nil
	}
	store := tenancy.NewStore(pool)
	return jellycompat.NewProfileDownloadServer(svc, tenancy.NewSubjectResolver(tenancy.NewResolver(store), store))
}
