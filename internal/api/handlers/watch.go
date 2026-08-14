package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/watchdoc"
)

// watchHomeItemLimit bounds the home document. The document is the first frame
// of a television home screen, so it is a window on the library rather than the
// whole of it: the most recently added items, capped at the browse
// repository's own default page size.
const watchHomeItemLimit = 100

// secondsPerMinute converts the catalog's minute-grained runtimes to the
// contract's seconds.
const secondsPerMinute = 60

// watchRecentProgressRows bounds the second progress read: the profile's most
// recently updated rows, which is where an episode of a listed series that the
// document did not name is found. It is a bound on one viewer's own progress
// list, not on the library.
const watchRecentProgressRows = 200

// watchRecentlyAddedSort is the browse repository's added-time ordering:
// first_seen_at descending, content identifier ascending. The document's own
// order is the same total order, so the two never disagree.
const watchRecentlyAddedSort = "recently_added"

// WatchHandler serves the contracts' watch_document_v1 documents.
//
// Both endpoints are profile-scoped: the document names what one profile may
// watch and carries that profile's progress, so a request without a profile is
// refused rather than answered for the account.
type WatchHandler struct {
	reader watchdoc.Reader
}

// NewWatchHandler creates a WatchHandler over a composition reader. A nil
// reader leaves the endpoints mounted but unavailable, which is honest about a
// deployment with no catalog rather than pretending the library is empty.
func NewWatchHandler(reader watchdoc.Reader) *WatchHandler {
	return &WatchHandler{reader: reader}
}

// HandleWatchHome handles GET /watch/home.
func (h *WatchHandler) HandleWatchHome(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.requestScope(w, r)
	if !ok {
		return
	}
	document, err := watchdoc.ComposeHome(r.Context(), h.reader, scope)
	if err != nil {
		slog.ErrorContext(r.Context(), "composing watch home document failed",
			"component", "api", "profile_id", scope.ProfileID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to compose the Watch home document")
		return
	}
	writeWatchDocument(w, document)
}

// HandleWatchItem handles GET /watch/items/{content_id}.
func (h *WatchHandler) HandleWatchItem(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.requestScope(w, r)
	if !ok {
		return
	}
	contentID := strings.TrimSpace(chi.URLParam(r, "content_id"))
	if contentID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "content_id is required")
		return
	}
	document, err := watchdoc.ComposeItem(r.Context(), h.reader, scope, contentID)
	switch {
	case errors.Is(err, watchdoc.ErrItemNotFound):
		// An item the profile may not see is answered exactly like an item
		// that does not exist, so a restricted profile cannot probe for it.
		writeError(w, http.StatusNotFound, "not_found", "Item not found")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "composing watch item document failed",
			"component", "api", "profile_id", scope.ProfileID, "content_id", contentID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to compose the Watch item document")
		return
	}
	writeWatchDocument(w, document)
}

// requestScope resolves the viewer scope, refusing the request when it cannot.
func (h *WatchHandler) requestScope(w http.ResponseWriter, r *http.Request) (watchdoc.ProfileScope, bool) {
	if h.reader == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Watch documents are not available")
		return watchdoc.ProfileScope{}, false
	}
	profileID := strings.TrimSpace(apimw.GetProfileID(r.Context()))
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "profile_required", "A profile is required for Watch documents")
		return watchdoc.ProfileScope{}, false
	}
	scope := watchdoc.ProfileScope{
		UserID:    apimw.GetUserID(r.Context()),
		ProfileID: profileID,
	}
	if viewer, ok := access.GetScope(r.Context()); ok {
		scope.AllowedLibraryIDs = viewer.AllowedLibraryIDs
		scope.DisabledLibraryIDs = viewer.DisabledLibraryIDs
		scope.MaxContentRating = viewer.MaxContentRating
		if viewer.UserID != 0 {
			scope.UserID = viewer.UserID
		}
	}
	return scope, true
}

func writeWatchDocument(w http.ResponseWriter, document watchdoc.Document) {
	// The document carries one profile's library view and progress: it is
	// never shared or revalidated by an intermediary.
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, document)
}

// --- catalog-backed reader -------------------------------------------------

// WatchBrowseSource lists the movies and series a profile may see.
type WatchBrowseSource interface {
	BrowsePage(ctx context.Context, filters catalog.BrowseFilters, includeTotal bool) (*catalog.BrowseResult, error)
}

// WatchItemSource resolves single items and re-checks access.
type WatchItemSource interface {
	GetByIDsWithAccess(ctx context.Context, contentIDs []string, filter catalog.AccessFilter) ([]*models.MediaItem, error)
	EnsureAccessibleIDs(ctx context.Context, contentIDs []string, filter catalog.AccessFilter) (map[string]bool, error)
}

// WatchEpisodeSource lists a series' episodes and resolves episodes by
// identifier.
type WatchEpisodeSource interface {
	ListBySeries(ctx context.Context, seriesID string) ([]*models.Episode, error)
	GetByIDs(ctx context.Context, contentIDs []string) ([]*models.Episode, error)
}

// WatchSeasonSource supplies season titles for a series' seasons.
type WatchSeasonSource interface {
	ListBySeries(ctx context.Context, seriesID string) ([]*models.Season, error)
}

// WatchFileSource resolves playable media files.
type WatchFileSource interface {
	ListByContentIDs(ctx context.Context, contentIDs []string) (map[string][]*models.MediaFile, error)
	ListByEpisodeIDs(ctx context.Context, episodeIDs []string) (map[string][]*models.MediaFile, error)
}

// CatalogWatchReader is the database-backed watchdoc.Reader: the one place the
// Watch join between the catalog, episode, media-file and progress stores
// lives.
type CatalogWatchReader struct {
	browse   WatchBrowseSource
	items    WatchItemSource
	episodes WatchEpisodeSource
	seasons  WatchSeasonSource
	files    WatchFileSource
	stores   userstore.UserStoreProvider
}

// NewCatalogWatchReader wires a reader over the existing repositories. seasons
// may be nil: season titles are then omitted rather than the document failing.
func NewCatalogWatchReader(
	browse WatchBrowseSource,
	items WatchItemSource,
	episodes WatchEpisodeSource,
	seasons WatchSeasonSource,
	files WatchFileSource,
	stores userstore.UserStoreProvider,
) *CatalogWatchReader {
	return &CatalogWatchReader{
		browse:   browse,
		items:    items,
		episodes: episodes,
		seasons:  seasons,
		files:    files,
		stores:   stores,
	}
}

var _ watchdoc.Reader = (*CatalogWatchReader)(nil)

// Items returns the most recently added movies and series the profile may see.
//
// The browse page is re-checked against EnsureAccessibleIDs, which applies the
// per-item EXISTS / NOT EXISTS library predicates. Browse's own disabled-library
// predicate is a single join, and an item linked to both a permitted and a
// disabled library satisfies it through the permitted row; the second pass is
// what makes "a document never names an item the profile may not see" true
// rather than nearly true.
func (r *CatalogWatchReader) Items(ctx context.Context, scope watchdoc.ProfileScope) ([]watchdoc.Item, error) {
	if r.browse == nil {
		return nil, nil
	}
	filter := watchAccessFilter(scope)
	result, err := r.browse.BrowsePage(ctx, catalog.BrowseFilters{
		Type:               itemTypeMovie + "," + itemTypeSeries,
		LibraryIDs:         filter.AllowedLibraryIDs,
		DisabledLibraryIDs: filter.DisabledLibraryIDs,
		MaxContentRating:   filter.MaxContentRating,
		Sort:               watchRecentlyAddedSort,
		Order:              "desc",
		Limit:              watchHomeItemLimit,
	}, false)
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Items) == 0 {
		return nil, nil
	}

	contentIDs := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		contentIDs = append(contentIDs, item.ContentID)
	}
	accessible := map[string]bool{}
	if r.items != nil {
		accessible, err = r.items.EnsureAccessibleIDs(ctx, contentIDs, filter)
		if err != nil {
			return nil, err
		}
	}

	items := make([]watchdoc.Item, 0, len(result.Items))
	for _, item := range result.Items {
		if r.items != nil && !accessible[item.ContentID] {
			continue
		}
		items = append(items, watchItemFromMediaItem(item))
	}
	return items, nil
}

// Item returns one movie or series, applying the viewer's access predicates.
func (r *CatalogWatchReader) Item(ctx context.Context, scope watchdoc.ProfileScope, contentID string) (watchdoc.Item, bool, error) {
	if r.items == nil {
		return watchdoc.Item{}, false, nil
	}
	found, err := r.items.GetByIDsWithAccess(ctx, []string{contentID}, watchAccessFilter(scope))
	if err != nil {
		return watchdoc.Item{}, false, err
	}
	for _, item := range found {
		if item == nil || item.ContentID != contentID {
			continue
		}
		// Watch serves video only: an audiobook, ebook or manga item is not a
		// Watch item, and is answered as absent rather than as a broken card.
		if item.Type != itemTypeMovie && item.Type != itemTypeSeries {
			continue
		}
		return watchItemFromMediaItem(item), true, nil
	}
	return watchdoc.Item{}, false, nil
}

// Episodes returns one series' episodes, with their season titles attached.
func (r *CatalogWatchReader) Episodes(ctx context.Context, _ watchdoc.ProfileScope, seriesID string) ([]watchdoc.Episode, error) {
	if r.episodes == nil {
		return nil, nil
	}
	found, err := r.episodes.ListBySeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	titles := map[int]string{}
	if r.seasons != nil {
		seasons, err := r.seasons.ListBySeries(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		for _, season := range seasons {
			if season != nil {
				titles[season.SeasonNumber] = season.Title
			}
		}
	}

	episodes := make([]watchdoc.Episode, 0, len(found))
	for _, episode := range found {
		if episode == nil {
			continue
		}
		episodes = append(episodes, watchdoc.Episode{
			ContentID:      episode.ContentID,
			SeriesID:       episode.SeriesID,
			SeasonNumber:   episode.SeasonNumber,
			EpisodeNumber:  episode.EpisodeNumber,
			Title:          episode.Title,
			Overview:       episode.Overview,
			RuntimeSeconds: episode.Runtime * secondsPerMinute,
			SeasonTitle:    titles[episode.SeasonNumber],
		})
	}
	return episodes, nil
}

// FilesByContentIDs resolves the playable file for movie and episode
// identifiers alike. Both stores exclude files marked missing and order by
// identifier, so the file named for an item is the same one on every request.
func (r *CatalogWatchReader) FilesByContentIDs(ctx context.Context, contentIDs []string) (map[string]int64, error) {
	fileIDs := make(map[string]int64, len(contentIDs))
	if r.files == nil || len(contentIDs) == 0 {
		return fileIDs, nil
	}
	byContent, err := r.files.ListByContentIDs(ctx, contentIDs)
	if err != nil {
		return nil, err
	}
	byEpisode, err := r.files.ListByEpisodeIDs(ctx, contentIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range contentIDs {
		if fileID := firstPlayableFileID(byContent[id]); fileID > 0 {
			fileIDs[id] = fileID
			continue
		}
		if fileID := firstPlayableFileID(byEpisode[id]); fileID > 0 {
			fileIDs[id] = fileID
		}
	}
	return fileIDs, nil
}

// Progress returns the profile's rows for the requested items.
//
// It runs two bounded reads instead of one unbounded one:
//
//  1. the rows stored directly against the requested identifiers, which covers
//     movies and the episodes a detail document already named;
//  2. the profile's most recently updated rows, capped, so an episode of a
//     listed series that the document did not name — the home screen's whole
//     Continue Watching case — still resolves.
//
// The alternative, expanding every listed series into its episodes, reads the
// whole episode table for a hundred series on every home request. The cap here
// is on the viewer's own progress list, which is tens of rows, not on the size
// of the library.
//
// A stored episode row is keyed by the episode's own identifier and carries no
// series linkage, so the series is re-attached here rather than left for the
// client to guess.
func (r *CatalogWatchReader) Progress(ctx context.Context, scope watchdoc.ProfileScope, contentIDs []string) ([]watchdoc.Progress, error) {
	if r.stores == nil || len(contentIDs) == 0 {
		return nil, nil
	}
	store, err := r.stores.ForUser(ctx, scope.UserID)
	if err != nil {
		return nil, err
	}

	requested := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		requested[id] = true
	}

	direct, err := store.ListProgressByMediaItems(ctx, scope.ProfileID, contentIDs)
	if err != nil {
		return nil, err
	}
	recent, err := store.ListProgress(ctx, scope.ProfileID, "", watchRecentProgressRows, 0)
	if err != nil {
		return nil, err
	}

	// One batched episode lookup answers both questions: which requested
	// identifiers are episodes (so their row is keyed by their series), and
	// which recent rows belong to a series this document lists.
	candidates := make([]string, 0, len(direct)+len(recent))
	seen := make(map[string]bool, len(direct)+len(recent))
	for mediaItemID := range direct {
		if !seen[mediaItemID] {
			seen[mediaItemID] = true
			candidates = append(candidates, mediaItemID)
		}
	}
	for _, row := range recent {
		if !seen[row.MediaItemID] {
			seen[row.MediaItemID] = true
			candidates = append(candidates, row.MediaItemID)
		}
	}
	seriesByEpisode := map[string]string{}
	if r.episodes != nil && len(candidates) > 0 {
		episodes, err := r.episodes.GetByIDs(ctx, candidates)
		if err != nil {
			return nil, err
		}
		for _, episode := range episodes {
			if episode == nil || episode.ContentID == "" || episode.SeriesID == "" {
				continue
			}
			seriesByEpisode[episode.ContentID] = episode.SeriesID
		}
	}

	collected := make(map[string]userstore.WatchProgress, len(direct)+len(recent))
	for mediaItemID, row := range direct {
		collected[mediaItemID] = row
	}
	for _, row := range recent {
		if _, ok := collected[row.MediaItemID]; ok {
			continue
		}
		// A recent row only belongs in this document when its series is one of
		// the items the document lists.
		if seriesID, ok := seriesByEpisode[row.MediaItemID]; ok && requested[seriesID] {
			collected[row.MediaItemID] = row
		}
	}

	progress := make([]watchdoc.Progress, 0, len(collected))
	for mediaItemID, row := range collected {
		updatedAt, err := time.Parse(time.RFC3339, row.UpdatedAt)
		if err != nil {
			// A row whose timestamp cannot be read cannot take part in the
			// clients' newest-write-wins merge; it is left out rather than
			// emitted with a time that would silently lose or win.
			continue
		}
		entry := watchdoc.Progress{
			ContentID:       mediaItemID,
			PositionSeconds: row.PositionSeconds,
			DurationSeconds: row.DurationSeconds,
			Completed:       row.Completed,
			UpdatedAt:       updatedAt,
		}
		if seriesID, ok := seriesByEpisode[mediaItemID]; ok {
			entry.ContentID = seriesID
			entry.EpisodeID = mediaItemID
		}
		progress = append(progress, entry)
	}
	return progress, nil
}

func watchItemFromMediaItem(item *models.MediaItem) watchdoc.Item {
	if item == nil {
		return watchdoc.Item{}
	}
	converted := watchdoc.Item{
		Kind:           item.Type,
		ContentID:      item.ContentID,
		Title:          item.Title,
		Year:           item.Year,
		RuntimeSeconds: item.Runtime * secondsPerMinute,
		Rating:         item.ContentRating,
		Overview:       item.Overview,
		Genres:         item.Genres,
		Keywords:       item.Keywords,
		AddedAt:        item.AddedAt,
	}
	if item.SeasonCount != nil {
		converted.SeasonCount = *item.SeasonCount
	}
	return converted
}

// firstPlayableFileID names the file a client should play. Both listings are
// already ordered by identifier and exclude missing files, so the lowest
// identifier is a stable choice; which version is actually delivered is the
// playback plan's decision, not the document's.
func firstPlayableFileID(files []*models.MediaFile) int64 {
	for _, file := range files {
		if file != nil && file.ID > 0 {
			return int64(file.ID)
		}
	}
	return 0
}

func watchAccessFilter(scope watchdoc.ProfileScope) catalog.AccessFilter {
	return catalog.AccessFilter{
		AllowedLibraryIDs:  scope.AllowedLibraryIDs,
		DisabledLibraryIDs: scope.DisabledLibraryIDs,
		MaxContentRating:   scope.MaxContentRating,
		UserID:             scope.UserID,
		ProfileID:          scope.ProfileID,
	}
}
