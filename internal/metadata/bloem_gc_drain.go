package metadata

// Bloem-owned. A one-shot drain for the artwork revision GC backlog.
//
// Run() issues one DeleteObjects call PER CANDIDATE. Measured against B2 that
// call costs ~2.9s whether it carries one key or ninety-nine -- the cost is per
// call, not per object -- so a 100-candidate run takes ~290s and the queue
// cannot drain. This deployment reached 6.7M candidates covering 9.26M objects.
//
// DrainArtworkRevisions does the same work with the S3 calls batched across the
// whole claim instead. It lives in this package so it reuses the GC's own
// claim, reference check and finalize helpers rather than reimplementing the
// reference rules -- getting those wrong deletes live artwork.
//
// Safety follows the design's existing assumption: objects may be deleted while
// a path races back into use, which is exactly what the pending-heal path
// already handles by clearing the surviving reference so it re-caches.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
)

// DrainStats reports one drain pass.
type DrainStats struct {
	Claimed    int
	Referenced int
	Deleted    int
	Objects    int
	Calls      int
}

// DrainArtworkRevisions claims up to batch candidates, deletes every object
// they own in as few DeleteObjects calls as the client allows, and finalizes
// the rows. It returns the number claimed so a caller can loop until zero.
func (g *ArtworkRevisionGarbageCollector) DrainArtworkRevisions(ctx context.Context, batch int) (DrainStats, error) {
	stats := DrainStats{}
	if g == nil || g.pool == nil || g.s3 == nil {
		return stats, fmt.Errorf("artwork revision GC is not configured")
	}
	workerID := uuid.NewString()
	candidates, err := g.claim(ctx, workerID, batch)
	if err != nil {
		return stats, err
	}
	stats.Claimed = len(candidates)
	if len(candidates) == 0 {
		return stats, nil
	}

	// Same batched reference pre-check the GC uses: anything still referenced
	// is parked, never deleted.
	due := candidates
	if referenced, refErr := g.referencedPaths(ctx, candidatePaths(candidates)); refErr == nil {
		var parked []int64
		due = due[:0]
		for _, candidate := range candidates {
			if _, ok := referenced[candidate.originalPath]; ok && candidate.deletedAt == nil {
				parked = append(parked, candidate.id)
				continue
			}
			due = append(due, candidate)
		}
		if len(parked) > 0 {
			if parkErr := g.parkClaimed(ctx, parked, workerID); parkErr != nil {
				return stats, parkErr
			}
			stats.Referenced = len(parked)
		}
	} else {
		return stats, fmt.Errorf("artwork revision GC drain: reference pre-check: %w", refErr)
	}
	if len(due) == 0 {
		return stats, nil
	}

	keys := make([]string, 0, len(due)*2)
	ids := make([]int64, 0, len(due))
	paths := make([]string, 0, len(due))
	for _, candidate := range due {
		objectKeys := candidate.objectKeys
		if len(objectKeys) == 0 {
			objectKeys = artworkkey.ObjectKeys(candidate.originalPath, candidate.imageType)
		}
		keys = append(keys, objectKeys...)
		ids = append(ids, candidate.id)
		paths = append(paths, candidate.originalPath)
	}
	stats.Objects = len(keys)

	if len(keys) > 0 {
		start := time.Now()
		deleted, delErr := g.s3.Delete(ctx, keys)
		if delErr != nil {
			return stats, fmt.Errorf("artwork revision GC drain: delete objects: %w", delErr)
		}
		// DeleteObjects can report per-object failures without a top-level
		// error. Finalizing on a short count would drop the rows while their
		// objects survive -- an untracked leak, and the candidates are the only
		// record that those objects exist. Stop instead; the rows keep their
		// lease and the next pass retries them.
		if deleted != len(keys) {
			return stats, fmt.Errorf(
				"artwork revision GC drain: deleted %d of %d objects; stopping before finalize",
				deleted, len(keys))
		}
		stats.Calls = (len(keys) + 999) / 1000
		slog.InfoContext(ctx, "artwork revision GC drain: deleted objects",
			"component", "metadata", "objects", deleted, "keys", len(keys),
			"candidates", len(due), "elapsed", time.Since(start).String())
	}

	// finalizePendingHeals only removes rows whose objects are already gone
	// (deleted_at IS NOT NULL), mirroring the normal path's delete-then-mark
	// order. Without this the objects would be deleted and every row left
	// behind, so the next pass would reclaim nothing.
	if _, markErr := g.pool.Exec(ctx, `
		UPDATE artwork_revision_gc_candidates
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = ANY($1) AND locked_by = $2 AND deleted_at IS NULL`, ids, workerID); markErr != nil {
		return stats, fmt.Errorf("artwork revision GC drain: mark deleted: %w", markErr)
	}

	finalized, _, finErr := g.finalizePendingHeals(ctx, ids, paths, workerID, true)
	if finErr != nil {
		return stats, finErr
	}
	stats.Deleted = len(finalized)
	return stats, nil
}
