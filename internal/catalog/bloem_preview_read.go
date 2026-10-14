package catalog

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// ForPreview builds a request-local reader over the same authorized catalog.
// It intentionally constructs a fresh service rather than copying sync.Once.
func (s *DetailService) ForPreview(ctx context.Context) *DetailService {
	if s == nil || !previewcontext.Active(ctx) {
		return s
	}
	return &DetailService{
		itemRepo: s.itemRepo, episodeRepo: s.episodeRepo, seasonRepo: s.seasonRepo, personRepo: s.personRepo,
		itemLocRepo: s.itemLocRepo, seasonLocRepo: s.seasonLocRepo, episodeLocRepo: s.episodeLocRepo, folderRepo: s.folderRepo,
		fileFetcher: s.fileFetcher, videoRepo: s.videoRepo, ratingSourceRepo: s.ratingSourceRepo, extraRepo: s.extraRepo,
		rootClaimRepo: s.rootClaimRepo, groupClaimRepo: s.groupClaimRepo, imageResolver: s.imageResolver,
		workSummary: s.workSummary, workLinker: s.workLinker, originalLangFn: s.originalLangFn,
	}
}

type bloemPreviewSearch struct{ ordinary, keyword CatalogSearchProvider }

func BloemPreviewSearch(ordinary CatalogSearchProvider, repo *ItemRepository) CatalogSearchProvider {
	return &bloemPreviewSearch{ordinary: ordinary, keyword: NewPostgresSearchProvider(repo)}
}
func (s *bloemPreviewSearch) Search(ctx context.Context, req CatalogSearchRequest) (*CatalogSearchResult, error) {
	if previewcontext.Active(ctx) {
		return s.keyword.Search(ctx, req)
	}
	return s.ordinary.Search(ctx, req)
}

// ForPreview retains the original provider's concrete identity and capabilities
// for subscribers; previews receive a request-local keyword-only resolver.
func (r *CatalogResolver) ForPreview(ctx context.Context) *CatalogResolver {
	if r == nil || !previewcontext.Active(ctx) {
		return r
	}
	clone := *r
	clone.searchProvider = NewPostgresSearchProvider(r.itemRepo)
	clone.watchlistPromoter = nil
	return &clone
}
