package watchdoc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Silo-Server/silo-server/internal/watchdoc"
)

// contractsRootEnv is the environment variable that points at the
// vondel-client-contracts checkout holding the versioned Watch schema. The
// documents these tests build are only worth anything if they are validated
// against the schema both TV clients compile their decoders from, so the
// schema is read from the contracts repository rather than restated here as a
// hand-written field list that could drift away from it.
const contractsRootEnv = "VONDEL_CONTRACTS_ROOT"

// contractsRoot locates the contracts checkout: the environment variable
// first, then the variable the repository's existing conformance test already
// uses, then the standard adjacent checkout. When none of them resolve the
// test skips and names the variable to set — a silent pass would let a
// non-conforming document ship.
func contractsRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv(contractsRootEnv),
		os.Getenv("VONDEL_CLIENT_CONTRACTS_DIR"),
		filepath.Join("..", "..", "..", "vondel-client-contracts"),
	}
	var looked []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		looked = append(looked, abs)
		if _, err := os.Stat(filepath.Join(abs, "schema", "watch", "document.schema.json")); err == nil {
			return abs
		}
	}
	t.Skipf("watch document schema unavailable: set %s to a vondel-client-contracts checkout (looked in %s)",
		contractsRootEnv, strings.Join(looked, ", "))
	return ""
}

func watchDocumentSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join(contractsRoot(t), "schema", "watch", "document.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read watch document schema: %v", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode watch document schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	// Format assertions are advisory by default in draft 2020-12; the clients
	// parse snapshot/updated_at as RFC 3339, so assert them here.
	compiler.AssertFormat()
	if err := compiler.AddResource("watch-document.json", document); err != nil {
		t.Fatalf("add watch document schema: %v", err)
	}
	schema, err := compiler.Compile("watch-document.json")
	if err != nil {
		t.Fatalf("compile watch document schema: %v", err)
	}
	return schema
}

// assertConformsToContract validates the encoded document against the
// contracts schema and re-checks the two invariants both client validators
// enforce beyond the schema: file identifiers are unique, and no content
// identifier appears twice.
func assertConformsToContract(t *testing.T, document watchdoc.Document) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("decode encoded document: %v", err)
	}
	if err := watchDocumentSchema(t).Validate(value); err != nil {
		t.Fatalf("document does not conform to watch_document_v1: %v\nbody: %s", err, encoded)
	}

	body, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("document is not a JSON object: %s", encoded)
	}
	seenContent := map[string]bool{}
	seenFile := map[float64]bool{}
	items, _ := body["items"].([]any)
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("item is not a JSON object: %s", encoded)
		}
		contentID, _ := item["content_id"].(string)
		if seenContent[contentID] {
			t.Errorf("duplicate content_id %q in items: %s", contentID, encoded)
		}
		seenContent[contentID] = true
		if fileID, present := item["file_id"]; present {
			id, ok := fileID.(float64)
			if !ok || id <= 0 {
				t.Errorf("file_id %#v for %q is not a positive number", fileID, contentID)
				continue
			}
			if seenFile[id] {
				t.Errorf("duplicate file_id %v in items: %s", id, encoded)
			}
			seenFile[id] = true
		}
	}
	if snapshot, _ := body["snapshot"].(string); snapshot != "" {
		if _, err := time.Parse(time.RFC3339, snapshot); err != nil {
			t.Errorf("snapshot %q is not RFC3339: %v", snapshot, err)
		}
	}
	return body
}

// --- fake reader -----------------------------------------------------------

// fakeReader stands in for the catalog, episode, media-file and progress
// stores. It applies the profile's library restrictions itself, exactly as the
// database-backed reader does, so composition can be exercised without a
// database.
type fakeReader struct {
	items      []watchdoc.Item
	episodes   map[string][]watchdoc.Episode
	files      map[string]int64
	progress   []watchdoc.Progress
	markers    map[int64]watchdoc.FileMarkers
	cast       map[string][]watchdoc.CastMember
	crew       map[string][]watchdoc.CrewMember
	restricted map[string]bool // content IDs this profile may not see

	err error

	itemsCalls    int
	progressCalls [][]string
	fileScopes    []watchdoc.ProfileScope
	markersCalls  [][]int64
	creditsCalls  []string
}

func (f *fakeReader) visible(contentID string) bool { return !f.restricted[contentID] }

func (f *fakeReader) Items(_ context.Context, _ watchdoc.ProfileScope) ([]watchdoc.Item, error) {
	f.itemsCalls++
	if f.err != nil {
		return nil, f.err
	}
	var out []watchdoc.Item
	for _, item := range f.items {
		if f.visible(item.ContentID) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeReader) Item(_ context.Context, _ watchdoc.ProfileScope, contentID string) (watchdoc.Item, bool, error) {
	if f.err != nil {
		return watchdoc.Item{}, false, f.err
	}
	for _, item := range f.items {
		if item.ContentID == contentID && f.visible(contentID) {
			return item, true, nil
		}
	}
	return watchdoc.Item{}, false, nil
}

func (f *fakeReader) Episodes(_ context.Context, _ watchdoc.ProfileScope, seriesID string) ([]watchdoc.Episode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.episodes[seriesID], nil
}

func (f *fakeReader) FilesByContentIDs(_ context.Context, scope watchdoc.ProfileScope, contentIDs []string) (map[string]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.fileScopes = append(f.fileScopes, scope)
	out := map[string]int64{}
	for _, id := range contentIDs {
		if fileID, ok := f.files[id]; ok {
			out[id] = fileID
		}
	}
	return out, nil
}

func (f *fakeReader) Progress(_ context.Context, _ watchdoc.ProfileScope, contentIDs []string) ([]watchdoc.Progress, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.progressCalls = append(f.progressCalls, append([]string(nil), contentIDs...))
	wanted := map[string]bool{}
	for _, id := range contentIDs {
		wanted[id] = true
	}
	var out []watchdoc.Progress
	for _, row := range f.progress {
		if wanted[row.ContentID] {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeReader) Markers(_ context.Context, _ watchdoc.ProfileScope, fileIDs []int64) (map[int64]watchdoc.FileMarkers, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.markersCalls = append(f.markersCalls, append([]int64(nil), fileIDs...))
	out := map[int64]watchdoc.FileMarkers{}
	for _, id := range fileIDs {
		if markers, ok := f.markers[id]; ok {
			out[id] = markers
		}
	}
	return out, nil
}

func (f *fakeReader) Credits(_ context.Context, _ watchdoc.ProfileScope, contentID string) ([]watchdoc.CastMember, []watchdoc.CrewMember, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	f.creditsCalls = append(f.creditsCalls, contentID)
	return f.cast[contentID], f.crew[contentID], nil
}

// --- invented fixture world ------------------------------------------------

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed.UTC()
}

func timePtr(value time.Time) *time.Time { return &value }

// inventedWorld is an entirely invented library: no real title, person or
// hostname appears in it.
func inventedWorld(t *testing.T) *fakeReader {
	t.Helper()
	return &fakeReader{
		items: []watchdoc.Item{
			{
				Kind:           watchdoc.KindMovie,
				ContentID:      "4242",
				Title:          "The Invented Crossing",
				Year:           2026,
				RuntimeSeconds: 6480,
				Rating:         "PG",
				Overview:       "A harbor surveyor follows a light that appears only after midnight.",
				Genres:         []string{"Drama"},
				AddedAt:        timePtr(at(t, "2026-08-12T09:00:00Z")),
			},
			{
				Kind:           watchdoc.KindMovie,
				ContentID:      "1717",
				Title:          "Nine Lanterns Down",
				Year:           2025,
				RuntimeSeconds: 5400,
				Rating:         "PG-13",
				AddedAt:        timePtr(at(t, "2026-08-13T09:00:00Z")),
			},
			{
				Kind:        watchdoc.KindSeries,
				ContentID:   "8080",
				Title:       "Eight Quiet Rooms",
				Year:        2026,
				SeasonCount: 2,
				Overview:    "Residents of an unfinished building discover that every empty room remembers a different visitor.",
				AddedAt:     timePtr(at(t, "2026-08-11T09:00:00Z")),
			},
			{
				Kind:      watchdoc.KindMovie,
				ContentID: "9001",
				Title:     "The Sealed Wing",
				Year:      2026,
				Rating:    "R",
				AddedAt:   timePtr(at(t, "2026-08-14T09:00:00Z")),
			},
		},
		episodes: map[string][]watchdoc.Episode{
			"8080": {
				{ContentID: "8080-s02e02", SeriesID: "8080", SeasonNumber: 2, EpisodeNumber: 2, Title: "Eight Chairs at Dawn", RuntimeSeconds: 2700, SeasonTitle: "Blue Hour"},
				{ContentID: "8080-s01e02", SeriesID: "8080", SeasonNumber: 1, EpisodeNumber: 2, Title: "Echoes in the Stairwell", RuntimeSeconds: 2700, SeasonTitle: "Lantern Floor"},
				{ContentID: "8080-s01e01", SeriesID: "8080", SeasonNumber: 1, EpisodeNumber: 1, Title: "The First Locked Room", RuntimeSeconds: 2700, SeasonTitle: "Lantern Floor"},
				{ContentID: "8080-s02e01", SeriesID: "8080", SeasonNumber: 2, EpisodeNumber: 1, Title: "The Room That Kept Time", RuntimeSeconds: 2700, SeasonTitle: "Blue Hour"},
			},
		},
		files: map[string]int64{
			"4242":        4242001,
			"1717":        1717001,
			"9001":        9001001,
			"8080-s01e01": 8080001,
			"8080-s01e02": 8080002,
			"8080-s02e01": 8080004,
			"8080-s02e02": 8080005,
		},
		progress: []watchdoc.Progress{
			{ContentID: "4242", PositionSeconds: 1234.5, DurationSeconds: 6480, UpdatedAt: at(t, "2026-08-13T11:45:00Z")},
			{ContentID: "1717", PositionSeconds: 5300, DurationSeconds: 5400, Completed: true, UpdatedAt: at(t, "2026-08-13T10:00:00Z")},
			{ContentID: "8080", EpisodeID: "8080-s01e02", PositionSeconds: 960, DurationSeconds: 2700, UpdatedAt: at(t, "2026-08-13T11:50:00Z")},
		},
	}
}

func itemKinds(t *testing.T, body map[string]any) []string {
	t.Helper()
	var kinds []string
	items, _ := body["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind, _ := item["kind"].(string)
		kinds = append(kinds, kind)
	}
	return kinds
}

func contentIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	var ids []string
	items, _ := body["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		id, _ := item["content_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- home ------------------------------------------------------------------

func TestWatchHomeListsMoviesAndSeriesWithDeterministicFeatured(t *testing.T) {
	reader := inventedWorld(t)
	scope := watchdoc.ProfileScope{UserID: 7, ProfileID: "profile-invented"}

	document, err := watchdoc.ComposeHome(context.Background(), reader, scope)
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)

	if document.Schema != "watch_document_v1" {
		t.Errorf("schema = %q, want watch_document_v1", document.Schema)
	}
	// Most recently added first, content ID ascending on ties: 9001, 1717,
	// 4242, 8080.
	want := []string{"9001", "1717", "4242", "8080"}
	if got := contentIDs(t, body); !equalStrings(got, want) {
		t.Errorf("home item order = %v, want %v", got, want)
	}
	if got := itemKinds(t, body); !equalStrings(got, []string{"movie", "movie", "movie", "series"}) {
		t.Errorf("home item kinds = %v", got)
	}
	// 9001 is the most recently added movie and has no completed progress row.
	if document.FeaturedContentID != "9001" {
		t.Errorf("featured_content_id = %q, want 9001", document.FeaturedContentID)
	}
	// Composition must be a pure function of the reader's answers.
	repeat, err := watchdoc.ComposeHome(context.Background(), reader, scope)
	if err != nil {
		t.Fatalf("recompose home: %v", err)
	}
	if repeat.FeaturedContentID != document.FeaturedContentID {
		t.Errorf("featured_content_id is not stable: %q then %q", document.FeaturedContentID, repeat.FeaturedContentID)
	}
}

func TestWatchHomeFeaturedSkipsCompletedMoviesAndFallsBack(t *testing.T) {
	reader := inventedWorld(t)
	// Every movie completed: the rule falls back to the first item in
	// document order rather than naming a watched title.
	reader.progress = []watchdoc.Progress{
		{ContentID: "9001", PositionSeconds: 100, DurationSeconds: 200, Completed: true, UpdatedAt: at(t, "2026-08-13T12:00:00Z")},
		{ContentID: "1717", PositionSeconds: 5300, DurationSeconds: 5400, Completed: true, UpdatedAt: at(t, "2026-08-13T10:00:00Z")},
		{ContentID: "4242", PositionSeconds: 6400, DurationSeconds: 6480, Completed: true, UpdatedAt: at(t, "2026-08-13T11:45:00Z")},
	}
	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	assertConformsToContract(t, document)
	if document.FeaturedContentID != "9001" {
		t.Errorf("fallback featured_content_id = %q, want the first item 9001", document.FeaturedContentID)
	}
}

func TestWatchHomeWithNoItemsOmitsFeaturedAndKeepsArrays(t *testing.T) {
	reader := &fakeReader{}
	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-empty"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)
	if document.FeaturedContentID != "" {
		t.Errorf("featured_content_id = %q, want empty for an empty library", document.FeaturedContentID)
	}
	if _, ok := body["featured_content_id"]; ok {
		t.Error("featured_content_id must be omitted rather than empty")
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %#v, want an empty array", body["items"])
	}
	if progress, ok := body["progress"].([]any); !ok || len(progress) != 0 {
		t.Errorf("progress = %#v, want an empty array", body["progress"])
	}
}

func TestWatchHomeProgressIsExactlyTheProfileRowsForListedItems(t *testing.T) {
	reader := inventedWorld(t)
	// A row for an item the document does not list must never be emitted: the
	// client cannot attribute it, and it would name an item the profile was
	// not shown.
	reader.progress = append(reader.progress, watchdoc.Progress{
		ContentID: "3030", PositionSeconds: 10, DurationSeconds: 100, UpdatedAt: at(t, "2026-08-13T12:00:00Z"),
	})

	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)

	listed := map[string]bool{}
	for _, id := range contentIDs(t, body) {
		listed[id] = true
	}
	rows, _ := body["progress"].([]any)
	if len(rows) != 3 {
		t.Fatalf("progress rows = %d, want 3", len(rows))
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id, _ := row["content_id"].(string)
		if !listed[id] {
			t.Errorf("progress row for %q is not in items", id)
		}
	}
	// The episode row is attributable only through its series, and carries the
	// episode identifier the client needs to key the checkpoint.
	var episodeRow map[string]any
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["content_id"] == "8080" {
			episodeRow = row
		}
	}
	if episodeRow == nil {
		t.Fatal("no progress row for the series 8080")
	}
	if episodeRow["episode_id"] != "8080-s01e02" {
		t.Errorf("series progress episode_id = %#v, want 8080-s01e02", episodeRow["episode_id"])
	}
	// A movie row must not carry an episode identifier.
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["content_id"] == "4242" {
			if _, ok := row["episode_id"]; ok {
				t.Errorf("movie progress row carries episode_id: %#v", row)
			}
		}
	}
}

func TestWatchHomeNeverNamesARestrictedItemInItemsOrProgress(t *testing.T) {
	reader := inventedWorld(t)
	// The profile may not see the R-rated movie or the series.
	reader.restricted = map[string]bool{"9001": true, "8080": true}
	// A leaking progress store is not a license to name the item: the reader
	// still answers with rows for both restricted items.
	reader.progress = append(reader.progress,
		watchdoc.Progress{ContentID: "9001", PositionSeconds: 30, DurationSeconds: 300, UpdatedAt: at(t, "2026-08-13T12:00:00Z")},
	)

	document, err := watchdoc.ComposeHome(context.Background(), reader,
		watchdoc.ProfileScope{ProfileID: "profile-restricted", AllowedLibraryIDs: []int{3}, MaxContentRating: "PG-13"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)

	for _, id := range contentIDs(t, body) {
		if id == "9001" || id == "8080" {
			t.Errorf("restricted item %q appears in items", id)
		}
	}
	rows, _ := body["progress"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["content_id"] == "9001" || row["content_id"] == "8080" {
			t.Errorf("restricted item appears in progress: %#v", row)
		}
	}
	if document.FeaturedContentID == "9001" {
		t.Error("featured_content_id names a restricted item")
	}
	// The reader is asked for progress only for items the profile may see.
	if len(reader.progressCalls) != 1 {
		t.Fatalf("progress lookups = %d, want 1", len(reader.progressCalls))
	}
	for _, id := range reader.progressCalls[0] {
		if id == "9001" || id == "8080" {
			t.Errorf("progress was requested for restricted item %q", id)
		}
	}
}

func TestWatchHomeOmitsItemsWithoutAPlayableFile(t *testing.T) {
	reader := inventedWorld(t)
	delete(reader.files, "1717")

	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)

	for _, id := range contentIDs(t, body) {
		if id == "1717" {
			t.Error("a movie with no playable file is listed")
		}
	}
	items, _ := body["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if fileID, ok := item["file_id"]; ok {
			if value, _ := fileID.(float64); value <= 0 {
				t.Errorf("item %#v carries a non-positive file_id", item)
			}
		} else if item["kind"] != "series" {
			t.Errorf("playable item %#v carries no file_id", item)
		}
	}
}

func TestWatchHomeDropsDuplicateContentIDsAndFileIDs(t *testing.T) {
	reader := inventedWorld(t)
	// A store anomaly must not produce a document every client validator
	// rejects whole. The duplicate series rows are the case no other rule can
	// mask: a series carries no file identifier, so nothing but the
	// deduplication itself can stop the second row.
	reader.items = append(reader.items,
		watchdoc.Item{
			Kind: watchdoc.KindSeries, ContentID: "8080", Title: "Eight Quiet Rooms (second membership row)",
			AddedAt: timePtr(at(t, "2026-08-05T09:00:00Z")),
		},
		watchdoc.Item{
			Kind: watchdoc.KindSeries, ContentID: "8080", Title: "Eight Quiet Rooms (third membership row)",
			AddedAt: timePtr(at(t, "2026-08-14T09:00:00Z")),
		},
		watchdoc.Item{
			Kind: watchdoc.KindMovie, ContentID: "4242", Title: "The Invented Crossing (duplicate row)",
			AddedAt: timePtr(at(t, "2026-08-10T09:00:00Z")),
		},
		watchdoc.Item{
			Kind: watchdoc.KindMovie, ContentID: "5150", Title: "A Second Claim on One File",
			AddedAt: timePtr(at(t, "2026-08-09T09:00:00Z")),
		},
	)
	reader.files["5150"] = 4242001 // already claimed by 4242

	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)
	seen := map[string]int{}
	for _, id := range contentIDs(t, body) {
		seen[id]++
	}
	if seen["8080"] != 1 {
		t.Errorf("content_id 8080 appears %d times, want 1", seen["8080"])
	}
	if seen["4242"] != 1 {
		t.Errorf("content_id 4242 appears %d times, want 1", seen["4242"])
	}
	if seen["5150"] != 0 {
		t.Error("an item claiming an already-used file_id is listed")
	}
	// The surviving duplicate is the most recently added row, deterministically.
	for _, raw := range body["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["content_id"] == "8080" && item["title"] != "Eight Quiet Rooms (third membership row)" {
			t.Errorf("surviving duplicate = %#v, want the most recently added row", item["title"])
		}
	}
}

func TestWatchFileLookupsCarryTheViewerScope(t *testing.T) {
	reader := inventedWorld(t)
	scope := watchdoc.ProfileScope{
		ProfileID:          "profile-restricted",
		AllowedLibraryIDs:  []int{4},
		MaxPlaybackQuality: "1080p",
	}
	if _, err := watchdoc.ComposeHome(context.Background(), reader, scope); err != nil {
		t.Fatalf("compose home: %v", err)
	}
	if len(reader.fileScopes) == 0 {
		t.Fatal("no file lookup was made")
	}
	// A file identifier the viewer cannot play is worse than none: the client
	// renders a Play button that the playback endpoint then refuses. The scope
	// has to reach the file lookup for it to be filtered there.
	for _, seen := range reader.fileScopes {
		if seen.MaxPlaybackQuality != "1080p" || len(seen.AllowedLibraryIDs) != 1 {
			t.Errorf("file lookup scope = %#v, want the viewer's ceiling and libraries", seen)
		}
	}
}

func TestWatchHomeSurfacesReaderFailures(t *testing.T) {
	reader := &fakeReader{err: errors.New("catalog unavailable")}
	if _, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "p"}); err == nil {
		t.Fatal("compose home succeeded with a failing reader")
	}
}

// --- detail ----------------------------------------------------------------

func TestWatchSeriesDetailOrdersSeasonsAndEpisodes(t *testing.T) {
	reader := inventedWorld(t)

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "8080")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	if document.FeaturedContentID != "8080" {
		t.Errorf("featured_content_id = %q, want the requested item", document.FeaturedContentID)
	}
	want := []string{"8080", "8080-s01e01", "8080-s01e02", "8080-s02e01", "8080-s02e02"}
	if got := contentIDs(t, body); !equalStrings(got, want) {
		t.Fatalf("series detail order = %v, want %v", got, want)
	}

	items, _ := body["items"].([]any)
	series, _ := items[0].(map[string]any)
	if series["kind"] != "series" {
		t.Errorf("first item kind = %#v, want series", series["kind"])
	}
	seasons, _ := series["seasons"].([]any)
	if len(seasons) != 2 {
		t.Fatalf("seasons = %#v, want two", series["seasons"])
	}
	first, _ := seasons[0].(map[string]any)
	second, _ := seasons[1].(map[string]any)
	if first["season_number"] != float64(1) || second["season_number"] != float64(2) {
		t.Errorf("seasons are not ascending: %#v", seasons)
	}
	if first["title"] != "Lantern Floor" || second["title"] != "Blue Hour" {
		t.Errorf("season titles = %#v", seasons)
	}

	for _, raw := range items[1:] {
		episode, _ := raw.(map[string]any)
		if episode["kind"] != "episode" {
			t.Fatalf("item %#v is not an episode", episode)
		}
		for _, field := range []string{"series_id", "season_number", "episode_number", "file_id"} {
			if _, ok := episode[field]; !ok {
				t.Errorf("episode %#v is missing %s", episode, field)
			}
		}
		if episode["series_id"] != "8080" {
			t.Errorf("episode series_id = %#v, want 8080", episode["series_id"])
		}
	}

	// The series' own progress row travels with the detail document.
	rows, _ := body["progress"].([]any)
	if len(rows) != 1 {
		t.Fatalf("progress rows = %d, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if row["content_id"] != "8080" || row["episode_id"] != "8080-s01e02" {
		t.Errorf("series progress row = %#v", row)
	}
}

func TestWatchSeriesDetailDropsEpisodesThatCannotBeValidated(t *testing.T) {
	reader := inventedWorld(t)
	reader.episodes["8080"] = append(reader.episodes["8080"],
		// A special: season 0 is below the contract's minimum of 1.
		watchdoc.Episode{ContentID: "8080-s00e01", SeriesID: "8080", SeasonNumber: 0, EpisodeNumber: 1, Title: "A Room Before the Building"},
		// Cataloged but not on disk: no file identifier to play.
		watchdoc.Episode{ContentID: "8080-s02e03", SeriesID: "8080", SeasonNumber: 2, EpisodeNumber: 3, Title: "The Ninth Room"},
	)
	reader.files["8080-s00e01"] = 8080000

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "8080")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)
	for _, id := range contentIDs(t, body) {
		if id == "8080-s00e01" {
			t.Error("a season-zero episode is listed; the client validator requires season_number >= 1")
		}
		if id == "8080-s02e03" {
			t.Error("an episode with no playable file is listed")
		}
	}
}

func TestWatchMovieDetailCarriesItsFileIdentifier(t *testing.T) {
	reader := inventedWorld(t)

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("movie detail items = %d, want 1", len(items))
	}
	movie, _ := items[0].(map[string]any)
	if movie["kind"] != "movie" || movie["content_id"] != "4242" {
		t.Fatalf("movie detail item = %#v", movie)
	}
	if movie["file_id"] != float64(4242001) {
		t.Errorf("movie file_id = %#v, want 4242001", movie["file_id"])
	}
	if movie["runtime_seconds"] != float64(6480) {
		t.Errorf("movie runtime_seconds = %#v, want 6480", movie["runtime_seconds"])
	}
	if document.FeaturedContentID != "4242" {
		t.Errorf("featured_content_id = %q, want 4242", document.FeaturedContentID)
	}
	rows, _ := body["progress"].([]any)
	if len(rows) != 1 {
		t.Fatalf("progress rows = %d, want 1", len(rows))
	}
}

func TestWatchMovieDetailCarriesChaptersAndSkipIntroForItsFile(t *testing.T) {
	reader := inventedWorld(t)
	introStart, introEnd := 0.0, 62.5
	reader.markers = map[int64]watchdoc.FileMarkers{
		4242001: {
			Chapters: []watchdoc.Chapter{
				{Index: 0, Title: "Opening", StartSeconds: 0, EndSeconds: 62.5},
				{Index: 1, Title: "Arrival", StartSeconds: 62.5, EndSeconds: 610},
			},
			IntroStart: &introStart,
			IntroEnd:   &introEnd,
		},
	}

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	items, _ := body["items"].([]any)
	movie, _ := items[0].(map[string]any)
	chapters, _ := movie["chapters"].([]any)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(chapters))
	}
	first, _ := chapters[0].(map[string]any)
	if first["title"] != "Opening" || first["end_seconds"] != 62.5 {
		t.Errorf("first chapter = %#v", first)
	}
	if movie["intro_start_seconds"] != 0.0 || movie["intro_end_seconds"] != 62.5 {
		t.Errorf("intro range = %#v..%#v, want 0..62.5", movie["intro_start_seconds"], movie["intro_end_seconds"])
	}
	if len(reader.markersCalls) != 1 || len(reader.markersCalls[0]) != 1 || reader.markersCalls[0][0] != 4242001 {
		t.Errorf("markers requested = %v, want exactly [4242001]", reader.markersCalls)
	}
}

func TestWatchMovieDetailWithNoKnownMarkersOmitsTheFields(t *testing.T) {
	reader := inventedWorld(t)

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	items, _ := body["items"].([]any)
	movie, _ := items[0].(map[string]any)
	if _, present := movie["chapters"]; present {
		t.Errorf("chapters present with nothing known: %#v", movie["chapters"])
	}
	if _, present := movie["intro_start_seconds"]; present {
		t.Errorf("intro_start_seconds present with nothing known: %#v", movie["intro_start_seconds"])
	}
}

func TestWatchMovieDetailCarriesCastAndCrew(t *testing.T) {
	reader := inventedWorld(t)
	reader.cast = map[string][]watchdoc.CastMember{
		"4242": {{PersonID: "1", Name: "Invented Actor", Character: "The Surveyor", PhotoURL: "https://example/a.jpg"}},
	}
	reader.crew = map[string][]watchdoc.CrewMember{
		"4242": {{PersonID: "2", Name: "Invented Director", Job: "Director"}},
	}

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	items, _ := body["items"].([]any)
	movie, _ := items[0].(map[string]any)
	cast, _ := movie["cast"].([]any)
	if len(cast) != 1 {
		t.Fatalf("cast = %d, want 1", len(cast))
	}
	member, _ := cast[0].(map[string]any)
	if member["name"] != "Invented Actor" || member["character"] != "The Surveyor" {
		t.Errorf("cast member = %#v", member)
	}
	crew, _ := movie["crew"].([]any)
	if len(crew) != 1 {
		t.Fatalf("crew = %d, want 1", len(crew))
	}
	if reader.creditsCalls[0] != "4242" {
		t.Errorf("credits requested for %v, want [4242]", reader.creditsCalls)
	}
}

func TestWatchMovieDetailWithNoKnownCreditsOmitsTheFields(t *testing.T) {
	reader := inventedWorld(t)

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	items, _ := body["items"].([]any)
	movie, _ := items[0].(map[string]any)
	if _, present := movie["cast"]; present {
		t.Errorf("cast present with nothing known: %#v", movie["cast"])
	}
	if _, present := movie["crew"]; present {
		t.Errorf("crew present with nothing known: %#v", movie["crew"])
	}
}

func TestWatchHomeDocumentNeverAsksForCredits(t *testing.T) {
	reader := inventedWorld(t)
	scope := watchdoc.ProfileScope{UserID: 7, ProfileID: "profile-invented"}

	if _, err := watchdoc.ComposeHome(context.Background(), reader, scope); err != nil {
		t.Fatalf("compose home: %v", err)
	}
	if len(reader.creditsCalls) != 0 {
		t.Errorf("home document called Credits %d times, want 0", len(reader.creditsCalls))
	}
}

func TestWatchHomeDocumentNeverAsksForMarkers(t *testing.T) {
	reader := inventedWorld(t)
	scope := watchdoc.ProfileScope{UserID: 7, ProfileID: "profile-invented"}

	if _, err := watchdoc.ComposeHome(context.Background(), reader, scope); err != nil {
		t.Fatalf("compose home: %v", err)
	}
	if len(reader.markersCalls) != 0 {
		t.Errorf("home document called Markers %d times, want 0 — see FileMarkers' own doc for why", len(reader.markersCalls))
	}
}

func TestWatchMovieDetailWithoutAPlayableFileKeepsTheItem(t *testing.T) {
	reader := inventedWorld(t)
	delete(reader.files, "4242")

	document, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "4242")
	if err != nil {
		t.Fatalf("compose item: %v", err)
	}
	body := assertConformsToContract(t, document)

	// A detail screen the viewer navigated to must render, with Play
	// unavailable — a blank document is the worse answer. The home document
	// still omits the item; only the detail root is exempt from the file gate.
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("movie detail items = %d, want the movie itself", len(items))
	}
	movie, _ := items[0].(map[string]any)
	if movie["content_id"] != "4242" {
		t.Fatalf("movie detail item = %#v", movie)
	}
	if _, ok := movie["file_id"]; ok {
		t.Errorf("movie with no playable file carries file_id %#v", movie["file_id"])
	}
	if document.FeaturedContentID != "4242" {
		t.Errorf("featured_content_id = %q, want the requested item", document.FeaturedContentID)
	}
}

func TestWatchHomeStillOmitsAMovieWithoutAPlayableFile(t *testing.T) {
	reader := inventedWorld(t)
	delete(reader.files, "4242")

	document, err := watchdoc.ComposeHome(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"})
	if err != nil {
		t.Fatalf("compose home: %v", err)
	}
	body := assertConformsToContract(t, document)
	for _, id := range contentIDs(t, body) {
		if id == "4242" {
			t.Error("home lists a movie with no playable file")
		}
	}
}

func TestWatchSeriesDetailLogsEveryDroppedEpisode(t *testing.T) {
	reader := inventedWorld(t)
	reader.episodes["8080"] = append(reader.episodes["8080"],
		watchdoc.Episode{ContentID: "8080-s00e01", SeriesID: "8080", SeasonNumber: 0, EpisodeNumber: 1, Title: "A Room Before the Building"},
		watchdoc.Episode{ContentID: "8080-s02e03", SeriesID: "8080", SeasonNumber: 2, EpisodeNumber: 3, Title: "The Ninth Room"},
		watchdoc.Episode{ContentID: "8080-s02e04", SeriesID: "8080", SeasonNumber: 2, EpisodeNumber: 4},
	)
	reader.files["8080-s00e01"] = 8080000

	var captured bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&captured, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if _, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "8080"); err != nil {
		t.Fatalf("compose item: %v", err)
	}

	// An episode that silently vanishes from a series is a support ticket
	// nobody can answer. Every drop names the series, the episode and why.
	logged := captured.String()
	for _, want := range []string{"8080-s00e01", "8080-s02e03", "8080-s02e04"} {
		if !strings.Contains(logged, want) {
			t.Errorf("dropped episode %s is not in the log:\n%s", want, logged)
		}
	}
	for _, want := range []string{"season_or_episode_number_below_one", "no_playable_file", "missing_title"} {
		if !strings.Contains(logged, want) {
			t.Errorf("drop reason %s is not in the log:\n%s", want, logged)
		}
	}
	if !strings.Contains(logged, "series_id=8080") {
		t.Errorf("drops do not name the series:\n%s", logged)
	}
}

func TestWatchDetailForUnknownContentIDIsNotFound(t *testing.T) {
	reader := inventedWorld(t)
	_, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-invented"}, "not-a-content-id")
	if !errors.Is(err, watchdoc.ErrItemNotFound) {
		t.Fatalf("error = %v, want ErrItemNotFound", err)
	}
}

func TestWatchDetailForARestrictedItemIsNotFound(t *testing.T) {
	reader := inventedWorld(t)
	reader.restricted = map[string]bool{"9001": true}
	_, err := watchdoc.ComposeItem(context.Background(), reader, watchdoc.ProfileScope{ProfileID: "profile-restricted"}, "9001")
	if !errors.Is(err, watchdoc.ErrItemNotFound) {
		t.Fatalf("error = %v, want ErrItemNotFound for a restricted item", err)
	}
}
