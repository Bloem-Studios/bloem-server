package executor

import (
	"maps"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestReseedSnapshotMatchesFullSeed pins that restoring the household
// snapshot leaves the database in the state a from-scratch seed does: the same
// row count in every table and the same position on every table-owned
// sequence. A table the seed writes through a trigger outside the truncated
// closure would show up here as a count that only the full seed moves.
//
// Free-standing revision counters (api_key_configuration_revision_seq and
// friends, advanced by triggers) are deliberately not compared: a full seed
// advances them, a restore leaves them where they are and puts back the
// revisions the seeded rows carried. Both only ever move forward, and each
// scenario reads its revision tags from the state it runs against. Runs only
// when SILO_SCENARIO_DATABASE_URL is set.
func TestReseedSnapshotMatchesFullSeed(t *testing.T) {
	env := New(t)
	if !env.HasDatabase() {
		t.Skip(DatabaseEnv + " is not set")
	}
	if env.snapshot == nil {
		t.Skip("snapshot restore unavailable for this database role")
	}

	// Dirty the household the way scenarios do, then restore.
	env.mustExec(`INSERT INTO invite_codes (code, label, max_uses, created_by) VALUES ('SNAPDIRT', 'dirt', 1, $1)`, env.users[fixtureAdmin].ID)
	env.mustExec(`UPDATE users SET enabled = false WHERE id = $1`, env.users[fixtureMember].ID)
	env.Reseed()
	restored := databaseShape(t, env)
	restoredFixtures := maps.Clone(env.fixtures)

	// Now the from-scratch path on the same database.
	env.snapshot = nil
	env.Reseed()
	seeded := databaseShape(t, env)

	for key, want := range seeded {
		if got := restored[key]; got != want {
			t.Errorf("%s: restored %d, full seed %d", key, got, want)
		}
	}
	for key, got := range restored {
		if _, ok := seeded[key]; !ok {
			t.Errorf("%s: restored %d, absent after full seed", key, got)
		}
	}
	for key, want := range env.fixtures {
		switch key {
		case "caller_invite_code", "locked_profile_token":
			continue // random, or minted with a fresh issue time
		}
		if got := restoredFixtures[key]; got != want {
			t.Errorf("fixture %s: restored %q, full seed %q", key, got, want)
		}
	}
}

// databaseShape maps every public table to its row count and every
// table-owned public sequence to its position.
func databaseShape(t *testing.T, env *Env) map[string]int64 {
	t.Helper()
	ctx := env.ctx
	rows, err := env.pool.Query(ctx, `SELECT quote_ident(tablename) FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	shape := map[string]int64{}
	for _, table := range tables {
		if table == "goose_db_version" {
			continue
		}
		var n int64
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM public.`+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		shape["table "+table] = n
	}
	seqRows, err := env.pool.Query(ctx, `
		SELECT sequences.sequencename, COALESCE(sequences.last_value, 0)
		FROM pg_sequences AS sequences
		WHERE sequences.schemaname = 'public'
		  AND EXISTS (
			SELECT 1 FROM pg_depend AS dep
			WHERE dep.classid = 'pg_class'::regclass
			  AND dep.objid = format('%I.%I', sequences.schemaname, sequences.sequencename)::regclass
			  AND dep.deptype IN ('a', 'i')
		  )`)
	if err != nil {
		t.Fatal(err)
	}
	defer seqRows.Close()
	for seqRows.Next() {
		var name string
		var value int64
		if err := seqRows.Scan(&name, &value); err != nil {
			t.Fatal(err)
		}
		shape["sequence "+name] = value
	}
	if err := seqRows.Err(); err != nil {
		t.Fatal(err)
	}
	return shape
}
