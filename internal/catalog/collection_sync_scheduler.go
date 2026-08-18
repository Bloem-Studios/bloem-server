package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Silo-Server/silo-server/internal/dblock"
	"github.com/Silo-Server/silo-server/internal/models"
)

// collectionSyncSchedulerLockKey guards CollectionSyncScheduler.RunOnce so
// only one replica actually syncs due collections on a given tick. RunOnce
// is invoked periodically by the taskmanager "sync_collections" task, whose
// interval trigger (internal/taskmanager/triggers) is a plain in-process
// timer with no cross-replica coordination of its own — every replica's
// TaskManager fires it independently. Without this lock, N replicas would
// all list the same due collections and race to sync (and hit) the same
// external metadata providers concurrently.
var collectionSyncSchedulerLockKey = dblock.Key("catalog.collection_sync_scheduler")

// CollectionSyncScheduler finds collections due for automatic sync and
// processes them with bounded concurrency. It is driven by a TaskManager
// task on a short interval (e.g., every 5 minutes).
type CollectionSyncScheduler struct {
	repo    *LibraryCollectionRepository
	service *LibraryCollectionService
	logger  *slog.Logger

	// inFlight tracks collection IDs currently being synced to prevent
	// concurrent syncs of the same collection (manual vs scheduled).
	inFlight sync.Map

	// tryLockFunc overrides advisory-lock acquisition in tests. Nil in
	// production, where RunOnce falls back to dblock.TryLock.
	tryLockFunc func(ctx context.Context, key int64) (*dblock.Lock, bool, error)
}

// CollectionSyncResult is the JSON summary attached to the task execution.
type CollectionSyncResult struct {
	Due     int `json:"due"`
	Synced  int `json:"synced"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// NewCollectionSyncScheduler creates a new scheduler.
func NewCollectionSyncScheduler(
	repo *LibraryCollectionRepository,
	service *LibraryCollectionService,
	logger *slog.Logger,
) *CollectionSyncScheduler {
	return &CollectionSyncScheduler{
		repo:    repo,
		service: service,
		logger:  logger,
	}
}

// RunOnce queries for due collections and syncs them with bounded concurrency.
// It returns a JSON summary suitable for task result data.
//
// Guarded by a Postgres advisory lock (try-and-skip, not held across the
// whole run): on multiple replicas, only the replica that wins the lock for
// this tick actually lists and syncs due collections, so a redundant
// replica logs and returns an empty result instead of duplicating work and
// external provider calls.
func (s *CollectionSyncScheduler) RunOnce(ctx context.Context) (json.RawMessage, error) {
	lock, locked, err := s.acquireLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("collection sync scheduler: advisory lock: %w", err)
	}
	if !locked {
		s.logger.InfoContext(ctx, "collection sync scheduler: another replica holds the lock, skipping run")
		return marshalResult(CollectionSyncResult{}), nil
	}
	defer s.releaseLock(lock)

	due, err := s.repo.ListDueForSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing due collections: %w", err)
	}

	if len(due) == 0 {
		return marshalResult(CollectionSyncResult{}), nil
	}

	s.logger.InfoContext(ctx, "collection sync scheduler: starting",
		"due", len(due),
	)

	var (
		mu      sync.Mutex
		result  = CollectionSyncResult{Due: len(due)}
		g, gctx = errgroup.WithContext(ctx)
	)
	g.SetLimit(3)

	for _, collection := range due {
		collection := collection

		g.Go(func() error {
			s.syncOne(gctx, collection, &mu, &result)
			return nil // never propagate; failures are per-collection
		})
	}

	_ = g.Wait()

	s.logger.InfoContext(ctx, "collection sync scheduler: complete",
		"due", result.Due,
		"synced", result.Synced,
		"failed", result.Failed,
		"skipped", result.Skipped,
	)

	return marshalResult(result), nil
}

// syncOne syncs a single collection and advances its next_sync_at.
func (s *CollectionSyncScheduler) syncOne(ctx context.Context, collection *models.LibraryCollection, mu *sync.Mutex, result *CollectionSyncResult) {
	// Guard against concurrent sync of the same collection (e.g., manual trigger).
	if _, loaded := s.inFlight.LoadOrStore(collection.ID, struct{}{}); loaded {
		s.logger.InfoContext(ctx, "collection sync scheduler: skipping (already in flight)",
			"collection_id", collection.ID,
			"title", collection.Title,
		)
		mu.Lock()
		result.Skipped++
		mu.Unlock()
		return
	}
	defer s.inFlight.Delete(collection.ID)

	startedAt := time.Now()

	_, syncErr := s.service.SyncCollection(ctx, collection.ID)

	completedAt := time.Now()

	// Always advance next_sync_at, even on failure, so we retry on the
	// natural cron schedule rather than every poll interval.
	if collection.SyncSchedule != nil {
		next := ComputeNextSyncAtFrom(*collection.SyncSchedule, completedAt)
		if err := s.repo.UpdateNextSyncAt(ctx, collection.ID, next); err != nil {
			s.logger.ErrorContext(ctx, "collection sync scheduler: failed to advance schedule",
				"collection_id", collection.ID,
				"error", err,
			)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if syncErr != nil {
		result.Failed++
		s.logger.ErrorContext(ctx, "collection sync scheduler: sync failed",
			"collection_id", collection.ID,
			"title", collection.Title,
			"duration", completedAt.Sub(startedAt).Round(time.Millisecond),
			"error", syncErr,
		)
	} else {
		result.Synced++
		s.logger.InfoContext(ctx, "collection sync scheduler: synced",
			"collection_id", collection.ID,
			"title", collection.Title,
			"duration", completedAt.Sub(startedAt).Round(time.Millisecond),
		)
	}
}

// IsInFlight returns true if the given collection is currently being synced
// by the scheduler. Used by the manual sync handler to avoid overlap.
func (s *CollectionSyncScheduler) IsInFlight(collectionID string) bool {
	_, ok := s.inFlight.Load(collectionID)
	return ok
}

func (s *CollectionSyncScheduler) acquireLock(ctx context.Context) (*dblock.Lock, bool, error) {
	if s.tryLockFunc != nil {
		return s.tryLockFunc(ctx, collectionSyncSchedulerLockKey)
	}
	if s.repo == nil || s.repo.pool == nil {
		return nil, false, fmt.Errorf("collection sync scheduler: no database pool available")
	}
	return dblock.TryLock(ctx, s.repo.pool, collectionSyncSchedulerLockKey)
}

func (s *CollectionSyncScheduler) releaseLock(lock *dblock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Unlock(unlockCtx); err != nil {
		s.logger.ErrorContext(unlockCtx, "collection sync scheduler: failed to release advisory lock", "error", err)
	}
}

func marshalResult(r CollectionSyncResult) json.RawMessage {
	data, _ := json.Marshal(r)
	return data
}
