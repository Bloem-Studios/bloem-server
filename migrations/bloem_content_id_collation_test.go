package migrations

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

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
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var migrated bool
	if err := conn.QueryRow(t.Context(), "SELECT to_regclass('public.metadata_enrichment_state') IS NOT NULL").Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Skip("database is not migrated")
	}
	return conn
}

// TestBloemContentIDReferencesUseCCollationPostgres is Silo's
// TestContentIDFamilyReferencesUseCCollationPostgres (Silo #3480) ahead of its
// merge: every column with a foreign key to media_items, seasons or episodes
// has the collation of the column it references, COLLATE "C". Across two
// collations a join on content_id cannot use the referencing table's index,
// so a NOT EXISTS against it compares every outer row with every row of the
// table. Bloem's migrations 20261104065534 and 20261104080323 move the columns
// Silo created without it; delete this test when Silo's arrives.
func TestBloemContentIDReferencesUseCCollationPostgres(t *testing.T) {
	conn := migratedTestConn(t)
	rows, err := conn.Query(t.Context(), `SELECT DISTINCT con.conrelid::regclass::text || '.' || a.attname
FROM pg_constraint con
CROSS JOIN LATERAL unnest(con.conkey, con.confkey) AS k(attnum, refnum)
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
JOIN pg_attribute r ON r.attrelid = con.confrelid AND r.attnum = k.refnum
WHERE con.contype = 'f'
  AND con.confrelid IN ('media_items'::regclass, 'seasons'::regclass, 'episodes'::regclass)
  AND a.attcollation <> r.attcollation
ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(wrong) > 0 {
		t.Fatalf("content ID references without COLLATE \"C\": %s", strings.Join(wrong, ", "))
	}
}
