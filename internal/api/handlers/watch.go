package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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

// watchSearchResultLimit bounds a single search response. A client that wants
// more is a client that wants scroll pagination, which this endpoint does not
// offer yet — this is the same "first frame" tradeoff watchHomeItemLimit
// makes, not an oversight.
const watchSearchResultLimit = 50

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

// WatchSearcher answers a server-side title search over the movies and series
// a profile may see. Separate from watchdoc.Reader because search is not a
// document composition — no progress, no featured item, no seasons or
// episodes — so it never had a reason to grow that interface.
type WatchSearcher interface {
	Search(ctx context.Context, scope watchdoc.ProfileScope, query string) ([]watchdoc.Item, error)
}

// WatchHandler serves the contracts' watch_document_v1 documents.
//
// Both endpoints are profile-scoped: the document names what one profile may
// watch and carries that profile's progress, so a request without a profile is
// refused rather than answered for the account.
type WatchHandler struct {
	reader   watchdoc.Reader
	searcher WatchSearcher
}

// NewWatchHandler creates a WatchHandler over a composition reader and a
// searcher. Either may be nil, in which case its endpoints stay mounted but
// answer unavailable — honest about a deployment with no catalog or no search
// provider rather than pretending the library is empty or search found
// nothing.
func NewWatchHandler(reader watchdoc.Reader, searcher WatchSearcher) *WatchHandler {
	return &WatchHandler{reader: reader, searcher: searcher}
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

// HandleWatchSearch handles GET /watch/search?q=.
func (h *WatchHandler) HandleWatchSearch(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.requestScope(w, r)
	if !ok {
		return
	}
	if h.searcher == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Search is not available")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "q is required")
		return
	}
	items, err := h.searcher.Search(r.Context(), scope, query)
	if err != nil {
		slog.ErrorContext(r.Context(), "watch search failed",
			"component", "api", "profile_id", scope.ProfileID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Search failed")
		return
	}
	document, err := watchdoc.ComposeSearchResults(r.Context(), h.reader, scope, items)
	if err != nil {
		slog.ErrorContext(r.Context(), "composing watch search document failed",
			"component", "api", "profile_id", scope.ProfileID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to compose the Watch search document")
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
		// The same vocabulary apimw.RequireProfile puts on the wire ahead of
		// this handler. Two spellings of one refusal on one surface would leave
		// a client branching on which route it happened to call.
		writeError(w, http.StatusBadRequest, "bad_request", "X-Profile-Id header is required")
		return watchdoc.ProfileScope{}, false
	}
	// requestAccessFilter is the one place the viewer scope is read into a
	// catalog filter; deriving from it is how the playback-quality ceiling
	// reaches the file lookup instead of being quietly dropped here.
	filter := requestAccessFilter(r)
	return watchdoc.ProfileScope{
		UserID:             filter.UserID,
		ProfileID:          profileID,
		AllowedLibraryIDs:  filter.AllowedLibraryIDs,
		DisabledLibraryIDs: filter.DisabledLibraryIDs,
		MaxContentRating:   filter.MaxContentRating,
		MaxPlaybackQuality: filter.MaxPlaybackQuality,
	}, true
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

// WatchMarkerSource resolves chapter and skip-intro data for files already
// named by identifier — never a second content- or episode-keyed lookup, the
// same files WatchFileSource already found.
type WatchMarkerSource interface {
	GetByIDs(ctx context.Context, ids []int) ([]*models.MediaFile, error)
}

// WatchPeopleSource resolves the cast and crew credited on one item.
type WatchPeopleSource interface {
	ListForItem(ctx context.Context, contentID string) ([]models.ItemPerson, error)
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
	markers  WatchMarkerSource
	people   WatchPeopleSource
	stores   userstore.UserStoreProvider
	images   catalog.ImageResolver
	search   catalog.CatalogSearchProvider
}

// NewCatalogWatchReader wires a reader over the existing repositories. seasons
// may be nil: season titles are then omitted rather than the document failing.
// images may be nil: posters are then omitted rather than the document
// carrying a stored path or a plugin-prefixed reference no client can fetch.
// search may be nil: HandleWatchSearch answers unavailable rather than
// searching nothing when no provider is configured. markers may be nil: a
// detail document then carries no chapters or skip-intro range rather than
// failing to compose. people may be nil: a detail document then carries no
// cast or crew rather than failing to compose.
func NewCatalogWatchReader(
	browse WatchBrowseSource,
	items WatchItemSource,
	episodes WatchEpisodeSource,
	seasons WatchSeasonSource,
	files WatchFileSource,
	markers WatchMarkerSource,
	people WatchPeopleSource,
	stores userstore.UserStoreProvider,
	images catalog.ImageResolver,
	search catalog.CatalogSearchProvider,
) *CatalogWatchReader {
	return &CatalogWatchReader{
		browse:   browse,
		items:    items,
		episodes: episodes,
		seasons:  seasons,
		files:    files,
		markers:  markers,
		people:   people,
		stores:   stores,
		images:   images,
		search:   search,
	}
}

// watchPosterVariant is the image size hint Watch documents ask for: cards in
// a horizontally scrolling rail, never the larger "featured" or "full" crops
// a detail hero uses.
const watchPosterVariant = "card"

// resolvePosterURLs resolves every non-empty poster path in one batched call,
// so a home document with a hundred items costs one round trip through the
// resolver rather than one per item. A nil resolver, or a path the resolver
// could not answer for, means the item carries no poster rather than a URL
// nothing can fetch.
func (r *CatalogWatchReader) resolvePosterURLs(ctx context.Context, paths []string) map[string]string {
	if r.images == nil || len(paths) == 0 {
		return map[string]string{}
	}
	unique := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		unique = append(unique, path)
	}
	if len(unique) == 0 {
		return map[string]string{}
	}
	return r.images.ResolveImageURLs(ctx, unique, watchPosterVariant)
}

var _ watchdoc.Reader = (*CatalogWatchReader)(nil)

// Items returns the most recently added movies and series the profile may see,
// unioned with whatever the profile is part-way through.
//
// The window alone is not enough: a viewer whose Continue Watching titles are
// all older than the hundred most recently added items would get a home
// document with nothing to resume — the longer they have used the server, the
// worse it gets. The union is resolved through GetByIDsWithAccess, so an item
// that arrives through a progress row is access-checked exactly like one that
// arrives through browse.
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

	window := []*models.MediaItem{}
	if result != nil {
		window = result.Items
	}
	contentIDs := make([]string, 0, len(window))
	listed := make(map[string]bool, len(window))
	for _, item := range window {
		if item == nil {
			continue
		}
		contentIDs = append(contentIDs, item.ContentID)
		listed[item.ContentID] = true
	}
	accessible := map[string]bool{}
	if r.items != nil && len(contentIDs) > 0 {
		accessible, err = r.items.EnsureAccessibleIDs(ctx, contentIDs, filter)
		if err != nil {
			return nil, err
		}
	}

	items := make([]watchdoc.Item, 0, len(window))
	rawPosterPaths := make(map[string]string, len(window))
	for _, item := range window {
		if item == nil || (r.items != nil && !accessible[item.ContentID]) {
			continue
		}
		items = append(items, watchItemFromMediaItem(item))
		rawPosterPaths[item.ContentID] = item.PosterPath
	}

	resumed, resumedPosterPaths, err := r.itemsInProgress(ctx, scope, filter, listed)
	if err != nil {
		return nil, err
	}
	for contentID, path := range resumedPosterPaths {
		rawPosterPaths[contentID] = path
	}

	all := append(items, resumed...)
	paths := make([]string, 0, len(rawPosterPaths))
	for _, path := range rawPosterPaths {
		paths = append(paths, path)
	}
	resolved := r.resolvePosterURLs(ctx, paths)
	for index := range all {
		all[index].PosterURL = resolved[rawPosterPaths[all[index].ContentID]]
	}
	return all, nil
}

// itemsInProgress resolves the movies and series behind the profile's recent
// progress rows that the browse window did not already list. An episode row is
// resolved to its series, which is what a Watch document can name.
func (r *CatalogWatchReader) itemsInProgress(
	ctx context.Context,
	scope watchdoc.ProfileScope,
	filter catalog.AccessFilter,
	listed map[string]bool,
) ([]watchdoc.Item, map[string]string, error) {
	if r.stores == nil || r.items == nil {
		return nil, nil, nil
	}
	store, err := r.stores.ForUser(ctx, scope.UserID)
	if err != nil {
		return nil, nil, err
	}
	// Video only: an audiobook or ebook row cannot become a Watch item, and
	// letting those rows consume the window would hide the ones that can.
	rows, err := store.ListProgressFiltered(ctx, scope.ProfileID, "",
		[]string{itemTypeMovie, itemTypeSeries, itemTypeEpisode}, nil, watchRecentProgressRows, 0)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	progressed := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.MediaItemID == "" || listed[row.MediaItemID] || seen[row.MediaItemID] {
			continue
		}
		seen[row.MediaItemID] = true
		progressed = append(progressed, row.MediaItemID)
	}
	if len(progressed) == 0 {
		return nil, nil, nil
	}

	// An episode row names an episode; the document names its series. The
	// episode identifiers are then dropped from the catalog lookup rather than
	// asked about — they are not media_items rows and never will be.
	isEpisode := make(map[string]bool)
	candidates := make([]string, 0, len(progressed))
	if r.episodes != nil {
		episodes, err := r.episodes.GetByIDs(ctx, progressed)
		if err != nil {
			return nil, nil, err
		}
		for _, episode := range episodes {
			if episode == nil || episode.ContentID == "" {
				continue
			}
			isEpisode[episode.ContentID] = true
			if episode.SeriesID == "" || listed[episode.SeriesID] || seen[episode.SeriesID] {
				continue
			}
			seen[episode.SeriesID] = true
			candidates = append(candidates, episode.SeriesID)
		}
	}
	for _, id := range progressed {
		if !isEpisode[id] {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}

	found, err := r.items.GetByIDsWithAccess(ctx, candidates, filter)
	if err != nil {
		return nil, nil, err
	}
	resumed := make([]watchdoc.Item, 0, len(found))
	posterPaths := make(map[string]string, len(found))
	for _, item := range found {
		if item == nil || listed[item.ContentID] {
			continue
		}
		if item.Type != itemTypeMovie && item.Type != itemTypeSeries {
			continue
		}
		listed[item.ContentID] = true
		resumed = append(resumed, watchItemFromMediaItem(item))
		posterPaths[item.ContentID] = item.PosterPath
	}
	return resumed, posterPaths, nil
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
		converted := watchItemFromMediaItem(item)
		resolved := r.resolvePosterURLs(ctx, []string{item.PosterPath})
		converted.PosterURL = resolved[item.PosterPath]
		return converted, true, nil
	}
	return watchdoc.Item{}, false, nil
}

// Search answers a server-side title search over the movies and series the
// profile may see, in the search provider's own relevance order. watchdoc.
// ComposeSearchResults is the one place that order is preserved rather than
// re-sorted, so this method does not reorder its input either.
func (r *CatalogWatchReader) Search(ctx context.Context, scope watchdoc.ProfileScope, query string) ([]watchdoc.Item, error) {
	if r.search == nil {
		return nil, nil
	}
	result, err := r.search.Search(ctx, catalog.CatalogSearchRequest{
		Query:     query,
		ItemTypes: []string{itemTypeMovie, itemTypeSeries},
		Limit:     watchSearchResultLimit,
		Access:    watchAccessFilter(scope),
		SkipTotal: true,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	items := make([]watchdoc.Item, 0, len(result.Items))
	posterPaths := make(map[string]string, len(result.Items))
	for _, item := range result.Items {
		if item == nil {
			continue
		}
		items = append(items, watchItemFromMediaItem(item))
		posterPaths[item.ContentID] = item.PosterPath
	}
	paths := make([]string, 0, len(posterPaths))
	for _, path := range posterPaths {
		paths = append(paths, path)
	}
	resolved := r.resolvePosterURLs(ctx, paths)
	for index := range items {
		items[index].PosterURL = resolved[posterPaths[items[index].ContentID]]
	}
	return items, nil
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
//
// Every version is put through catalog.FilterMediaFilesByAccess first — the
// same predicate the detail and item surfaces apply, and the one
// /playback/start enforces before it will mint a plan. Without it a document
// can name a version in a library the profile may not see, or above its
// quality ceiling, and the client renders a Play button that answers 404.
func (r *CatalogWatchReader) FilesByContentIDs(ctx context.Context, scope watchdoc.ProfileScope, contentIDs []string) (map[string]int64, error) {
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
	filter := watchAccessFilter(scope)
	for _, id := range contentIDs {
		if fileID := firstPlayableFileID(catalog.FilterMediaFilesByAccess(byContent[id], filter)); fileID > 0 {
			fileIDs[id] = fileID
			continue
		}
		if fileID := firstPlayableFileID(catalog.FilterMediaFilesByAccess(byEpisode[id], filter)); fileID > 0 {
			fileIDs[id] = fileID
		}
	}
	return fileIDs, nil
}

// Markers resolves chapter and skip-intro data for the given file
// identifiers, unfiltered by profile scope: a file this document already
// named is a file the profile's access predicates already cleared, so a
// second access check here would only reject files FilesByContentIDs already
// approved.
func (r *CatalogWatchReader) Markers(ctx context.Context, _ watchdoc.ProfileScope, fileIDs []int64) (map[int64]watchdoc.FileMarkers, error) {
	found := make(map[int64]watchdoc.FileMarkers, len(fileIDs))
	if r.markers == nil || len(fileIDs) == 0 {
		return found, nil
	}
	ids := make([]int, 0, len(fileIDs))
	for _, id := range fileIDs {
		ids = append(ids, int(id))
	}
	files, err := r.markers.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file == nil {
			continue
		}
		markers := watchdoc.FileMarkers{
			Chapters:   mediaChapters(file.Chapters),
			IntroStart: file.IntroStart,
			IntroEnd:   file.IntroEnd,
		}
		if len(markers.Chapters) == 0 && markers.IntroStart == nil && markers.IntroEnd == nil {
			continue
		}
		found[int64(file.ID)] = markers
	}
	return found, nil
}

func mediaChapters(chapters []models.MediaChapter) []watchdoc.Chapter {
	if len(chapters) == 0 {
		return nil
	}
	converted := make([]watchdoc.Chapter, 0, len(chapters))
	for _, chapter := range chapters {
		converted = append(converted, watchdoc.Chapter{
			Index:        chapter.Index,
			Title:        chapter.Title,
			StartSeconds: chapter.StartSeconds,
			EndSeconds:   chapter.EndSeconds,
		})
	}
	return converted
}

// Credits returns the cast and crew credited on contentID. A person's photo is
// resolved through the same catalog.ImageResolver poster resolution already
// uses, at "featured" size — the same crop the detail screens across this
// server already ask for a person's photo at.
func (r *CatalogWatchReader) Credits(ctx context.Context, _ watchdoc.ProfileScope, contentID string) ([]watchdoc.CastMember, []watchdoc.CrewMember, error) {
	if r.people == nil {
		return nil, nil, nil
	}
	people, err := r.people.ListForItem(ctx, contentID)
	if err != nil {
		return nil, nil, err
	}
	var cast []watchdoc.CastMember
	var crew []watchdoc.CrewMember
	for _, person := range people {
		photoURL := ""
		if r.images != nil && person.PhotoPath != "" && person.PhotoPath != "-" {
			photoURL = r.images.ResolveImageURL(ctx, person.PhotoPath, "featured")
		}
		personID := strconv.FormatInt(person.ID, 10)
		switch person.Kind {
		case models.PersonKindActor, models.PersonKindGuestStar:
			cast = append(cast, watchdoc.CastMember{
				PersonID:  personID,
				Name:      person.Name,
				Character: person.Character,
				PhotoURL:  photoURL,
			})
		default:
			crew = append(crew, watchdoc.CrewMember{
				PersonID: personID,
				Name:     person.Name,
				Job:      person.Kind.String(),
				PhotoURL: photoURL,
			})
		}
	}
	return cast, crew, nil
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
	// Episode rows only, and every status: a household that also reads and
	// listens would otherwise fill this window with rows no Watch document can
	// use, while an empty status keeps completed episodes in the client's
	// newest-write-wins merge.
	recent, err := store.ListProgressFiltered(ctx, scope.ProfileID, "", []string{itemTypeEpisode}, nil, watchRecentProgressRows, 0)
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

// watchAccessFilter rebuilds the catalog filter the handler derived the scope
// from. The round trip through ProfileScope is what keeps the composition
// package free of catalog types; the fields are the same ones
// requestAccessFilter read.
func watchAccessFilter(scope watchdoc.ProfileScope) catalog.AccessFilter {
	return catalog.AccessFilter{
		AllowedLibraryIDs:  scope.AllowedLibraryIDs,
		DisabledLibraryIDs: scope.DisabledLibraryIDs,
		MaxContentRating:   scope.MaxContentRating,
		MaxPlaybackQuality: scope.MaxPlaybackQuality,
		UserID:             scope.UserID,
		ProfileID:          scope.ProfileID,
	}
}
