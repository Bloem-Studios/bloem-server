package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

type previewQueue struct{ calls int }

func (q *previewQueue) QueueFileIDs(context.Context, []int)                       { q.calls++ }
func (q *previewQueue) QueuePriorityFileIDs(context.Context, []int)               { q.calls++ }
func (q *previewQueue) QueuePriorityFileAtPosition(context.Context, int, float64) { q.calls++ }
func TestPreviewDetailDoesNotProcessFiles(t *testing.T) {
	probe, race, queue := &recordingProbeEnsurer{}, &recordingCopySafetyRacer{}, &previewQueue{}
	original := &DetailService{probeEnsurer: probe, copySafetyRacer: race, chapterThumbs: queue}
	ctx := previewcontext.With(t.Context())
	reader := original.ForPreview(ctx)
	files := []*models.MediaFile{h264File(7, nil)}
	reader.prepareBrowseFiles(ctx, files)
	reader.preparePlaybackFiles(ctx, files)
	reader.queueWatchPlaybackFiles(ctx, "movie:1", "movie", files)
	if len(probe.probeCalls)+len(probe.cachedCalls)+len(race.raced)+queue.calls != 0 {
		t.Fatal("preview processed files")
	}
	if original.ForPreview(t.Context()) != original {
		t.Fatal("subscriber service replaced")
	}
	original.preparePlaybackFiles(t.Context(), files)
	original.queueWatchPlaybackFiles(t.Context(), "movie:1", "movie", files)
	if len(probe.cachedCalls) != 1 || len(race.raced) != 1 || queue.calls != 1 {
		t.Fatal("subscriber file work changed")
	}
}

type previewSearchRecorder struct{ calls int }

func (s *previewSearchRecorder) Search(context.Context, CatalogSearchRequest) (*CatalogSearchResult, error) {
	s.calls++
	return &CatalogSearchResult{Mode: "hybrid"}, nil
}
func TestPreviewSearchNeverCallsConfiguredProvider(t *testing.T) {
	original, keyword := &previewSearchRecorder{}, &previewSearchRecorder{}
	p := &bloemPreviewSearch{ordinary: original, keyword: keyword}
	if _, err := p.Search(previewcontext.With(t.Context()), CatalogSearchRequest{Query: "a story"}); err != nil {
		t.Fatal(err)
	}
	if original.calls != 0 || keyword.calls != 1 {
		t.Fatal("preview reached configured provider")
	}
	if _, err := p.Search(t.Context(), CatalogSearchRequest{Query: "a story"}); err != nil {
		t.Fatal(err)
	}
	if original.calls != 1 || keyword.calls != 1 {
		t.Fatal("subscriber provider changed")
	}
}

type previewPromotionRecorder struct{ calls int }

func (p *previewPromotionRecorder) PromoteWatchlist(context.Context, AccessFilter) { p.calls++ }
func TestPreviewCatalogueKeepsSubscriberProviderAndSuppressesPromotion(t *testing.T) {
	promotion := &previewPromotionRecorder{}
	original := NewCatalogResolver(nil, nil).WithSearchProvider(&MeilisearchSearchProvider{}).WithWatchlistPromoter(promotion)
	ctx := previewcontext.With(t.Context())
	reader := original.ForPreview(ctx)
	capabilities, err := reader.SearchContinuationCapabilities(ctx)
	if err != nil || capabilities.Provider != SearchProviderPostgres {
		t.Fatalf("preview capabilities: %+v %v", capabilities, err)
	}
	_, _ = reader.resolvePersonalSource(ctx, CatalogRequest{Source: CatalogSourceWatchlist}, AccessFilter{})
	if promotion.calls != 0 {
		t.Fatal("preview query promoted watchlist")
	}
	capabilities, err = original.SearchContinuationCapabilities(t.Context())
	if capabilities.Provider != SearchProviderMeilisearch || !errors.Is(err, ErrSearchContinuationUnavailable) {
		t.Fatalf("subscriber provider changed: %+v %v", capabilities, err)
	}
	_, _ = original.resolvePersonalSource(t.Context(), CatalogRequest{Source: CatalogSourceWatchlist}, AccessFilter{})
	if promotion.calls != 1 {
		t.Fatal("subscriber promotion changed")
	}
}
