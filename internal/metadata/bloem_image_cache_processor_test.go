package metadata

// Bloem per-job image-cache timeout coverage moved out of Silo's
// image_cache_processor_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

func TestImageCacheProcessorAppliesPerJobTimeout(t *testing.T) {
	jobs := &fakeImageCacheJobs{claimed: []*models.MetadataImageCacheJob{{
		ID:                5,
		TargetType:        ImageCacheTargetEpisode,
		TargetContentID:   "episode-tvdb-1-1-1",
		SourcePath:        "tvdb://banners/episode.jpg",
		ProviderID:        "tvdb",
		ProviderContentID: "1",
		ContentType:       "series",
		ImageType:         ImageCacheImageStill,
		SeasonNumber:      intPointer(1),
		EpisodeNumber:     intPointer(1),
	}}}
	cacher := &bloemContextImageCacher{result: &CacheImageResult{}}
	resolver := &fakeImageResolver{url: "https://artworks.thetvdb.com/banners/episode.jpg"}
	episodes := &fakeEpisodeStillUpdater{}

	processor := NewImageCacheProcessor(jobs, cacher, resolver, nil, episodes)
	// context.Background() never expires on its own, so seeing a deadline on
	// the context CacheImage received proves processClaimedJobs applied its
	// own per-job timeout rather than passing the parent through unbounded.
	if _, err := processor.RunOnce(context.Background(), "test-worker", 10, 1); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if cacher.lastCtx == nil {
		t.Fatal("CacheImage was never called")
	}
	deadline, ok := cacher.lastCtx.Deadline()
	if !ok {
		t.Fatal("per-job context has no deadline; one stalled download/upload can block the whole batch forever")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > ImageCacheJobTimeout {
		t.Fatalf("per-job deadline is %s from now, want (0, %s]", remaining, ImageCacheJobTimeout)
	}
}

func TestImageCacheProcessorStuckJobDoesNotBlockBatchForever(t *testing.T) {
	// Reproduces the production deadlock this fix closes: one job's CacheImage
	// call hangs indefinitely (e.g. a stalled upload). Without a per-job
	// timeout, processClaimedJobs' wg.Wait() never returns, which holds
	// runUntilIdle's single-execution gate forever — no future scheduler tick
	// can start a new run to reclaim the lease.
	stuckJob := &models.MetadataImageCacheJob{
		ID:                6,
		TargetType:        ImageCacheTargetEpisode,
		TargetContentID:   "episode-tvdb-1-1-1",
		SourcePath:        "tvdb://banners/stuck.jpg",
		ProviderID:        "tvdb",
		ProviderContentID: "1",
		ContentType:       "series",
		ImageType:         ImageCacheImageStill,
		SeasonNumber:      intPointer(1),
		EpisodeNumber:     intPointer(1),
	}
	jobs := &fakeImageCacheJobs{claimed: []*models.MetadataImageCacheJob{stuckJob}}
	cacher := &bloemContextImageCacher{block: make(chan struct{})} // never closed: only returns via ctx cancellation
	resolver := &fakeImageResolver{url: "https://artworks.thetvdb.com/banners/stuck.jpg"}
	episodes := &fakeEpisodeStillUpdater{}

	processor := NewImageCacheProcessor(jobs, cacher, resolver, nil, episodes)
	processor.jobTimeout = 20 * time.Millisecond

	done := make(chan struct{})
	var stats ImageCacheRunStats
	var runErr error
	go func() {
		stats, runErr = processor.RunOnce(context.Background(), "test-worker", 10, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not return; a stuck job blocked the batch forever")
	}
	if runErr != nil {
		t.Fatalf("RunOnce() error = %v", runErr)
	}
	if stats.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", stats.Failed)
	}
	if jobs.failedID != stuckJob.ID {
		t.Fatalf("failedID = %d, want %d", jobs.failedID, stuckJob.ID)
	}
}

// bloemContextImageCacher records the context CacheImage receives and can
// block until that context ends, standing in for a stalled download/upload.
type bloemContextImageCacher struct {
	result  *CacheImageResult
	lastCtx context.Context
	block   <-chan struct{}
}

func (f *bloemContextImageCacher) CacheImage(ctx context.Context, _ CacheImageRequest) (*CacheImageResult, error) {
	f.lastCtx = ctx
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.result, nil
}
