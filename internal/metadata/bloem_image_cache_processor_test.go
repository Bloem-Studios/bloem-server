package metadata

// Per-job image-cache timeout coverage. Silo bounds each job by
// ImageCacheJobTimeout itself; this test pins that a job's context carries
// that deadline, so one stalled download or upload cannot block a batch.

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

// bloemContextImageCacher records the context CacheImage receives.
type bloemContextImageCacher struct {
	result  *CacheImageResult
	lastCtx context.Context
}

func (f *bloemContextImageCacher) CacheImage(ctx context.Context, _ CacheImageRequest) (*CacheImageResult, error) {
	f.lastCtx = ctx
	return f.result, nil
}
