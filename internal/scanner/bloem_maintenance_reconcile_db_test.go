package scanner

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// reconcileMaintenanceMembershipsByLists is reconcileMaintenanceMemberships as
// it was until 2026-11-04, passing every item and episode the maintenance
// owner's files name. The parity test holds the current path to its answers.
func (s *Scanner) reconcileMaintenanceMembershipsByLists(ctx context.Context, folderID int, restoreEpisodes bool, protected []string) (int, int, []string, error) {
	items, episodes, err := s.fileRepo.maintenanceMembershipTargets(ctx, folderID, "", true)
	if err != nil {
		return 0, 0, nil, err
	}
	if s.episodeLibraryRepo != nil && restoreEpisodes {
		if err = s.episodeLibraryRepo.RestoreMaintenanceEpisodes(ctx, folderID, s.fileRepo.maintenanceScope.Predicate("mf")); err != nil {
			return 0, 0, nil, err
		}
	}
	if s.episodeLibraryRepo != nil {
		if _, err = s.episodeLibraryRepo.RemoveStaleEpisodeMemberships(ctx, folderID, episodes); err != nil {
			return 0, 0, nil, err
		}
	}
	retained, err := s.fileRepo.maintenanceRetainedItems(ctx, items)
	if err != nil {
		return 0, 0, nil, err
	}
	return s.reconcileMaintenanceItems(ctx, folderID, items, protected, retained)
}

// maintenanceScenario is one library holding every case a whole-folder
// maintenance reconcile decides: local and native files, present and
// missing, memberships with and without files, a protected root, a manga
// parent, a cross-folder native version and episodes reached through a span.
type maintenanceScenario struct {
	pool      *pgxpool.Pool
	s         *Scanner
	folder    *models.MediaFolder
	protected []string
	items     map[string]string
	episodes  map[string]string
}

func newMaintenanceScenario(t *testing.T, tag string) *maintenanceScenario {
	t.Helper()
	pool, s, folder, location := mixedStorageFixture(t)
	ctx := t.Context()
	sc := &maintenanceScenario{pool: pool, s: s, folder: folder, items: map[string]string{}, episodes: map[string]string{},
		protected: []string{"/protected/" + tag}}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	native := []string{"present", "missing", "elsewhere", "episode", "span", "far"}
	entries := make([]*storagev1.Entry, 0, len(native))
	for _, role := range native {
		entries = append(entries, storageEbookEntry(role+"-"+tag, "epub", &storagev1.EbookMetadata{Title: "Maintenance " + tag + " " + role}, "r1"))
	}
	publishStorageEbooks(t, pool, s, folder, location, entries...)
	nativeFile := map[string]int{}
	for _, role := range native {
		var id int
		var content string
		if err := pool.QueryRow(ctx, `SELECT f.id,f.content_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
			WHERE r.location_id=$1 AND r.entry_id=$2`, location.ID, "epub/"+role+"-"+tag).Scan(&id, &content); err != nil {
			t.Fatalf("native %s: %v", role, err)
		}
		nativeFile[role], sc.items["native-"+role] = id, content
	}

	local := func(role, kind string) string {
		id := "maintenance-" + tag + "-" + role
		exec(`INSERT INTO media_items(content_id,type,title) VALUES($1,$2,$1)`, id, kind)
		sc.items[role] = id
		return id
	}
	member := func(id string) {
		exec(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)`, id, folder.ID)
	}
	file := func(id, path string, missing bool, episode any) {
		missingSince := any(nil)
		if missing {
			missingSince = "2026-10-01T00:00:00Z"
		}
		exec(`INSERT INTO media_files(content_id,episode_id,media_folder_id,file_path,missing_since) VALUES($1,$2,$3,$4,$5::timestamptz)`,
			id, episode, folder.ID, path, missingSince)
	}
	member(file2(local("local-present", "ebook"), func(id string) { file(id, "/local/"+tag+"/present.epub", false, nil) }))
	member(file2(local("local-missing", "ebook"), func(id string) { file(id, "/local/"+tag+"/missing.epub", true, nil) }))
	member(local("fileless", "ebook"))
	member(file2(local("protected-missing", "ebook"), func(id string) { file(id, "/protected/"+tag+"/book.epub", true, nil) }))
	file2(local("orphan-file", "ebook"), func(id string) { file(id, "/local/"+tag+"/orphan.epub", true, nil) })
	member(local("manga", "manga"))

	// The native "missing" version stays native; the "elsewhere" one moves to
	// another library while a missing local version takes its place here.
	exec(`UPDATE media_files SET missing_since='2026-10-01' WHERE id=$1`, nativeFile["missing"])
	var other int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('ebooks',$1) RETURNING id`, "Maintenance other "+tag).Scan(&other); err != nil {
		t.Fatal(err)
	}
	elsewhere := sc.items["native-elsewhere"]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_items WHERE content_id=$1`, elsewhere)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id=$1`, other)
	})
	exec(`UPDATE media_files SET media_folder_id=$1,missing_since='2026-10-01' WHERE id=$2`, other, nativeFile["elsewhere"])
	file(elsewhere, "/local/"+tag+"/elsewhere.epub", true, nil)

	// A series with local and native episode files. Episode 21's only file is
	// native and missing, but the local episode 20 reaches it through Silo's
	// span widening; 50 and 60 have no local episode within the span.
	series := local("series", "series")
	member(series)
	episode := func(role string, number int) string {
		id := "maintenance-" + tag + "-episode-" + role
		exec(`INSERT INTO episodes(content_id,series_id,season_number,episode_number,title) VALUES($1,$2,1,$3,$1)`, id, series, number)
		exec(`INSERT INTO episode_libraries(episode_id,media_folder_id) VALUES($1,$2)`, id, folder.ID)
		sc.episodes[role] = id
		return id
	}
	file(series, "/local/"+tag+"/s01e01.mkv", false, episode("present", 1))
	file(series, "/local/"+tag+"/s01e02.mkv", true, episode("missing", 2))
	episode("fileless", 3)
	file(series, "/local/"+tag+"/s01e20.mkv", false, episode("reaching", 20))
	for role, number := range map[string]int{"span": 21, "episode": 50, "far": 60} {
		exec(`UPDATE media_files SET content_id=$1,episode_id=$2,missing_since='2026-10-01' WHERE id=$3`, series, episode("native-"+role, number), nativeFile[role])
	}
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	return sc
}

// file2 runs add for id and returns id, so a case reads as one line.
func file2(id string, add func(string)) string {
	add(id)
	return id
}

// outcome reports, per case, whether the item and its membership here, and
// each episode's membership here, survived.
func (sc *maintenanceScenario) outcome(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for role, id := range sc.items {
		var item, membership bool
		if err := sc.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1),
			EXISTS(SELECT 1 FROM media_item_libraries WHERE content_id=$1 AND media_folder_id=$2)`, id, sc.folder.ID).Scan(&item, &membership); err != nil {
			t.Fatal(err)
		}
		out["item "+role] = fmt.Sprintf("item=%v membership=%v", item, membership)
	}
	for role, id := range sc.episodes {
		var membership bool
		if err := sc.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM episode_libraries WHERE episode_id=$1 AND media_folder_id=$2)`, id, sc.folder.ID).Scan(&membership); err != nil {
			t.Fatal(err)
		}
		out["episode "+role] = fmt.Sprintf("membership=%v", membership)
	}
	return out
}

// TestMaintenanceReconcileMatchesListedReconcileDB holds the whole-folder
// maintenance reconcile to the answers of the version that passed every item
// and episode of the folder: the same libraries, the same memberships and the
// same deleted items, case by case.
func TestMaintenanceReconcileMatchesListedReconcileDB(t *testing.T) {
	listed := newMaintenanceScenario(t, "listed")
	current := newMaintenanceScenario(t, "current")
	if _, _, _, err := listed.s.reconcileMaintenanceMembershipsByLists(t.Context(), listed.folder.ID, true, listed.protected); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := current.s.reconcileMaintenanceMemberships(t.Context(), current.folder.ID, true, current.protected); err != nil {
		t.Fatal(err)
	}
	want, got := listed.outcome(t), current.outcome(t)
	if !reflect.DeepEqual(got, want) {
		for key := range want {
			if got[key] != want[key] {
				t.Errorf("%s: current %s, listed %s", key, got[key], want[key])
			}
		}
		t.FailNow()
	}
	// The scenario must exercise every decision, not two no-ops that agree.
	for key, state := range map[string]string{
		"item local-present":     "item=true membership=true",
		"item local-missing":     "item=false membership=false",
		"item fileless":          "item=false membership=false",
		"item protected-missing": "item=true membership=false",
		"item orphan-file":       "item=false membership=false",
		"item manga":             "item=true membership=true",
		"item native-missing":    "item=true membership=true",
		"item native-elsewhere":  "item=true membership=false",
		"episode present":        "membership=true",
		"episode missing":        "membership=false",
		"episode fileless":       "membership=false",
		"episode reaching":       "membership=true",
		"episode native-span":    "membership=false",
		"episode native-episode": "membership=true",
		"episode native-far":     "membership=true",
		"item series":            "item=true membership=true",
		"item native-present":    "item=true membership=true",
	} {
		if want[key] != state {
			t.Errorf("%s = %s, want %s", key, want[key], state)
		}
	}
}

// listArgTracer records the longest text array any statement is sent.
type listArgTracer struct {
	mu      sync.Mutex
	longest int
}

func (l *listArgTracer) note(args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, arg := range args {
		if list, ok := arg.([]string); ok && len(list) > l.longest {
			l.longest = len(list)
		}
	}
}

func (l *listArgTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	l.note(data.Args)
	return ctx
}

func (l *listArgTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestMaintenanceReconcileSendsOnlyStaleCandidatesDB pins the cost of a
// whole-folder maintenance reconcile: it sends the database only the items and
// episodes that can change, not every one the folder's files name. On
// production a TV library sent 1.05 million episode IDs and read back 1.07
// million rows every pass.
func TestMaintenanceReconcileSendsOnlyStaleCandidatesDB(t *testing.T) {
	sc := newMaintenanceScenario(t, "budget")
	ctx := t.Context()
	healthy := 200
	if _, err := sc.pool.Exec(ctx, `
		WITH items AS (
			INSERT INTO media_items(content_id,type,title)
			SELECT 'maintenance-budget-healthy-' || n, 'ebook', 'healthy' FROM generate_series(1,$2::int) n
			RETURNING content_id
		), files AS (
			INSERT INTO media_files(content_id,media_folder_id,file_path)
			SELECT content_id, $1, '/local/budget/' || content_id || '.epub' FROM items
		)
		INSERT INTO media_item_libraries(content_id,media_folder_id) SELECT content_id, $1 FROM items`, sc.folder.ID, healthy); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.pool.Exec(ctx, `
		WITH episodes AS (
			INSERT INTO episodes(content_id,series_id,season_number,episode_number,title)
			SELECT 'maintenance-budget-healthy-episode-' || n, $2, 2, n, 'healthy' FROM generate_series(1,$3::int) n
			RETURNING content_id
		), files AS (
			INSERT INTO media_files(content_id,episode_id,media_folder_id,file_path)
			SELECT $2, content_id, $1, '/local/budget/' || content_id || '.mkv' FROM episodes
		)
		INSERT INTO episode_libraries(episode_id,media_folder_id) SELECT content_id, $1 FROM episodes`, sc.folder.ID, sc.items["series"], healthy); err != nil {
		t.Fatal(err)
	}

	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	tracer := &listArgTracer{}
	config.ConnConfig.Tracer = tracer
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	s := NewScanner(NewFileRepository(traced), "", nil, 1, false, 0)
	s.SetMaintenanceFileScope(NativeMaintenanceFileScope{})
	if _, _, _, err := s.reconcileMaintenanceMemberships(ctx, sc.folder.ID, true, sc.protected); err != nil {
		t.Fatal(err)
	}
	// The scenario has eight stale items and episodes; the healthy ones never
	// travel.
	if tracer.longest > 10 {
		t.Fatalf("a statement was sent a list of %d IDs for a folder with %d healthy items and episodes", tracer.longest, 2*healthy)
	}
}
