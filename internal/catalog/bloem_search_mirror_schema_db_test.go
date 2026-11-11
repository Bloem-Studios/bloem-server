package catalog

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mirrorSchemaColumn is what the mirror's schema promises about one column.
type mirrorSchemaColumn struct {
	udtName  string
	nullable string
	// dflt is information_schema's column_default; empty means no default.
	dflt string
}

// readMirrorSchemaColumn returns the column's facts from information_schema, failing the
// test when the column does not exist.
func readMirrorSchemaColumn(t *testing.T, pool *pgxpool.Pool, table, column string) mirrorSchemaColumn {
	t.Helper()
	var facts mirrorSchemaColumn
	var dflt *string
	err := pool.QueryRow(context.Background(), `
		SELECT udt_name, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&facts.udtName, &facts.nullable, &dflt)
	if err != nil {
		t.Fatalf("read column %s.%s: %v", table, column, err)
	}
	if dflt != nil {
		facts.dflt = *dflt
	}
	return facts
}

// readMirrorSchemaPrimaryKey returns the columns of the table's primary key in key order.
func readMirrorSchemaPrimaryKey(t *testing.T, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	var cols []string
	err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(array_agg(a.attname::text ORDER BY k.ord), '{}')
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		WHERE n.nspname = 'public' AND c.relname = $1 AND i.indisprimary`,
		table).Scan(&cols)
	if err != nil {
		t.Fatalf("read primary key of %s: %v", table, err)
	}
	return cols
}

// A search event carries the id of the transaction that wrote it, so the
// mirror can tell committed-and-visible events from ones still in flight.
// Inside a savepoint the writer holds a subtransaction id, and the mirror's
// snapshot logic only works with the top-level one.
func TestSearchMirrorTxIDIsTheWritingTransaction(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	var eventTx string
	err = tx.QueryRow(ctx, `
		INSERT INTO catalog_search_index_events (provider, action, content_id)
		VALUES ('bloem_schema_test', 'upsert', 'x')
		RETURNING tx_id::text`).Scan(&eventTx)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var current string
	if err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&current); err != nil {
		t.Fatalf("read current transaction id: %v", err)
	}
	if eventTx == "" || eventTx != current {
		t.Fatalf("event tx_id = %q, want the writing transaction's id %q", eventTx, current)
	}
}

// Two rows written by one transaction share a tx_id and a later transaction
// gets a larger one, which is what makes (tx_id, id) a usable resume point.
func TestSearchMirrorTxIDSeparatesTransactions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	writeOne := func(contentIDs ...string) []string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var ids []string
		for _, contentID := range contentIDs {
			var id string
			err := tx.QueryRow(ctx, `
				INSERT INTO catalog_search_index_events (provider, action, content_id)
				VALUES ('bloem_schema_test', 'upsert', $1)
				RETURNING tx_id::text`, contentID).Scan(&id)
			if err != nil {
				t.Fatalf("insert event: %v", err)
			}
			ids = append(ids, id)
		}
		return ids
	}

	first := writeOne("a", "b")
	if first[0] != first[1] {
		t.Fatalf("one transaction wrote tx_ids %q and %q, want the same", first[0], first[1])
	}
	second := writeOne("c")
	var later bool
	err := pool.QueryRow(ctx, `SELECT $1::text::xid8 > $2::text::xid8`, second[0], first[0]).Scan(&later)
	if err != nil {
		t.Fatalf("compare tx_ids: %v", err)
	}
	if !later {
		t.Fatalf("later transaction tx_id %q is not greater than %q", second[0], first[0])
	}
}

func TestSearchMirrorTxIDColumnIsNullableWithDefault(t *testing.T) {
	pool := newTestPool(t)

	got := readMirrorSchemaColumn(t, pool, "catalog_search_index_events", "tx_id")
	want := mirrorSchemaColumn{udtName: "xid8", nullable: "YES", dflt: "pg_current_xact_id()"}
	if got != want {
		t.Fatalf("catalog_search_index_events.tx_id = %+v, want %+v", got, want)
	}
}

func TestSearchMirrorTxIndexIsValid(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var valid, ready bool
	var table string
	var keyColumns []string
	err := pool.QueryRow(ctx, `
		SELECT i.indisvalid, i.indisready, t.relname::text,
		       (SELECT array_agg(a.attname::text ORDER BY k.ord)
		        FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		        JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_index i ON i.indexrelid = c.oid
		JOIN pg_class t ON t.oid = i.indrelid
		WHERE n.nspname = 'public' AND c.relname = 'idx_catalog_search_index_events_tx'`).
		Scan(&valid, &ready, &table, &keyColumns)
	if err != nil {
		t.Fatalf("read idx_catalog_search_index_events_tx: %v", err)
	}
	if !valid || !ready {
		t.Fatalf("idx_catalog_search_index_events_tx valid=%t ready=%t, want both true", valid, ready)
	}
	if table != "catalog_search_index_events" {
		t.Fatalf("index is on %q, want catalog_search_index_events", table)
	}
	if want := []string{"tx_id", "id"}; !slices.Equal(keyColumns, want) {
		t.Fatalf("index columns = %v, want %v", keyColumns, want)
	}
}

func TestSearchMirrorStateColumnExists(t *testing.T) {
	pool := newTestPool(t)

	got := readMirrorSchemaColumn(t, pool, "catalog_search_index_state", "bloem_mirror")
	want := mirrorSchemaColumn{udtName: "jsonb", nullable: "YES"}
	if got != want {
		t.Fatalf("catalog_search_index_state.bloem_mirror = %+v, want %+v", got, want)
	}
}

// The dead-letter and process-lease tables have the columns, defaults and
// keys the mirror's later code relies on.
func TestSearchMirrorDeadLetterAndProcessTables(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	tables := []struct {
		name    string
		key     []string
		columns []struct {
			name string
			mirrorSchemaColumn
		}
	}{
		{
			name: "bloem_search_mirror_dead_letters",
			key:  []string{"content_id"},
			columns: []struct {
				name string
				mirrorSchemaColumn
			}{
				{"content_id", mirrorSchemaColumn{udtName: "text", nullable: "NO"}},
				{"first_failed_at", mirrorSchemaColumn{udtName: "timestamptz", nullable: "NO", dflt: "now()"}},
				{"last_failed_at", mirrorSchemaColumn{udtName: "timestamptz", nullable: "NO", dflt: "now()"}},
				{"attempts", mirrorSchemaColumn{udtName: "int4", nullable: "NO", dflt: "1"}},
				{"last_error", mirrorSchemaColumn{udtName: "text", nullable: "NO", dflt: "''::text"}},
			},
		},
		{
			name: "bloem_search_mirror_processes",
			key:  []string{"instance_id"},
			columns: []struct {
				name string
				mirrorSchemaColumn
			}{
				{"instance_id", mirrorSchemaColumn{udtName: "uuid", nullable: "NO"}},
				{"host", mirrorSchemaColumn{udtName: "text", nullable: "NO", dflt: "''::text"}},
				{"started_at", mirrorSchemaColumn{udtName: "timestamptz", nullable: "NO", dflt: "now()"}},
				{"heartbeat_at", mirrorSchemaColumn{udtName: "timestamptz", nullable: "NO", dflt: "now()"}},
			},
		},
	}
	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx, `
				SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1`, table.name).Scan(&count)
			if err != nil {
				t.Fatalf("count columns: %v", err)
			}
			if count != len(table.columns) {
				t.Fatalf("%s has %d columns, want %d", table.name, count, len(table.columns))
			}
			for _, column := range table.columns {
				if got := readMirrorSchemaColumn(t, pool, table.name, column.name); got != column.mirrorSchemaColumn {
					t.Errorf("%s.%s = %+v, want %+v", table.name, column.name, got, column.mirrorSchemaColumn)
				}
			}
			if got := readMirrorSchemaPrimaryKey(t, pool, table.name); !slices.Equal(got, table.key) {
				t.Errorf("%s primary key = %v, want %v", table.name, got, table.key)
			}
		})
	}

	// The defaults fill a row that names only its key.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var attempts int
	var lastError string
	var failedTogether bool
	err = tx.QueryRow(ctx, `
		INSERT INTO bloem_search_mirror_dead_letters (content_id) VALUES ('bloem_schema_test')
		RETURNING attempts, last_error, first_failed_at = last_failed_at`).
		Scan(&attempts, &lastError, &failedTogether)
	if err != nil {
		t.Fatalf("insert dead letter: %v", err)
	}
	if attempts != 1 || lastError != "" || !failedTogether {
		t.Fatalf("dead letter defaults = attempts %d, last_error %q, first=last %t; want 1, empty, true",
			attempts, lastError, failedTogether)
	}

	var host string
	var started bool
	err = tx.QueryRow(ctx, `
		INSERT INTO bloem_search_mirror_processes (instance_id)
		VALUES ('00000000-0000-4000-8000-000000000001')
		RETURNING host, started_at = heartbeat_at`).Scan(&host, &started)
	if err != nil {
		t.Fatalf("insert process: %v", err)
	}
	if host != "" || !started {
		t.Fatalf("process defaults = host %q, started=heartbeat %t; want empty, true", host, started)
	}
}
