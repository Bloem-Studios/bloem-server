package watchdoc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Reasons an episode cannot be described by the contract and is left out of a
// series document. Each one is logged with the series and episode it cost, so
// "my episode is missing from the app" is answerable from the server log
// instead of by guesswork.
const (
	dropMissingTitle    = "missing_title"
	dropNumberBelowOne  = "season_or_episode_number_below_one"
	dropNoPlayableFile  = "no_playable_file"
	dropDuplicateRow    = "duplicate_episode_row"
	dropUnknownSeriesID = "unknown_series_id"
)

// logDroppedEpisode records one episode that will not reach the client.
func logDroppedEpisode(ctx context.Context, seriesID, episodeID, reason string) {
	slog.WarnContext(ctx, "watch document dropped an episode",
		"component", "watchdoc", "series_id", seriesID, "episode_id", episodeID, "reason", reason)
}

// ErrItemNotFound is returned when a detail document is asked for a content
// identifier the profile cannot see — either because it does not exist, or
// because the viewer's library restrictions exclude it. The two are the same
// answer on purpose: distinguishing them would let a restricted profile probe
// for the existence of items it may not see.
var ErrItemNotFound = errors.New("watchdoc: item not found")

// ErrNoReader is returned when composition is attempted without a source.
var ErrNoReader = errors.New("watchdoc: no reader configured")

// ProfileScope is the viewer identity and access ceiling every read is made
// under. It is the composition's whole notion of "who is asking": nothing here
// reads request state, so the same scope always produces the same document
// from the same store contents.
type ProfileScope struct {
	UserID    int
	ProfileID string
	// AllowedLibraryIDs, when non-nil, is the exhaustive set of libraries the
	// profile may see. A non-nil empty slice means "no libraries".
	AllowedLibraryIDs []int
	// DisabledLibraryIDs is only meaningful while AllowedLibraryIDs is nil.
	DisabledLibraryIDs []int
	// MaxContentRating is the profile's content-rating ceiling.
	MaxContentRating string
	// MaxPlaybackQuality is the profile's effective quality ceiling. It reaches
	// the file lookup because a file above the ceiling is one the playback
	// endpoint will refuse: naming it here would render a Play button that
	// cannot work.
	MaxPlaybackQuality string
}

// Item is a movie or a series the profile may see.
type Item struct {
	Kind           string
	ContentID      string
	Title          string
	Year           int
	RuntimeSeconds int
	Rating         string
	Overview       string
	Genres         []string
	Keywords       []string
	SeasonCount    int
	// AddedAt is when the item entered the profile's libraries. It drives the
	// document order and the featured rule; a nil value sorts last.
	AddedAt *time.Time
}

// Episode is one episode of a series the profile may see.
type Episode struct {
	ContentID      string
	SeriesID       string
	SeasonNumber   int
	EpisodeNumber  int
	Title          string
	Overview       string
	RuntimeSeconds int
	// SeasonTitle is the season's own title, used to build the series item's
	// season summaries without a second round trip.
	SeasonTitle string
}

// Progress is one durable progress row for the requesting profile.
type Progress struct {
	// ContentID is the identifier the caller asked about: the movie, or the
	// series an episode row belongs to.
	ContentID string
	// EpisodeID names the episode for an episode row, and is empty otherwise.
	EpisodeID       string
	PositionSeconds float64
	DurationSeconds float64
	Completed       bool
	UpdatedAt       time.Time
}

// Reader is the composition's whole view of the server's stores. It is a
// narrow interface rather than the concrete catalog, episode, media-file and
// progress repositories so composition stays unit-testable without a database,
// and so the join lives in one place instead of being restated per endpoint.
//
// Implementations must apply the profile's access restrictions themselves: an
// item the scope excludes must not be returned by Items or Item, and Progress
// must not answer for an identifier that was not asked about.
type Reader interface {
	// Items returns the movies and series the profile may see, in any order.
	Items(ctx context.Context, scope ProfileScope) ([]Item, error)
	// Item returns one movie or series by content identifier. The boolean is
	// false when the profile may not see it or it does not exist.
	Item(ctx context.Context, scope ProfileScope, contentID string) (Item, bool, error)
	// Episodes returns the episodes of one series, in any order.
	Episodes(ctx context.Context, scope ProfileScope, seriesID string) ([]Episode, error)
	// FilesByContentIDs maps each content identifier that has a playable file
	// to that file's identifier. Identifiers with no playable file are absent
	// from the map rather than mapped to zero. The scope is passed because
	// playability is per viewer: a version in a library the profile may not
	// see, or above its quality ceiling, is not a file this profile can play.
	FilesByContentIDs(ctx context.Context, scope ProfileScope, contentIDs []string) (map[string]int64, error)
	// Progress returns the profile's rows for the given identifiers. A row for
	// an episode of a requested series carries the series' identifier in
	// ContentID and the episode's in EpisodeID, because the stored row has no
	// series linkage of its own.
	Progress(ctx context.Context, scope ProfileScope, contentIDs []string) ([]Progress, error)
}

// ComposeHome builds the Watch home document for one profile.
func ComposeHome(ctx context.Context, reader Reader, scope ProfileScope) (Document, error) {
	if reader == nil {
		return Document{}, ErrNoReader
	}
	items, err := reader.Items(ctx, scope)
	if err != nil {
		return Document{}, fmt.Errorf("watchdoc: reading items: %w", err)
	}

	ordered := orderLibraryItems(items)
	fileIDs, err := resolveFiles(ctx, reader, scope, playableContentIDs(ordered))
	if err != nil {
		return Document{}, err
	}

	claimedFiles := map[int64]bool{}
	documentItems := make([]DocumentItem, 0, len(ordered))
	for _, item := range ordered {
		documentItem, ok := newLibraryDocumentItem(item, fileIDs, claimedFiles, requirePlayableFile)
		if !ok {
			continue
		}
		documentItems = append(documentItems, documentItem)
	}

	return finishDocument(ctx, reader, scope, documentItems, "")
}

// ComposeItem builds the detail document for one movie or series.
//
// A series carries its seasons and its episodes in ascending season then
// episode order; a movie carries itself. The requested item is the document's
// featured identifier, so a client that renders the featured entry uniformly
// does not need a special case for detail screens.
func ComposeItem(ctx context.Context, reader Reader, scope ProfileScope, contentID string) (Document, error) {
	if reader == nil {
		return Document{}, ErrNoReader
	}
	contentID = strings.TrimSpace(contentID)
	if contentID == "" {
		return Document{}, ErrItemNotFound
	}

	item, found, err := reader.Item(ctx, scope, contentID)
	if err != nil {
		return Document{}, fmt.Errorf("watchdoc: reading item %s: %w", contentID, err)
	}
	if !found || !isLibraryKind(item.Kind) {
		return Document{}, ErrItemNotFound
	}

	var (
		documentItems = make([]DocumentItem, 0, 1)
		episodeItems  []DocumentItem
		seasons       []DocumentSeason
		claimedFiles  = map[int64]bool{}
	)

	if item.Kind == KindSeries {
		episodes, err := reader.Episodes(ctx, scope, item.ContentID)
		if err != nil {
			return Document{}, fmt.Errorf("watchdoc: reading episodes of %s: %w", item.ContentID, err)
		}
		ordered := orderEpisodes(ctx, item.ContentID, episodes)
		fileIDs, err := resolveFiles(ctx, reader, scope, episodeContentIDs(ordered))
		if err != nil {
			return Document{}, err
		}
		seasonTitles := map[int]string{}
		seenSeasons := map[int]bool{}
		for _, episode := range ordered {
			documentItem, ok := newEpisodeDocumentItem(ctx, episode, item.ContentID, fileIDs, claimedFiles)
			if !ok {
				continue
			}
			episodeItems = append(episodeItems, documentItem)
			// Seasons are summarized from the episodes that survived, so a
			// season whose episodes are all unplayable is not advertised as
			// something to open.
			if !seenSeasons[documentItem.SeasonNumber] {
				seenSeasons[documentItem.SeasonNumber] = true
				seasons = append(seasons, DocumentSeason{SeasonNumber: documentItem.SeasonNumber})
			}
			if title := strings.TrimSpace(episode.SeasonTitle); title != "" && seasonTitles[documentItem.SeasonNumber] == "" {
				seasonTitles[documentItem.SeasonNumber] = title
			}
		}
		for index := range seasons {
			seasons[index].Title = seasonTitles[seasons[index].SeasonNumber]
		}
	}

	// The requested item is exempt from the home document's playability rule: a
	// viewer who navigated to a detail screen gets the screen, with Play
	// unavailable, rather than a blank document. A series whose episodes are
	// all unplayable already keeps its shell for the same reason.
	fileIDs, err := resolveFiles(ctx, reader, scope, playableContentIDs([]Item{item}))
	if err != nil {
		return Document{}, err
	}
	if root, ok := newLibraryDocumentItem(item, fileIDs, claimedFiles, allowMissingFile); ok {
		root.Seasons = seasons
		documentItems = append(documentItems, root)
		documentItems = append(documentItems, episodeItems...)
	}

	return finishDocument(ctx, reader, scope, documentItems, item.ContentID)
}

// finishDocument attaches the profile's progress rows, chooses the featured
// item and stamps the snapshot. featuredPreference names the item a detail
// document was asked for; the home document passes an empty string and takes
// the rule's own choice.
func finishDocument(
	ctx context.Context,
	reader Reader,
	scope ProfileScope,
	items []DocumentItem,
	featuredPreference string,
) (Document, error) {
	rows, err := readProgress(ctx, reader, scope, items)
	if err != nil {
		return Document{}, err
	}

	featured := featuredPreference
	if featured == "" || !containsContentID(items, featured) {
		featured = chooseFeatured(items, rows)
	}
	for index := range items {
		if items[index].ContentID == featured {
			items[index].Featured = true
		}
	}

	return Document{
		Schema:            SchemaWatchDocumentV1,
		Snapshot:          time.Now().UTC().Format(time.RFC3339),
		FeaturedContentID: featured,
		Items:             items,
		Progress:          rows,
	}, nil
}

// chooseFeatured implements the server's single closed featured rule: the most
// recently added movie the profile has not finished, and otherwise the first
// item in document order.
//
// Determinism comes from the item order, which is a strict total order (added
// time descending, then the unique content identifier ascending, with
// duplicates already collapsed). The first match under a total order is unique,
// so the same store contents always name the same item. The rule is closed on
// purpose: what a client does with the name — hero, banner, nothing — is the
// client's own presentation decision, and the server never ships layout.
func chooseFeatured(items []DocumentItem, rows []DocumentProgress) string {
	if len(items) == 0 {
		return ""
	}
	finished := map[string]bool{}
	for _, row := range rows {
		// Only a row for the item itself counts: an episode the viewer
		// finished does not make its series watched.
		if row.Completed && row.EpisodeID == "" {
			finished[row.ContentID] = true
		}
	}
	for _, item := range items {
		if item.Kind == KindMovie && !finished[item.ContentID] {
			return item.ContentID
		}
	}
	return items[0].ContentID
}

// readProgress asks for the profile's rows for the listed items and keeps only
// what the contract and the clients accept.
func readProgress(ctx context.Context, reader Reader, scope ProfileScope, items []DocumentItem) ([]DocumentProgress, error) {
	rows := make([]DocumentProgress, 0)
	// Progress is requested only for items the document lists, which are
	// already the items the profile may see. A restricted item therefore has no
	// route into the progress array even if the progress store still holds a
	// row for it.
	requested := make([]string, 0, len(items))
	listed := map[string]bool{}
	attributable := map[string]bool{}
	for _, item := range items {
		if listed[item.ContentID] {
			continue
		}
		listed[item.ContentID] = true
		requested = append(requested, item.ContentID)
		// A progress row may be attributed to a movie or a series, never to an
		// episode: an episode's row travels under its series with the episode
		// named, which is the only shape the clients can key a checkpoint from.
		if item.Kind != KindEpisode {
			attributable[item.ContentID] = true
		}
	}
	if len(requested) == 0 {
		return rows, nil
	}

	found, err := reader.Progress(ctx, scope, requested)
	if err != nil {
		return nil, fmt.Errorf("watchdoc: reading progress: %w", err)
	}

	newest := map[string]Progress{}
	for _, row := range found {
		if !attributable[row.ContentID] {
			continue
		}
		// The schema requires a positive duration and a non-negative position;
		// a row that cannot describe a resume point is not worth emitting.
		if row.DurationSeconds <= 0 || row.UpdatedAt.IsZero() {
			continue
		}
		if row.PositionSeconds < 0 {
			row.PositionSeconds = 0
		}
		key := row.ContentID + "\x00" + row.EpisodeID
		if existing, ok := newest[key]; ok && !row.UpdatedAt.After(existing.UpdatedAt) {
			continue
		}
		newest[key] = row
	}

	for _, row := range newest {
		rows = append(rows, DocumentProgress{
			ContentID:       row.ContentID,
			EpisodeID:       row.EpisodeID,
			PositionSeconds: row.PositionSeconds,
			DurationSeconds: row.DurationSeconds,
			Completed:       row.Completed,
			UpdatedAt:       row.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ContentID != rows[j].ContentID {
			return rows[i].ContentID < rows[j].ContentID
		}
		return rows[i].EpisodeID < rows[j].EpisodeID
	})
	return rows, nil
}

// orderLibraryItems drops what cannot be emitted, collapses duplicate content
// identifiers and returns a strict total order: most recently added first,
// content identifier ascending on ties and on unknown added times.
func orderLibraryItems(items []Item) []Item {
	ordered := make([]Item, 0, len(items))
	// Deduplication is by identifier, not by adjacency: an item can reach this
	// list twice through two library memberships, and the two rows need not
	// sort next to each other. A series carries no file identifier, so nothing
	// downstream would catch a duplicate one — the map is the only guard.
	position := make(map[string]int, len(items))
	for _, item := range items {
		item.ContentID = strings.TrimSpace(item.ContentID)
		item.Title = strings.TrimSpace(item.Title)
		if item.ContentID == "" || item.Title == "" || !isLibraryKind(item.Kind) {
			continue
		}
		if index, seen := position[item.ContentID]; seen {
			// Keep the most recently added row, deterministically, whatever
			// order the store answered in.
			if compareAddedAt(item.AddedAt, ordered[index].AddedAt) < 0 {
				ordered[index] = item
			}
			continue
		}
		position[item.ContentID] = len(ordered)
		ordered = append(ordered, item)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if difference := compareAddedAt(left.AddedAt, right.AddedAt); difference != 0 {
			return difference < 0
		}
		return left.ContentID < right.ContentID
	})
	return ordered
}

// compareAddedAt orders two added times most-recent-first, with an unknown
// time last. It returns a negative number when left sorts before right.
func compareAddedAt(left, right *time.Time) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return 1
	case right == nil:
		return -1
	case left.After(*right):
		return -1
	case right.After(*left):
		return 1
	default:
		return 0
	}
}

// orderEpisodes drops episodes the contract cannot describe and returns them in
// ascending season, episode, content identifier order. Every drop is logged.
func orderEpisodes(ctx context.Context, seriesID string, episodes []Episode) []Episode {
	ordered := make([]Episode, 0, len(episodes))
	seen := map[string]bool{}
	for _, episode := range episodes {
		episode.ContentID = strings.TrimSpace(episode.ContentID)
		episode.Title = strings.TrimSpace(episode.Title)
		if episode.ContentID == "" {
			logDroppedEpisode(ctx, seriesID, "", dropUnknownSeriesID)
			continue
		}
		// The contract requires a non-empty title. Inventing one here would be
		// the server writing the copy the clients own, so the episode is
		// dropped and the loss is recorded instead.
		if episode.Title == "" {
			logDroppedEpisode(ctx, seriesID, episode.ContentID, dropMissingTitle)
			continue
		}
		// season_number and episode_number are required to be at least one, so
		// specials (season zero) and unnumbered rows are not describable here.
		if episode.SeasonNumber < 1 || episode.EpisodeNumber < 1 {
			logDroppedEpisode(ctx, seriesID, episode.ContentID, dropNumberBelowOne)
			continue
		}
		if seen[episode.ContentID] {
			logDroppedEpisode(ctx, seriesID, episode.ContentID, dropDuplicateRow)
			continue
		}
		seen[episode.ContentID] = true
		ordered = append(ordered, episode)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.SeasonNumber != right.SeasonNumber {
			return left.SeasonNumber < right.SeasonNumber
		}
		if left.EpisodeNumber != right.EpisodeNumber {
			return left.EpisodeNumber < right.EpisodeNumber
		}
		return left.ContentID < right.ContentID
	})
	return ordered
}

// filePolicy says whether an item may be listed without a playable file.
type filePolicy bool

const (
	// requirePlayableFile drops a playable item with no file: a tile that
	// cannot play is worse than an absent one.
	requirePlayableFile filePolicy = true
	// allowMissingFile keeps the item and omits file_id, which the contract
	// permits. Used for the item a detail document was asked for.
	allowMissingFile filePolicy = false
)

// newLibraryDocumentItem converts a movie or series, reporting false when the
// item cannot be emitted under the given file policy.
func newLibraryDocumentItem(item Item, fileIDs map[string]int64, claimedFiles map[int64]bool, policy filePolicy) (DocumentItem, bool) {
	documentItem := DocumentItem{
		Kind:           item.Kind,
		ContentID:      strings.TrimSpace(item.ContentID),
		Title:          strings.TrimSpace(item.Title),
		Year:           item.Year,
		RuntimeSeconds: item.RuntimeSeconds,
		Rating:         strings.TrimSpace(item.Rating),
		Overview:       strings.TrimSpace(item.Overview),
		Genres:         item.Genres,
		Keywords:       item.Keywords,
	}
	if documentItem.ContentID == "" || documentItem.Title == "" || !isLibraryKind(documentItem.Kind) {
		return DocumentItem{}, false
	}
	if documentItem.RuntimeSeconds < 0 {
		documentItem.RuntimeSeconds = 0
	}
	if item.Kind == KindSeries {
		if item.SeasonCount > 0 {
			documentItem.SeasonCount = item.SeasonCount
		}
		return documentItem, true
	}
	fileID, ok := claimFile(fileIDs[documentItem.ContentID], claimedFiles)
	if !ok {
		if policy == requirePlayableFile {
			return DocumentItem{}, false
		}
		return documentItem, true
	}
	documentItem.FileID = fileID
	return documentItem, true
}

// newEpisodeDocumentItem converts an episode, reporting false when it cannot
// carry the full set of fields the clients require of an episode.
func newEpisodeDocumentItem(ctx context.Context, episode Episode, seriesID string, fileIDs map[string]int64, claimedFiles map[int64]bool) (DocumentItem, bool) {
	linkedSeriesID := strings.TrimSpace(episode.SeriesID)
	if linkedSeriesID == "" {
		linkedSeriesID = seriesID
	}
	if linkedSeriesID == "" {
		logDroppedEpisode(ctx, seriesID, episode.ContentID, dropUnknownSeriesID)
		return DocumentItem{}, false
	}
	fileID, ok := claimFile(fileIDs[episode.ContentID], claimedFiles)
	if !ok {
		logDroppedEpisode(ctx, linkedSeriesID, episode.ContentID, dropNoPlayableFile)
		return DocumentItem{}, false
	}
	runtime := episode.RuntimeSeconds
	if runtime < 0 {
		runtime = 0
	}
	return DocumentItem{
		Kind:           KindEpisode,
		ContentID:      episode.ContentID,
		Title:          episode.Title,
		Overview:       strings.TrimSpace(episode.Overview),
		RuntimeSeconds: runtime,
		SeriesID:       linkedSeriesID,
		SeasonNumber:   episode.SeasonNumber,
		EpisodeNumber:  episode.EpisodeNumber,
		FileID:         fileID,
	}, true
}

// claimFile enforces the clients' "positive and unique" rule on file
// identifiers: a non-positive identifier, or one already used in this
// document, means the item is dropped rather than emitted with a value that
// fails the whole document.
func claimFile(fileID int64, claimedFiles map[int64]bool) (int64, bool) {
	if fileID <= 0 || claimedFiles[fileID] {
		return 0, false
	}
	claimedFiles[fileID] = true
	return fileID, true
}

func resolveFiles(ctx context.Context, reader Reader, scope ProfileScope, contentIDs []string) (map[string]int64, error) {
	if len(contentIDs) == 0 {
		return map[string]int64{}, nil
	}
	fileIDs, err := reader.FilesByContentIDs(ctx, scope, contentIDs)
	if err != nil {
		return nil, fmt.Errorf("watchdoc: reading media files: %w", err)
	}
	if fileIDs == nil {
		return map[string]int64{}, nil
	}
	return fileIDs, nil
}

// playableContentIDs returns the identifiers that need a file before they can
// be listed. A series is a container: its playability is decided per episode on
// its detail document.
func playableContentIDs(items []Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind == KindMovie {
			ids = append(ids, strings.TrimSpace(item.ContentID))
		}
	}
	return ids
}

func episodeContentIDs(episodes []Episode) []string {
	ids := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		ids = append(ids, episode.ContentID)
	}
	return ids
}

func containsContentID(items []DocumentItem, contentID string) bool {
	for _, item := range items {
		if item.ContentID == contentID {
			return true
		}
	}
	return false
}

func isLibraryKind(kind string) bool {
	return kind == KindMovie || kind == KindSeries
}
