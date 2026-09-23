package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// collectionSyncSchedulerLockKey guards CollectionSyncScheduler.RunOnce so
// only one replica actually syncs due collections on a given tick. RunOnce
// is invoked periodically by the taskmanager "sync_collections" task, whose
// interval trigger (internal/taskmanager/triggers) is a plain in-process
// timer with no cross-replica coordination of its own — every replica's
// TaskManager fires it independently. Without this lock, N replicas would
// all list the same due collections and race to sync (and hit) the same
// external metadata providers concurrently.
var collectionSyncSchedulerLockKey = pglock.Key("catalog.collection_sync_scheduler")

func (s *CollectionSyncScheduler) acquireLock(ctx context.Context) (*pglock.Lock, bool, error) {
	if s.tryLockFunc != nil {
		return s.tryLockFunc(ctx, collectionSyncSchedulerLockKey)
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, false, fmt.Errorf("collection sync scheduler: no database pool available")
	}
	return pglock.TryAcquire(ctx, s.repo.pool, collectionSyncSchedulerLockKey)
}

func (s *CollectionSyncScheduler) releaseLock(lock *pglock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Release(unlockCtx); err != nil {
		s.logger.ErrorContext(unlockCtx, "collection sync scheduler: failed to release advisory lock", "error", err)
	}
}
