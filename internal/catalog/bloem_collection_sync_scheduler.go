package catalog

import (
	"context"
	"encoding/json"
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

// bloemCollectionSyncLock is embedded in CollectionSyncScheduler to carry
// Bloem's advisory-lock test seam.
type bloemCollectionSyncLock struct {
	// tryLockFunc overrides advisory-lock acquisition in tests. Nil in
	// production, where RunOnce falls back to pglock.TryAcquire.
	tryLockFunc func(ctx context.Context, key int64) (*pglock.Lock, bool, error)
}

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

// lockRun guards RunOnce with a Postgres advisory lock (try-and-skip, not
// held across the whole run): on multiple replicas, only the replica that
// wins the lock for this tick actually lists and syncs due collections, so a
// redundant replica logs and returns an empty result instead of duplicating
// work and external provider calls. A nil release means RunOnce returns the
// accompanying result and error.
func (s *CollectionSyncScheduler) lockRun(ctx context.Context) (func(), json.RawMessage, error) {
	lock, locked, err := s.acquireLock(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("collection sync scheduler: advisory lock: %w", err)
	}
	if !locked {
		s.logger.InfoContext(ctx, "collection sync scheduler: another replica holds the lock, skipping run")
		return nil, marshalResult(CollectionSyncResult{}), nil
	}
	return func() { s.releaseLock(lock) }, nil, nil
}
