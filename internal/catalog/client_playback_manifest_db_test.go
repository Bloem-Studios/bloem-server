package catalog

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This fixture never accepts the general/shared test database variable. It
// requires an explicitly owned empty database and builds only catalog tables.
func TestClientPlaybackManifestOwnedPostgres(t *testing.T) {
	dsn := os.Getenv("SILO_CLIENT_MANIFEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires an isolated owned manifest test database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "notifications_manifest_") || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port == 55443 {
		t.Fatal("refusing non-owned database")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("owned fixture database must be empty")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile("../../migrations/sql/001_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"media_items", "media_files", "media_item_libraries"} {
		ddl := regexp.MustCompile(`(?s)CREATE TABLE public\.` + name + ` \(.*?\n\);`).FindString(string(schema))
		if ddl == "" {
			t.Fatalf("missing base schema table %s", name)
		}
		exec(ddl)
	}
	exec(`ALTER TABLE media_items ADD COLUMN default_metadata_language text, ADD COLUMN poster_source_path text,
 ADD COLUMN backdrop_source_path text, ADD COLUMN logo_source_path text, ADD COLUMN keywords text[],
 ADD COLUMN original_language text, ADD COLUMN release_date date, ADD COLUMN air_time text,
 ADD COLUMN air_timezone text, ADD COLUMN show_status text, ADD COLUMN episode_metadata_incomplete boolean DEFAULT false,
 ADD COLUMN episode_metadata_last_checked_at timestamptz`)
	exec(`ALTER TABLE media_files ADD COLUMN extra_id text, ADD COLUMN presentation_kind text,
 ADD COLUMN presentation_group_key text, ADD COLUMN presentation_part_index integer,
 ADD COLUMN presentation_part_total integer, ADD COLUMN edition_key text`)
	exec(`INSERT INTO media_items(content_id,type,title,content_rating,sort_title,default_metadata_language,
 original_title,year,runtime,overview,tagline,imdb_id,tmdb_id,tvdb_id,original_language,show_status)
 VALUES ('book','audiobook','Fixture','PG','fixture','en','Fixture',2026,1,'','','','','','en','')`)
	exec(`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES ('book',2)`)
	exec(`INSERT INTO media_files(id,content_id,media_folder_id,file_path,duration,probe_source,presentation_kind,presentation_group_key,presentation_part_index,presentation_part_total)
 VALUES (7,'book',2,'ignored-c',10,'local','multipart','book',1,3),
 (8,'book',2,'ignored-a',20,'local','multipart','book',2,3),
 (9,'book',2,'ignored-b',30,'local','multipart','book',3,3)`)
	resolver := NewClientPlaybackManifestResolver(pool)
	scope := manifestTestScope()
	scope.AllowedLibraryIDs = []int{2}
	scope.MaxContentRating = "PG"
	ctx := access.SetScope(t.Context(), scope)
	first, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 8)
	if err != nil {
		t.Fatal(err)
	}
	if first.Parts[0].FileID != 7 || first.Parts[2].OffsetSeconds != 30 || first.DurationSeconds != 60 {
		t.Fatalf("wrong SQL manifest: %+v", first)
	}
	// Every anchor in an edition receives the same full-manifest digest.
	sibling, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 9)
	if err != nil || sibling.TimelineID != first.TimelineID {
		t.Fatalf("anchor changed digest: %v", err)
	}
	exec(`UPDATE media_files SET duration=21 WHERE id=8`)
	changed, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 8)
	if err != nil || changed.TimelineID == first.TimelineID {
		t.Fatalf("duration change lost: %v", err)
	}
	exec(`UPDATE media_files SET presentation_part_index=CASE id WHEN 7 THEN 2 WHEN 8 THEN 1 ELSE 3 END`)
	reordered, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 8)
	if err != nil || reordered.TimelineID == changed.TimelineID || reordered.Parts[0].FileID != 8 {
		t.Fatalf("order change lost: %v", err)
	}
	exec(`UPDATE media_files SET missing_since=now() WHERE id=9`)
	if _, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 8); err == nil {
		t.Fatal("missing member collapsed")
	}
	exec(`UPDATE media_files SET missing_since=NULL WHERE id=9`)
	denied := scope
	denied.AllowedLibraryIDs = []int{}
	if _, err := resolver.ResolveClientPlaybackManifest(access.SetScope(t.Context(), denied), 3, "viewer", 8); !errors.Is(err, ErrItemNotFound) {
		t.Fatal("empty policy allowed")
	}
	denied = scope
	denied.DisabledLibraryIDs = []int{2}
	if _, err := resolver.ResolveClientPlaybackManifest(access.SetScope(t.Context(), denied), 3, "viewer", 8); !errors.Is(err, ErrItemNotFound) {
		t.Fatal("disabled library allowed")
	}
	denied = scope
	denied.MaxContentRating = "G"
	if _, err := resolver.ResolveClientPlaybackManifest(access.SetScope(t.Context(), denied), 3, "viewer", 8); !errors.Is(err, ErrItemNotFound) {
		t.Fatal("rating denial lost")
	}
	exec(`UPDATE media_files SET media_folder_id=3 WHERE id=9`)
	if _, err := resolver.ResolveClientPlaybackManifest(ctx, 3, "viewer", 8); !errors.Is(err, ErrItemNotFound) {
		t.Fatal("unauthorized part omitted")
	}
	exec(`UPDATE media_files SET media_folder_id=2 WHERE id=9`)
	// The same reader used by production observes one repeatable-read snapshot
	// even if catalog metadata changes between its file and authorization reads.
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	reader := clientPlaybackManifestFileReader{tx}
	before, err := reader.loadClientPlaybackFiles(t.Context(), 8)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE media_files SET duration=99 WHERE id=8`)
	after, err := reader.loadClientPlaybackFiles(t.Context(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if before[1].Duration != after[1].Duration {
		t.Fatal("snapshot changed under resolver")
	}
}
