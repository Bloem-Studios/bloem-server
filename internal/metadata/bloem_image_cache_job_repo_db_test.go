package metadata

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestImageCacheClaimDuePrefersPosterFamilyJobs(t *testing.T) {
	pool := imageCacheQueueTestPool(t)
	ctx := context.Background()
	repo := NewImageCacheJobRepository(pool)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	stillContentID := "image-cache-priority-still-" + suffix
	posterContentID := "image-cache-priority-poster-" + suffix
	workerID := "priority-test-worker-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM metadata_image_cache_jobs WHERE target_content_id IN ($1, $2)`, stillContentID, posterContentID)
	})

	// Enqueue the low-priority episode still FIRST, so plain FIFO order would
	// claim it before the poster enqueued after it.
	seasonNumber, episodeNumber := 1, 1
	if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
		TargetType:      ImageCacheTargetEpisode,
		TargetContentID: stillContentID,
		SeriesID:        stillContentID,
		SourcePath:      "https://artworks.thetvdb.com/banners/priority-still.jpg",
		ImageType:       ImageCacheImageStill,
		ContentType:     "series",
		SeasonNumber:    &seasonNumber,
		EpisodeNumber:   &episodeNumber,
	}); err != nil {
		t.Fatalf("enqueue episode still: %v", err)
	}
	if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
		TargetType:      ImageCacheTargetItem,
		TargetContentID: posterContentID,
		SourcePath:      "https://image.tmdb.org/t/p/original/priority-poster.jpg",
		ImageType:       ImageCacheImagePoster,
		ContentType:     "series",
	}); err != nil {
		t.Fatalf("enqueue item poster: %v", err)
	}

	jobs, err := repo.ClaimDue(ctx, workerID, 1)
	if err != nil {
		t.Fatalf("ClaimDue() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}
	if jobs[0].TargetContentID != posterContentID {
		t.Fatalf("claimed target_content_id = %q, want the poster job %q enqueued after it; job_priority ordering did not take effect", jobs[0].TargetContentID, posterContentID)
	}
	if jobs[0].ImageType != ImageCacheImagePoster {
		t.Fatalf("claimed image_type = %q, want %q", jobs[0].ImageType, ImageCacheImagePoster)
	}
}
