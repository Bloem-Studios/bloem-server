package metadata

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func imageCacheQueueTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// parkFailedImageCacheJob forces a job into the terminal state a spent attempt
// budget or a permanent tombstone would leave behind, so the re-admission gate
// can be exercised without waiting out a real backoff.
func parkFailedImageCacheJob(t *testing.T, pool *pgxpool.Pool, contentID string, park time.Duration) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `
		UPDATE metadata_image_cache_jobs
		SET status = 'failed',
			attempt_count = $2,
			next_attempt_at = NOW() + $3::interval,
			last_error = 'test parked failure'
		WHERE target_type = 'item'
		  AND target_content_id = $1
		  AND image_type = 'poster'
		  AND target_language = ''
	`, contentID, imageCacheMaxAttempts, intervalLiteral(park))
	if err != nil {
		t.Fatalf("park failed job: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("park failed job affected %d rows, want 1", tag.RowsAffected())
	}
}

func readImageCacheJobState(t *testing.T, pool *pgxpool.Pool, contentID string) (string, int) {
	t.Helper()
	var status string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempt_count
		FROM metadata_image_cache_jobs
		WHERE target_type = 'item'
		  AND target_content_id = $1
		  AND image_type = 'poster'
		  AND target_language = ''
	`, contentID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read job state: %v", err)
	}
	return status, attempts
}

// TestImageCacheFailedJobReadmission covers the re-admission gate shared by the
// enqueue upsert and catalog discovery: a job parked past the recovery window is
// a tombstone and stays put for an unchanged source, while a job that merely ran
// out of attempts comes back once its cooldown has elapsed.
func TestImageCacheFailedJobReadmission(t *testing.T) {
	pool := imageCacheQueueTestPool(t)
	ctx := context.Background()
	repo := NewImageCacheJobRepository(pool)

	newJob := func(t *testing.T, sourcePath string) string {
		t.Helper()
		contentID := fmt.Sprintf("image-cache-readmit-%d", time.Now().UnixNano())
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM metadata_image_cache_jobs WHERE target_content_id = $1`, contentID)
		})
		if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
			TargetType:      ImageCacheTargetItem,
			TargetContentID: contentID,
			SourcePath:      sourcePath,
			ImageType:       ImageCacheImagePoster,
			ContentType:     "movie",
		}); err != nil {
			t.Fatalf("enqueue job: %v", err)
		}
		return contentID
	}

	const sourcePath = "https://image.tmdb.org/t/p/original/readmit.jpg"

	t.Run("attempt exhausted job revives after its cooldown", func(t *testing.T) {
		contentID := newJob(t, sourcePath)
		parkFailedImageCacheJob(t, pool, contentID, -time.Minute)

		if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
			TargetType:      ImageCacheTargetItem,
			TargetContentID: contentID,
			SourcePath:      sourcePath,
			ImageType:       ImageCacheImagePoster,
			ContentType:     "movie",
		}); err != nil {
			t.Fatalf("re-enqueue job: %v", err)
		}

		status, attempts := readImageCacheJobState(t, pool, contentID)
		if status != ImageCacheStatusQueued {
			t.Fatalf("status = %q, want %q: an attempt-exhausted job must be recoverable", status, ImageCacheStatusQueued)
		}
		if attempts != 0 {
			t.Fatalf("attempt_count = %d, want 0", attempts)
		}
	})

	t.Run("tombstoned job stays parked for an unchanged source", func(t *testing.T) {
		contentID := newJob(t, sourcePath)
		parkFailedImageCacheJob(t, pool, contentID, imageCachePermanentPark)

		if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
			TargetType:      ImageCacheTargetItem,
			TargetContentID: contentID,
			SourcePath:      sourcePath,
			ImageType:       ImageCacheImagePoster,
			ContentType:     "movie",
		}); err != nil {
			t.Fatalf("re-enqueue job: %v", err)
		}

		status, attempts := readImageCacheJobState(t, pool, contentID)
		if status != ImageCacheStatusFailed {
			t.Fatalf("status = %q, want %q: a tombstoned job must not be retried", status, ImageCacheStatusFailed)
		}
		if attempts != imageCacheMaxAttempts {
			t.Fatalf("attempt_count = %d, want %d", attempts, imageCacheMaxAttempts)
		}
	})

	t.Run("tombstoned job revives when the source changes", func(t *testing.T) {
		contentID := newJob(t, sourcePath)
		parkFailedImageCacheJob(t, pool, contentID, imageCachePermanentPark)

		if err := repo.Enqueue(ctx, EnqueueImageCacheJobInput{
			TargetType:      ImageCacheTargetItem,
			TargetContentID: contentID,
			SourcePath:      "https://image.tmdb.org/t/p/original/replacement.jpg",
			ImageType:       ImageCacheImagePoster,
			ContentType:     "movie",
		}); err != nil {
			t.Fatalf("re-enqueue job with new source: %v", err)
		}

		status, attempts := readImageCacheJobState(t, pool, contentID)
		if status != ImageCacheStatusQueued {
			t.Fatalf("status = %q, want %q: a new source must clear the tombstone", status, ImageCacheStatusQueued)
		}
		if attempts != 0 {
			t.Fatalf("attempt_count = %d, want 0", attempts)
		}
	})
}

// TestImageCacheClaimDuePrefersPosterFamilyJobs covers the priority column
// added for viewer-visible artwork: item/season poster, backdrop, and logo
// jobs must claim ahead of episode stills queued earlier, not just in
// enqueue order. Reproduces the production symptom this fixes — TV series
// posters barely progressing because they sat FIFO-behind a much larger
// episode-still backlog.
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
