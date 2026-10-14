package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

func (h *ItemsHandler) previewReader(ctx context.Context) *ItemsHandler {
	if h == nil || !previewcontext.Active(ctx) {
		return h
	}
	clone := *h
	clone.detailSvc = h.detailSvc.ForPreview(ctx)
	clone.catalogResolver = h.catalogResolver.ForPreview(ctx)
	clone.storeProvider = nil
	clone.MarkerPopulation = nil
	clone.MarkerImageURLs = nil
	clone.ebookProgressStore = nil
	clone.ebookReadStateStore = nil
	return &clone
}
func (h *CatalogResourceHandler) previewReader(ctx context.Context) *CatalogResourceHandler {
	if h == nil || !previewcontext.Active(ctx) {
		return h
	}
	return &CatalogResourceHandler{items: h.items.previewReader(ctx)}
}

type BloemPreviewCatalog struct{ *CatalogResourceHandler }

func (h BloemPreviewCatalog) ItemDetail(ctx context.Context, v ItemViewer, id string) (*catalog.ItemDetail, error) {
	return h.previewReader(ctx).ItemDetail(ctx, v, id)
}
func (h BloemPreviewCatalog) ItemVersions(ctx context.Context, v ItemViewer, id string) ([]catalog.FileVersion, error) {
	return h.previewReader(ctx).ItemVersions(ctx, v, id)
}
func (h BloemPreviewCatalog) MangaFiles(ctx context.Context, v ItemViewer, id string) (*catalog.MangaSeriesFiles, error) {
	return h.previewReader(ctx).MangaFiles(ctx, v, id)
}
func (h BloemPreviewCatalog) ItemEpisodes(ctx context.Context, v ItemViewer, id string) ([]EpisodeView, error) {
	return h.previewReader(ctx).ItemEpisodes(ctx, v, id)
}
func (h BloemPreviewCatalog) SeriesSeasons(ctx context.Context, v ItemViewer, id string, includeArtwork bool) ([]SeasonView, error) {
	return h.previewReader(ctx).SeriesSeasons(ctx, v, id, includeArtwork)
}
func (h BloemPreviewCatalog) SeriesSeason(ctx context.Context, v ItemViewer, id string, num int) (SeasonView, error) {
	return h.previewReader(ctx).SeriesSeason(ctx, v, id, num)
}
func (h BloemPreviewCatalog) SeasonEpisodes(ctx context.Context, v ItemViewer, id string, num int) ([]EpisodeView, error) {
	return h.previewReader(ctx).SeasonEpisodes(ctx, v, id, num)
}

// PreviewReadOnly constructs a watch-document reader without personal state or markers.
func (r *CatalogWatchReader) PreviewReadOnly() *CatalogWatchReader {
	clone := *r
	clone.markers = nil
	clone.stores = nil
	return &clone
}

func (h *CatalogHandler) previewReader(ctx context.Context) *CatalogHandler {
	if h == nil || !previewcontext.Active(ctx) {
		return h
	}
	clone := *h
	clone.resolver = h.resolver.ForPreview(ctx)
	clone.itemsH = h.itemsH.previewReader(ctx)
	return &clone
}

type BloemPreviewBrowse struct{ *CatalogHandler }

func (h BloemPreviewBrowse) Browse(ctx context.Context, v ItemViewer, req catalog.CatalogRequest, grouped bool) (CatalogBrowseView, error) {
	return h.previewReader(ctx).Browse(ctx, v, req, grouped)
}
func (h BloemPreviewBrowse) Filters(ctx context.Context, v ItemViewer, req catalog.CatalogRequest, technical bool) (CatalogFiltersView, error) {
	return h.previewReader(ctx).Filters(ctx, v, req, technical)
}
func (h BloemPreviewBrowse) SearchFacet(ctx context.Context, v ItemViewer, req catalog.CatalogRequest, facet, q string, limit int) (CatalogFacetSearchView, error) {
	return h.previewReader(ctx).SearchFacet(ctx, v, req, facet, q, limit)
}
func (h BloemPreviewBrowse) AudiobookGroups(ctx context.Context, v ItemViewer, q catalog.AudiobookGroupsQuery) (AudiobookGroupsView, error) {
	return h.previewReader(ctx).AudiobookGroups(ctx, v, q)
}
func (h BloemPreviewBrowse) SearchContinuationCapabilities(ctx context.Context) (catalog.SearchContinuationCapabilities, error) {
	return h.previewReader(ctx).SearchContinuationCapabilities(ctx)
}
