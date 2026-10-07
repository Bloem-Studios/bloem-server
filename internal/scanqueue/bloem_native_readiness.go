package scanqueue

import (
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativeStorageComposition checks the existing queue, not a readiness flag or
// heartbeat. A stopped/cancelled queue cannot advertise future scan admission.
func (s *Service) NativeStorageComposition(pool *pgxpool.Pool, folders *catalog.FolderRepository, executor *libraryingest.Executor) bool {
	if s == nil || pool == nil || folders == nil || executor == nil || s.repo == nil || s.repo.pool != pool || s.folders != folders || s.ingester != executor || s.appCtx == nil || s.appCtx.Err() != nil || s.stop == nil {
		return false
	}
	select {
	case <-s.stop:
		return false
	default:
		return true
	}
}
