package migrations

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// bloemCollatedContentIDColumns are the Silo columns that reference
// media_items.content_id without Silo's COLLATE "C" (see
// docs/architecture/deterministic-content-id.md) and that Bloem's migration
// 20261104065534_bloem_content_id_collation moves to it.
var bloemCollatedContentIDColumns = []string{
	"metadata_enrichment_state.content_id",
	"audiobook_enrichment_state.content_id",
	"media_item_tmdb_collections.content_id",
	"manga_enrichment_state.content_id",
	"manga_chapters.chapter_content_id",
	"manga_chapters.series_content_id",
}

func migratedTestConn(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(t.Context()) })
	var migrated bool
	if err := conn.QueryRow(t.Context(), "SELECT to_regclass('public.metadata_enrichment_state') IS NOT NULL").Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Skip("database is not migrated")
	}
	return conn
}

// A content ID column outside COLLATE "C" cannot answer a join on
// media_items.content_id from its index, so a NOT EXISTS against it compares
// every outer row with every row of the table.
func TestBloemContentIDColumnsUseCCollationPostgres(t *testing.T) {
	conn := migratedTestConn(t)
	var wrong []string
	for _, column := range bloemCollatedContentIDColumns {
		table, name, _ := strings.Cut(column, ".")
		var collation string
		if err := conn.QueryRow(t.Context(), `
SELECT COALESCE(c.collname, 'default')
FROM pg_attribute a LEFT JOIN pg_collation c ON c.oid = a.attcollation
WHERE a.attrelid = ('public.' || $1)::regclass AND a.attname = $2`, table, name).Scan(&collation); err != nil {
			t.Fatalf("%s: %v", column, err)
		}
		if collation != "C" {
			wrong = append(wrong, column+" ("+collation+")")
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("content ID columns without COLLATE \"C\": %s", strings.Join(wrong, ", "))
	}
}

// The bulk enrichment pass's probe (metadata.enrichmentCandidatePredicate)
// checks each matched item against metadata_enrichment_state. With the
// collations matching, that check is a primary-key lookup; without, Postgres
// can only filter a scan of the whole state table per item, which took 15
// minutes per hourly probe on Bloem's catalog.
func TestBloemEnrichmentStateJoinUsesItsKeyPostgres(t *testing.T) {
	conn := migratedTestConn(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, setting := range []string{"enable_seqscan", "enable_hashjoin", "enable_mergejoin", "enable_material"} {
		if _, err := tx.Exec(t.Context(), "SET LOCAL "+setting+" = off"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := tx.Query(t.Context(), `EXPLAIN (COSTS OFF)
SELECT mi.content_id FROM media_items mi
WHERE NOT EXISTS (
	SELECT 1 FROM metadata_enrichment_state es
	WHERE es.content_id = mi.content_id AND es.provider = 'mdblist')`)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	keyed := false
	for _, line := range lines {
		if strings.Contains(line, "Index Cond:") && strings.Contains(line, "content_id = mi.content_id") {
			keyed = true
		}
	}
	if !keyed {
		t.Fatalf("the state check does not look metadata_enrichment_state up by content ID:\n%s", strings.Join(lines, "\n"))
	}
}
