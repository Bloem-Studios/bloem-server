package executor

import (
	"fmt"
	"maps"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
)

// snapshotSchema holds the copied household rows. It lives in the executor's
// own scratch database and is dropped when the Env closes.
const snapshotSchema = "scenario_executor_snapshot"

// householdSnapshot is the state the first Reseed produced: the rows of every
// table the household truncation reaches, the sequences those tables own, and
// the Go-side fixture identities that point at those rows.
//
// Restoring it is equivalent to truncating and re-running the household seeding: the
// same closure of tables is emptied, the restored rows are byte-identical to
// the seeded ones (same ids, hashes, and incarnations), and the owned
// sequences resume where seeding left them. What seeding costs is the bcrypt
// hashes and dozens of round trips through the repositories; restoring is
// one pipelined batch inside one transaction.
type householdSnapshot struct {
	deletes   []string // one DELETE per closure table
	inserts   []string // INSERT .. SELECT from the snapshot schema
	sequences []snapshotSequence
	users     map[string]models.User
	sessions  map[string]string
	apiKeys   map[string]string
	fixtures  map[string]string
}

type snapshotSequence struct {
	name      string
	lastValue int64
	isCalled  bool
}

// householdClosureSQL lists the tables TRUNCATE .. CASCADE on householdRoots
// empties: the roots plus every table referencing one, transitively.
// Partitions are folded into their partitioned parent, which is what the
// snapshot copies and restores through.
const householdClosureSQL = `
WITH RECURSIVE closure(rel) AS (
	SELECT to_regclass('public.' || root) FROM unnest($1::text[]) AS root
	UNION
	SELECT constraints.conrelid::regclass
	FROM pg_constraint AS constraints
	JOIN closure ON constraints.confrelid = closure.rel
	WHERE constraints.contype = 'f'
)
SELECT DISTINCT COALESCE(parent.inhparent, closure.rel)::regclass::text
FROM closure
JOIN pg_class AS relation ON relation.oid = closure.rel
LEFT JOIN pg_inherits AS parent ON relation.relispartition AND parent.inhrelid = relation.oid
ORDER BY 1`

// takeSnapshot copies the freshly seeded household. Restoring needs triggers
// and foreign-key checks off while rows go back in (session_replication_role,
// a superuser setting, as the executor's scratch database normally grants);
// when the role cannot set it, the Env keeps seeding from scratch.
func (e *Env) takeSnapshot(fixtures map[string]string) {
	e.t.Helper()
	if e.snapshotDisabled {
		return
	}
	ctx := e.ctx
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		e.t.Fatalf("scenario executor: snapshot: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		e.snapshotDisabled = true
		e.t.Logf("scenario executor: reseeding from scratch every time; snapshot restore unavailable: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		e.t.Fatalf("scenario executor: snapshot: %v", err)
	}
	mustTx := func(sql string, args ...any) {
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			e.t.Fatalf("scenario executor: snapshot %q: %v", sql, err)
		}
	}
	mustTx(`DROP SCHEMA IF EXISTS ` + snapshotSchema + ` CASCADE`)
	mustTx(`CREATE SCHEMA ` + snapshotSchema)

	var tables []string
	rows, err := tx.Query(ctx, householdClosureSQL, householdRoots)
	if err != nil {
		e.t.Fatalf("scenario executor: snapshot closure: %v", err)
	}
	tables, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatalf("scenario executor: snapshot closure: %v", err)
	}

	snap := &householdSnapshot{fixtures: fixtures}
	for _, table := range tables {
		snap.deletes = append(snap.deletes, `DELETE FROM `+table)
	}
	for i, table := range tables {
		// Rewritten every Reseed because its rows expire relative to now.
		if table == deviceLoginTable {
			continue
		}
		var nonEmpty bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+`)`).Scan(&nonEmpty); err != nil {
			e.t.Fatalf("scenario executor: snapshot %s: %v", table, err)
		}
		if !nonEmpty {
			continue
		}
		var columns []string
		colRows, err := tx.Query(ctx, `
			SELECT quote_ident(attname) FROM pg_attribute
			WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped AND attgenerated = ''
			ORDER BY attnum`, table)
		if err != nil {
			e.t.Fatalf("scenario executor: snapshot columns %s: %v", table, err)
		}
		columns, err = pgx.CollectRows(colRows, pgx.RowTo[string])
		if err != nil {
			e.t.Fatalf("scenario executor: snapshot columns %s: %v", table, err)
		}
		list := strings.Join(columns, ", ")
		copyName := fmt.Sprintf("%s.t%03d", snapshotSchema, i)
		mustTx(`CREATE TABLE ` + copyName + ` AS SELECT ` + list + ` FROM ` + table)
		snap.inserts = append(snap.inserts,
			`INSERT INTO `+table+` (`+list+`) OVERRIDING SYSTEM VALUE SELECT `+list+` FROM `+copyName)
	}

	// Sequences owned by (serial) or backing (identity) a column in the
	// closure: the truncation restarts them, so the restore sets them back to
	// where seeding left them.
	seqRows, err := tx.Query(ctx, `
		SELECT DISTINCT dep.objid::regclass::text
		FROM pg_depend AS dep
		JOIN pg_class AS seq ON seq.oid = dep.objid AND seq.relkind = 'S'
		WHERE dep.classid = 'pg_class'::regclass
		  AND dep.refclassid = 'pg_class'::regclass
		  AND dep.deptype IN ('a', 'i')
		  AND dep.refobjid = ANY (SELECT unnest($1::text[])::regclass)
		ORDER BY 1`, tables)
	if err != nil {
		e.t.Fatalf("scenario executor: snapshot sequences: %v", err)
	}
	seqNames, err := pgx.CollectRows(seqRows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatalf("scenario executor: snapshot sequences: %v", err)
	}
	for _, name := range seqNames {
		seq := snapshotSequence{name: name}
		if err := tx.QueryRow(ctx, `SELECT last_value, is_called FROM `+name).Scan(&seq.lastValue, &seq.isCalled); err != nil {
			e.t.Fatalf("scenario executor: snapshot sequence %s: %v", name, err)
		}
		snap.sequences = append(snap.sequences, seq)
	}
	if err := tx.Commit(ctx); err != nil {
		e.t.Fatalf("scenario executor: snapshot commit: %v", err)
	}

	snap.users = make(map[string]models.User, len(e.users))
	for name, u := range e.users {
		snap.users[name] = *u
	}
	snap.sessions = maps.Clone(e.sessions)
	snap.apiKeys = maps.Clone(e.apiKeys)
	e.snapshot = snap
}

// restoreSnapshot empties the tables Reseed's truncation cascade reaches and
// puts the snapshot rows back, in one transaction with triggers and
// foreign-key checks suspended (the rows were valid when copied, and the
// triggers already did their work then). It deletes rather than truncates:
// with foreign-key triggers off, a plain DELETE per table empties the same
// closure, and on these small tables it costs a fraction of a
// 180-table TRUNCATE, which rewrites every relation's storage. The owned
// sequences the truncation would restart are set to their snapshot positions.
func (e *Env) restoreSnapshot() {
	e.t.Helper()
	ctx := e.ctx
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		e.t.Fatalf("scenario executor: restore: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One pipelined round trip for the whole restore.
	batch := &pgx.Batch{}
	batch.Queue(`SET LOCAL session_replication_role = replica`)
	for _, del := range e.snapshot.deletes {
		batch.Queue(del)
	}
	for _, insert := range e.snapshot.inserts {
		batch.Queue(insert)
	}
	for _, seq := range e.snapshot.sequences {
		batch.Queue(`SELECT setval($1::regclass, $2, $3)`, seq.name, seq.lastValue, seq.isCalled)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		e.t.Fatalf("scenario executor: restore: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		e.t.Fatalf("scenario executor: restore commit: %v", err)
	}
}

// restoreHouseholdState points the Go-side fixture identities back at the
// restored rows, as seeding would have left them.
func (e *Env) restoreHouseholdState() {
	for name, u := range e.snapshot.users {
		u := u
		e.users[name] = &u
	}
	maps.Copy(e.sessions, e.snapshot.sessions)
	maps.Copy(e.apiKeys, e.snapshot.apiKeys)
	maps.Copy(e.fixtures, e.snapshot.fixtures)
}

// dropSnapshot removes the snapshot schema; registered as an Env cleanup.
func (e *Env) dropSnapshot() {
	if _, err := e.pool.Exec(e.ctx, `DROP SCHEMA IF EXISTS `+snapshotSchema+` CASCADE`); err != nil {
		e.t.Logf("scenario executor: drop snapshot: %v", err)
	}
}
