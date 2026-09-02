package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

// The invented world this test seeds. No real title, person, path or hostname
// appears in it.
const (
	watchMovieLibraryID  = 1
	watchSeriesLibraryID = 2

	watchMovieContentID  = "4242"
	watchSeriesContentID = "8080"
	watchEpisodeOneID    = "8080-s01e01"
	watchEpisodeTwoID    = "8080-s01e02"

	watchMovieFileID    = 4242001
	watchEpisodeOneFile = 8080001
	watchEpisodeTwoFile = 8080002

	// watchFillerCount fills the recently-added window exactly, so the seeded
	// pair can only reach the document through the in-progress union.
	watchFillerCount = 100
	// watchForgottenContentID is the control: old, playable, and never watched,
	// so it must stay out of the document once the window is full.
	watchForgottenContentID = "3003"
)

// clientSurfaceFixture is a real router over a disposable database with one
// account, one profile and a usable access token: the starting point for any
// test that drives the native client surface end to end.
type clientSurfaceFixture struct {
	pool      *pgxpool.Pool
	provider  userstore.UserStoreProvider
	router    http.Handler
	token     string
	userID    int
	profileID string
}

func newClientSurfaceFixture(t *testing.T) clientSurfaceFixture {
	t.Helper()
	pool := newWatchDatabase(t)
	ctx := context.Background()
	provider := pgstore.NewPostgresProvider(pool)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	router := NewRouter(Dependencies{
		DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "client-surface-mount-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     provider,
		FileRepo:              scanner.NewFileRepository(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"watch.viewer","email":"watch.viewer@example.invalid",
		"password":"invented password for a disposable database",
		"create_default_profile":true,"default_profile_name":"Watch Viewer"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}

	fixture := clientSurfaceFixture{pool: pool, provider: provider, router: router, token: decodeLogin(t, setup).AccessToken}
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'watch.viewer'`).Scan(&fixture.userID); err != nil {
		t.Fatalf("load account: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, fixture.userID).Scan(&fixture.profileID); err != nil {
		t.Fatalf("load primary profile: %v", err)
	}
	return fixture
}

func (f clientSurfaceFixture) profileHeaders() map[string]string {
	return map[string]string{"X-Profile-Id": f.profileID}
}

// TestWatchDocumentsAreServedByTheRealRouter drives the real router end to end:
// the routes are mounted inside the authenticated, viewer-scoped,
// profile-scoped group on /api/bloem/v1, the database-backed reader runs its real
// queries, and both documents conform to the contracts schema. The
// handler-level tests use a fake reader, so this is the only place the
// adapter's SQL is exercised.
func TestWatchDocumentsAreServedByTheRealRouter(t *testing.T) {
	fixture := newClientSurfaceFixture(t)
	ctx := context.Background()
	pool, provider, router := fixture.pool, fixture.provider, fixture.router
	token, profileID := fixture.token, fixture.profileID

	seedWatchCatalog(t, pool)
	seedWatchProgress(t, provider, fixture.userID, profileID)

	profileHeaders := fixture.profileHeaders()

	t.Run("unauthenticated", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/home", "", "", profileHeaders)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("home without a token = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("without a profile", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/home", "", token, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("home without a profile = %d %s", response.Code, response.Body.String())
		}
		// The vocabulary the handler's own guard must agree with: one refusal,
		// one spelling, whichever layer answers it.
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode refusal: %v", err)
		}
		if body["error"] != "bad_request" {
			t.Errorf("error = %#v, want bad_request", body["error"])
		}
	})

	t.Run("home", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/home", "", token, profileHeaders)
		if response.Code != http.StatusOK {
			t.Fatalf("home = %d %s", response.Code, response.Body.String())
		}
		document := assertWatchDocumentConforms(t, response.Body.Bytes())
		ids := watchContentIDs(document)
		if !contains(ids, watchMovieContentID) || !contains(ids, watchSeriesContentID) {
			t.Fatalf("home items = %v, want the seeded movie and series", ids)
		}
		if document["featured_content_id"] != watchMovieContentID {
			t.Errorf("featured_content_id = %#v, want %s", document["featured_content_id"], watchMovieContentID)
		}
		for _, item := range watchItems(document) {
			if item["content_id"] == watchMovieContentID && item["file_id"] != float64(watchMovieFileID) {
				t.Errorf("movie file_id = %#v, want %d", item["file_id"], watchMovieFileID)
			}
		}
		// The seeded progress rows: the movie's own row, and the series' row
		// carried under the series with its episode named.
		rows := watchProgressRows(document)
		if len(rows) != 2 {
			t.Fatalf("progress rows = %d, want 2: %s", len(rows), response.Body.String())
		}
		for _, row := range rows {
			switch row["content_id"] {
			case watchMovieContentID:
				if _, ok := row["episode_id"]; ok {
					t.Errorf("movie progress row carries episode_id: %#v", row)
				}
			case watchSeriesContentID:
				if row["episode_id"] != watchEpisodeOneID {
					t.Errorf("series progress episode_id = %#v, want %s", row["episode_id"], watchEpisodeOneID)
				}
			default:
				t.Errorf("unexpected progress row: %#v", row)
			}
		}
	})

	t.Run("series detail", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/items/"+watchSeriesContentID, "", token, profileHeaders)
		if response.Code != http.StatusOK {
			t.Fatalf("series detail = %d %s", response.Code, response.Body.String())
		}
		document := assertWatchDocumentConforms(t, response.Body.Bytes())
		want := []string{watchSeriesContentID, watchEpisodeOneID, watchEpisodeTwoID}
		if got := watchContentIDs(document); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("series detail items = %v, want %v", got, want)
		}
		items := watchItems(document)
		seasons, _ := items[0]["seasons"].([]any)
		if len(seasons) != 1 {
			t.Fatalf("seasons = %#v, want one", items[0]["seasons"])
		}
		season, _ := seasons[0].(map[string]any)
		if season["title"] != "Lantern Floor" {
			t.Errorf("season title = %#v", season["title"])
		}
		if items[1]["file_id"] != float64(watchEpisodeOneFile) || items[2]["file_id"] != float64(watchEpisodeTwoFile) {
			t.Errorf("episode file ids = %#v, %#v", items[1]["file_id"], items[2]["file_id"])
		}
	})

	t.Run("movie detail", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/items/"+watchMovieContentID, "", token, profileHeaders)
		if response.Code != http.StatusOK {
			t.Fatalf("movie detail = %d %s", response.Code, response.Body.String())
		}
		document := assertWatchDocumentConforms(t, response.Body.Bytes())
		items := watchItems(document)
		if len(items) != 1 || items[0]["file_id"] != float64(watchMovieFileID) {
			t.Fatalf("movie detail items = %#v", items)
		}
	})

	t.Run("unknown item", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/items/6003", "", token, profileHeaders)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown item = %d %s", response.Code, response.Body.String())
		}
	})

	// The Silo-compatible projection keeps exactly the watch route it had. Its
	// GET /api/v1/watch/{id} still serves catalog item detail — "home" is just
	// an identifier to it — and it has grown no items route at all.
	t.Run("the v1 projection is untouched", func(t *testing.T) {
		detail := performJSONRequest(t, router, http.MethodGet, "/api/v1/watch/"+watchMovieContentID, "", token, profileHeaders)
		if detail.Code != http.StatusOK {
			t.Fatalf("v1 watch detail = %d %s", detail.Code, detail.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode v1 watch detail: %v", err)
		}
		if _, isDocument := body["progress"]; isDocument {
			t.Errorf("GET /api/v1/watch/{id} answered with a watch document: %v", body)
		}
		items := performJSONRequest(t, router, http.MethodGet, "/api/v1/watch/items/"+watchMovieContentID, "", token, profileHeaders)
		if items.Code != http.StatusNotFound {
			t.Errorf("GET /api/v1/watch/items/{id} = %d, want 404: the Watch documents live on the native surface", items.Code)
		}
	})

	// A hundred newer titles push the seeded pair out of the recently-added
	// window. Continue Watching must survive that: the movie has its own
	// progress row and the series has one through an episode, so both belong in
	// the document even though neither is in the window. A title that is merely
	// old, with no progress, must not come back with them.
	t.Run("continue watching outside the window", func(t *testing.T) {
		seedWatchFillerLibrary(t, pool)

		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/home", "", token, profileHeaders)
		if response.Code != http.StatusOK {
			t.Fatalf("home = %d %s", response.Code, response.Body.String())
		}
		document := assertWatchDocumentConforms(t, response.Body.Bytes())
		ids := watchContentIDs(document)
		if len(ids) <= watchFillerCount {
			t.Fatalf("home items = %d, want more than the %d-item window", len(ids), watchFillerCount)
		}
		if !contains(ids, watchMovieContentID) {
			t.Error("an in-progress movie outside the window is missing from the home document")
		}
		if !contains(ids, watchSeriesContentID) {
			t.Error("the series behind an in-progress episode is missing from the home document")
		}
		if contains(ids, watchForgottenContentID) {
			t.Error("an old title with no progress was pulled back in; the union must be progress-driven")
		}
		rows := watchProgressRows(document)
		if len(rows) != 2 {
			t.Fatalf("progress rows = %d, want the movie and the episode: %s", len(rows), response.Body.String())
		}
	})

	// Restricting the profile to the movie library must remove the series from
	// both arrays and make its detail document unreachable.
	t.Run("library restrictions", func(t *testing.T) {
		store, err := provider.ForUser(ctx, fixture.userID)
		if err != nil {
			t.Fatalf("open user store: %v", err)
		}
		// Two decoy versions of the permitted movie, both with lower
		// identifiers than the playable one: a 4K version inside the permitted
		// library, and a 1080p version in the library the profile loses. A
		// document naming either hands the client a Play button that
		// /playback/start refuses.
		if _, err := pool.Exec(ctx, `INSERT INTO media_files
			(id, content_id, media_folder_id, file_path, file_size, duration, container, resolution)
			VALUES (4241999, $1, $2, '/invented/films/the-invented-crossing-2160p.mp4', 4194304, 6480, 'mp4', '2160p'),
			       (4242000, $1, $3, '/invented/films/the-invented-crossing-other-library.mp4', 1048576, 6480, 'mp4', '1080p')`,
			watchMovieContentID, watchMovieLibraryID, watchSeriesLibraryID); err != nil {
			t.Fatalf("seed decoy files: %v", err)
		}

		restricted := true
		allowed := []int{watchMovieLibraryID}
		ceiling := "1080p"
		if err := store.UpdateProfile(ctx, profileID, userstore.UpdateProfileInput{
			LibraryRestrictionsEnabled: &restricted,
			AllowedLibraryIDs:          &allowed,
			MaxPlaybackQuality:         &ceiling,
		}); err != nil {
			t.Fatalf("restrict profile libraries: %v", err)
		}

		response := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/home", "", token, profileHeaders)
		if response.Code != http.StatusOK {
			t.Fatalf("restricted home = %d %s", response.Code, response.Body.String())
		}
		document := assertWatchDocumentConforms(t, response.Body.Bytes())
		ids := watchContentIDs(document)
		if contains(ids, watchSeriesContentID) {
			t.Errorf("restricted home lists the series: %v", ids)
		}
		if !contains(ids, watchMovieContentID) {
			t.Errorf("restricted home lost the permitted movie: %v", ids)
		}
		for _, item := range watchItems(document) {
			if item["content_id"] != watchMovieContentID {
				continue
			}
			if item["file_id"] != float64(watchMovieFileID) {
				t.Errorf("restricted movie file_id = %#v, want %d — the only version inside the viewer's libraries and quality ceiling",
					item["file_id"], watchMovieFileID)
			}
		}
		for _, row := range watchProgressRows(document) {
			if row["content_id"] == watchSeriesContentID {
				t.Errorf("restricted home carries progress for the series: %#v", row)
			}
		}

		detail := performJSONRequest(t, router, http.MethodGet, NativeAPIPrefix+"/watch/items/"+watchSeriesContentID, "", token, profileHeaders)
		if detail.Code != http.StatusNotFound {
			t.Fatalf("restricted series detail = %d %s", detail.Code, detail.Body.String())
		}
	})
}

func newWatchDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newDisposableAPIDatabase(t, "bloem_watch_", false)
	if err := database.RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership first-run setup creates.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(context.Background(), pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	return pool
}

func seedWatchCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO media_folders (id, type, name) VALUES ($1, 'movie', 'Invented Watch Films'), ($2, 'series', 'Invented Watch Series')`,
			[]any{watchMovieLibraryID, watchSeriesLibraryID}},
		{`INSERT INTO media_items (content_id, type, title, year, runtime, content_rating, overview)
		  VALUES ($1, 'movie', 'The Invented Crossing', 2026, 108, 'PG', 'A harbor surveyor follows a light that appears only after midnight.'),
		         ($2, 'series', 'Eight Quiet Rooms', 2026, 0, 'PG', 'Residents of an unfinished building discover that every empty room remembers a different visitor.')`,
			[]any{watchMovieContentID, watchSeriesContentID}},
		{`INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		  VALUES ($1, $3, '2026-08-13T09:00:00Z'), ($2, $4, '2026-08-11T09:00:00Z')`,
			[]any{watchMovieContentID, watchSeriesContentID, watchMovieLibraryID, watchSeriesLibraryID}},
		{`INSERT INTO seasons (content_id, series_id, season_number, title, overview, poster_path, poster_thumbhash, metadata_s3_path, metadata_etag)
		  VALUES ('8080-s01', $1, 1, 'Lantern Floor', 'The first floor to be finished.', '', '', '', '')`,
			[]any{watchSeriesContentID}},
		{`INSERT INTO episodes (content_id, series_id, season_id, season_number, episode_number, title, overview, runtime)
		  VALUES ($2, $1, '8080-s01', 1, 1, 'The First Locked Room', 'A brass key is found inside a wall that has never had a door.', 45),
		         ($3, $1, '8080-s01', 1, 2, 'Echoes in the Stairwell', 'The residents hear their own footsteps a day before they make them.', 45)`,
			[]any{watchSeriesContentID, watchEpisodeOneID, watchEpisodeTwoID}},
		// Episodes are only visible once they belong to a library: the
		// episode listings gate on episode_libraries.
		{`INSERT INTO episode_libraries (episode_id, media_folder_id, first_seen_at)
		  VALUES ($1, $3, '2026-08-11T09:00:00Z'), ($2, $3, '2026-08-11T09:00:00Z')`,
			[]any{watchEpisodeOneID, watchEpisodeTwoID, watchSeriesLibraryID}},
		{`INSERT INTO media_files (id, content_id, media_folder_id, file_path, file_size, duration, container, resolution)
		  VALUES ($1, $2, $3, '/invented/films/the-invented-crossing.mp4', 1048576, 6480, 'mp4', '1080p')`,
			[]any{watchMovieFileID, watchMovieContentID, watchMovieLibraryID}},
		{`INSERT INTO media_files (id, content_id, episode_id, media_folder_id, file_path, season_number, episode_number, file_size, duration, container, resolution)
		  VALUES ($1, $5, $3, $4, '/invented/series/eight-quiet-rooms/s01e01.mp4', 1, 1, 524288, 2700, 'mp4', '1080p'),
		         ($2, $5, $6, $4, '/invented/series/eight-quiet-rooms/s01e02.mp4', 1, 2, 524288, 2700, 'mp4', '1080p')`,
			[]any{watchEpisodeOneFile, watchEpisodeTwoFile, watchEpisodeOneID, watchSeriesLibraryID, watchSeriesContentID, watchEpisodeTwoID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed catalog (%s): %v", strings.TrimSpace(strings.SplitN(statement.sql, "\n", 2)[0]), err)
		}
	}
}

// seedWatchFillerLibrary adds a hundred titles newer than everything else, plus
// one older title with no progress.
func seedWatchFillerLibrary(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	statements := []string{
		`INSERT INTO media_items (content_id, type, title, year, runtime, content_rating, overview)
		 SELECT 'filler-' || n, 'movie', 'Filler Reel ' || n, 2026, 90, 'PG', 'An invented filler title.'
		 FROM generate_series(1, ` + strconv.Itoa(watchFillerCount) + `) AS n`,
		`INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		 SELECT 'filler-' || n, 1, TIMESTAMPTZ '2026-08-20T09:00:00Z' + (n || ' minutes')::interval
		 FROM generate_series(1, ` + strconv.Itoa(watchFillerCount) + `) AS n`,
		`INSERT INTO media_files (id, content_id, media_folder_id, file_path, file_size, duration, container, resolution)
		 SELECT 5000000 + n, 'filler-' || n, 1, '/invented/films/filler-' || n || '.mp4', 1048576, 5400, 'mp4', '1080p'
		 FROM generate_series(1, ` + strconv.Itoa(watchFillerCount) + `) AS n`,
		`INSERT INTO media_items (content_id, type, title, year, runtime, content_rating, overview)
		 VALUES ('` + watchForgottenContentID + `', 'movie', 'The Long Forgotten Hallway', 2019, 95, 'PG', 'Old, playable, and never started.')`,
		`INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		 VALUES ('` + watchForgottenContentID + `', 1, '2026-08-01T09:00:00Z')`,
		`INSERT INTO media_files (id, content_id, media_folder_id, file_path, file_size, duration, container, resolution)
		 VALUES (3003001, '` + watchForgottenContentID + `', 1, '/invented/films/the-long-forgotten-hallway.mp4', 1048576, 5700, 'mp4', '1080p')`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("seed filler library: %v", err)
		}
	}
}

func seedWatchProgress(t *testing.T, provider userstore.UserStoreProvider, userID int, profileID string) {
	t.Helper()
	ctx := context.Background()
	store, err := provider.ForUser(ctx, userID)
	if err != nil {
		t.Fatalf("open user store: %v", err)
	}
	if err := store.SetProgressAt(ctx, profileID, watchMovieContentID, 1234.5, 6480, false,
		time.Date(2026, 8, 13, 11, 45, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed movie progress: %v", err)
	}
	// An episode row: stored against the episode's own identifier, with no
	// series linkage of its own.
	if err := store.SetProgressAt(ctx, profileID, watchEpisodeOneID, 960, 2700, false,
		time.Date(2026, 8, 13, 11, 50, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed episode progress: %v", err)
	}
}

func assertWatchDocumentConforms(t *testing.T, body []byte) map[string]any {
	t.Helper()
	root := watchContractsCheckout(t)
	raw, err := os.ReadFile(filepath.Join(root, "schema", "watch", "document.schema.json"))
	if err != nil {
		t.Fatalf("read watch document schema: %v", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(raw, &schemaDocument); err != nil {
		t.Fatalf("decode watch document schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("watch-document.json", schemaDocument); err != nil {
		t.Fatalf("add watch document schema: %v", err)
	}
	schema, err := compiler.Compile("watch-document.json")
	if err != nil {
		t.Fatalf("compile watch document schema: %v", err)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode watch document %s: %v", body, err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("response does not conform to watch_document_v1: %v\nbody: %s", err, body)
	}
	document, _ := value.(map[string]any)
	return document
}

// watchContractsCheckout locates the contracts repository, skipping with the
// variable named rather than passing when it cannot be found.
func watchContractsCheckout(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("BLOEM_CONTRACTS_ROOT"),
		os.Getenv("BLOEM_CLIENT_CONTRACTS_DIR"),
		filepath.Join("..", "..", "..", "bloem-client-contracts"),
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
	t.Skipf("watch document schema unavailable: set BLOEM_CONTRACTS_ROOT to a bloem-client-contracts checkout (looked in %s)",
		strings.Join(looked, ", "))
	return ""
}

func watchItems(document map[string]any) []map[string]any {
	raw, _ := document["items"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, _ := entry.(map[string]any)
		items = append(items, item)
	}
	return items
}

func watchProgressRows(document map[string]any) []map[string]any {
	raw, _ := document["progress"].([]any)
	rows := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		row, _ := entry.(map[string]any)
		rows = append(rows, row)
	}
	return rows
}

func watchContentIDs(document map[string]any) []string {
	var ids []string
	for _, item := range watchItems(document) {
		id, _ := item["content_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
