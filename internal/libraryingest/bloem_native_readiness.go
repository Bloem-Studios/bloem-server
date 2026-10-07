package libraryingest

import (
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NativeStorageComposition requires the actual mode-aware executor's concrete
// native consumer and the SAME host, publisher, pool and durable folder reader.
func (e *Executor) NativeStorageComposition(pool *pgxpool.Pool, host *nativestorage.Host, folders *catalog.FolderRepository, publisher *scanner.Scanner) bool {
	if e == nil || pool == nil || host == nil || folders == nil || publisher == nil || e.folders != folders || e.scanner != publisher || folders.Pool() != pool || !publisher.NativePublicationReady(pool) {
		return false
	}
	c, ok := e.nativeIngestor.(*NativeConsumer)
	return ok && c != nil && c.host == host && c.scanner == publisher && c.sources.NativeStorageUsesPool(pool) && c.resources.NativeStorageUsesPool(pool) && host.NativeStorageReady(pool)
}
